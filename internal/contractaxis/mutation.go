package contractaxis

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	mutationChallengeEnv  = "CONTRACT_AXIS_CHALLENGE"
	mutationFailurePrefix = "CONTRACT_AXIS_KILL"
)

// Command is one directly executed mutation fence command.
type Command struct {
	Name string
	Args []string
	Env  []string
	Dir  string
}

// MutationSpec describes a disposable source mutation and the assertion it must kill.
type MutationSpec struct {
	ID              string
	Axis            string
	Item            string
	Case            string
	Patch           string
	Compile         Command
	Assertion       Command
	ExpectedFailure string
}

// MutationTarget identifies one resolved Git repository at one immutable HEAD.
// Its fields are private so adapters obtain it from ResolveMutationTarget rather
// than constructing provenance by assertion.
type MutationTarget struct {
	repository string
	head       string
}

// Repository returns the resolved repository root used for mutation.
func (t MutationTarget) Repository() string { return t.repository }

// Head returns the full Git commit identifier used for mutation.
func (t MutationTarget) Head() string { return t.head }

func (t MutationTarget) valid() bool {
	return strings.TrimSpace(t.repository) != "" && strings.TrimSpace(t.head) != ""
}

// ResolveMutationTarget resolves a repository root and its current HEAD for an Axis.
func ResolveMutationTarget(ctx context.Context, repo string) (MutationTarget, error) {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return MutationTarget{}, fmt.Errorf("absolute repository path: %w", err)
	}
	root, err := gitOutput(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return MutationTarget{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(strings.TrimSpace(root))
	if err != nil {
		return MutationTarget{}, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	head, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return MutationTarget{}, fmt.Errorf("resolve repository HEAD: %w", err)
	}
	return MutationTarget{repository: root, head: strings.TrimSpace(head)}, nil
}

// ErrMutationUnsupported means this platform cannot yet contain a complete
// subprocess tree, so mutation execution is refused instead of partially run.
var ErrMutationUnsupported = errors.New("contract-axis mutation execution is unsupported")

// MutationFailure returns the nonce-attested failure marker that a named Go
// assertion must emit when the production wire is cut.
func MutationFailure(reason string) string {
	return attestedFailureMarker(os.Getenv(mutationChallengeEnv), strings.TrimSpace(reason))
}

// RunMutation applies a patch in a disposable Git worktree and proves that it
// compiles, kills the named assertion, and restores to the exact pristine tree.
func RunMutation(ctx context.Context, repo string, spec MutationSpec) (result MutantEvidence, err error) {
	result.id = strings.TrimSpace(spec.ID)
	result.axis = strings.TrimSpace(spec.Axis)
	result.item = strings.TrimSpace(spec.Item)
	result.caseID = strings.TrimSpace(spec.Case)
	result.compile = commandString(spec.Compile)
	result.assertion = commandString(spec.Assertion)
	result.expectedFailure = strings.TrimSpace(spec.ExpectedFailure)
	if result.id == "" {
		return result, errors.New("mutation id is required")
	}
	if isReservedContractIdentifier(result.id) {
		return result, errors.New("mutation id is reserved for structural residuals")
	}
	if result.axis == "" {
		return result, errors.New("mutation axis is required")
	}
	if isReservedContractIdentifier(result.axis) {
		return result, errors.New("mutation axis is reserved for structural residuals")
	}
	if result.item == "" || result.caseID == "" {
		return result, errors.New("mutation item and case are required; use * for an axis mutant")
	}
	if result.item != "*" && isReservedContractIdentifier(result.item) {
		return result, errors.New("mutation item is reserved for structural residuals")
	}
	if result.caseID != "*" && isReservedContractIdentifier(result.caseID) {
		return result, errors.New("mutation case is reserved for structural residuals")
	}
	if strings.TrimSpace(spec.Patch) == "" {
		return result, errors.New("mutation patch is required")
	}
	if problem := validateCommand("compile", spec.Compile); problem != "" {
		return result, errors.New(problem)
	}
	if problem := validateCommand("assertion", spec.Assertion); problem != "" {
		return result, errors.New(problem)
	}
	if result.expectedFailure == "" {
		return result, errors.New("mutation expected failure marker is required")
	}
	if strings.ContainsAny(result.expectedFailure, "\r\n") {
		return result, errors.New("mutation expected failure marker must fit on one line")
	}
	if platformErr := mutationPlatformError(); platformErr != nil {
		return result, platformErr
	}

	target, err := ResolveMutationTarget(ctx, repo)
	if err != nil {
		return result, err
	}
	result.target = target
	repo = target.repository
	if status, statusErr := gitOutput(ctx, repo, "status", "--porcelain", "--untracked-files=all"); statusErr != nil {
		return result, fmt.Errorf("read repository status: %w", statusErr)
	} else if strings.TrimSpace(status) != "" {
		return result, fmt.Errorf("repository must be clean before mutation:\n%s", status)
	}
	sourcePristine, err := treeDigest(repo)
	if err != nil {
		return result, fmt.Errorf("digest primary repository: %w", err)
	}
	result.patchDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(spec.Patch)))
	challenge, err := mutationChallenge()
	if err != nil {
		return result, err
	}
	failureMarker := attestedFailureMarker(challenge, result.expectedFailure)

	worktree, err := os.MkdirTemp("", "contractaxis-mutant-")
	if err != nil {
		return result, fmt.Errorf("create mutation worktree: %w", err)
	}
	if _, err = gitOutput(ctx, repo, "worktree", "add", "--detach", worktree, target.head); err != nil {
		_ = os.RemoveAll(worktree)
		return result, fmt.Errorf("add mutation worktree: %w", err)
	}

	applied := false
	var pristine [sha256.Size]byte
	defer func() {
		// The caller may cancel while the mutant runs. Cleanup must still remove
		// Git's administrative worktree entry, so it gets its own bounded context.
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		var restoreErr error
		treeRestored := false
		assertionRestored := !applied
		if applied {
			if _, reverseErr := gitInput(cleanupCtx, worktree, spec.Patch, "apply", "--reverse", "--whitespace=nowarn", "-"); reverseErr != nil {
				restoreErr = fmt.Errorf("reverse mutation: %w", reverseErr)
			}
		}
		if applied && restoreErr == nil {
			restoredOutput, restoredCode, restoredErr := runCommandWithEnv(cleanupCtx, worktree, spec.Assertion, mutationChallengeEnv+"="+challenge)
			switch {
			case restoredErr != nil || restoredCode != 0:
				restoreErr = fmt.Errorf("restored assertion must pass (exit %d): %s", restoredCode, trimOutput(restoredOutput))
				restoreErr = errors.Join(restoreErr, restoredErr)
			case strings.Count(restoredOutput, failureMarker) != 0:
				restoreErr = errors.New("restored assertion emitted the expected failure marker")
			default:
				assertionRestored = true
			}
		}
		if restoreErr == nil {
			if restored, digestErr := treeDigest(worktree); digestErr != nil {
				restoreErr = fmt.Errorf("digest restored worktree: %w", digestErr)
			} else if restored != pristine {
				restoreErr = errors.New("mutation left a generated, derived, or modified artifact behind")
			} else {
				treeRestored = true
			}
		}
		if headErr := requireRepositoryHead(cleanupCtx, worktree, target.head, "disposable worktree"); headErr != nil {
			restoreErr = errors.Join(restoreErr, headErr)
		}
		if _, removeErr := gitOutput(cleanupCtx, repo, "worktree", "remove", "--force", worktree); removeErr != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("remove mutation worktree: %w", removeErr))
		}
		if removeErr := os.RemoveAll(worktree); removeErr != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("remove mutation worktree directory: %w", removeErr))
		}
		if sourceRestored, digestErr := treeDigest(repo); digestErr != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("digest primary repository after mutation: %w", digestErr))
		} else if sourceRestored != sourcePristine {
			restoreErr = errors.Join(restoreErr, errors.New("mutation command changed the primary repository"))
		}
		if headErr := requireRepositoryHead(cleanupCtx, repo, target.head, "primary repository"); headErr != nil {
			restoreErr = errors.Join(restoreErr, headErr)
		}
		result.restored = treeRestored && assertionRestored && restoreErr == nil
		if restoreErr != nil {
			err = errors.Join(err, restoreErr)
			result.detail = appendDetail(result.detail, restoreErr.Error())
		}
	}()

	pristine, err = treeDigest(worktree)
	if err != nil {
		return result, fmt.Errorf("digest pristine worktree: %w", err)
	}
	cleanOutput, cleanCode, cleanErr := runCommandWithEnv(ctx, worktree, spec.Assertion, mutationChallengeEnv+"="+challenge)
	if cleanErr != nil || cleanCode != 0 {
		result.detail = appendDetail(result.detail, "clean assertion: "+trimOutput(cleanOutput))
		return result, fmt.Errorf("clean assertion must pass before mutation (exit %d): %w", cleanCode, cleanErr)
	}
	if strings.Count(cleanOutput, failureMarker) != 0 {
		return result, errors.New("clean assertion emitted the expected failure marker")
	}
	if afterClean, digestErr := treeDigest(worktree); digestErr != nil {
		return result, fmt.Errorf("digest after clean assertion: %w", digestErr)
	} else if afterClean != pristine {
		return result, errors.New("clean assertion changed the disposable worktree")
	}

	applyOutput, applyErr := gitInput(ctx, worktree, spec.Patch, "apply", "--whitespace=nowarn", "-")
	if applyErr != nil {
		result.detail = appendDetail(result.detail, "apply: "+trimOutput(applyOutput))
		return result, fmt.Errorf("apply mutation: %w", applyErr)
	}
	applied = true
	result.applied = true
	changedPaths, pathErr := mutationPaths(ctx, worktree)
	if pathErr != nil {
		return result, fmt.Errorf("enumerate mutation paths: %w", pathErr)
	}
	result.paths = changedPaths
	if len(result.paths) == 0 {
		return result, errors.New("mutation patch changed no repository paths")
	}

	compileOutput, compileCode, compileErr := runCommand(ctx, worktree, spec.Compile)
	if compileErr != nil || compileCode != 0 {
		result.detail = appendDetail(result.detail, "compile: "+trimOutput(compileOutput))
		return result, fmt.Errorf("mutant must compile (exit %d): %w", compileCode, compileErr)
	}
	result.compiled = true

	mutantOutput, mutantCode, mutantErr := runCommandWithEnv(ctx, worktree, spec.Assertion, mutationChallengeEnv+"="+challenge)
	if mutantErr == nil && mutantCode == 0 {
		result.detail = appendDetail(result.detail, "mutant survived: "+trimOutput(mutantOutput))
		return result, errors.New("mutant survived the named assertion")
	}
	if mutantCode < 0 || ctx.Err() != nil {
		result.detail = appendDetail(result.detail, "assertion did not start: "+trimOutput(mutantOutput))
		return result, fmt.Errorf("run mutant assertion: %w", mutantErr)
	}
	if strings.Count(mutantOutput, failureMarker) != 1 {
		result.detail = appendDetail(result.detail, "unrelated assertion failure: "+trimOutput(mutantOutput))
		return result, fmt.Errorf("mutant assertion did not contain nonce-attested failure marker for %q", result.expectedFailure)
	}
	result.killed = true
	result.detail = appendDetail(result.detail, "killed by "+result.assertion)
	return result, nil
}

func validateCommand(label string, command Command) string {
	if strings.TrimSpace(command.Name) == "" {
		return label + " command name is required"
	}
	for _, env := range command.Env {
		if strings.HasPrefix(env, mutationChallengeEnv+"=") {
			return label + " command may not set the runner challenge environment"
		}
	}
	if command.Dir == "" {
		return ""
	}
	clean := filepath.Clean(command.Dir)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return label + " command directory must stay inside the mutation worktree"
	}
	return ""
}

func runCommand(ctx context.Context, worktree string, command Command) (string, int, error) {
	return runCommandWithEnv(ctx, worktree, command, "")
}

func runCommandWithEnv(ctx context.Context, worktree string, command Command, extraEnv string) (string, int, error) {
	dir, err := resolveCommandDir(worktree, command.Dir)
	if err != nil {
		return "", -1, err
	}
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	configureCommand(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), command.Env...)
	if extraEnv != "" {
		cmd.Env = append(cmd.Env, extraEnv)
	}
	output, err := cmd.CombinedOutput()
	if cleanupErr := cleanupCommand(cmd); cleanupErr != nil {
		return string(output), -1, errors.Join(err, fmt.Errorf("terminate command process group: %w", cleanupErr))
	}
	if err == nil {
		return string(output), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(output), exitErr.ExitCode(), err
	}
	return string(output), -1, err
}

func mutationChallenge() (string, error) {
	var challenge [16]byte
	if _, err := rand.Read(challenge[:]); err != nil {
		return "", fmt.Errorf("create mutation challenge: %w", err)
	}
	return fmt.Sprintf("%x", challenge[:]), nil
}

func mutationPaths(ctx context.Context, worktree string) ([]string, error) {
	tracked, err := gitOutput(ctx, worktree, "diff", "--name-only", "-z", "--no-renames")
	if err != nil {
		return nil, err
	}
	untracked, err := gitOutput(ctx, worktree, "ls-files", "--others", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, output := range []string{tracked, untracked} {
		for _, path := range strings.Split(output, "\x00") {
			if path != "" {
				seen[filepath.ToSlash(path)] = true
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func requireRepositoryHead(ctx context.Context, repo, want, label string) error {
	head, err := gitOutput(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve %s HEAD after mutation: %w", label, err)
	}
	if got := strings.TrimSpace(head); got != want {
		return fmt.Errorf("%s HEAD changed from %s to %s", label, want, got)
	}
	return nil
}

func attestedFailureMarker(challenge, expected string) string {
	return mutationFailurePrefix + ":" + challenge + ":" + expected
}

func resolveCommandDir(worktree, relative string) (string, error) {
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", fmt.Errorf("resolve mutation worktree: %w", err)
	}
	dir := root
	if relative != "" {
		dir = filepath.Join(root, filepath.Clean(relative))
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve mutation command directory: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("compare mutation command directory: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("mutation command directory resolves outside the worktree")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat mutation command directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("mutation command directory is not a directory")
	}
	return resolved, nil
}

func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	configureCommand(cmd)
	output, err := cmd.CombinedOutput()
	if cleanupErr := cleanupCommand(cmd); cleanupErr != nil {
		err = errors.Join(err, fmt.Errorf("terminate git process group: %w", cleanupErr))
	}
	if err != nil {
		return string(output), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimOutput(string(output)))
	}
	return string(output), nil
}

func gitInput(ctx context.Context, repo, input string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	configureCommand(cmd)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if cleanupErr := cleanupCommand(cmd); cleanupErr != nil {
		err = errors.Join(err, fmt.Errorf("terminate git process group: %w", cleanupErr))
	}
	if err != nil {
		return string(output), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimOutput(string(output)))
	}
	return string(output), nil
}

func treeDigest(root string) ([sha256.Size]byte, error) {
	type entry struct {
		path string
		mode fs.FileMode
		data []byte
	}
	var entries []entry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			entries = append(entries, entry{path: filepath.ToSlash(rel), mode: info.Mode()})
			return nil
		}
		var data []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			data = []byte(target)
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported special file %s (%s)", filepath.ToSlash(rel), info.Mode().Type())
		} else {
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		entries = append(entries, entry{path: filepath.ToSlash(rel), mode: info.Mode(), data: data})
		return nil
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hash := sha256.New()
	for _, item := range entries {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", item.path, item.mode.String())
		_, _ = hash.Write(item.data)
		_, _ = hash.Write([]byte{0})
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func commandString(command Command) string {
	type identity struct {
		Name      string   `json:"name"`
		Args      []string `json:"args"`
		Dir       string   `json:"dir"`
		EnvKeys   []string `json:"env_keys,omitempty"`
		EnvDigest string   `json:"env_sha256,omitempty"`
	}
	args := make([]string, len(command.Args))
	copy(args, command.Args)
	record := identity{Name: command.Name, Args: args, Dir: command.Dir}
	if len(command.Env) > 0 {
		keys := map[string]bool{}
		for _, entry := range command.Env {
			key, _, _ := strings.Cut(entry, "=")
			keys[key] = true
		}
		record.EnvKeys = sortedKeys(keys)
		record.EnvDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(command.Env, "\x00"))))
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func appendDetail(existing, detail string) string {
	if strings.TrimSpace(detail) == "" {
		return existing
	}
	if existing == "" {
		return detail
	}
	return existing + "; " + detail
}

func trimOutput(output string) string {
	output = strings.TrimSpace(output)
	const max = 2000
	if len(output) <= max {
		return output
	}
	return output[:max] + "…"
}
