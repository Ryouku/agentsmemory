package palace

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestChunkRankingUnitCannotBeSelected fails when the retired chunk-ranked
// control returns as a selectable production path. A flag, a setter, or a
// hydrate-after-chunk-rank helper is a second Search.
func TestChunkRankingUnitCannotBeSelected(t *testing.T) {
	banned := map[string]bool{
		"memoryLevelRanking":     true,
		"WithMemoryLevelRanking": true,
		"hydrateResultMemories":  true,
	}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok || !banned[ident.Name] {
				return true
			}
			t.Errorf("%s names %s; the chunk-ranked unit is a second production Search", path, ident.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
