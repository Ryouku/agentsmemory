package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// fullCatalog registers every tool the server exposes and returns their names.
// The services are nil for the same reason adminCatalog's are: registration only
// builds tools and closures, so no handler runs and no database is needed.
func fullCatalog(local bool) []string {
	reg := &registrar{srv: server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))}
	registerAll(reg, Deps{Local: local})
	names := make([]string, 0, len(reg.catalog))
	for _, e := range reg.catalog {
		names = append(names, e.Name)
	}
	return names
}

// liveSurface returns both sides of the registration contract: the catalogue
// accumulated at the registrar and the tools a real MCP client receives from
// tools/list. Keeping the client call here prevents catalogue-only tests from
// blessing metadata that was never published on the wire.
func liveSurface(t *testing.T, local bool) ([]CatalogEntry, []mcp.Tool) {
	t.Helper()
	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	reg := &registrar{srv: srv}
	registerAll(reg, Deps{Local: local})

	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("new in-process client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start in-process client: %v", err)
	}
	if _, err := cli.Initialize(t.Context(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("initialize in-process client: %v", err)
	}
	res, err := cli.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list live tools: %v", err)
	}
	return reg.catalog, res.Tools
}

// TestCatalogSizeIsWhatTheReadmeClaims makes the tool count a gate instead of a
// sentence somebody has to remember to edit.
//
// The README is the first thing a new operator reads and the only place the tool
// surface is described in prose, so a stale number there is a small lie told at
// the widest point of contact — it drifted to "36 of 37" while the server had
// grown to 41. Prose cannot be trusted to track code; an assertion can.
//
// The numbers are deliberately spelled out rather than derived from the README:
// a test that reads its expectation out of the file it is checking would pass
// against any value the file happens to hold.
func TestCatalogSizeIsWhatTheReadmeClaims(t *testing.T) {
	const (
		wantHosted = 41 // every tool except delete_wing
		wantLocal  = 42 // + delete_wing, safe only when the agent owns the machine
	)

	hosted, local := fullCatalog(false), fullCatalog(true)
	if len(hosted) != wantHosted {
		t.Errorf("hosted catalogue has %d tools, expected %d — update the README and this test together", len(hosted), wantHosted)
	}
	if len(local) != wantLocal {
		t.Errorf("local catalogue has %d tools, expected %d — update the README and this test together", len(local), wantLocal)
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(readme)
	for _, n := range []int{wantHosted, wantLocal} {
		if !strings.Contains(text, strconv.Itoa(n)) {
			t.Errorf("README does not mention %d anywhere; it describes the tool surface, so it must state the real count", n)
		}
	}
	// The counts it used to claim must be gone, or the new number sits beside the
	// stale one and a reader picks whichever they meet first.
	for _, stale := range []string{
		"36 of the planned 37", "All 37 MCP tools", "gives your agent 37 tools",
		"stateless liveness probe",
	} {
		if strings.Contains(text, stale) {
			t.Errorf("README still says %q — the server exposes %d/%d", stale, wantHosted, wantLocal)
		}
	}
	var reconnectRow string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "`am_reconnect`") {
			reconnectRow = strings.ToLower(line)
			break
		}
	}
	for _, want := range []string{"write-gated", "may create backend state"} {
		if !strings.Contains(reconnectRow, want) {
			t.Errorf("README am_reconnect row does not explain its backend write; missing %q: %s", want, reconnectRow)
		}
	}
}

// TestEveryToolNameIsUniqueAndPrefixed pins the two properties the catalogue is
// relied on for elsewhere: am_* names are what the protocol documents and what
// the miner's wing routing greps for, and a duplicate registration would shadow
// a tool silently — the server would advertise it twice and dispatch one.
func TestEveryToolNameIsUniqueAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range fullCatalog(true) {
		if !strings.HasPrefix(name, mcpprotocol.ToolPrefix) {
			t.Errorf("tool %q is missing the %q namespace prefix", name, mcpprotocol.ToolPrefix)
		}
		if seen[name] {
			t.Errorf("tool %q is registered twice", name)
		}
		seen[name] = true
	}
}

func TestLiveToolMetadataMatchesRegistrationPolicy(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "hosted"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			catalog, tools := liveSurface(t, local)
			if len(tools) != len(catalog) {
				t.Fatalf("tools/list returned %d tools, registrar catalogued %d", len(tools), len(catalog))
			}

			byName := make(map[string]CatalogEntry, len(catalog))
			for _, entry := range catalog {
				byName[entry.Name] = entry
			}
			for _, tool := range tools {
				entry, ok := byName[tool.Name]
				if !ok {
					t.Errorf("tools/list exposes %q but the registrar did not catalogue it", tool.Name)
					continue
				}
				delete(byName, tool.Name)
				if tool.Description != entry.Description {
					t.Errorf("%s description differs between tools/list and catalogue", tool.Name)
				}
				if tool.Annotations.ReadOnlyHint == nil {
					t.Errorf("%s omits readOnlyHint; clients cannot classify it safely", tool.Name)
					continue
				}
				if got := *tool.Annotations.ReadOnlyHint; got == entry.Write {
					t.Errorf("%s readOnlyHint=%t, catalogue write=%t", tool.Name, got, entry.Write)
				}
				if !entry.Write && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
					t.Errorf("read-only tool %s is advertised as destructive", tool.Name)
				}
			}
			for name := range byName {
				t.Errorf("catalogue contains %q but tools/list does not expose it", name)
			}
		})
	}
}

func TestLocalCatalogAddsOnlyDeleteWing(t *testing.T) {
	_, hosted := liveSurface(t, false)
	_, local := liveSurface(t, true)
	hostedNames := make(map[string]bool, len(hosted))
	for _, tool := range hosted {
		hostedNames[tool.Name] = true
	}
	var extra []string
	for _, tool := range local {
		if !hostedNames[tool.Name] {
			extra = append(extra, tool.Name)
		}
	}
	if len(extra) != 1 || extra[0] != "am_delete_wing" {
		t.Fatalf("local-only tools = %v, want [am_delete_wing]", extra)
	}
}
