package mcpserver

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

func TestProductionMCPRegistryHasOneOwner(t *testing.T) {
	root := filepath.Clean("../..")
	factories, err := namedTypeFactories(root, "github.com/mark3labs/mcp-go/server", "server", "MCPServer")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		mcpPackage := importedAs(file, "github.com/mark3labs/mcp-go/mcp", "mcp")
		serverPackage := importedAs(file, "github.com/mark3labs/mcp-go/server", "server")
		memoryServerPackage := importedAs(file, "github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver", "mcpserver")
		serverFields := fieldsOfType(file, serverPackage, "MCPServer")
		serverFactories := factories[packageKey(rel, file)]
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			serverValues := valuesOfType(function, serverPackage, "MCPServer", serverFactories)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "New" && rel == "internal/mcpserver/server.go" {
					if function.Name.Name != "Compose" {
						t.Errorf("%s calls New in %s; only Compose may construct the MCP server", function.Name.Name, rel)
					}
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, _ := selector.X.(*ast.Ident)
				switch {
				case selector.Sel.Name == "NewTool" && pkg != nil && pkg.Name == mcpPackage:
					if rel != "internal/mcpserver/server.go" {
						t.Errorf("%s.%s owns MCP registration in %s; only internal/mcpserver/server.go may construct/register tools", function.Name.Name, selector.Sel.Name, rel)
					}
				case selector.Sel.Name == "NewMCPServer" && pkg != nil && pkg.Name == serverPackage:
					if rel != "internal/mcpserver/server.go" {
						t.Errorf("%s.%s owns MCP registration in %s; only internal/mcpserver/server.go may construct/register tools", function.Name.Name, selector.Sel.Name, rel)
					}
				case selector.Sel.Name == "AddTool" && hasServerProvenance(selector.X, serverPackage, serverValues, serverFields, serverFactories):
					if rel != "internal/mcpserver/server.go" {
						t.Errorf("%s.%s owns MCP registration in %s; only internal/mcpserver/server.go may construct/register tools", function.Name.Name, selector.Sel.Name, rel)
					}
				case selector.Sel.Name == "New" && pkg != nil && pkg.Name == memoryServerPackage:
					t.Errorf("%s calls mcpserver.New from %s; production and the harness must use Compose", function.Name.Name, rel)
				case selector.Sel.Name == "Compose" && pkg != nil && pkg.Name == memoryServerPackage:
					productionSeam := rel == "cmd/server/main.go" && function.Name.Name == "productionMCPServer"
					testHarness := rel == "internal/mcptest/harness.go"
					if !productionSeam && !testHarness {
						t.Errorf("%s calls mcpserver.Compose from %s; only productionMCPServer and the harness may", function.Name.Name, rel)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMCPServerProvenanceIncludesProductionFactoryResult(t *testing.T) {
	root := filepath.Clean("../..")
	factories, err := namedTypeFactories(root, "github.com/mark3labs/mcp-go/server", "server", "MCPServer")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "cmd/server/main.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	serverPackage := importedAs(file, "github.com/mark3labs/mcp-go/server", "server")
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "run" {
			continue
		}
		values := valuesOfType(function, serverPackage, "MCPServer", factories[packageKey("cmd/server/main.go", file)])
		if !values["mcpSrv"] {
			t.Fatal("productionMCPServer result mcpSrv has no MCPServer provenance; registry ownership gate can miss the live server")
		}
		return
	}
	t.Fatal("run function not found")
}

func namedTypeFactories(root, importPath, fallback, typeName string) (map[string]map[string]bool, error) {
	factories := make(map[string]map[string]bool)
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		pkg := importedAs(file, importPath, fallback)
		if pkg == "" {
			return nil
		}
		key := packageKey(rel, file)
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !returnsNamedType(function, pkg, typeName) {
				continue
			}
			if factories[key] == nil {
				factories[key] = make(map[string]bool)
			}
			factories[key][function.Name.Name] = true
		}
		return nil
	})
	return factories, err
}

func packageKey(rel string, file *ast.File) string {
	return filepath.ToSlash(filepath.Dir(rel)) + ":" + file.Name.Name
}

func returnsNamedType(function *ast.FuncDecl, pkg, typeName string) bool {
	if function.Type.Results == nil {
		return false
	}
	for _, result := range function.Type.Results.List {
		if isNamedType(result.Type, pkg, typeName) {
			return true
		}
	}
	return false
}

func fieldsOfType(file *ast.File, pkg, typeName string) map[string]bool {
	fields := make(map[string]bool)
	if pkg == "" {
		return fields
	}
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok || !isNamedType(field.Type, pkg, typeName) {
			return true
		}
		for _, name := range field.Names {
			fields[name.Name] = true
		}
		return true
	})
	return fields
}

func valuesOfType(function *ast.FuncDecl, pkg, typeName string, factories map[string]bool) map[string]bool {
	values := make(map[string]bool)
	if pkg == "" {
		return values
	}
	rememberFields := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			if !isNamedType(field.Type, pkg, typeName) {
				continue
			}
			for _, name := range field.Names {
				values[name.Name] = true
			}
		}
	}
	rememberFields(function.Recv)
	rememberFields(function.Type.Params)

	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.ValueSpec:
			if !isNamedType(value.Type, pkg, typeName) {
				return true
			}
			for _, name := range value.Names {
				values[name.Name] = true
			}
		case *ast.AssignStmt:
			for index, right := range value.Rhs {
				if !constructsNamedType(right, pkg, typeName, factories) || index >= len(value.Lhs) {
					continue
				}
				if name, ok := value.Lhs[index].(*ast.Ident); ok {
					values[name.Name] = true
				}
			}
		}
		return true
	})
	return values
}

func hasServerProvenance(expression ast.Expr, pkg string, values, fields, factories map[string]bool) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return values[value.Name]
	case *ast.SelectorExpr:
		return fields[value.Sel.Name]
	case *ast.CallExpr:
		return constructsNamedType(value, pkg, "MCPServer", factories)
	case *ast.ParenExpr:
		return hasServerProvenance(value.X, pkg, values, fields, factories)
	}
	return false
}

func isNamedType(expression ast.Expr, pkg, typeName string) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != typeName {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == pkg
}

func constructsNamedType(expression ast.Expr, pkg, typeName string, factories map[string]bool) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	if name, ok := call.Fun.(*ast.Ident); ok {
		return factories[name.Name]
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "New"+typeName {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == pkg
}

func importedAs(file *ast.File, importPath, fallback string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return fallback
	}
	return ""
}

func TestProductionAndHarnessAssignEveryDepsField(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields := depsFieldNames(file)
	if len(fields) == 0 {
		t.Fatal("Deps struct not found")
	}
	sites := []struct{ path, fn string }{
		{"../../cmd/server/main.go", "productionMCPServer"},
		{"../../internal/mcptest/harness.go", "newStreamWith"},
	}
	for _, site := range sites {
		assigned := depsFieldsAssignedIn(t, site.path, site.fn)
		for _, name := range fields {
			if !assigned[name] {
				t.Errorf("%s does not assign Deps.%s; a new collaborator would be omitted on one path", site.fn, name)
			}
		}
	}
}

func depsFieldNames(file *ast.File) []string {
	var fields []string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Deps" {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				fields = append(fields, name.Name)
			}
		}
		return true
	})
	return fields
}

func depsFieldsAssignedIn(t *testing.T, path, fn string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	assigned := map[string]bool{}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != fn || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CompositeLit:
				if !isDepsLiteral(n) {
					return true
				}
				for _, elt := range n.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if ident, ok := kv.Key.(*ast.Ident); ok {
						assigned[ident.Name] = true
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					assigned[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	return assigned
}

func isDepsLiteral(lit *ast.CompositeLit) bool {
	switch t := lit.Type.(type) {
	case *ast.Ident:
		return t.Name == "Deps"
	case *ast.SelectorExpr:
		return t.Sel.Name == "Deps"
	}
	return false
}
