//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package contractaxis

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMutationRunnerKillsACompilingWireCutAndRestoresSource(t *testing.T) {
	repo := newMutationFixture(t, false)
	result, err := RunMutation(context.Background(), repo, mutationSpec(falsePatch()))
	if err != nil {
		t.Fatalf("run mutation: %v (%+v)", err, result)
	}
	if !result.Verified() {
		t.Fatalf("incomplete mutation evidence: %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestMutationRunnerRejectsASurvivingMutant(t *testing.T) {
	repo := newMutationFixture(t, false)
	patch := strings.Replace(falsePatch(), "+\treturn false", "+\treturn true // survived", 1)
	result, err := RunMutation(context.Background(), repo, mutationSpec(patch))
	if err == nil || !strings.Contains(err.Error(), "survived") {
		t.Fatalf("surviving mutant error = %v, result = %+v", err, result)
	}
	if result.killed || !result.restored {
		t.Fatalf("surviving mutant evidence = %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestMutationRunnerRejectsAMutantThatDoesNotCompile(t *testing.T) {
	repo := newMutationFixture(t, false)
	patch := strings.Replace(falsePatch(), "+\treturn false", "+\treturn missingIdentifier", 1)
	result, err := RunMutation(context.Background(), repo, mutationSpec(patch))
	if err == nil || !strings.Contains(err.Error(), "must compile") {
		t.Fatalf("compile failure error = %v, result = %+v", err, result)
	}
	if result.compiled || result.killed || !result.restored {
		t.Fatalf("non-compiling mutant evidence = %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestMutationRunnerRejectsAnUnrelatedAssertionFailure(t *testing.T) {
	repo := newMutationFixture(t, false)
	spec := mutationSpec(falsePatch())
	spec.ExpectedFailure = "a different failure"
	result, err := RunMutation(context.Background(), repo, spec)
	if err == nil || !strings.Contains(err.Error(), "expected failure marker") {
		t.Fatalf("unrelated failure error = %v, result = %+v", err, result)
	}
	if result.killed || !result.restored {
		t.Fatalf("unrelated failure became kill evidence: %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestMutationRunnerRejectsAFailureMarkerPrintedByTheCleanAssertion(t *testing.T) {
	repo := newMutationFixture(t, false)
	spec := mutationSpec(falsePatch())
	spec.Assertion = Command{
		Name: "sh", Args: []string{"-c", `printf '%s\n' 'production selector is disconnected'; go test ./... -run '^TestEnabled$' -count=1`},
		Env: []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off"},
	}
	result, err := RunMutation(context.Background(), repo, spec)
	if err == nil || !strings.Contains(err.Error(), "clean assertion emitted") {
		t.Fatalf("clean marker error = %v, result = %+v", err, result)
	}
	if result.applied || result.killed || !result.restored {
		t.Fatalf("clean marker became mutation evidence: %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestMutationRunnerDetectsADerivedArtifactThePatchCannotRestore(t *testing.T) {
	repo := newMutationFixture(t, true)
	result, err := RunMutation(context.Background(), repo, mutationSpec(falsePatch()))
	if err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("artifact error = %v, result = %+v", err, result)
	}
	if !result.killed || result.restored {
		t.Fatalf("artifact mutation evidence = %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestMutationRunnerRefusesADirtySourceRepository(t *testing.T) {
	repo := newMutationFixture(t, false)
	writeFixtureFile(t, filepath.Join(repo, "untracked.txt"), "not committed\n")
	_, err := RunMutation(context.Background(), repo, mutationSpec(falsePatch()))
	if err == nil || !strings.Contains(err.Error(), "must be clean") {
		t.Fatalf("dirty repository error = %v", err)
	}
}

func TestMutationCommandCannotEscapeTheDisposableWorktree(t *testing.T) {
	repo := newMutationFixture(t, false)
	spec := mutationSpec(falsePatch())
	spec.Compile.Dir = "../outside"
	_, err := RunMutation(context.Background(), repo, spec)
	if err == nil || !strings.Contains(err.Error(), "must stay inside") {
		t.Fatalf("escaping command directory error = %v", err)
	}
}

func TestMutationCleanupOutlivesACancelledRunContext(t *testing.T) {
	repo := newMutationFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	spec := mutationSpec(falsePatch())
	marker := filepath.Join(t.TempDir(), "mutant-running")
	spec.Assertion = Command{
		Name: "sh", Args: []string{
			"-c",
			`go test ./... -run '^TestEnabled$' -count=1; code=$?; if [ "$code" -ne 0 ]; then touch "$1"; sleep 30; fi; exit "$code"`,
			"contract-axis", marker,
		},
		Env: []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off"},
	}
	result := make(chan error, 1)
	go func() {
		_, err := RunMutation(ctx, repo, spec)
		result <- err
	}()
	for attempts := 0; ; attempts++ {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat mutation marker: %v", err)
		}
		if attempts == 200 {
			t.Fatal("mutant assertion did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancelledAt := time.Now()
	cancel()

	err := <-result
	if elapsed := time.Since(cancelledAt); elapsed > 3*time.Second {
		t.Fatalf("cancelled process group took %s to stop", elapsed)
	}
	if err == nil {
		t.Fatal("cancelled mutation unexpectedly succeeded")
	}
	assertFixtureClean(t, repo)
}

func TestSuccessfulCommandCannotLeaveABackgroundChild(t *testing.T) {
	worktree := t.TempDir()
	ready := filepath.Join(t.TempDir(), "child-ready")
	leaked := filepath.Join(t.TempDir(), "child-leaked")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	output, code, err := runCommand(ctx, worktree, Command{
		Name: "sh",
		Args: []string{
			"-c",
			`(touch "$1"; sleep 1; touch "$2") </dev/null >/dev/null 2>&1 & while [ ! -f "$1" ]; do sleep 0.01; done`,
			"contract-axis", ready, leaked,
		},
	})
	if err != nil || code != 0 {
		t.Fatalf("successful parent command = exit %d, err %v, output %q", code, err, output)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("background child survived successful parent: %v", err)
	}
}

func TestMutationRunnerRejectsStateLeftOutsideTheRestoredTree(t *testing.T) {
	repo := newMutationFixture(t, false)
	state := filepath.Join(t.TempDir(), "external-state")
	writeFixtureFile(t, filepath.Join(repo, "stateful_test.go"), `package fixture

import (
	"os"
	"testing"
)

func TestStatefulEnabled(t *testing.T) {
	state := os.Getenv("MUTATION_STATE")
	if Enabled() {
		if _, err := os.Stat(state); err == nil {
			t.Fatal("mutation state survived restoration")
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat mutation state: %v", err)
		}
		return
	}
	if err := os.WriteFile(state, []byte("mutant ran\n"), 0o600); err != nil {
		t.Fatalf("write mutation state: %v", err)
	}
	t.Fatal("production selector is disconnected")
}
`)
	runFixtureGit(t, repo, "add", "stateful_test.go")
	runFixtureGit(t, repo, "-c", "user.name=Contract Axis", "-c", "user.email=contract-axis@example.invalid", "commit", "-qm", "stateful assertion")

	spec := mutationSpec(falsePatch())
	spec.Assertion.Args = []string{"test", "./...", "-run", "^TestStatefulEnabled$", "-count=1"}
	spec.Assertion.Env = append(spec.Assertion.Env, "MUTATION_STATE="+state)
	result, err := RunMutation(context.Background(), repo, spec)
	if err == nil || !strings.Contains(err.Error(), "restored assertion must pass") {
		t.Fatalf("stateful restoration error = %v, result = %+v", err, result)
	}
	if !result.killed || result.restored || result.Verified() {
		t.Fatalf("external state became verified mutation evidence: %+v", result)
	}
	assertFixtureClean(t, repo)
}

func TestTreeDigestIncludesEmptyDirectories(t *testing.T) {
	root := t.TempDir()
	before, err := treeDigest(root)
	if err != nil {
		t.Fatalf("digest empty root: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "generated"), 0o700); err != nil {
		t.Fatalf("create empty generated directory: %v", err)
	}
	after, err := treeDigest(root)
	if err != nil {
		t.Fatalf("digest generated directory: %v", err)
	}
	if before == after {
		t.Fatal("empty generated directory did not change the tree digest")
	}
}

func TestTreeDigestIncludesDirectoryMode(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "existing")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("set initial directory mode: %v", err)
	}
	before, err := treeDigest(root)
	if err != nil {
		t.Fatalf("digest initial directory mode: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("change directory mode: %v", err)
	}
	after, err := treeDigest(root)
	if err != nil {
		t.Fatalf("digest changed directory mode: %v", err)
	}
	if before == after {
		t.Fatal("directory mode change did not change the tree digest")
	}
}

func mutationSpec(patch string) MutationSpec {
	return MutationSpec{
		ID:              "wire-cut",
		Patch:           patch,
		ExpectedFailure: "production selector is disconnected",
		Compile: Command{
			Name: "go", Args: []string{"test", "./...", "-run", "^$"},
			Env: []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off"},
		},
		Assertion: Command{
			Name: "go", Args: []string{"test", "./...", "-run", "^TestEnabled$", "-count=1"},
			Env: []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOTELEMETRY=off"},
		},
	}
}

func falsePatch() string {
	return "diff --git a/feature.go b/feature.go\n" +
		"--- a/feature.go\n" +
		"+++ b/feature.go\n" +
		"@@ -1,5 +1,5 @@\n" +
		" package fixture\n" +
		" \n" +
		" func Enabled() bool {\n" +
		"-\treturn true\n" +
		"+\treturn false\n" +
		" }\n"
}

func newMutationFixture(t *testing.T, writesArtifact bool) string {
	t.Helper()
	repo := t.TempDir()
	writeFixtureFile(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.25.7\n")
	writeFixtureFile(t, filepath.Join(repo, "feature.go"), "package fixture\n\nfunc Enabled() bool {\n\treturn true\n}\n")
	writeFixtureFile(t, filepath.Join(repo, "feature_test.go"), fmt.Sprintf(`package fixture

import (
	"os"
	"testing"
)

func TestEnabled(t *testing.T) {
	if Enabled() {
		return
	}
	if %t {
		if err := os.WriteFile("generated.txt", []byte("from mutant\n"), 0o600); err != nil {
			t.Fatalf("write derived artifact: %%v", err)
		}
	}
	t.Fatal("production selector is disconnected")
}
`, writesArtifact))
	runFixtureGit(t, repo, "init", "-q")
	runFixtureGit(t, repo, "add", "go.mod", "feature.go", "feature_test.go")
	runFixtureGit(t, repo, "-c", "user.name=Contract Axis", "-c", "user.email=contract-axis@example.invalid", "commit", "-qm", "fixture")
	return repo
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runFixtureGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func assertFixtureClean(t *testing.T, repo string) {
	t.Helper()
	if status := strings.TrimSpace(runFixtureGit(t, repo, "status", "--porcelain", "--untracked-files=all")); status != "" {
		t.Fatalf("fixture repository changed:\n%s", status)
	}
	if worktrees := runFixtureGit(t, repo, "worktree", "list", "--porcelain"); strings.Count(worktrees, "worktree ") != 1 {
		t.Fatalf("mutation worktree was not removed:\n%s", worktrees)
	}
}
