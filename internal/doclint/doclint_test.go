// Package doclint holds one repository-wide check: that a doc comment documents
// the declaration it is attached to.
//
// It exists because a reader caught what every automated gate missed. A setter
// was inserted BETWEEN an existing function's doc comment and its declaration,
// which left the original function undocumented and made `go doc` print its
// paragraph as if it described the new one. gofmt reformats it happily, the
// compiler has no opinion, and a 27-agent review read straight past it — the
// text is still there and still true, just attached to the wrong thing. Only
// `go doc` or a human notices, and a human noticing is not a gate.
//
// It then recurred in a position the first version of this check could not
// see: a new STRUCT FIELD landed between an existing field's comment and its
// declaration. Same defect, one nesting level down — which is why this walks
// struct and interface bodies too, and why fields are checked whether they are
// exported or not.
//
// The first sweep found five more: an eval type wearing Evaluate's
// documentation (leaving Evaluate undocumented), a renamed function still
// carrying its old name's comment describing behaviour it no longer has, and
// three tests whose comments had drifted onto their neighbours.
package doclint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// TestDocCommentsMatchTheirDeclaration walks every Go file in the module and
// fails when an exported declaration's doc comment opens with the name of a
// DIFFERENT declaration.
//
// The rule is deliberately narrow, because a noisy gate gets deleted. Comments
// that open in prose ("A stale lock file is reusable…") are not flagged: Go's
// convention prefers the name, but violating style is not the defect this
// catches. What is flagged is a first word that either names another
// declaration in the same file — the drift signature — or is unmistakably an
// identifier rather than a word, meaning two or more capitals inside it. That
// second half catches the renamed-function case, where the name a comment opens
// with no longer exists anywhere.
func TestDocCommentsMatchTheirDeclaration(t *testing.T) {
	root := moduleRoot(t)
	var problems []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			// Vendored and generated trees are not ours to hold to this rule,
			// and .claude holds agent worktrees — copies of this very repo,
			// whose findings would be reported twice.
			case ".git", "vendor", "node_modules", "testdata", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			// Unparseable Go is the compiler's complaint to make, not this
			// test's — it would only duplicate a clearer error.
			return nil
		}
		declared := declaredNames(file)
		for _, d := range documented(file) {
			if d.name == "" || d.doc == nil {
				continue
			}
			// Struct fields are checked whether exported or not: an unexported
			// field wearing its neighbour's comment misleads the next reader of
			// this package exactly as much, and this package is where the
			// reasoning lives.
			// Unexported declarations are checked too, since 6f17446f: the live
			// defect this gate exists for landed on an UNEXPORTED method — a
			// 320-line evalCase lost its doc comment to a var inserted below it,
			// and the gate stayed green precisely because of this skip. The
			// argument already made for struct fields ("an unexported field
			// wearing its neighbour's comment misleads the next reader of the
			// package just as much") applies unchanged. Measured cost before
			// removing it: 4 sites in 1,141 documented unexported declarations.
			first := firstWord(d.doc)
			// "Deprecated" without the colon: firstWord already trims trailing
			// punctuation, so the old comparison against "Deprecated:" could
			// never match and read as load-bearing while doing nothing.
			if first == "" || first == d.name || first == "Deprecated" {
				continue
			}
			// A comment opening with a PREFIX of the name is describing the same
			// thing, not a different one: "OpenCollective wiring (used when …)"
			// heads a group whose only member is OpenCollectiveProjectURL. Every
			// real drift found so far fails this test — Evaluate is no prefix of
			// Progress, ImportClosets none of AbsorbClosets — so the exemption
			// costs no detection.
			if strings.HasPrefix(d.name, first) {
				continue
			}
			if !declared[first] && !looksLikeIdentifier(first) {
				continue // ordinary prose, not a drifted name
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			problems = append(problems, rel+":"+itoa(fset.Position(d.pos).Line)+
				": the doc comment on "+d.name+" opens with "+first+
				" — it documents something else, so a reader of "+d.name+" gets the wrong text")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	for _, p := range problems {
		t.Error(p)
	}
	if len(problems) > 0 {
		t.Log("fix: give each declaration its own comment, or move the drifted one back to what it describes (`go doc <name>` shows what a reader gets)")
	}
}

// documentedDecl is one thing that can carry a doc comment: a top-level
// declaration, or a field inside a struct or interface. Fields matter as much as
// declarations here — the second recurrence of this defect put a new struct
// field between an existing field's comment and its declaration, a position a
// declaration-only check cannot see.
type documentedDecl struct {
	name  string
	doc   *ast.CommentGroup
	pos   token.Pos
	field bool
}

// documented lists everything in a file that carries a doc comment, descending
// into struct and interface bodies.
func documented(file *ast.File) []documentedDecl {
	var out []documentedDecl
	for _, decl := range file.Decls {
		name, doc, pos := declInfo(decl)
		if name != "" {
			out = append(out, documentedDecl{name: name, doc: doc, pos: pos})
		}
		// Grouped const/var/type blocks: each spec's OWN comment is checked
		// against that spec. declInfo deliberately skips the block, and the
		// reason it gives — per-spec comments belong to the spec — is the
		// argument for walking the specs, not for skipping them. This is the
		// likeliest position for the defect to occur at all: inserting an entry
		// above an existing one is exactly how a comment ends up over the wrong
		// declaration, and a grouped block is where entries get inserted.
		gd, ok := decl.(*ast.GenDecl)
		if !ok || len(gd.Specs) < 2 {
			continue
		}
		for _, sp := range gd.Specs {
			switch spec := sp.(type) {
			case *ast.ValueSpec:
				if spec.Doc != nil && len(spec.Names) > 0 {
					out = append(out, documentedDecl{name: spec.Names[0].Name, doc: spec.Doc, pos: spec.Pos()})
				}
			case *ast.TypeSpec:
				if spec.Doc != nil {
					out = append(out, documentedDecl{name: spec.Name.Name, doc: spec.Doc, pos: spec.Pos()})
				}
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		var fields *ast.FieldList
		switch t := n.(type) {
		case *ast.StructType:
			fields = t.Fields
		case *ast.InterfaceType:
			fields = t.Methods
		default:
			return true
		}
		if fields == nil {
			return true
		}
		for _, f := range fields.List {
			if f.Doc == nil || len(f.Names) == 0 {
				continue
			}
			out = append(out, documentedDecl{name: f.Names[0].Name, doc: f.Doc, pos: f.Pos(), field: true})
		}
		return true
	})
	return out
}

// declInfo returns the name, doc comment and position of a declaration, or an
// empty name for the kinds this check does not cover — grouped const/var/type
// blocks, whose per-spec comments belong to the spec rather than the block.
func declInfo(decl ast.Decl) (string, *ast.CommentGroup, token.Pos) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return d.Name.Name, d.Doc, d.Pos()
	case *ast.GenDecl:
		if d.Doc == nil || len(d.Specs) != 1 {
			return "", nil, token.NoPos
		}
		switch spec := d.Specs[0].(type) {
		case *ast.TypeSpec:
			return spec.Name.Name, d.Doc, d.Pos()
		case *ast.ValueSpec:
			if len(spec.Names) > 0 {
				return spec.Names[0].Name, d.Doc, d.Pos()
			}
		}
	}
	return "", nil, token.NoPos
}

// declaredNames collects every top-level name a file declares, including the
// members of grouped const/var blocks: a comment that opens with any of them is
// documenting that one, not the declaration it sits on.
func declaredNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.StructType:
			for _, f := range t.Fields.List {
				for _, fn := range f.Names {
					names[fn.Name] = true
				}
			}
		case *ast.InterfaceType:
			for _, m := range t.Methods.List {
				for _, mn := range m.Names {
					names[mn.Name] = true
				}
			}
		}
		return true
	})
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			names[d.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names
}

// firstWord returns the first word of a doc comment's first line, stripped of
// the comment marker and trailing punctuation, so "// Foo: does x" yields "Foo".
func firstWord(doc *ast.CommentGroup) string {
	if len(doc.List) == 0 {
		return ""
	}
	// Both comment forms. Stripping only "//" left a block comment's first word
	// as "/*", which is neither a declared name nor CamelCase, so every
	// /* Foo does x */ hijack fell out through the prose exemption.
	raw := doc.List[0].Text
	raw = strings.TrimPrefix(raw, "//")
	raw = strings.TrimPrefix(raw, "/*")
	raw = strings.TrimSuffix(raw, "*/")
	line := strings.TrimSpace(raw)
	if line == "" && len(doc.List) == 1 {
		// A `/*` on its own line: the text starts on the next one.
		for _, l := range strings.Split(doc.List[0].Text, "\n")[1:] {
			l = strings.TrimSpace(strings.TrimSuffix(l, "*/"))
			if l != "" {
				line = l
				break
			}
		}
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimRight(fields[0], ".,:;")
}

// looksLikeIdentifier reports whether a word is unmistakably a Go name rather
// than an English one: two or more capitals mean CamelCase. One capital is just
// a sentence opening, which is why "Releasing the lock…" does not trip this.
func looksLikeIdentifier(w string) bool {
	capitals := 0
	for _, r := range w {
		if unicode.IsUpper(r) {
			capitals++
		}
	}
	return capitals >= 2
}

// moduleRoot walks up from this package to the directory holding go.mod, so the
// check covers the whole module however `go test` was invoked.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
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

// itoa keeps the failure message free of a fmt import for one integer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// hijacked runs the same detection the repo-wide check runs, over one parsed
// file, and returns the names it flags. It exists so the gate's own coverage can
// be tested against a fixture instead of against whatever the tree happens to
// contain today.
func hijacked(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	declared := declaredNames(file)
	var out []string
	for _, d := range documented(file) {
		if d.name == "" || d.doc == nil {
			continue
		}
		// Same rule as the repo-wide check: unexported declarations count.
		first := firstWord(d.doc)
		if first == "" || first == d.name || first == "Deprecated" {
			continue
		}
		if strings.HasPrefix(d.name, first) {
			continue
		}
		if !declared[first] && !looksLikeIdentifier(first) {
			continue
		}
		out = append(out, d.name)
	}
	sort.Strings(out)
	return out
}

// TestGateSeesGroupedSpecsAndBlockComments pins the two positions this check was
// blind to, both reported by review and both since shipped into.
//
// `declInfo` returned an empty name for any GenDecl with more than one spec, and
// `documented`'s walk descended only into struct and interface bodies — so a
// grouped `const (…)` or `var (…)` block was visited by nothing. That is the
// likeliest place for the defect to occur at all: inserting a new entry above an
// existing one is exactly how a comment ends up over the wrong declaration, and a
// grouped block is where entries get inserted.
//
// `firstWord` stripped only `//`, so `/* Foo does x */` yielded `"/*"`, which is
// neither a declared name nor CamelCase and fell out through the prose exemption.
//
// The fixture is the reviewer's, kept verbatim in shape: four hijacks, one per
// position. Before the fix this returned one of the four.
func TestGateSeesGroupedSpecsAndBlockComments(t *testing.T) {
	const src = `package probe

const (
	// RealConst is the constant everyone reads.
	InsertedConst = 5
	RealConst     = 60
)

var (
	// RealVar is the shared registry.
	InsertedVar = map[string]bool{}
	RealVar     = map[string]bool{}
)

/*
ExportedFn does the thing.
*/
func BlockCommentVictim() {}

// ExportedFn is the real one.
func ExportedFn() {}

type Holder struct {
	// ExportedFn is the drifted field comment.
	Inserted int
	Real     int
}
`
	got := hijacked(t, src)
	want := []string{"BlockCommentVictim", "Inserted", "InsertedConst", "InsertedVar"}
	if len(got) != len(want) {
		t.Fatalf("flagged %v, want %v — a position the check cannot see is a position the defect can live in", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("flagged %v, want %v", got, want)
			break
		}
	}
}

// TestGateLeavesCorrectGroupedCommentsAlone is the other half: the fix must not
// start flagging the ordinary case, where a grouped block's per-spec comments
// each name their own spec. A gate with false alarms is one people delete.
func TestGateLeavesCorrectGroupedCommentsAlone(t *testing.T) {
	const src = `package probe

const (
	// ArmVector is the baseline.
	ArmVector = "vector"
	// ArmHybrid adds fusion.
	ArmHybrid = "hybrid"
)

var (
	// Registry holds the arms.
	Registry = map[string]bool{}
)

// Prose about the package that happens to start with a capital word.
const Threshold = 3
`
	if got := hijacked(t, src); len(got) != 0 {
		t.Errorf("flagged %v on a file where every comment names its own declaration", got)
	}
}
