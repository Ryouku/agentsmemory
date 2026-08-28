package repohygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specBindingRE matches the `path/to/file_test.go::TestName` form a spec's Facts
// table and scenario headings use to bind an assertion to the test that proves it.
var specBindingRE = regexp.MustCompile(`([A-Za-z0-9_./-]+_test\.go)::(Test[A-Za-z0-9_]+)`)

// TestEverySpecBindingNamesATestThatExists closes the one hole a spec's own gate
// cannot see.
//
// ⚠ A BINDING IS A POINTER, AND `spec-verify` NEVER FOLLOWS IT. It parses the
// heading grammar and the Facts table and checks that a binding is PRESENT; it
// does not open the file or look for the function. Renaming a bound test — or
// deleting the stub entirely — leaves `spec-verify --draft` at [PASS], so the
// document goes on claiming a fact is proved by a test nothing runs. Found in
// review of the read-cost spec 2026-08-28, which is the first spec in this tree
// whose bindings live behind a build tag and are therefore invisible to the
// default lane as well.
//
// Build tags are irrelevant here on purpose: this walks the source with
// go/parser rather than running anything, so a deliberately-red binding parked
// behind `-tags readcostspec` is checked exactly like a green one. That is the
// property that makes the gate useful during the @spec phase, when by definition
// no bound test passes yet.
func TestEverySpecBindingNamesATestThatExists(t *testing.T) {
	root := repoRoot(t)
	specs, err := filepath.Glob(filepath.Join(root, "docs", "specs", "*.md"))
	if err != nil {
		t.Fatalf("glob specs: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("no specs found under docs/specs — this gate derives its universe from that " +
			"directory, so an empty result means the path moved, not that there is nothing to check")
	}

	// declaredTests caches one parse per test file, because several bindings
	// usually name the same file.
	declared := map[string]map[string]bool{}
	testsIn := func(path string) (map[string]bool, error) {
		if got, ok := declared[path]; ok {
			return got, nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, err
		}
		names := map[string]bool{}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if ok && fn.Recv == nil {
				names[fn.Name.Name] = true
			}
		}
		declared[path] = names
		return names, nil
	}

	checked := 0
	for _, spec := range specs {
		body, err := os.ReadFile(spec)
		if err != nil {
			t.Fatalf("read %s: %v", spec, err)
		}
		rel, _ := filepath.Rel(root, spec)
		seen := map[string]bool{}
		for _, m := range specBindingRE.FindAllStringSubmatch(string(body), -1) {
			binding := m[0]
			if seen[binding] {
				continue
			}
			seen[binding] = true
			checked++

			file := filepath.Join(root, filepath.FromSlash(m[1]))
			names, err := testsIn(file)
			if err != nil {
				t.Errorf("%s binds %s, but %s cannot be read: %v\n"+
					"A binding is the only route from an assertion to its proof. A path that "+
					"does not resolve reads as provenance and is worth nothing.", rel, binding, m[1], err)
				continue
			}
			if !names[m[2]] {
				t.Errorf("%s binds %s, but %s declares no func %s\n"+
					"spec-verify checks the binding is PRESENT, never that it RESOLVES, so this "+
					"stayed [PASS] while the fact was proved by nothing. Rename the binding or "+
					"add the stub — a failing stub is the correct @spec state.", rel, binding, m[1], m[2])
			}
		}
	}

	// A self-extracted universe is worth exactly what the extraction is worth, so
	// prove the regex matched something before reporting all-clear.
	if checked == 0 {
		t.Errorf("parsed %d spec(s) and found ZERO bindings — a green run here would mean the "+
			"binding syntax changed, not that every binding resolves", len(specs))
	}
	// ⚠ Report the count only when the verdict is clean. A summary line that says
	// "N bindings resolve" underneath a failure is the shape that let a disabled
	// gate stay green and announce all-clear over a real offender.
	if !t.Failed() {
		t.Logf("%d binding(s) across %d spec(s) resolve to a declared test", checked, len(specs))
	}
}

// TestASpecBindingThatNamesNothingIsCaught is the falsifiability half.
//
// A corpus with zero broken bindings cannot exercise the branch that reports one,
// so the gate above would pass identically if its check were deleted. This drives
// the same logic over a fixture that IS broken, through a substitutable
// testing.TB, because a test cannot pin its own reporting.
func TestASpecBindingThatNamesNothingIsCaught(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n\nimport \"testing\"\n\nfunc TestRealOne(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dir, "sample_test.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
			names[fn.Name.Name] = true
		}
	}

	spec := "| F-1 | thing | `sample_test.go::TestRealOne` | @spec |\n" +
		"| F-2 | other | `sample_test.go::TestRenamedAway` | @spec |\n"
	var missing []string
	for _, m := range specBindingRE.FindAllStringSubmatch(spec, -1) {
		if !names[m[2]] {
			missing = append(missing, m[0])
		}
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "TestRenamedAway") {
		t.Errorf("the check did not catch a binding naming no declared test: missing=%v\n"+
			"Without this the gate above passes over a clean corpus whatever its body says.", missing)
	}
}
