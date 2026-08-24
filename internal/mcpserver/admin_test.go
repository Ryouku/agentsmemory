package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/mark3labs/mcp-go/mcp"
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
	const deleteWing = mcpprotocol.ToolPrefix + "delete_wing"

	saas := adminCatalog(false)
	if saas[deleteWing] {
		t.Fatalf("%s must not be exposed on the multi-tenant server", deleteWing)
	}
	// The neighbours prove the catalogue was actually populated, so an empty map
	// cannot pass the check above by accident.
	for _, want := range []string{mcpprotocol.ToolPrefix + "merge_wing", mcpprotocol.ToolPrefix + "memories_filed_away"} {
		if !saas[want] {
			t.Fatalf("expected %s in the admin catalogue, got %v", want, saas)
		}
	}

	if !adminCatalog(true)[deleteWing] {
		t.Fatalf("%s must be exposed in local mode — it is the only way to delete a wing there", deleteWing)
	}
}

// recordingMerger is the palace, reduced to the two calls merge_wing makes and
// the ORDER it makes them in. A double rather than a database because the
// question here is sequencing, not storage.
type recordingMerger struct {
	calls        []string
	recomputeErr error
}

func (m *recordingMerger) MergeWing(_ context.Context, teamID string, sources []string, target string) (palace.MergeWingResult, error) {
	m.calls = append(m.calls, fmt.Sprintf("MergeWing(team=%s,sources=%v,target=%s)", teamID, sources, target))
	return palace.MergeWingResult{Sources: sources, Target: target, Drawers: 5, Closets: 2}, nil
}

func (m *recordingMerger) RecomputeGraph(_ context.Context, teamID, wing string, prune bool) (palace.RecomputeResult, error) {
	m.calls = append(m.calls, fmt.Sprintf("RecomputeGraph(team=%s,wing=%q,prune=%v)", teamID, wing, prune))
	return palace.RecomputeResult{}, m.recomputeErr
}

// mergeWingCall drives the REGISTERED merge_wing handler — the one an agent
// actually reaches through tools/call — rather than calling mergeWingHandler
// directly.
//
// ⚠The distinction is the whole point, and skipping it cost this package its
// coverage once already. Calling mergeWingHandler by name proves the BODY is
// right and proves nothing about whether registerMergeWing SELECTS it: an
// inlined copy of the body missing RecomputeGraph kept the entire suite green
// while the shipped tool silently stopped rebuilding the graph. That is this
// repo's named defect — a test exercising the component rather than the
// selection — so the handler is resolved out of the live catalogue instead.
//
// Registering also puts the call through writeGuard, so the tenant needs a
// writing role; the unmetered-local-operator context is what lets usageSvc be
// nil, because admit returns before it is touched.
func mergeWingCall(t *testing.T, merger *recordingMerger) *mcp.CallToolResult {
	t.Helper()
	reg := &registrar{srv: server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))}
	registerMergeWing(reg, merger, nil)

	const name = mcpprotocol.ToolPrefix + "merge_wing"
	registered := reg.srv.GetTool(name)
	if registered.Handler == nil {
		t.Fatalf("%s is not registered, so nothing here tests the shipped tool", name)
	}

	ctx := WithUnmeteredLocalOperator(auth.WithTenant(context.Background(),
		tenant.Tenant{TeamID: "team-1", Role: tenant.RoleAdmin}))
	res, err := registered.Handler(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: map[string]any{"sources": []any{"wing_a"}, "target": "wing_b"},
		},
	})
	if err != nil {
		t.Fatalf("merge_wing: %v", err)
	}
	return res
}

// TestMergeWingRelabelsBeforeRebuildingTheGraph pins the ORDER, which is the
// half a call-counting check cannot see.
//
// RecomputeGraph derives hallways and tunnels from the drawer rows MergeWing
// relabels. Run it first and it rebuilds the graph from rows the merge has not
// touched yet, leaving precisely the stale layout the pair exists to prevent —
// and that inversion compiles, returns success, and satisfies any assertion that
// merely counts the two calls. The wing argument is empty and prune is on
// because a merge moves drawers ACROSS wings, so the rebuild cannot be narrowed
// to one of them; mergejob.Worker passes the same pair.
func TestMergeWingRelabelsBeforeRebuildingTheGraph(t *testing.T) {
	merger := &recordingMerger{}
	res := mergeWingCall(t, merger)

	if res.IsError {
		t.Fatalf("merge_wing reported an error: %s", errText(res))
	}
	want := []string{
		"MergeWing(team=team-1,sources=[wing_a],target=wing_b)",
		`RecomputeGraph(team=team-1,wing="",prune=true)`,
	}
	if !slices.Equal(merger.calls, want) {
		t.Fatalf("merge_wing palace calls:\n got %v\nwant %v", merger.calls, want)
	}
}

// TestMergeWingReportsWhatSurvivedAFailedRebuild covers the state the ordering
// creates: the relabel has committed and the rebuild has not, so the caller must
// learn both that their merge happened and that the graph is stale, or they will
// retry a merge that already succeeded.
func TestMergeWingReportsWhatSurvivedAFailedRebuild(t *testing.T) {
	merger := &recordingMerger{recomputeErr: errors.New("graph boom")}
	res := mergeWingCall(t, merger)

	if !res.IsError {
		t.Fatal("a failed graph rebuild must not report success — the merge is only half done")
	}
	body := errText(res)
	for _, want := range []string{"5", "2", "graph boom", "recompute_graph"} {
		if !strings.Contains(body, want) {
			t.Errorf("rebuild-failure message is missing %q, so the caller cannot tell what landed:\n%s", want, body)
		}
	}
}
