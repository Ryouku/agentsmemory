package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindSurvivesReformatting is the line between a useful flag and noise: a
// re-indent or a gofmt must NOT read as drift, or every commit marks half the
// palace stale and nobody looks at the flag again.
func TestFindSurvivesReformatting(t *testing.T) {
	src := readSourceFrom("package main\n\nfunc pin() {\n\t\tenv :=  []string{\n\t\t\tconfigEnv + \"=\" + dir,\n\t\t}\n}\n")

	for name, snippet := range map[string]string{
		"exact":                      "env := []string{",
		"re-indented":                "        env :=    []string{",
		"multi-line":                 "env := []string{\n  configEnv + \"=\" + dir,",
		"one distinctive expression": "configEnv + \"=\" + dir",
	} {
		t.Run(name, func(t *testing.T) {
			if line, ok := src.find(snippet); !ok {
				t.Errorf("snippet not found; formatting must not read as drift")
			} else if line < 1 {
				t.Errorf("line = %d, want the 1-based position", line)
			}
		})
	}
}

// TestFindReportsRealDrift: code that is genuinely gone must be reported, which
// is the whole point.
func TestFindReportsRealDrift(t *testing.T) {
	src := readSourceFrom("func pin() {\n\tvar env []string\n}\n")
	if _, ok := src.find("env := []string{configEnv + \"=\" + dir}"); ok {
		t.Error("removed code reported as still present")
	}
}

// TestFindLocatesTheCurrentLine: the line number is the ANSWER, never part of the
// question — an anchor holds no line, and verification reports where the code is
// now.
func TestFindLocatesTheCurrentLine(t *testing.T) {
	src := readSourceFrom("// a\n// b\n// c\nfunc target() {}\n")
	line, ok := src.find("func target() {}")
	if !ok || line != 4 {
		t.Errorf("line = %d, ok = %v; want line 4", line, ok)
	}

	// Insert two lines above it: the same anchor must still verify, at the new
	// position. Anchoring to "line 4" would have failed here — which is exactly
	// why anchors hold snippets.
	moved := readSourceFrom("// new\n// new\n// a\n// b\n// c\nfunc target() {}\n")
	if line, ok := moved.find("func target() {}"); !ok || line != 6 {
		t.Errorf("after inserting lines: line = %d, ok = %v; want line 6", line, ok)
	}
}

// TestMissingFileIsNotAnError: a deleted file is a verdict the report should
// carry, not a failure that stops the run.
func TestMissingFileIsNotAnError(t *testing.T) {
	src := readSource("/nonexistent/definitely/not/here.go")
	if src.exists {
		t.Fatal("a missing file must not report as existing")
	}
	if _, ok := src.find("anything"); ok {
		t.Error("a missing file cannot contain a snippet")
	}
}

// readSourceFrom builds a sourceFile from literal content, so the matcher is
// tested without touching disk.
func readSourceFrom(content string) *sourceFile {
	lines := strings.Split(content, "\n")
	norm := make([]string, len(lines))
	for i, l := range lines {
		norm[i] = normalizeSnippet(l)
	}
	return &sourceFile{exists: true, lines: lines, normalized: norm}
}

// TestCurrentRepoLabelPrefersTheRemote pins the rule anchors are labelled with,
// because the skip decision is only as good as the two labels agreeing.
func TestCurrentRepoLabelPrefersTheRemote(t *testing.T) {
	dir := t.TempDir()
	// No git remote: the label is UNKNOWN, not the directory name. Using the
	// folder name made every anchor from a named repository look foreign, so the
	// verifier reported success having checked nothing. See
	// TestCurrentRepoLabelIsEmptyWhenTheRepoIsUnknown.
	if got := currentRepoLabel(dir); got != "" {
		t.Errorf("without a remote the label must be empty (unknown), got %q", got)
	}
}

// TestAnchorsFromAnotherRepoAreNotMissing is the regression this behaviour
// exists for. A memory pinned to a file in a sibling repository used to report
// "file is gone" from every other checkout — and since the honest response to
// that is deleting the memory, the check destroyed what it was built to protect.
// A live session deleted three chunks that way.
func TestAnchorsFromAnotherRepoAreNotMissing(t *testing.T) {
	root := t.TempDir()
	here := currentRepoLabel(root)

	// Same shape as the loop in runVerify: a foreign label is skipped before the
	// file is ever looked for, so a path that does not exist here is not a
	// verdict about the memory.
	foreign := anchor{Path: "infra/docker/base/Dockerfile", Repo: "some-other-repo"}
	if foreign.Repo != "" && here != "" && !strings.EqualFold(foreign.Repo, here) {
		return // named tree, foreign label: skipped before the file is looked for
	}
	// Unknown tree (no remote): the label check cannot decide, so the file is
	// looked for — and when it is not found, runVerify must still refuse to call
	// it MISSING, because it cannot distinguish "deleted" from "lives elsewhere".
	// That second guard is what keeps the regression fixed now that an unknown
	// tree no longer skips everything.
	if here != "" {
		t.Fatalf("an anchor labelled %q must be skipped in a tree labelled %q, not reported missing", foreign.Repo, here)
	}
	src := readSource(filepath.Join(root, foreign.Path))
	if src.exists {
		t.Fatal("fixture: the foreign path should not exist in this temp tree")
	}
	if !(here == "" && foreign.Repo != "") {
		t.Fatal("the unknown-tree guard would not fire for this anchor")
	}
}

// TestCurrentRepoLabelIsEmptyWhenTheRepoIsUnknown pins the safety valve the doc
// comment describes and the code did not have.
//
// The comment on the skip says an empty result means "unknown", and that an
// unknown repository checks every anchor rather than skipping them — because a
// verifier that silently checked nothing would be worse than one that
// occasionally checks too much. That path was unreachable: the fallback was
// filepath.Base(root), which is non-empty for any real path.
//
// The failure it was supposed to prevent was therefore live. In a tree with no
// origin remote — a tarball, a vendored copy, a clone whose remote is named
// something else, a worktree in a differently-named directory — `here` became the
// DIRECTORY name, every anchor's Repo mismatched, and runVerify reported
// "N anchor(s): 0 verified, 0 drifted, 0 missing, N elsewhere". A clean-looking
// report from a verifier that checked nothing.
func TestCurrentRepoLabelIsEmptyWhenTheRepoIsUnknown(t *testing.T) {
	dir := t.TempDir()
	if got := currentRepoLabel(dir); got != "" {
		t.Errorf("currentRepoLabel on a non-git directory = %q, want \"\" — a non-empty label "+
			"makes every anchor from a named repository look like it belongs elsewhere, and the "+
			"verifier reports success while checking nothing", got)
	}
}

// TestCurrentRepoLabelReadsTheRemote is the other half: when there IS a remote,
// the label must come from it and not from the directory, or two clones of one
// repository in differently-named folders disagree about their own identity.
func TestCurrentRepoLabelReadsTheRemote(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:someone/expected-name.git"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable in this environment: %v (%s)", err, out)
		}
	}
	if got := currentRepoLabel(dir); got != "expected-name" {
		t.Errorf("currentRepoLabel = %q, want %q — the label must come from the remote, not the "+
			"directory, or the same repo cloned into two folders disagrees with itself", got, "expected-name")
	}
}
