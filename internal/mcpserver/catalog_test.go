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
		// Equal since ADR-038 T4. delete_wing was the only tool local added, and
		// removing it from the agent surface removed the difference: an agent may
		// not erase on either deployment, because "the agent owns the machine" was
		// never a reason for the agent to be able to destroy the record.
		wantHosted = 41
		wantLocal  = 41
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

// TestDestructiveToolsAreAbsentFromTheAgentCatalogue is rung 3 for ADR-038's
// erasure boundary, and it is a REGISTRATION check on purpose: a behavioural test
// that never calls a tool passes whether or not the tool is offered, so only the
// catalogue can fail on this.
//
// ⚠ It is built with local=true, and that is load-bearing. delete_wing is
// registered only when local, so a fixture on the default hosted server finds it
// absent today, passes for the wrong reason, and stays green if someone restores
// the line. local was never a boundary — it is the case where the agent and the
// operator share a process, which is the case where an agent CAN erase.
//
// Erasure does not disappear; it moves to the operator, who has the database file
// and no palace protocol telling them a delete is a correction.
func TestDestructiveToolsAreAbsentFromTheAgentCatalogue(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "hosted"
		if local {
			name = "local"
		}
		t.Run(name, func(t *testing.T) {
			offered := map[string]bool{}
			for _, n := range fullCatalog(local) {
				offered[n] = true
			}
			for _, gone := range []string{
				mcpprotocol.ToolPrefix + "delete_drawer",
				mcpprotocol.ToolPrefix + "delete_tunnel",
				mcpprotocol.ToolPrefix + "delete_hallway",
				mcpprotocol.ToolPrefix + "delete_wing",
			} {
				if offered[gone] {
					t.Errorf("%s is still in the %s catalogue. A tool an agent can SEE is a tool an "+
						"agent will call: an agent doing a retraction currently gets an erasure, and "+
						"the destroyed record is the one thing irrecoverable at any price",
						gone, name)
				}
			}
			// The replacements must be there, or this is a removal rather than a
			// boundary and an agent that cannot delete files a duplicate instead.
			for _, want := range []string{
				mcpprotocol.ToolPrefix + "invalidate_drawer",
				mcpprotocol.ToolPrefix + "kg_supersede",
			} {
				if !offered[want] {
					t.Errorf("%s is not in the %s catalogue; removing erasure without offering the "+
						"correction leaves an agent no way to say a memory is wrong", want, name)
				}
			}
		})
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

// TestLocalAndHostedOfferTheSameTools replaces TestLocalCatalogAddsOnlyDeleteWing.
//
// local used to add exactly one tool — delete_wing — on the reasoning that a
// self-hoster has no dashboard, so the alternative to an agent deleting a wing was
// nobody deleting it. ADR-038 removed it: local is the case where the operator
// boundary is ABSENT, not the case where destroying a record is safe, and erasure
// moved to `agentsmemory wing delete`.
//
// Asserting the two surfaces are now IDENTICAL is what keeps that from being
// undone quietly — a tool added behind the local flag would fail here, and the
// flag is exactly where a destructive verb would be tempting to hide.
func TestLocalAndHostedOfferTheSameTools(t *testing.T) {
	_, hosted := liveSurface(t, false)
	_, local := liveSurface(t, true)
	hostedNames := make(map[string]bool, len(hosted))
	for _, tool := range hosted {
		hostedNames[tool.Name] = true
	}
	if len(hosted) == 0 {
		t.Fatal("the hosted surface is empty; this check reads nothing")
	}
	var extra []string
	for _, tool := range local {
		if !hostedNames[tool.Name] {
			extra = append(extra, tool.Name)
		}
	}
	if len(extra) != 0 {
		t.Errorf("local offers tools hosted does not: %v. Since ADR-038 the deployments differ in "+
			"who runs the server, not in what an agent may destroy", extra)
	}
	if len(local) != len(hosted) {
		t.Errorf("local has %d tools and hosted %d", len(local), len(hosted))
	}
}

// TestEveryCatalogToolIsNamedInTheReadme: the README's tool table must name every
// tool the server registers.
//
// Its sibling above counts the catalogue and greps the README for the number.
// That is a proxy, and it went green for five tools nobody had documented —
// am_bootstrap, am_entry_point, am_list_anchors, am_mark_anchors and
// am_recall_stats were all registered, all reachable, and absent from the table
// (measured 2026-08-26 against the running server's tools/list). The count check
// cannot see that, because a count is satisfied by the number being right while
// the rows are wrong, and `strings.Contains(readme, "43")` is satisfied by any
// "43" anywhere in a 1,600-line file.
//
// This is the repo's own named defect arriving in its documentation gate: a test
// that ranges over a proxy rather than the thing it is about. A tool absent from
// the table is not merely undocumented — it is undiscoverable to the one reader
// who cannot ask the server, which is the reader the README exists for.
func TestEveryCatalogToolIsNamedInTheReadme(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	// Only TABLE ROWS count — a line starting with "|". The first version of this
	// check accepted the name anywhere in the file, and its own mutation survived:
	// deleting the am_bootstrap row left am_bootstrap named inside the
	// am_entry_point row's prose, so the gate stayed green with the row gone. That
	// is the same proxy defect one level up, written by the check meant to fix it.
	rows := map[string]bool{}
	for _, line := range strings.Split(string(readme), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		// The tool column is the first cell; a name mentioned in the description
		// of some other tool's row does not document it either.
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		for _, name := range fullCatalog(true) {
			if strings.Contains(cells[1], "`"+name+"`") {
				rows[name] = true
			}
		}
	}
	for _, name := range fullCatalog(true) {
		if !rows[name] {
			t.Errorf("the server registers %s and no README table row lists it; a tool absent from the table is undiscoverable to a reader who cannot query the server", name)
		}
	}
}
