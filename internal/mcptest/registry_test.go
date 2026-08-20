package mcptest_test

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// scenarios is the end-to-end exercise set. It starts almost empty on purpose:
// the gate above reports what is missing, and that report is the measurement
// ADR-008 opens with. Entries are added by T3 and T4.
var scenarios = []mcptest.Scenario{
	{
		Name:  "a filed memory is recalled by the question it answers",
		Tools: []string{"am_add_drawer", "am_search"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_scenario", "room": "decisions",
				"content": "rollbacks go through the previous image tag, never a rebuild",
			})
			if out := h.MustCall(t, "am_search", map[string]any{
				"query": "how do we roll back", "wing": "wing_scenario", "limit": 5,
			}); !contains(out, "previous image tag") {
				t.Errorf("a filed memory was not recalled by its own question:\n%s", out)
			}
		},
	},
	{
		Name:  "the wake-up call reports the workspace it is scoped to",
		Tools: []string{"am_add_drawer", "am_status"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_scenario", "room": "decisions", "content": "something to count",
			})
			out := h.MustCall(t, "am_status", map[string]any{})
			m := h.JSON(t, out)
			if m["ok"] != true {
				t.Errorf("am_status did not report ok:\n%s", out)
			}
			if m["total_drawers"] == nil {
				t.Errorf("am_status reported no drawer total, so it cannot ground a waking agent:\n%s", out)
			}
		},
	},
	{
		// The scenario that would have caught a capability shipped unreachable.
		//
		// am_update_drawer's handler read code_anchors from the moment it was
		// written, and the tool never DECLARED the argument. Every test was a
		// source grep or a unit test on the parser, all green, while no agent
		// reading the schema could learn the capability existed — which was the
		// exact complaint the work set out to fix. A review caught it; a call
		// through the tool surface would have caught it the same day.
		Name:  "a corrected memory can be re-anchored, and the new anchor is the one that sticks",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_list_anchors"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_anchor", "room": "decisions",
				"content": "the retry budget is three attempts",
				"code_anchors": []any{map[string]any{
					"path": "internal/retry/retry.go", "snippet": "const budget = 3",
				}},
			})
			id := firstDrawerID(t, h, out)

			h.MustCall(t, "am_update_drawer", map[string]any{
				"id": id, "content": "the retry budget is five attempts",
				"code_anchors": []any{map[string]any{
					"path": "internal/retry/retry.go", "snippet": "const budget = 5",
				}},
			})

			got := h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_anchor"})
			if !contains(got, "budget = 5") {
				t.Errorf("the corrected memory did not carry its new anchor — a correction that "+
					"keeps its dead anchor stays flagged STALE forever:\n%s", got)
			}
			if contains(got, "budget = 3") {
				t.Errorf("the superseded anchor is still live beside the new one; REPLACE must "+
					"not merge:\n%s", got)
			}
		},
	},
	{
		// A refused argument must leave the memory as it found it. The first
		// version updated the drawer and THEN validated the anchors, so this call
		// changed the content and returned an error announcing a refusal.
		Name:  "a refused anchor list leaves the memory's content untouched",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_get_drawer"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_atomic", "room": "decisions",
				"content": "the original sentence, which must survive a refused update",
			})
			id := firstDrawerID(t, h, out)

			h.MustRefuse(t, "am_update_drawer", map[string]any{
				"id": id, "content": "a replacement that must NOT be applied",
				"code_anchors": []any{map[string]any{"paht": "typo.go", "snippet": "x"}},
			})

			got := h.MustCall(t, "am_get_drawer", map[string]any{"id": id})
			if !contains(got, "the original sentence") {
				t.Errorf("a refused call changed the content anyway, so the error announced a "+
					"refusal that did not happen:\n%s", got)
			}
			if contains(got, "must NOT be applied") {
				t.Errorf("the rejected content was written:\n%s", got)
			}
		},
	},
	{
		// REGRESSION — the chunk-0-only update.
		//
		// A memory over ChunkSize is several drawers sharing a parent, each with
		// its own embedding. am_update_drawer rewrote chunk 0 and left chunk 1
		// live, still returning the OLD text from search with nothing marking it
		// retracted. The update reported success while a false half of the memory
		// kept competing on equal footing with the correction.
		Name:  "regression: an updated memory has no stale half left in search",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_search"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			// Over ChunkSize (1600) so the memory really is several drawers; a
			// fixture below the threshold cannot reproduce this at all.
			old := "SUPERSEDED-MARKER never brief from the index file. " + filler(1900)
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_chunked", "room": "decisions", "content": old,
			})
			if n := drawerCount(t, h, out); n < 2 {
				t.Fatalf("fixture produced %d drawer(s); this scenario needs a multi-chunk memory "+
					"or it cannot see the defect it exists for", n)
			}
			id := firstDrawerID(t, h, out)

			// The fix REFUSES rather than rewriting every chunk, and the refusal
			// is the contract: a partial rewrite is what produced a false half
			// competing with its own correction, so the tool declines and says
			// what to do instead.
			msg := h.MustRefuse(t, "am_update_drawer", map[string]any{
				"id": id, "content": "CORRECTED-MARKER always brief from the index file.",
			})
			if !contains(msg, "chunk") {
				t.Errorf("the refusal does not explain that this is a multi-chunk memory:\n%s", msg)
			}

			// And nothing may have half-landed: the original must still be whole,
			// with no corrected fragment beside it.
			got := h.MustCall(t, "am_search", map[string]any{
				"query": "brief from the index file", "wing": "wing_chunked", "limit": 20,
			})
			if !contains(got, "SUPERSEDED-MARKER") {
				t.Errorf("the refused update removed the original text anyway:\n%s", got)
			}
			if contains(got, "CORRECTED-MARKER") {
				t.Errorf("a refused multi-chunk update wrote one chunk anyway — that is the exact "+
					"defect this scenario exists for:\n%s", got)
			}
		},
	},
	{
		// REGRESSION — the delete that orphaned child chunks.
		//
		// Deleting a multi-chunk memory by its parent id removed the parent and
		// left the children embedded and searchable, pointing at a parent that no
		// longer existed. A get said it was gone; only a search could see it.
		Name:  "regression: deleting a memory leaves no chunk behind, by any route",
		Tools: []string{"am_add_drawer", "am_delete_drawer", "am_search", "am_get_drawer"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			// The marker must land in the LAST chunk, not the first. The defect
			// leaves the CHILDREN behind, so a marker in chunk 0 is removed by the
			// buggy delete too and the scenario passes while the orphan survives.
			// Measured: with the marker at the front, truncating the delete to the
			// parent row left this scenario green.
			body := filler(1900) + " ORPHAN-MARKER the rollback procedure for the queue worker."
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_orphan", "room": "decisions", "content": body,
			})
			if n := drawerCount(t, h, out); n < 2 {
				t.Fatalf("fixture produced %d drawer(s); the orphaning defect only exists for a "+
					"multi-chunk memory", n)
			}
			id := firstDrawerID(t, h, out)

			h.MustCall(t, "am_delete_drawer", map[string]any{"id": id})

			// Checked by BOTH routes, and the pair is the point: a get of the
			// parent said "gone" while the children were still embedded and
			// searchable. Either route alone would have reported this fixed.
			if out, isErr, err := h.Call(t, "am_get_drawer", map[string]any{"id": id}); err != nil {
				t.Fatalf("am_get_drawer: %v", err)
			} else if !isErr && contains(out, "rollback procedure") {
				t.Errorf("the deleted parent is still fetchable by id:\n%s", out)
			}
			if got := h.MustCall(t, "am_search", map[string]any{
				"query": "rollback procedure for the queue worker", "wing": "wing_orphan", "limit": 20,
			}); contains(got, "ORPHAN-MARKER") {
				t.Errorf("a deleted memory is still searchable, so a chunk outlived the delete — "+
					"the marker sits in the LAST chunk precisely so this can see it:\n%s", got)
			}
		},
	},
	{
		// REGRESSION — the anchor list that cleared instead of refusing.
		Name:  "regression: an all-unreadable anchor list refuses and keeps the old anchors",
		Tools: []string{"am_add_drawer", "am_update_drawer", "am_list_anchors"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			out := h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_keepanchor", "room": "decisions",
				"content": "the cache is invalidated on write",
				"code_anchors": []any{map[string]any{
					"path": "internal/cache/cache.go", "snippet": "func Invalidate() {",
				}},
			})
			id := firstDrawerID(t, h, out)

			h.MustRefuse(t, "am_update_drawer", map[string]any{
				"id":           id,
				"code_anchors": []any{map[string]any{"paht": "internal/cache/cache.go", "snippet": "x"}},
			})

			if got := h.MustCall(t, "am_list_anchors", map[string]any{"wing": "wing_keepanchor"}); !contains(got, "func Invalidate() {") {
				t.Errorf("a refused anchor list cleared the anchors it refused to replace:\n%s", got)
			}
		},
	},
	{
		// A tunnel is the only way a scoped recall can cross wings, so an unwoven
		// or unreadable one is a cross-project relationship that is invisible
		// forever. Audited 2026-08-20: every tunnel in the live palace had
		// access_count 0, and nothing had ever tested that they can be read back.
		Name:  "a tunnel woven between two wings is findable from either end",
		Tools: []string{"am_add_drawer", "am_create_tunnel", "am_list_tunnels", "am_find_tunnels"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			for _, w := range []string{"wing_app", "wing_infra"} {
				h.MustCall(t, "am_add_drawer", map[string]any{
					"wing": w, "room": "decisions", "content": "a decision filed in " + w,
				})
			}
			h.MustCall(t, "am_create_tunnel", map[string]any{
				"source_wing": "wing_app", "source_room": "decisions",
				"target_wing": "wing_infra", "target_room": "decisions",
				"label": "the deploy behaviour is explained by the infra decision",
			})

			if got := h.MustCall(t, "am_list_tunnels", map[string]any{"wing": "wing_app"}); !contains(got, "wing_infra") {
				t.Errorf("the tunnel is not listed from its source wing:\n%s", got)
			}
			// Tunnels are symmetric, so the far end must see it too — a link only
			// its author can find is not a link.
			if got := h.MustCall(t, "am_list_tunnels", map[string]any{"wing": "wing_infra"}); !contains(got, "wing_app") {
				t.Errorf("the tunnel is not listed from its TARGET wing; a link only its author "+
					"can find is not a link:\n%s", got)
			}
			// am_find_tunnels answers a different question — which ROOMS span two
			// wings, a passive connector rather than the woven link above. Asserted
			// against what it promises, not against what its name suggests.
			if got := h.MustCall(t, "am_find_tunnels", map[string]any{
				"wing_a": "wing_infra", "wing_b": "wing_app",
			}); !contains(got, "decisions") {
				t.Errorf("am_find_tunnels does not report the room both wings share:\n%s", got)
			}
		},
	},
	{
		// The knowledge graph is the only structure that can say a fact STOPPED
		// being true; search returns the best match and never the most current.
		Name:  "a fact added to the graph is queryable by its subject",
		Tools: []string{"am_kg_add", "am_kg_query", "am_kg_stats"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_kg_add", map[string]any{
				"subject": "queue-worker", "predicate": "deploys_to", "object": "batch-node-3",
				"valid_from": "2026-08-01",
			})
			if got := h.MustCall(t, "am_kg_query", map[string]any{"entity": "queue-worker"}); !contains(got, "batch-node-3") {
				t.Errorf("a fact added to the graph is not returned by a query for its subject:\n%s", got)
			}
			if got := h.MustCall(t, "am_kg_stats", map[string]any{}); contains(got, "\"triples\":0") {
				t.Errorf("the graph reports no triples after one was added:\n%s", got)
			}
		},
	},
	{
		// The diary is the cross-session thread, and it is read by exact agent
		// name — measured 2026-08-20, 89 entries had already fragmented across 11
		// names, so a session picking a different one reads none of the others.
		Name:  "a diary entry is readable by the agent that wrote it",
		Tools: []string{"am_diary_write", "am_diary_read"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_diary_write", map[string]any{
				"agent_name": "scenario-agent", "wing": "wing_diary",
				"entry": "tried the batch path first; it deadlocked on the locked table",
			})
			if got := h.MustCall(t, "am_diary_read", map[string]any{
				"agent_name": "scenario-agent", "wing": "wing_diary",
			}); !contains(got, "deadlocked on the locked table") {
				t.Errorf("an agent cannot read back its own diary entry:\n%s", got)
			}
			// A different name must read nothing — that IS the fragmentation, and
			// pinning it means the behaviour is a decision rather than a surprise.
			if got := h.MustCall(t, "am_diary_read", map[string]any{
				"agent_name": "a-different-agent", "wing": "wing_diary",
			}); contains(got, "deadlocked on the locked table") {
				t.Errorf("one agent's diary answered another agent's read:\n%s", got)
			}
		},
	},
	{
		Name:  "the palace can describe its own shape",
		Tools: []string{"am_add_drawer", "am_list_wings", "am_list_rooms", "am_get_taxonomy"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_shape", "room": "gotchas", "content": "a gotcha worth keeping",
			})
			for tool, want := range map[string]string{
				"am_list_wings": "wing_shape", "am_get_taxonomy": "wing_shape",
			} {
				if got := h.MustCall(t, tool, map[string]any{}); !contains(got, want) {
					t.Errorf("%s does not report %s:\n%s", tool, want, got)
				}
			}
			if got := h.MustCall(t, "am_list_rooms", map[string]any{"wing": "wing_shape"}); !contains(got, "gotchas") {
				t.Errorf("am_list_rooms does not report the room just written to:\n%s", got)
			}
		},
	},
	{
		Name:  "a near-duplicate is reported before it is filed twice",
		Tools: []string{"am_add_drawer", "am_check_duplicate"},
		Run: func(t *testing.T, h *mcptest.Harness) {
			const text = "the reranker pool is taken off the fused head, so fusion decides what it sees"
			h.MustCall(t, "am_add_drawer", map[string]any{
				"wing": "wing_dup", "room": "decisions", "content": text,
			})
			if got := h.MustCall(t, "am_check_duplicate", map[string]any{"content": text}); !contains(got, "reranker pool") {
				t.Errorf("filing the same text twice is not reported as a duplicate:\n%s", got)
			}
		},
	},
}

// filler pads a memory past ChunkSize so it is stored as several drawers.
func filler(n int) string {
	const s = "The queue worker drains in batches and retries with backoff. "
	out := ""
	for len(out) < n {
		out += s
	}
	return out
}

// drawerCount reports how many drawers an add produced, so a multi-chunk
// scenario can prove its fixture is actually multi-chunk before asserting
// anything about chunks.
func drawerCount(t *testing.T, h *mcptest.Harness, out string) int {
	t.Helper()
	rows, _ := h.JSON(t, out)["drawers"].([]any)
	return len(rows)
}

// firstDrawerID pulls the id of the first drawer an add returned.
func firstDrawerID(t *testing.T, h *mcptest.Harness, out string) string {
	t.Helper()
	m := h.JSON(t, out)
	rows, ok := m["drawers"].([]any)
	if !ok || len(rows) == 0 {
		t.Fatalf("add returned no drawers:\n%s", out)
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected drawer shape:\n%s", out)
	}
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatalf("drawer has no id:\n%s", out)
	}
	return id
}

// unobservable names tools this in-process harness cannot see the effect of.
// Each needs an external dependency; the gate rejects any other reason.
var unobservable = []mcptest.Unobservable{}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
