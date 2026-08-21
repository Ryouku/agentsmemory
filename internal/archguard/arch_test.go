// Package archguard holds no code. It exists so the dependency rules in
// docs/architecture.md have somewhere to be enforced from, because a direction
// rule written only in prose is re-read by every contributor and re-interpreted
// by each of them.
//
// The rules here were not invented. They were measured off the real import graph
// (`go list -json ./...`) at the point the architecture document was written, so
// each one records a property the tree ALREADY had. That is deliberate: a rule
// introduced together with the violations it forbids is a wish, and a rule that
// starts green is a ratchet.
package archguard

import (
	"go/build"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/atvirokodosprendimai/agentsmemory"

// rule is one direction contract: packages matching From must not import any
// package matching To.
type rule struct {
	id     string
	from   func(string) bool
	to     func(string) bool
	reason string
	// heldBy says what actually prevents the violation. Measured, not assumed:
	// each rule was violated on purpose and the result recorded. Three of the five
	// turned out to be enforced by the Go toolchain, so this test does not hold
	// them and must not be read as if it did — cmd/server and clients/claude-code
	// are `package main` and cannot be imported at all, and internal/store
	// importing a backend is an import cycle. A test that cannot fail is
	// indistinguishable from one that passed, which is the defect this repository
	// keeps finding; naming it here is cheaper than pretending otherwise.
	heldBy heldBy
}

type heldBy string

const (
	// byThisTest: a compiling program can violate this rule, and only this test
	// catches it. Proven by writing the violating import and watching it go red.
	byThisTest heldBy = "this test"
	// byCompiler: the violation does not build. The rule is documentation of an
	// invariant the toolchain already guarantees, kept because a reader of the
	// architecture doc needs to know the direction, not because it is enforced here.
	byCompiler heldBy = "the Go compiler"
)

func is(pkg string) func(string) bool { return func(p string) bool { return p == pkg } }
func under(prefix string) func(string) bool {
	return func(p string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }
}

// strictlyUnder matches packages BELOW a prefix but not the prefix itself, which
// is what a "the parent must not import its children" rule needs. Writing it as
// under("internal/store/") looked right and matched nothing — the trailing slash
// made every comparison fail — so the rule forbade a set that could not exist and
// passed for that reason. TestEveryRuleCanFail is what caught it.
func strictlyUnder(prefix string) func(string) bool {
	return func(p string) bool { return strings.HasPrefix(p, prefix+"/") }
}

func anyOf(fs ...func(string) bool) func(string) bool {
	return func(p string) bool {
		for _, f := range fs {
			if f(p) {
				return true
			}
		}
		return false
	}
}

// surfaces are the packages that adapt the domain to an outside caller: a
// transport, a dashboard, a background job, an export format. The domain must not
// know any of them.
var surfaces = anyOf(
	under("internal/mcpserver"), under("internal/web"), under("internal/oauth"),
	under("internal/billing"), under("internal/share"), under("internal/mergejob"),
	under("internal/importer"), under("internal/dataexport"), under("internal/wingbundle"),
	under("internal/embedworker"), under("internal/mcptest"),
)

var rules = []rule{
	{
		id: "D1", from: func(string) bool { return true }, to: under("cmd"),
		reason: "cmd/server is the composition root: it constructs every adapter and is constructed " +
			"by nothing. A package that imports it has made the root a library, and the wiring " +
			"stops being findable in one place.",
		heldBy: byCompiler,
	},
	{
		id: "D2", from: is("internal/palace"), to: surfaces,
		reason: "internal/palace is the memory domain. It must not know which transport asked. A " +
			"domain that imports a surface cannot be exercised without standing that surface up, " +
			"which is how a test ends up asserting the transport instead of the rule.",
		heldBy: byThisTest,
	},
	{
		id: "D3", from: is("internal/tenant"), to: under("internal"),
		reason: "identity is a leaf. Every surface resolves a tenant, so anything tenant imported " +
			"would be imported by everything; keeping it at zero is what lets auth, usage, web and " +
			"the MCP server share it without a cycle.",
		heldBy: byThisTest,
	},
	{
		id: "D4", from: is("internal/store"), to: strictlyUnder("internal/store"),
		reason: "internal/store declares VectorStore and SourceOfTruth; the backends implement them. " +
			"The dependency points inward — sqlitevec, chromemvec and qdrant import store, never the " +
			"reverse — so adding a backend touches no existing one.",
		heldBy: byCompiler,
	},
	{
		id: "D5", from: under("internal"), to: under("clients"),
		reason: "clients/ ships the agent-side kit and is a consumer of the server's HTTP surface, " +
			"not a library the server may reach into. An import here would make the server's " +
			"behaviour depend on what an installer happens to write.",
		heldBy: byCompiler,
	},
}

// TestModuleDependenciesObeyTheContract walks the real import graph and fails on
// any edge a rule forbids.
//
// It reads the SOURCE TREE rather than a list of packages kept beside it, for the
// same reason every other gate here does: a list somebody has to remember is the
// thing that fails. A package added tomorrow is covered without anyone editing
// this file.
func TestModuleDependenciesObeyTheContract(t *testing.T) {
	root := repoRoot(t)
	graph := importGraph(t, root)
	if len(graph) < 20 {
		t.Fatalf("only %d packages found under %s — this check has stopped reading the tree", len(graph), root)
	}

	checked := 0
	for _, r := range rules {
		matched := 0
		for pkg, imports := range graph {
			if !r.from(pkg) {
				continue
			}
			matched++
			for _, imp := range imports {
				if pkg == imp || !r.to(imp) {
					continue
				}
				t.Errorf("%s violated: %s imports %s\n  %s", r.id, pkg, imp, r.reason)
			}
		}
		if matched == 0 {
			t.Errorf("%s matched no package at all — the rule names a package that no longer exists, "+
				"so it is enforcing nothing while reporting green", r.id)
		}
		checked++
	}
	if checked != len(rules) {
		t.Fatalf("%d of %d rules ran", checked, len(rules))
	}
	// At least one rule must be one this test actually holds. If every remaining
	// rule is compiler-enforced, this file has become a comment with a test
	// framework around it, and should be read that way rather than trusted.
	live := 0
	for _, r := range rules {
		if r.heldBy == byThisTest {
			live++
		}
	}
	if live == 0 {
		t.Error("every rule is compiler-enforced: this test enforces nothing and its green is " +
			"not evidence about the architecture")
	}
}

// TestEveryRuleCanFail is the ratchet's own guard: a rule whose From side matches
// nothing, or whose To side can never match any package in the tree, is a rule
// that passes for the wrong reason. Both halves must be able to select something.
//
// It caught D4 on its first run — written as under("internal/store/"), the
// trailing slash made every comparison fail, so the rule forbade an empty set and
// went green for that reason.
func TestEveryRuleCanFail(t *testing.T) {
	graph := importGraph(t, repoRoot(t))
	var pkgs []string
	for p := range graph {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	for _, r := range rules {
		from, to := 0, 0
		for _, p := range pkgs {
			if r.from(p) {
				from++
			}
			if r.to(p) {
				to++
			}
		}
		if from == 0 {
			t.Errorf("%s: its From side matches no package in the tree", r.id)
		}
		if to == 0 {
			t.Errorf("%s: its To side matches no package in the tree, so the rule forbids "+
				"something that does not exist and can never fail", r.id)
		}
	}
}

// importGraph maps each first-party package (module-relative) to the first-party
// packages it imports, test files excluded — a test may import anything.
func importGraph(t *testing.T, root string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	skip := map[string]bool{".git": true, "dist": true, "node_modules": true, "vendor": true, ".claude": true}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			if skip[fi.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		dir := filepath.Dir(p)
		rel, rerr := filepath.Rel(root, dir)
		if rerr != nil {
			return nil
		}
		pkg, perr := build.ImportDir(dir, build.ImportComment)
		if perr != nil {
			return nil
		}
		var deps []string
		for _, imp := range pkg.Imports {
			if strings.HasPrefix(imp, module) {
				deps = append(deps, strings.TrimPrefix(strings.TrimPrefix(imp, module), "/"))
			}
		}
		sort.Strings(deps)
		out[filepath.ToSlash(rel)] = deps
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		d = filepath.Dir(d)
	}
	t.Fatal("no go.mod above the working directory")
	return ""
}
