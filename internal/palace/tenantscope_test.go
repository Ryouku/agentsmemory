package palace

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryChunkQueriesRefuseToCrossTenants is the gate for the isolation
// control itself.
//
// The memory-level read path resolves a memory by asking for every row whose id
// OR whose parent_id names one of the requested roots. The second half is the
// dangerous one: parent_id is ordinary column data, so a row belonging to
// ANOTHER tenant that happens to carry this tenant's root as its parent matches
// the predicate on content alone. Only `team_id = ?` keeps it out — and it has
// to be on BOTH branches of the UNION, because either branch alone returns rows.
//
// What that leak would look like is not abstract: MemoryChunksByRoots feeds
// reassembleMemory, which feeds the hit's whole-memory content, which goes onto
// the wire. A tenant would receive another tenant's prose inside a memory it
// legitimately owns, with nothing in the response marking it foreign.
//
// The queries were correct when this test was written. That is exactly why the
// test exists: AGENTS.md's rule is that a test for "X holds" must fail when X is
// removed, and before this, deleting either predicate left the whole suite
// green. Mutation-proven — delete `team_id = ?` from either branch of
// memoryChunkQuery and this goes red naming the leaked content.
func TestMemoryChunkQueriesRefuseToCrossTenants(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	const (
		victim    = "team-victim"
		attacker  = "team-attacker"
		secret    = "ATTACKER-TENANT-SECRET-CONTENT"
		rootText  = "the victim's own memory, long enough to be worth reassembling"
		childText = "a second chunk that legitimately belongs to the victim"
	)

	// The victim owns a two-chunk memory, filed the ordinary way so the root and
	// its child carry the real parent_id relationship rather than a hand-built one.
	added, err := svc.Add(ctx, victim, AddInput{
		Wing: "wing_alpha", Room: "decisions", SourceFile: "victim",
		Content: strings.Repeat(rootText+" "+childText+" ", 40),
	})
	if err != nil {
		t.Fatalf("seed victim memory: %v", err)
	}
	if len(added.Drawers) < 2 {
		t.Fatalf("fixture produced %d chunks; it needs a root AND a child", len(added.Drawers))
	}
	root := added.Drawers[0].ID

	// The hostile row: another tenant's drawer whose parent_id points at the
	// victim's root. Written through the repo directly because no tool would let
	// a caller forge this — the point is what the QUERY does if such a row exists,
	// however it got there (a restore, an import, a bug, a hostile write).
	if err := svc.repo.Save(ctx, []Drawer{{
		TeamID: attacker, ID: "attacker-chunk-1", Wing: "wing_alpha", Room: "decisions",
		SourceFile: "attacker", ChunkIndex: 1, Content: secret, ParentID: root,
	}}); err != nil {
		t.Fatalf("seed hostile row: %v", err)
	}
	// And a hostile ROOT sharing the victim's id would be caught by the other
	// branch, so cover it too: same id, different tenant.
	if err := svc.repo.Save(ctx, []Drawer{{
		TeamID: attacker, ID: root, Wing: "wing_alpha", Room: "decisions",
		SourceFile: "attacker", ChunkIndex: 0, Content: secret,
	}}); err != nil {
		t.Fatalf("seed hostile root: %v", err)
	}

	t.Run("MemoryChunksByRoots", func(t *testing.T) {
		byRoot, err := svc.repo.MemoryChunksByRoots(ctx, victim, []string{root})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(byRoot[root]) == 0 {
			t.Fatal("victim cannot read its own memory; the fixture proves nothing")
		}
		for _, d := range byRoot[root] {
			if d.TeamID != victim {
				t.Errorf("returned a %s row to %s (id %s)", d.TeamID, victim, d.ID)
			}
			if strings.Contains(d.Content, secret) {
				t.Errorf("CROSS-TENANT LEAK: another tenant's content reached %s and would be "+
					"reassembled into its memory and returned on the wire (id %s)", victim, d.ID)
			}
		}
	})

	t.Run("MemoryChunkIDsByRoots", func(t *testing.T) {
		byRoot, err := svc.repo.MemoryChunkIDsByRoots(ctx, victim, []string{root})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if len(byRoot[root]) == 0 {
			t.Fatal("victim cannot resolve its own memory's chunk ids; the fixture proves nothing")
		}
		for _, id := range byRoot[root] {
			if id == "attacker-chunk-1" {
				t.Errorf("CROSS-TENANT LEAK: %s resolved another tenant's chunk id, which "+
					"AnchorsForMemories would then attach anchors from", victim)
			}
		}
	})

	// The attacker must not read the victim either — the predicate has to scope in
	// both directions, not merely exclude one known id.
	t.Run("the other direction", func(t *testing.T) {
		byRoot, err := svc.repo.MemoryChunksByRoots(ctx, attacker, []string{root})
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		for _, d := range byRoot[root] {
			if d.TeamID != attacker {
				t.Errorf("CROSS-TENANT LEAK: %s read a %s row (id %s)", attacker, d.TeamID, d.ID)
			}
		}
	})
}
