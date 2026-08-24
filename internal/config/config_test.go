package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestScopeSearchToWing(t *testing.T) {
	for _, test := range []struct {
		scope string
		want  bool
	}{
		{"", true},
		{"wing", true},
		{"WING", true},
		{"workspace", false},
		{"Workspace", false},
		{" workspace ", false},
	} {
		if got := (Config{SearchScope: test.scope}).ScopeSearchToWing(); got != test.want {
			t.Errorf("SearchScope %q: ScopeSearchToWing() = %v, want %v", test.scope, got, test.want)
		}
	}
	if !Default().ScopeSearchToWing() {
		t.Fatal("Default search scope must stay wing-scoped; the harness follows Default()")
	}
}

func TestScopeSearchToWingIsTheProductionSelector(t *testing.T) {
	files := []string{
		filepath.Clean("../../cmd/server/main.go"),
		filepath.Clean("../../internal/mcptest/harness.go"),
	}
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "ScopeSearchToWing" {
				calls++
			}
			return true
		})
		if calls != 1 {
			t.Errorf("%s calls ScopeSearchToWing %d times, want 1 — search scope must not be inlined beside productionMCPServer or the harness", path, calls)
		}
	}
}

// TestIsLoopback pins the classification that decides whether local mode warns
// about exposing its unauthenticated endpoint. The case that matters most is
// ":8080" — the multi-tenant default, which binds every interface and must NOT
// be read as safe just because it names no host.
func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{LocalAddr, true},
		{"127.0.0.1:9000", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{"127.5.5.5:8080", true}, // the whole 127/8 block is loopback
		{":8080", false},         // every interface — the dangerous default
		{"0.0.0.0:8080", false},
		{"192.168.1.10:8080", false},
		{"8080", false}, // unparseable: unknown must not be treated as safe
		{"", false},
	}
	for _, tc := range tests {
		if got := IsLoopback(tc.addr); got != tc.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// TestChromemPath pins where the embedded index lands relative to the database
// it indexes: same directory (so one volume and one backup cover both), and a
// name that cannot collide with the .db file itself.
func TestChromemPath(t *testing.T) {
	tests := []struct {
		dbPath string
		want   string
	}{
		{"agentsmemory.db", "agentsmemory.chromem"},
		{"/data/agentsmemory.db", "/data/agentsmemory.chromem"},
		{"/data/palace.sqlite", "/data/palace.chromem"},
		{"agentsmemory", "agentsmemory.chromem"}, // no extension to strip
		{"/var/lib/am/main.db", "/var/lib/am/main.chromem"},
	}
	for _, tc := range tests {
		if got := ChromemPath(tc.dbPath); got != tc.want {
			t.Errorf("ChromemPath(%q) = %q, want %q", tc.dbPath, got, tc.want)
		}
	}
}

// TestDocumentedDefaultsMatchDefault keeps operator-facing field documentation
// tied to the values the process actually starts with. Both Fusion and
// ClosetBoost drifted in a previous change because the prose and Default were
// reviewed independently.
//
// The universe is DERIVED, not listed: every Config field whose doc comment
// claims a default is checked against Default() by reflection. The first version
// of this gate named two fields in a table, and that is precisely how three more
// documented defaults stayed uncovered while it reported green — BM25Weight had
// no coverage anywhere in the repo, so changing it broke nothing and the comment
// simply began to lie.
func TestDocumentedDefaultsMatchDefault(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate config test source")
	}
	source := strings.TrimSuffix(testFile, "_test.go") + ".go"
	file, err := parser.ParseFile(token.NewFileSet(), source, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}

	docs := map[string]string{}
	ast.Inspect(file, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "Config" {
			return true
		}
		configType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range configType.Fields.List {
			if len(field.Names) == 1 && field.Doc != nil {
				docs[field.Names[0].Name] = field.Doc.Text()
			}
		}
		return false
	})

	defaults := reflect.ValueOf(Default())
	checked := 0
	for field, doc := range docs {
		claimed, ok := documentedDefault(doc)
		if !ok {
			continue
		}
		value := defaults.FieldByName(field)
		if !value.IsValid() {
			t.Errorf("Config.%s documents a default but Default() has no such field", field)
			continue
		}
		checked++
		if actual := defaultLiteral(value); claimed != actual {
			t.Errorf("Config.%s doc claims the default is %s, but Default() sets %s",
				field, claimed, actual)
		}
	}

	// A gate that finds nothing to require finds no gaps: if a refactor renames
	// the phrase these comments use, every field silently stops being checked and
	// this test keeps passing. The floor is what the file held when it was written.
	const documentedFields = 5
	if checked < documentedFields {
		t.Errorf("only %d field(s) claim a default; expected at least %d — the doc convention "+
			"probably changed and this gate stopped seeing them", checked, documentedFields)
	}
}

// defaultClaim is the phrase every Config doc comment uses to name its default.
const defaultClaim = "(the default"

// documentedDefault returns the value a doc comment claims is the default.
//
// The claim is the token immediately before "(the default", which is the shape
// every such comment in config.go uses whether the value is quoted ("rrf") or
// bare (0). Matching on the phrase rather than per-field patterns is what makes
// the universe derived: a new documented field is covered the day it is written.
func documentedDefault(doc string) (string, bool) {
	i := strings.Index(doc, defaultClaim)
	if i < 0 {
		return "", false
	}
	before := strings.Fields(doc[:i])
	if len(before) == 0 {
		return "", false
	}
	return before[len(before)-1], true
}

// defaultLiteral renders a config value the way a doc comment writes it —
// quoted for a string, bare for a number — so the comparison is against the
// prose an operator actually reads rather than against Go's formatting.
func defaultLiteral(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return strconv.Quote(v.String())
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	default:
		return fmt.Sprint(v.Interface())
	}
}
