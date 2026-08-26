// Package repohygiene holds checks about the shape of the repository itself
// rather than the behaviour of any package in it.
//
// Both checks here were written after finding what they now catch. The tree held
// two compiled arm64 binaries totalling 26 MB, committed because .gitignore
// covered the build outputs named `agentsmemory` and `server` but not the one
// `go build ./clients/claude-code` happens to produce; and it held absolute home
// paths belonging to named people, in a public repository, alongside a tool's
// runtime state file that nothing referenced.
//
// Neither is a bug in the sense a compiler or a unit test understands. Both are
// the kind of thing a reviewer notices once, mentions, and then stops noticing.
package repohygiene

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// binaryMagics are the leading bytes of the executable formats a developer on
// any of our platforms could accidentally commit.
var binaryMagics = [][]byte{
	{0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64-bit little-endian
	{0xce, 0xfa, 0xed, 0xfe}, // Mach-O 32-bit
	{0xca, 0xfe, 0xba, 0xbe}, // Mach-O universal
	{0x7f, 'E', 'L', 'F'},    // ELF
	{'M', 'Z'},               // PE
}

// homePath matches an absolute path into somebody's home directory. The capture
// is the user name, which is what decides whether this is a leak or an example.
var homePath = regexp.MustCompile(`/(?:Users|home)/([A-Za-z][A-Za-z0-9._-]*)`)

// placeholderUsers are the names that make a home path illustrative rather than
// somebody's actual machine. Documentation needs to show a path shape, and
// forbidding that outright would make the check something to work around.
var placeholderUsers = map[string]bool{
	"me": true, "you": true, "user": true, "username": true, "example": true,
	"someone": true, "alice": true, "bob": true, "name": true, "youruser": true,
}

// TestNoCompiledBinariesInTheTree fails when an executable sits in the working
// tree that .gitignore does not cover — which means it is either committed
// already or one `git add -A` away from it.
//
// The fix is always one of two things, and both are right: delete the artifact,
// or add it to .gitignore. That is why the check is phrased against .gitignore
// rather than against git's index, which is also what lets it run inside the
// build image, where git is not installed.
func TestNoCompiledBinariesInTheTree(t *testing.T) {
	root := repoRoot(t)
	ignored := gitignoreMatcher(t, root)

	for _, path := range walk(t, root, ignored) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		head := make([]byte, 4)
		n, _ := f.Read(head)
		f.Close()
		head = head[:max(n, 0)]
		for _, magic := range binaryMagics {
			if len(head) >= len(magic) && bytes.Equal(head[:len(magic)], magic) {
				rel, _ := filepath.Rel(root, path)
				info, _ := os.Stat(path)
				size := int64(0)
				if info != nil {
					size = info.Size() / 1024
				}
				t.Errorf("%s is a compiled binary (%d KB) that .gitignore does not cover — delete it, or ignore it if it is a local build artifact", rel, size)
				break
			}
		}
	}
}

// TestNoRealHomePathsInTrackedFiles fails when a file names an actual person's
// home directory.
//
// A path like /Users/me/code is a documented example and is fine. A path naming
// a real account is somebody's machine, in a public repository, and it arrives
// by accident every time — inside a tool's state file, a test fixture written on
// one laptop, or a README that recorded where its author happened to keep
// something.
func TestNoRealHomePathsInTrackedFiles(t *testing.T) {
	root := repoRoot(t)
	ignored := gitignoreMatcher(t, root)

	for _, path := range walk(t, root, ignored) {
		rel, _ := filepath.Rel(root, path)
		if rel == filepath.Join("internal", "repohygiene", "hygiene_test.go") {
			continue // this file necessarily contains the pattern it looks for
		}
		src, err := os.ReadFile(path)
		if err != nil || !looksTextual(src) {
			continue
		}
		seen := map[string]bool{}
		for _, m := range homePath.FindAllSubmatch(src, -1) {
			user := string(m[1])
			// A one- or two-character name is a test fixture, not an account:
			// /home/u/x is somebody building a temp path, and flagging those
			// would make the check something to silence rather than obey.
			if len(user) <= 2 || placeholderUsers[strings.ToLower(user)] || seen[user] {
				continue
			}
			seen[user] = true
			t.Errorf("%s names a real home directory (%s) — use a placeholder such as /Users/me, or drop the file if it is local state", rel, string(m[0]))
		}
	}
}

// looksTextual keeps the path check off binaries, whose bytes can match anything.
func looksTextual(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return !bytes.Contains(b, []byte{0})
}

// gitignoreMatcher builds a deliberately small .gitignore reader: exact names,
// /rooted paths, dir/ prefixes and *.ext suffixes. It covers what this
// repository's ignore file actually uses, and anything it misses fails LOUD (a
// reported artifact) rather than quiet (a missed one), which is the right way
// for an approximation to be wrong.
func gitignoreMatcher(t *testing.T, root string) func(rel string, isDir bool) bool {
	t.Helper()
	var exact, rooted, dirs, exts []string
	if src, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
		for _, line := range strings.Split(string(src), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			switch {
			case strings.HasPrefix(line, "*."):
				exts = append(exts, strings.TrimPrefix(line, "*"))
			case strings.HasPrefix(line, "/"):
				rooted = append(rooted, strings.Trim(line, "/"))
			case strings.HasSuffix(line, "/"):
				dirs = append(dirs, strings.TrimSuffix(line, "/"))
			default:
				exact = append(exact, line)
			}
		}
	}
	// Always skipped: git's own storage, agent worktrees (copies of this repo,
	// which would report every finding twice), and dependency trees we did not
	// write.
	dirs = append(dirs, ".git", ".claude", "node_modules", "vendor")

	return func(rel string, isDir bool) bool {
		base := filepath.Base(rel)
		for _, d := range dirs {
			if isDir && base == strings.Trim(d, "/") {
				return true
			}
			// git's own storage is a DIRECTORY in a clone and a FILE in a
			// worktree or submodule, holding a line like "gitdir: /Users/…". The
			// isDir gate above therefore walked that file, matched the home path
			// inside it, and accused the tree of leaking one — so this suite went
			// red for anyone reviewing from a worktree, which is the obvious way
			// to check out a PR. A gate that cries wolf is one people delete, and
			// this one cried at reviewers specifically.
			if !isDir && base == ".git" && strings.Trim(d, "/") == ".git" {
				return true
			}
		}
		for _, e := range exact {
			if base == e {
				return true
			}
		}
		for _, r := range rooted {
			if rel == r || strings.HasPrefix(rel, r+string(filepath.Separator)) {
				return true
			}
		}
		for _, e := range exts {
			if strings.HasSuffix(base, e) {
				return true
			}
		}
		return false
	}
}

func walk(t *testing.T, root string, ignored func(string, bool) bool) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if ignored(rel, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.IsDir() && info.Mode().IsRegular() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// exampleWings is every wing name this repository is allowed to contain.
//
// It is an ALLOWLIST, not a denylist, and that is the whole point: a denylist of
// real project names would have to spell those names out in a public repository,
// which is the leak it exists to prevent. Naming the permitted examples publishes
// nothing, and it puts a deliberate step exactly where the mistake happens.
//
// The mistake is specific and it has happened: an agent working with a live
// palace has that palace's real wing list in front of it — am_status returns it —
// and reaches for one of those names when it needs a fixture. Two real project
// names reached a committed test that way on 2026-08-21.
//
// Adding an entry is fine and expected. Adding one that is somebody's actual
// project is not, and the failure message says so rather than leaving it to
// judgement.
var exampleWings = map[string]bool{
	"wing_a": true, "wing_abc": true, "wing_acme": true, "wing_acme-billing": true,
	// ADR-036 T7: a wing that deliberately does not exist, so a test can assert
	// that having no entry point is reported as a fact rather than an error.
	"wing_no_such_place": true,
	"wing_acme_laravel":  true, "wing_acme-legacy": true, "wing_acme-old": true,
	"wing_acmee": true, "wing_agentmemories": true,
	"wing_alpha": true, "wing_anchor": true, "wing_anything": true, "wing_api": true,
	"wing_app": true, "wing_atlas": true, "wing_atomic": true, "wing_b": true,
	"wing_beta": true, "wing_big": true, "wing_billing": true, "wing_chunked": true,
	"wing_claude": true, "wing_craf": true, "wing_craft": true, "wing_diary": true,
	"wing_dup": true, "wing_env": true, "wing_explicit": true, "wing_from": true,
	"wing_gamma": true, "wing_gone": true, "wing_harness": true, "wing_infra": true,
	"wing_keepanchor": true, "wing_local": true, "wing_mined": true,
	"wing_missing": true, "wing_never_written": true, "wing_new": true,
	"wing_one": true, "wing_orders-db": true, "wing_orphan": true, "wing_other": true,
	"wing_proj": true, "wing_project": true, "wing_real": true,
	"wing_reconnect": true, "wing_research": true, "wing_restored": true,
	"wing_roles": true, "wing_room": true, "wing_rr": true, "wing_scenario": true, "wing_shape": true,
	"wing_shared": true, "wing_shared_name": true, "wing_solo": true,
	"wing_stats": true, "wing_storefront": true, "wing_sweep": true,
	"wing_that_never_existed": true, "wing_to": true, "wing_to-": true,
	"wing_to-beta": true, "wing_to-billing": true, "wing_to-someproject": true,
	"wing_to-x": true,
	"wing_two":  true, "wing_typo": true, "wing_unused": true, "wing_verdict": true,
	"wing_very-old-project": true, "wing_wake": true, "wing_written": true,
	"wing_x": true, "wing_zzzzzz": true,
}

// wingName matches a wing identifier anywhere in a tracked file.
var wingName = regexp.MustCompile(`wing_[a-z0-9][a-z0-9_-]*`)

// TestNoRealProjectNamesInWings fails when a wing name appears that is not a
// declared example.
//
// A wing IS a project namespace, so a wing name is a project name — which makes
// every fixture, README snippet and comment in this repository a place where
// somebody's real project can be published by accident. It arrives the same way
// every time: whoever is writing the fixture has a live palace open, and the
// nearest name is a real one.
func TestNoRealProjectNamesInWings(t *testing.T) {
	root := repoRoot(t)
	ignored := gitignoreMatcher(t, root)
	self := filepath.Join("internal", "repohygiene", "hygiene_test.go")

	found := 0
	for _, path := range walk(t, root, ignored) {
		rel, _ := filepath.Rel(root, path)
		if rel == self {
			continue // the allowlist necessarily contains every name it permits
		}
		// Postmortems and ADRs describe incidents in prose; they are held to the
		// same rule as code, which is why they are NOT skipped here.
		src, err := os.ReadFile(path)
		if err != nil || !looksTextual(src) {
			continue
		}
		seen := map[string]bool{}
		for _, m := range wingName.FindAll(src, -1) {
			name := strings.ToLower(string(m))
			if seen[name] {
				continue
			}
			seen[name] = true
			found++
			if !exampleWings[name] {
				t.Errorf("%s contains the wing name %q, which is not a declared example.\n"+
					"  A wing name is a PROJECT name. If this is somebody's real project, it must not be "+
					"committed — use wing_acme, wing_alpha or another neutral name.\n"+
					"  If it is genuinely an example, add it to exampleWings in %s.", rel, name, self)
			}
		}
	}
	if found == 0 {
		t.Error("no wing names were found anywhere in the tree — this check has stopped checking anything")
	}
}

// TestEveryComposeFileIsDocumented makes the deployment documentation
// load-bearing, in the same shape as the hook-event and agent-name gates in
// clients/claude-code.
//
// A compose file nobody documents is one nobody runs. That is not hypothetical
// here: docker-compose.ollama.yml — the overlay that removes the single most
// common first-run failure, "you have no embedder" — was named exactly once in
// the whole README, inside a command block, with no heading a reader could find
// it by.
//
// The expected set is the directory, so adding an overlay and forgetting the
// README fails a build rather than being noticed by someone who never had reason
// to look.
func TestEveryComposeFileIsDocumented(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "docker-compose*.yml"))
	if err != nil {
		t.Fatalf("glob compose files: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("found %d compose files — the glob is wrong, and an empty set would let this "+
			"check pass against a README documenting nothing", len(files))
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(readme)
	for _, path := range files {
		name := filepath.Base(path)
		if !strings.Contains(text, name) {
			t.Errorf("README.md never mentions %s: an operator cannot run a compose file they "+
				"cannot find, and the file's own header comment is not somewhere anyone looks "+
				"before deciding what to run", name)
		}
	}

	// The Dockerfile is the other half of "how do I run this": every compose file
	// that builds rather than pulls depends on it, and the no-compose path is a
	// plain `docker build`.
	if !strings.Contains(text, "docker build") {
		t.Error("README.md never shows `docker build`, so the image everything else is built " +
			"from has no documented origin")
	}
}

// TestADRNumbersAreUnique fails when two documents claim the same ADR-NNN-
// prefix. Main's last number is 021; two open PRs both wrote ADR-024 and CI
// would have stayed green whichever merged second.
func TestADRNumbersAreUnique(t *testing.T) {
	root := repoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "docs", "adr", "ADR-*.md"))
	if err != nil {
		t.Fatalf("glob ADRs: %v", err)
	}
	if len(matches) < 2 {
		t.Fatalf("found %d ADR files — an empty glob would let a collision through", len(matches))
	}
	re := regexp.MustCompile(`^ADR-(\d{3})-`)
	seen := map[string]string{}
	for _, path := range matches {
		base := filepath.Base(path)
		m := re.FindStringSubmatch(base)
		if m == nil {
			t.Errorf("%s does not match ADR-NNN-slug.md", base)
			continue
		}
		if prev, ok := seen[m[1]]; ok {
			t.Errorf("ADR-%s is claimed by both %s and %s", m[1], prev, base)
		}
		seen[m[1]] = base
	}
}
