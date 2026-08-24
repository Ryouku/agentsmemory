package palace

import (
	"context"
	"testing"
)

// TestTraverseCannotLeaveAWingItNeverEntered fails when a walk uses a shared room
// NAME as a bridge into a project it has nothing to do with.
//
// A room node is global — one node per room name, carrying every wing that uses
// that name — so "diary" is a single node standing for eleven unrelated wings.
// Matching neighbours against the current room's full wing set let a walk enter
// such a room from one project and leave it into any of the others. That is not a
// link between related memories; it is a name collision presented as one, and it
// crossed the wing boundary the rest of the protocol is built on without saying
// so. On the live palace a two-hop walk from a single-wing room returned 36 of 36
// rooms: the entire universe, at zero selectivity.
//
// The corpus here is the smallest thing that has the shape: origin belongs to
// wing_a only, shared belongs to both, stranger belongs to wing_b only. A walk
// from origin must reach shared (a real neighbour, same wing) and must NOT reach
// stranger, which is only reachable by treating shared's two wings as
// interchangeable.
//
// Asserting reachability BOTH ways is the point. A test that only checked
// stranger's absence would pass just as happily if traverse were broken outright
// and returned nothing but its start room.
func TestTraverseCannotLeaveAWingItNeverEntered(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mineForGraph(t, svc, team, "wing_a", "origin", "Redis", "Postgres")
	mineForGraph(t, svc, team, "wing_a", "shared", "Redis", "Postgres")
	mineForGraph(t, svc, team, "wing_b", "shared", "Redis", "Mongo")
	mineForGraph(t, svc, team, "wing_b", "stranger", "Redis", "Mongo")

	nodes, err := svc.Traverse(ctx, team, "origin", 2)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	reached := make(map[string]TraverseNode, len(nodes))
	for _, n := range nodes {
		reached[n.Room] = n
	}

	if _, ok := reached["origin"]; !ok {
		t.Fatalf("traverse did not return its own start room: %+v", nodes)
	}
	if _, ok := reached["shared"]; !ok {
		t.Fatalf("traverse from origin never reached shared, which is in the SAME wing — "+
			"the walk is not scoped, it is broken: %+v", nodes)
	}
	if n, ok := reached["stranger"]; ok {
		t.Errorf("traverse from origin reached stranger at hop %d via %v.\n"+
			"  origin is in wing_a only and stranger is in wing_b only. The only path\n"+
			"  between them is the room NAME \"shared\", which both wings happen to use.\n"+
			"  Stepping across it is a name collision being presented as a link, and it\n"+
			"  silently crosses a wing boundary: at scale this is what returned every\n"+
			"  room in the palace from a two-hop walk. A walk must only step onward\n"+
			"  through a wing it already arrived by.", n.Hop, n.Wings)
	}

	// The wing that admitted each hop has to be reported, or a caller cannot tell a
	// same-wing neighbour from a bridge even when the walk is correct.
	if via := reached["shared"].ConnectedVia; len(via) != 1 || via[0] != "wing_a" {
		t.Errorf("shared was reached via %v, want [wing_a] — the walk entered from wing_a "+
			"and connected_via is what discloses that to the caller", via)
	}
	if via := reached["origin"].ConnectedVia; len(via) != 0 {
		t.Errorf("the start room reports connected_via=%v, but it was not reached "+
			"through anything", via)
	}
}
