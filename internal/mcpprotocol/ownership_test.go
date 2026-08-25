package mcpprotocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
		LocalEnvVar:      "LocalEnvVar",
		SocketEnvVar:     "SocketEnvVar",
		MCPURLEnvVar:     "MCPURLEnvVar",
		ProxyURLEnvVar:   "ProxyURLEnvVar",
		HostedOrigin:     "HostedOrigin",
		HostedMCPURL:     "HostedMCPURL",
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// .claude holds agent worktrees — copies of this repo whose
			// literals would be reported twice.
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == ".claude" {
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

func TestNonGoClientsUseProtocolEnvNames(t *testing.T) {
	root := filepath.Clean("../..")
	for _, tc := range []struct {
		rel   string
		names []string
	}{
		{"clients/claude-code/extensions/agentsmemory.ts", []string{TokenEnvVar, LocalEnvVar, MCPURLEnvVar, HostedMCPURL}},
		{"clients/claude-code/hooks/agentsmemory-verify-hook.sh", []string{MCPURLEnvVar}},
		{"clients/claude-code/hooks/agentsmemory-stop-hook.sh", []string{MCPURLEnvVar}},
		{"clients/claude-code/hooks/agentsmemory-session-end-hook.sh", []string{MCPURLEnvVar}},
		{"clients/claude-code/hooks/agentsmemory-stats.sh", []string{MCPURLEnvVar}},
		{"internal/web/claude-guide.md", []string{HostedMCPURL}},
	} {
		raw, err := os.ReadFile(filepath.Join(root, tc.rel))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, name := range tc.names {
			if !strings.Contains(body, name) {
				t.Errorf("%s does not mention %s; renaming the protocol constant would leave this client on the old value", tc.rel, name)
			}
		}
	}
}

func TestPiExtensionDefaultsToHostedMCPURL(t *testing.T) {
	root := filepath.Clean("../..")
	raw, err := os.ReadFile(filepath.Join(root, "clients/claude-code/extensions/agentsmemory.ts"))
	if err != nil {
		t.Fatal(err)
	}
	want := `const DEFAULT_MCP_URL = "` + HostedMCPURL + `";`
	if !strings.Contains(string(raw), want) {
		t.Errorf("pi extension default is not mcpprotocol.HostedMCPURL (%s); TypeScript cannot import the Go constant, so this assignment is the one allowed copy", HostedMCPURL)
	}
}

func TestTemplatesDoNotHardcodeHostedOrigin(t *testing.T) {
	root := filepath.Clean("../..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// .claude holds agent worktrees — copies of this repo whose
			// literals would be reported twice.
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".templ") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(raw)
		rel, _ := filepath.Rel(root, path)
		for _, lit := range []string{HostedOrigin, HostedMCPURL} {
			if strings.Contains(body, lit) {
				t.Errorf("%s hardcodes %s; use mcpprotocol.HostedOrigin / siteURL", filepath.ToSlash(rel), lit)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
