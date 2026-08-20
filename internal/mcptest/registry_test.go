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
