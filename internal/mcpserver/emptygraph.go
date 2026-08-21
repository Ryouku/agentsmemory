package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// graphReader is the slice of the palace this note needs: what the derived graph
// currently holds, and a bounded look at the memories it is derived FROM.
// Declared at the consumer, like wingReader, so the decision can be driven by a
// test without a database behind it.
type graphReader interface {
	ListHallways(ctx context.Context, teamID, wing string) ([]palace.Hallway, error)
	List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]palace.Drawer, error)
}

// graphNoteSample bounds how many recently-filed memories the note reads to tell
// "nothing carries an entity" from "entities are there but never meet".
//
// Bounded on purpose. The exact answer needs a predicate scan of the whole
// scope, and this fires on a tool call rather than a maintenance job — a
// diagnostic that costs a scan is a diagnostic somebody turns off. So the note
// claims only what it actually looked at, the N most recently filed, and says
// so, rather than generalising from a sample to a whole wing.
const graphNoteSample = 50

// emptyGraphNote explains a DERIVED graph that holds nothing, which is a
// different fact from "this tool has nothing to report".
//
// Measured 2026-08-21 against the live self-hosted palace: 0 hallways, and 0 of
// 366 drawers carrying an entity. Hallways are derived from drawers.entities,
// and extractEntities is called in exactly one place in the tree — mining. The
// path every am_add_drawer takes writes its rows without touching Entities at
// all, so on a palace populated through the agent surface the graph is not
// empty-for-now, it is structurally unreachable: am_recompute_graph reports
// success and derives nothing however often it runs. An agent sees an empty
// result, concludes the graph is empty, and stops asking.
//
// Three states produce that empty result and each needs a different action, so
// each gets its own message: nothing is filed here at all; memories are filed
// but none carries an entity; entities are there but no pair co-occurs in enough
// drawers to clear the hallway floor.
//
// Why am_traverse and am_graph_stats carry a note about HALLWAYS even when their
// own room walk returns rows: both are advertised as graph tools, and an agent
// reading a walk that never crosses an entity — or total_edges:0 — concludes the
// graph is empty. The note is about the DERIVED graph and is silent whenever
// THAT has content. Recorded here so the mismatch is a decision rather than
// something a later reader has to infer.
//
// It returns "" the moment one hallway exists, so a working graph reads exactly
// as it did before, and it fails OPEN: a lookup failure leaves the call as it
// was rather than turning it into a warning about the palace's own health.
func emptyGraphNote(ctx context.Context, g graphReader, teamID, wing string) string {
	halls, err := g.ListHallways(ctx, teamID, wing)
	if err != nil {
		return "" // fails OPEN: a lookup failure must not turn a working call into a warning
	}
	if len(halls) > 0 {
		return ""
	}
	recent, err := g.List(ctx, teamID, wing, "", graphNoteSample, 0)
	if err != nil {
		return ""
	}

	// wing "" is the whole palace: am_traverse and am_graph_stats answer for the
	// team, not for one project.
	scope := "this palace"
	if w := strings.TrimSpace(wing); w != "" {
		scope = fmt.Sprintf("the wing %q", w)
	}

	if len(recent) == 0 {
		return fmt.Sprintf("the graph is derived from filed memories and %s holds no memories yet, so "+
			"there is nothing to derive one from. That is an empty palace rather than a graph that "+
			"failed to build: file memories and the graph follows them.", scope)
	}
	for _, d := range recent {
		if len(d.Entities) > 0 {
			return fmt.Sprintf("%s holds memories carrying entities, but no pair of entities co-occurs "+
				"in enough drawers to become a hallway. Run am_recompute_graph — it may simply not have "+
				"run since those memories were filed; if it still derives nothing, the entities are "+
				"there but never appear together.", scope)
		}
	}
	return fmt.Sprintf("%s holds memories, but not one of the %d most recently filed carries an entity, "+
		"and hallways are derived from entities: am_recompute_graph will report success and derive "+
		"nothing however often it runs. Entities are stamped by am_mine; a memory filed with "+
		"am_add_drawer carries none, so a palace populated through the agent surface has no derivable "+
		"graph at all.", scope, len(recent))
}
