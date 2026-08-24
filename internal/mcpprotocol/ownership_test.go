package mcpprotocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWireConstantsHaveOneLiteralOwner(t *testing.T) {
	root := filepath.Clean("../..")
	wantOwner := filepath.ToSlash("internal/mcpprotocol/constants.go")
	literals := map[string]string{
		ToolPrefix:       "ToolPrefix",
		WingHeader:       "WingHeader",
		TokenEnvVar:      "TokenEnvVar",
		LocalTokenEnvVar: "LocalTokenEnvVar",
		WingEnvVar:       "WingEnvVar",
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(rel) == wantOwner {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			if name, duplicated := literals[value]; duplicated {
				t.Errorf("%s repeats %s wire literal; import internal/mcpprotocol", filepath.ToSlash(rel), name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
