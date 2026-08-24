package palace

import (
	"context"
	"strings"
	"testing"
)

// TestAnchorsResolveFromAnySiblingChunk is the gate for finding B.
//
// AnchorsForMemories' whole claim is in its name: anchors attached to ANY chunk
// of a memory belong to the memory. Before this, replacing it with a
// per-drawer lookup at the MCP call site left the entire suite green — what kept
// the existing scenario test honest was that it happened to pass the ROOT id, so
// it exercised chunk zero and never a sibling.
//
// This matters in production rather than in theory: am_update_drawer accepts any
// chunk id, so an agent that corrected a memory from a search hit — which
// returns whichever chunk matched, not chunk zero — anchors the SIBLING. A
// per-drawer lookup then reports that memory as unanchored, and the staleness
// check meant to protect it silently covers nothing.
func TestAnchorsResolveFromAnySiblingChunk(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	const team = "team-anchors"

	added, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_alpha", Room: "decisions", SourceFile: "anchored",
		Content: strings.Repeat("a memory long enough to be stored as several chunks ", 60),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunks; it needs a root AND at least one sibling", len(added.Drawers))
	}
	root := added.Drawers[0].ID
	sibling := added.Drawers[1].ID
	if sibling == root {
		t.Fatal("sibling id equals the root; the fixture cannot distinguish the two paths")
	}

	// Anchor the SIBLING, never the root — this is the shape am_update_drawer
	// produces when an agent acts on a search hit.
	if _, err := svc.AddAnchors(ctx, team, sibling, []AnchorInput{{
		Path: "internal/palace/service.go", Snippet: "func (s *Service) Search(", Repo: "agentsmemory",
	}}); err != nil {
		t.Fatalf("anchor the sibling: %v", err)
	}

	byMemory, err := svc.AnchorsForMemories(ctx, team, []string{root})
	if err != nil {
		t.Fatalf("resolve anchors: %v", err)
	}
	if len(byMemory[root]) == 0 {
		t.Fatalf("an anchor on chunk %s is invisible when the memory %s is asked about — "+
			"staleness detection would report this memory as unanchored and check nothing",
			short12(sibling), short12(root))
	}
	if got := byMemory[root][0].DrawerID; got != sibling {
		t.Errorf("anchor resolved to drawer %s, want the sibling %s", short12(got), short12(sibling))
	}

	// ⚠The argument is a MEMORY ROOT, not any chunk id, and that is worth pinning
	// because it is not obvious from the call site. MemoryChunkIDsByRoots keys its
	// result by the root it derives from parent_id, so passing a child id yields a
	// map keyed by the root while the lookup asks for the child — an empty answer.
	//
	// It is not a defect: internal/mcpserver passes hit.MemoryID, which is already
	// the root. Asserting it here means a future caller that reaches for the
	// matched chunk id instead finds this test rather than a silently empty
	// anchor list.
	bySibling, err := svc.AnchorsForMemories(ctx, team, []string{sibling})
	if err != nil {
		t.Fatalf("resolve anchors by sibling: %v", err)
	}
	if len(bySibling[sibling]) != 0 {
		t.Errorf("a child id now resolves anchors directly; that is an improvement, but this "+
			"test and AnchorsForMemories' doc comment both say the argument is a ROOT — "+
			"update them together (got %d)", len(bySibling[sibling]))
	}
}
