package mcpserver

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEveryArgumentAHandlerReadsIsDeclared fails when a tool's handler reads an
// argument the tool does not advertise.
//
// This is the reachability defect in its tool-argument form, and it is the one
// shape no end-to-end test can catch. `am_update_drawer` read `code_anchors`
// from the day it was written and never declared it. The handler worked. A test
// that passes the argument works, because mcp-go forwards undeclared arguments
// untouched — so even a real client call through the real transport passes. What
// does NOT work is the only thing that matters: an agent reads the schema to
// learn what a tool accepts, finds no `code_anchors`, and never sends it. The
// capability was complete, tested, and undiscoverable, which is this
// repository's characteristic defect wearing its least visible costume.
//
// It has to be a source check rather than a behavioural one for exactly that
// reason: the behaviour is correct. Only the advertisement is missing, and the
// party it is missing for is a reader, not a caller.
func TestEveryArgumentAHandlerReadsIsDeclared(t *testing.T) {
	fset := token.NewFileSet()
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			tool, declared := declaredParams(fn)
			if tool == "" {
				continue // not a tool registration
			}
			checked++
			var undeclared []string
			for _, read := range readParams(fn) {
				if !declared[read] {
					undeclared = append(undeclared, read)
				}
			}
			sort.Strings(undeclared)
			if len(undeclared) > 0 {
				t.Errorf("tool %q reads argument(s) it never declares: %s\n"+
					"  An agent reads the schema to learn what a tool accepts. An argument the\n"+
					"  handler honours but the tool does not advertise is a capability nobody can\n"+
					"  discover — and no end-to-end test finds it, because a test that sends the\n"+
					"  argument works fine.", tool, strings.Join(undeclared, ", "))
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no tool registrations — this check has stopped checking anything")
	}
}

// declaredParams returns the tool a function registers and the argument names it
// advertises, or "" when the function registers no tool.
func declaredParams(fn *ast.FuncDecl) (string, map[string]bool) {
	name := ""
	declared := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if f.Name == "newTool" {
				if lit := stringLit(call.Args[0]); lit != "" && name == "" {
					name = lit
				}
			}
		case *ast.SelectorExpr:
			// mcp.WithString / WithArray / WithBoolean / WithNumber / WithObject…
			if id, ok := f.X.(*ast.Ident); ok && id.Name == "mcp" && strings.HasPrefix(f.Sel.Name, "With") {
				if lit := stringLit(call.Args[0]); lit != "" {
					declared[lit] = true
				}
			}
		}
		return true
	})
	return name, declared
}

// readParams returns the argument names a handler reads.
func readParams(fn *ast.FuncDecl) []string {
	var out []string
	ast.Inspect(fn, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IndexExpr:
			// Only indexing of the ARGUMENTS map counts. Writing into a response
			// map is an index expression too, and counting those reported five
			// tools as reading "warning" — a check that cries wolf on correct code
			// is one people learn to skip, so it is narrowed to the receiver.
			if !isArgumentsMap(v.X) {
				return true
			}
			if lit := stringLit(v.Index); lit != "" {
				out = append(out, lit)
			}
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok || len(v.Args) == 0 {
				return true
			}
			switch sel.Sel.Name {
			case "GetString", "GetBool", "GetInt", "GetFloat", "GetArguments",
				"RequireString", "RequireBool", "RequireInt", "RequireFloat":
				if lit := stringLit(v.Args[0]); lit != "" {
					out = append(out, lit)
				}
			}
		}
		return true
	})
	return out
}

// isArgumentsMap reports whether an expression is the tool-call arguments map:
// the local conventionally named `args`, or `req.GetArguments()` inline.
func isArgumentsMap(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name == "args"
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			return sel.Sel.Name == "GetArguments"
		}
	}
	return false
}

func stringLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return strings.Trim(lit.Value, `"`)
}
