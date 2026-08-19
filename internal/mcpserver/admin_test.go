package mcpserver

import (
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// adminCatalog registers the admin tools against a throwaway registrar and returns
// the tool names it advertised. The services are nil because registration only
// builds tools and closures — no handler runs — so this exercises the wiring
// without standing up a database, an embedder and a usage meter.
func adminCatalog(local bool) map[string]bool {
	reg := &registrar{srv: server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))}
	registerAdmin(reg, nil, nil, local)
	names := make(map[string]bool, len(reg.catalog))
	for _, e := range reg.catalog {
		names[e.Name] = true
	}
	return names
}

// TestDeleteWingIsLocalOnly pins the gate that keeps an unrecoverable mass delete
// off the multi-tenant server. It is a one-line condition guarding a tool that
// cannot be undone, which is exactly the kind of line that gets flipped by
// accident during an unrelated refactor.
func TestDeleteWingIsLocalOnly(t *testing.T) {
	const deleteWing = toolPrefix + "delete_wing"

	saas := adminCatalog(false)
	if saas[deleteWing] {
		t.Fatalf("%s must not be exposed on the multi-tenant server", deleteWing)
	}
	// The neighbours prove the catalogue was actually populated, so an empty map
	// cannot pass the check above by accident.
	for _, want := range []string{toolPrefix + "merge_wing", toolPrefix + "memories_filed_away"} {
		if !saas[want] {
			t.Fatalf("expected %s in the admin catalogue, got %v", want, saas)
		}
	}

	if !adminCatalog(true)[deleteWing] {
		t.Fatalf("%s must be exposed in local mode — it is the only way to delete a wing there", deleteWing)
	}
}
