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
// The same sweep then found five more: an eval type wearing Evaluate's
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
		for _, decl := range file.Decls {
			name, doc, pos := declInfo(decl)
			if name == "" || doc == nil || !ast.IsExported(name) {
				continue
			}
			first := firstWord(doc)
			if first == "" || first == name || first == "Deprecated:" {
				continue
			}
			if !declared[first] && !looksLikeIdentifier(first) {
				continue // ordinary prose, not a drifted name
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			problems = append(problems, rel+":"+itoa(fset.Position(pos).Line)+
				": the doc comment on "+name+" opens with "+first+
				" — it documents another declaration, so `go doc "+name+"` prints the wrong text")
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
	line := strings.TrimSpace(strings.TrimPrefix(doc.List[0].Text, "//"))
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
