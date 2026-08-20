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
