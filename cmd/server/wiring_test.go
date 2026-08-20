package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A setting is wired only when BOTH halves exist: something puts an operator's
// value into it, and something reads it back out. Either half alone is a knob
// that turns and does nothing, which is this project's most expensive recurring
// defect — an eval arm that was never registered, an IDF coverage function with
// no branch in Search, an embedding backend whose selector lived only in a
// comment, and a compose file advertising a rerank pool the server never read.
//
// Every one of those compiled, and every one had tests that passed, because the
// tests exercised the component instead of the wiring. These two check the wiring
// itself.

// operatorAssigns reports whether a Config field is populated from something an
// operator controls — `c.String("x")`, `c.Duration("x")`, an env lookup — rather
// than from `def.X`, which is the default it is supposed to override.
func operatorAssigns(text, field string) bool {
	for _, m := range regexp.MustCompile(`(?m)^\s*`+regexp.QuoteMeta(field)+`:\s*(.+?),?\s*$`).FindAllStringSubmatch(text, -1) {
		v := strings.TrimSpace(m[1])
		if strings.HasPrefix(v, "def.") || v == "" {
			continue // the default assigning itself is not an operator setting it
		}
		return true
	}
	return false
}

// TestEveryConfigFieldIsPopulatedAndRead fails when a field of config.Config is
// never assigned from the command line, or never read by anything outside the
// config package.
//
// A field nobody assigns is a setting an operator cannot set. A field nobody
// reads is a setting that changes nothing when they do. Both look identical from
// the outside — the program starts, accepts the flag, and behaves exactly as
// before — which is why this has to be mechanical.
func TestEveryConfigFieldIsPopulatedAndRead(t *testing.T) {
	root := repoRoot(t)
	fields := configFields(t, filepath.Join(root, "internal", "config", "config.go"))
	if len(fields) == 0 {
		t.Fatal("found no exported Config fields — this check has stopped checking anything")
	}

	// Assignment means an OPERATOR can set it — the field is populated from a flag
	// accessor, not from the defaults. The check used to be `strings.Contains(text,
	// f+":")`, which `HTTPTimeout: def.HTTPTimeout` satisfies, so a field nothing
	// exposed passed while the failure message below promised "an operator has no
	// way to set it". The message was right and the check was not.
	//
	// Reading: `.Field` appears anywhere outside the config package itself, where
	// the declaration and the defaults naturally mention every name.
	assigned := map[string]bool{}
	read := map[string]bool{}
	for _, path := range goFilesUnder(t, root) {
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, filepath.Join("internal", "config")) {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(src)
		for _, f := range fields {
			if operatorAssigns(text, f) {
				assigned[f] = true
			}
			if strings.Contains(text, "."+f) {
				read[f] = true
			}
		}
	}

	var broken []string
	for _, f := range fields {
		switch {
		case !assigned[f] && !read[f]:
			broken = append(broken, f+" is neither populated nor read — it is a field, not a setting")
		case !assigned[f]:
			broken = append(broken, f+" is read but never populated from the command line — an operator has no way to set it")
		case !read[f]:
			broken = append(broken, f+" is populated but never read — setting it changes nothing")
		}
	}
	sort.Strings(broken)
	for _, b := range broken {
		t.Errorf("config.Config.%s", b)
	}
}

// TestEveryFlagIsRead fails when a CLI flag is declared and never consulted.
//
// This is the same defect one layer out: `--fusion` existed as a flag before
// anything read it, and RERANK_TOP_K was set in the shipped compose file for
// months while the server read RERANK_POOL. A declared flag is a promise in the
// help output, and `--help` is documentation like any other.
func TestEveryFlagIsRead(t *testing.T) {
	root := repoRoot(t)
	declared := map[string]token.Position{}
	readNames := map[string]bool{}

	for _, path := range goFilesUnder(t, root) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				// Only a cli.*Flag literal declares a flag. A cli.Command also
				// has a Name, and a command is DISPATCHED rather than read — the
				// first version of this check reported every subcommand in the
				// binary as an unread flag, which is how a gate earns its way
				// into being deleted.
				if !isFlagLiteral(node.Type) {
					return true
				}
				for _, elt := range node.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Name" {
						continue
					}
					if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if name := strings.Trim(lit.Value, `"`); isFlagName(name) {
							declared[name] = fset.Position(lit.Pos())
						}
					}
				}
			case *ast.CallExpr:
				// c.String("flag-name"), c.Int(...), c.Bool(...), and friends.
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || len(node.Args) == 0 {
					return true
				}
				switch sel.Sel.Name {
				case "String", "Int", "Bool", "Float", "Float64", "Duration", "StringSlice", "IsSet":
					if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						readNames[strings.Trim(lit.Value, `"`)] = true
					}
				}
			}
			return true
		})
	}
	if len(declared) == 0 {
		t.Fatal("found no CLI flags — this check has stopped checking anything")
	}

	var unread []string
	for name, pos := range declared {
		if !readNames[name] {
			rel, _ := filepath.Rel(root, pos.Filename)
			unread = append(unread, fmt.Sprintf("%s:%d: --%s is declared but never read — it appears in --help and does nothing", rel, pos.Line, name))
		}
	}
	sort.Strings(unread)
	for _, u := range unread {
		t.Error(u)
	}
}

// isFlagLiteral reports whether a composite literal's type is one of urfave's
// flag types — cli.StringFlag, cli.IntFlag and friends.
func isFlagLiteral(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "cli" && strings.HasSuffix(sel.Sel.Name, "Flag")
}

// isFlagName keeps the check to things shaped like CLI flags: lower-case,
// hyphenated, no spaces. A `Name:` key also appears in unrelated structs.
func isFlagName(s string) bool {
	if s == "" || strings.ContainsAny(s, " _/.") || strings.ToLower(s) != s {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

// configFields lists the exported field names of the Config struct.
func configFields(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Config" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if ast.IsExported(name.Name) {
					out = append(out, name.Name)
				}
			}
		}
		return false
	})
	sort.Strings(out)
	return out
}
