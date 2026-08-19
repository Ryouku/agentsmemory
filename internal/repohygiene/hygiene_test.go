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
