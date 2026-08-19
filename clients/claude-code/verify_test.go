package main

import (
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
