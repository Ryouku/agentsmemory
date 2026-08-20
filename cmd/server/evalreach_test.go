package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestEvalOutputsAreReachableFromTheCommand fails when a function that produces
// part of an eval run's output is written but never called.
//
// This is the defect this repository is named after, and ADR-003 T2 walked
// straight into it: printClosetBlock, writeCells, cellsPath and
// readCasesWithMeta were all written, all unit-tested, all green — and none of
// them was called from runEval. Every test passed. The block would never have
// printed, the run record would never have been written, and the evidence
// directory the ADR is decided from would have stayed empty.
//
// Unit tests cannot catch this by construction: they call the function
// directly, which is exactly what the command was failing to do. The check has
// to be about SELECTION, so it reads the command's own body.
func TestEvalOutputsAreReachableFromTheCommand(t *testing.T) {
	// Each entry is a producer of run output and the function that must reach
	// it. A new one goes here when it is written, or it can ship unreachable.
	mustCall := map[string]string{
		"printEvalTable":    "runEval",
		"printClosetBlock":  "runEval",
		"writeResults":      "runEval",
		"writeCells":        "runEval",
		"cellsPath":         "runEval",
		"resultsPath":       "runEval",
		"readCasesWithMeta": "loadOrGenerateCases",
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "eval.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse eval.go: %v", err)
	}

	called := map[string]map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		names := map[string]bool{}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				names[id.Name] = true
			}
			return true
		})
		called[fn.Name.Name] = names
		return false
	})

	for producer, caller := range mustCall {
		body, ok := called[caller]
		if !ok {
			t.Errorf("%s is not a function in eval.go, so nothing checks that %s is reached — fix this map", caller, producer)
			continue
		}
		if !body[producer] {
			t.Errorf("%s is never called from %s: it is written, it is unit-tested, and it produces nothing at runtime", producer, caller)
		}
	}
}
