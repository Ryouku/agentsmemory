package mcpcli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestEveryGenericCLIUsesTheSharedRunner(t *testing.T) {
	adapters := map[string]string{
		filepath.Clean("../../cmd/server/mcp.go"):              "runMCP",
		filepath.Clean("../../clients/claude-code/mcpcall.go"): "runRemoteMCP",
	}
	for path, functionName := range adapters {
		t.Run(functionName, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var target *ast.FuncDecl
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && function.Name.Name == functionName {
					target = function
					break
				}
			}
			if target == nil {
				t.Fatalf("%s is not declared in %s", functionName, path)
			}

			runs := 0
			forbidden := map[string]int{}
			ast.Inspect(target.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "mcpcli" {
					return true
				}
				if selector.Sel.Name == "Run" {
					runs++
				}
				switch selector.Sel.Name {
				case "FindTool", "IsReadOnly", "ParseArgs", "PrimaryArg", "PrintCallResult", "PrintResult", "PrintTools":
					forbidden[selector.Sel.Name]++
				}
				return true
			})
			if runs != 1 {
				t.Errorf("%s calls mcpcli.Run %d times, want exactly one shared execution path", functionName, runs)
			}
			for helper, count := range forbidden {
				t.Errorf("%s calls mcpcli.%s %d time(s) beside Run; policy/parse/render must stay inside the shared runner", functionName, helper, count)
			}
		})
	}
}
