package palace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestEveryDeclaredArmIsRegistered fails when an `Arm…` constant exists that no
// code path ever adds to the arms slice.
//
// This is the mechanical form of the defect that has cost this project the most.
// ArmProduction — the arm whose entire purpose is to score the code path agents
// actually call — was declared, documented at length, and never appended, so it
// appeared in no table for as long as it existed. Nothing caught it: it compiled,
// every test passed, and the eval printed a full and plausible report without it.
// A human eventually noticed a missing row.
//
// The check is deliberately syntactic rather than behavioural. Running an eval to
// see which arms appear needs a corpus, an embedder and a reranker, so it cannot
// be a unit test; but "is this identifier mentioned in the function that builds
// the arms list" needs nothing and catches the same thing. An arm that is
// conditionally registered still passes, which is correct — ArmContextual is only
// added behind a flag, and that is a decision rather than an oversight.
func TestEveryDeclaredArmIsRegistered(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "eval.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse eval.go: %v", err)
	}

	declared := map[string]token.Pos{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// Only constants whose declared type is EvalArm.
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "EvalArm" {
				continue
			}
			for _, n := range vs.Names {
				if strings.HasPrefix(n.Name, "Arm") {
					declared[n.Name] = n.Pos()
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no EvalArm constants — this check has stopped checking anything")
	}

	// Collect every identifier mentioned inside the function that assembles the
	// arms list, which is where registration happens.
	registered := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "EvaluateWith" {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			if id, ok := inner.(*ast.Ident); ok {
				registered[id.Name] = true
			}
			return true
		})
		return false
	})

	for name, pos := range declared {
		if !registered[name] {
			t.Errorf("%s: %s is declared but never registered in EvaluateWith — it will appear in no eval table, silently",
				fset.Position(pos), name)
		}
	}
}

// TestSweptArmsAreReachable pins the two swept families the same way. They are
// built by helper functions rather than named constants, so the check is that the
// helpers are called at all: a sweep whose constructor is never invoked would
// report a table that silently omits a whole family of configurations.
func TestSweptArmsAreReachable(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "eval.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse eval.go: %v", err)
	}
	called := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			called[id.Name] = true
		}
		return true
	})
	for _, fn := range []string{"bm25Arm", "rerankArm"} {
		if !called[fn] {
			t.Errorf("%s is never called — the sweep it names is declared but produces no arm", fn)
		}
	}
}
