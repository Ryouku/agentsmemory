package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// kgQueryTestTeam is the tenant every case in this file runs as.
const kgQueryTestTeam = "team-kgquery"

// kgToolServer stands the real KG tools up over a migrated palace holding one
// entity with one retracted fact and one live one — the smallest graph in which
// "which half did you return" is a question with different answers.
//
// It reuses the throwaway-palace helpers the graph tests already established
// rather than standing up a second harness.
func kgToolServer(t *testing.T) *server.MCPServer {
	t.Helper()
	gdb := graphTestDB(t)
	drawers := palace.NewService(palace.NewRepo(gdb), graphTestEmbedder{}, sqlitevec.New(gdb), graphTestDim)
	ctx := context.Background()

	if _, err := drawers.KGAdd(ctx, kgQueryTestTeam, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("seed ended-to-be: %v", err)
	}
	if _, err := drawers.KGAdd(ctx, kgQueryTestTeam, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("seed survivor: %v", err)
	}
	if _, _, err := drawers.KGInvalidate(ctx, kgQueryTestTeam, "Alice", "works at", "Acme", "2025-06-01"); err != nil {
		t.Fatalf("seed invalidate: %v", err)
	}

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerKG(&registrar{srv: srv}, drawers, usage.NewService(usage.NewRepo(gdb), graphTestCaps{}))
	return srv
}

// callKGQuery drives the REGISTERED kg_query handler and decodes its wire body.
// Going through the registration rather than palace.KGQuery is the point: this
// file's job is to check what an agent actually receives.
func callKGQuery(t *testing.T, srv *server.MCPServer, args map[string]any) map[string]json.RawMessage {
	t.Helper()
	const name = mcpprotocol.ToolPrefix + "kg_query"

	st := srv.GetTool(name)
	if st == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", name)
	}
	ctx := auth.WithTenant(context.Background(), tenant.Tenant{
		TeamID: kgQueryTestTeam, UserID: "u1", Role: tenant.RoleAdmin,
	})
	res, err := st.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("kg_query: %v", err)
	}
	body := errText(res)
	if res.IsError {
		t.Fatalf("kg_query returned an error result: %s", body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("kg_query did not return a JSON object: %v\n  %s", err, body)
	}
	return fields
}

// TestFilteredResponseReportsWhatItWithheld is ADR-026 T3's gate.
//
// It asserts the withheld NUMBER, not the presence of the field, and it reads it
// off the WIRE rather than off KGQueryResult. Both choices are the same lesson:
// printSupersessionGate computed a near-miss explanation and discarded it for
// weeks — 246 characters produced, 0 printed — and only a test that read the
// value caught it. A gate asserting `withheld != nil` passes on a hard-coded
// zero, and a gate reading the struct passes on a handler that never serialises
// it.
func TestFilteredResponseReportsWhatItWithheld(t *testing.T) {
	srv := kgToolServer(t)

	for _, tc := range []struct {
		name         string
		status       string
		wantCount    int
		wantWithheld map[string]int64
	}{
		{
			name:         "current hides the one retracted fact and says so",
			status:       palace.KGStatusCurrent,
			wantCount:    1,
			wantWithheld: map[string]int64{palace.KGStatusEnded: 1},
		},
		{
			name:         "ended hides the one live fact and says so",
			status:       palace.KGStatusEnded,
			wantCount:    1,
			wantWithheld: map[string]int64{palace.KGStatusCurrent: 1},
		},
		{
			// Nothing was removed, so the keys must be ABSENT rather than zero.
			// Their presence is itself the signal that something is missing; a
			// withheld:{} on every response trains the reader to ignore it.
			name:      "all withholds nothing and stays silent about it",
			status:    palace.KGStatusAll,
			wantCount: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := callKGQuery(t, srv, map[string]any{"entity": "Alice", "status": tc.status})

			var count int
			if err := json.Unmarshal(fields["count"], &count); err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != tc.wantCount {
				t.Errorf("count = %d, want %d", count, tc.wantCount)
			}

			var status string
			if err := json.Unmarshal(fields["status"], &status); err != nil {
				t.Fatalf("status: %v", err)
			}
			if status != tc.status {
				t.Errorf("status echoed as %q, want %q — a caller cannot tell what was applied", status, tc.status)
			}

			raw, present := fields["withheld"]
			if tc.wantWithheld == nil {
				if present {
					t.Errorf("withheld is present with nothing withheld: %s", raw)
				}
				if _, ok := fields["hint"]; ok {
					t.Error("hint is present with nothing withheld")
				}
				return
			}
			if !present {
				t.Fatalf("withheld is absent though %v was filtered out — computed and never printed is this repo's recurring shape", tc.wantWithheld)
			}
			var got map[string]int64
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("withheld: %v", err)
			}
			if len(got) != len(tc.wantWithheld) {
				t.Fatalf("withheld = %v, want %v", got, tc.wantWithheld)
			}
			for k, want := range tc.wantWithheld {
				if got[k] != want {
					t.Errorf("withheld[%q] = %d, want %d", k, got[k], want)
				}
			}
			if _, ok := fields["hint"]; !ok {
				t.Error("something was withheld and no hint names the parameter that restores it")
			}
		})
	}
}
