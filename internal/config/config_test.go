package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"strings"
	"testing"
)

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

// TestRankingDefaultDocsMatchDefault keeps operator-facing field documentation
// tied to the values the process actually starts with. Both fields drifted in a
// previous change because the prose and Default were reviewed independently.
func TestRankingDefaultDocsMatchDefault(t *testing.T) {
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

	defaults := Default()
	for _, tc := range []struct {
		field string
		want  string
	}{
		{"Fusion", fmt.Sprintf("%q (the default", defaults.Fusion)},
		{"ClosetBoost", fmt.Sprintf("%g (the default)", defaults.ClosetBoost)},
	} {
		if !strings.Contains(docs[tc.field], tc.want) {
			t.Errorf("Config.%s doc does not match Default(): want %q in %q", tc.field, tc.want, docs[tc.field])
		}
	}
}
