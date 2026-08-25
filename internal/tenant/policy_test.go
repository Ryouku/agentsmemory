package tenant

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanWriteIsTheWorkspaceWritePolicy(t *testing.T) {
	for _, test := range []struct {
		role Role
		want bool
	}{
		{RoleMember, false},
		{RoleWriter, true},
		{RoleAdmin, true},
		{Role(""), false},
		{Role("owner"), false},
	} {
		if got := CanWrite(test.role); got != test.want {
			t.Errorf("CanWrite(%q) = %v, want %v", test.role, got, test.want)
		}
	}
}

func TestProductionCodeDoesNotReimplementCanWrite(t *testing.T) {
	root := filepath.Clean("../..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == ".claude" {
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
		if filepath.ToSlash(rel) == "internal/tenant/tenant.go" {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			expr, ok := node.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			if duplicatesCanWrite(expr, fset) {
				t.Errorf("%s reimplements writer/admin policy; call tenant.CanWrite", filepath.ToSlash(rel))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func duplicatesCanWrite(expr *ast.BinaryExpr, fset *token.FileSet) bool {
	wantComparison := token.ILLEGAL
	switch expr.Op {
	case token.LOR:
		wantComparison = token.EQL
	case token.LAND:
		wantComparison = token.NEQ
	default:
		return false
	}
	leftOperand, leftRole, leftOp, leftOK := roleComparison(expr.X, fset)
	rightOperand, rightRole, rightOp, rightOK := roleComparison(expr.Y, fset)
	if !leftOK || !rightOK || leftOp != wantComparison || rightOp != wantComparison || leftOperand != rightOperand {
		return false
	}
	return (leftRole == "RoleWriter" && rightRole == "RoleAdmin") ||
		(leftRole == "RoleAdmin" && rightRole == "RoleWriter")
}

func roleComparison(node ast.Expr, fset *token.FileSet) (operand, role string, operator token.Token, ok bool) {
	comparison, ok := unwrapExpression(node).(*ast.BinaryExpr)
	if !ok || (comparison.Op != token.EQL && comparison.Op != token.NEQ) {
		return "", "", token.ILLEGAL, false
	}
	if role = roleName(comparison.X); role != "" {
		return expressionText(fset, comparison.Y), role, comparison.Op, true
	}
	if role = roleName(comparison.Y); role != "" {
		return expressionText(fset, comparison.X), role, comparison.Op, true
	}
	return "", "", token.ILLEGAL, false
}

func roleName(node ast.Expr) string {
	switch value := unwrapExpression(node).(type) {
	case *ast.Ident:
		if value.Name == "RoleWriter" || value.Name == "RoleAdmin" {
			return value.Name
		}
	case *ast.SelectorExpr:
		if value.Sel.Name == "RoleWriter" || value.Sel.Name == "RoleAdmin" {
			return value.Sel.Name
		}
	}
	return ""
}

func unwrapExpression(node ast.Expr) ast.Expr {
	for {
		parenthesized, ok := node.(*ast.ParenExpr)
		if !ok {
			return node
		}
		node = parenthesized.X
	}
}

func expressionText(fset *token.FileSet, node ast.Expr) string {
	var out bytes.Buffer
	if err := format.Node(&out, fset, node); err != nil {
		return ""
	}
	return out.String()
}
