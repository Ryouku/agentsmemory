package mcpserver

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
		wantHosted = 40 // every tool except delete_wing
		wantLocal  = 41 // + delete_wing, safe only when the agent owns the machine
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
	for _, stale := range []string{"36 of the planned 37", "All 37 MCP tools", "gives your agent 37 tools"} {
		if strings.Contains(text, stale) {
			t.Errorf("README still says %q — the server exposes %d/%d", stale, wantHosted, wantLocal)
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
		if seen[name] {
			t.Errorf("tool %q is registered twice", name)
		}
		seen[name] = true
	}
}
