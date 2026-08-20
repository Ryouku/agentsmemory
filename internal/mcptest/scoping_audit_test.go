package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestEveryEnumerationHonoursTheRegistrationWing audits the CLASS, not the
// instance.
//
// am_list_drawers leaked because it took `wing` verbatim instead of resolving
// it. Fixing that one tool answers nothing about the others: a source read finds
// eight tools that take the argument raw, and reading cannot say which of those
// are leaks and which are deliberate — am_diary_read is scoped by AGENT and
// documents its empty wing as intentional, am_update_drawer's wing is a move
// TARGET rather than a filter.
//
// So this asks each enumeration the only question that settles it: with two
// projects in one workspace, does naming no wing show me the other project's
// content? A scope that one route ignores is not a scope, and the routes have to
// be enumerated by running them.
func TestEveryEnumerationHonoursTheRegistrationWing(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	// Seed both wings with content that is unmistakable if it surfaces.
	for _, h := range []*mcptest.Harness{a, b} {
		h.MustCall(t, "am_add_drawer", map[string]any{
			"wing": h.Wing, "room": "decisions",
			"content": "a decision belonging to " + h.Wing,
			"code_anchors": []any{map[string]any{
				"path": "internal/" + h.Wing + "/x.go", "snippet": "func Secret" + h.Wing + "() {",
			}},
		})
	}
	a.MustCall(t, "am_create_tunnel", map[string]any{
		"source_wing": "wing_alpha", "source_room": "decisions",
		"target_wing": "wing_alpha", "target_room": "decisions",
		"label": "ALPHA-TUNNEL-LABEL alpha's own cross reference",
	})

	// Each enumeration, called by BETA with no wing named, must not disclose
	// alpha's content. The needle is what alpha wrote.
	for _, c := range []struct {
		tool   string
		args   map[string]any
		needle string
		why    string
	}{
		{"am_list_drawers", map[string]any{"limit": 50}, "belonging to wing_alpha",
			"the call am_status recommends to a waking agent"},
		{"am_list_anchors", map[string]any{}, "Secretwing_alpha",
			"anchors carry verbatim source lines from another project's tree"},
		{"am_list_rooms", map[string]any{}, "wing_alpha",
			"room names disclose what another project files and how much"},
		{"am_list_tunnels", map[string]any{}, "ALPHA-TUNNEL-LABEL",
			"a tunnel label is free text written by another project's session"},
	} {
		got := b.MustCall(t, c.tool, c.args)
		if strings.Contains(got, c.needle) {
			t.Errorf("%s with no wing named disclosed another project's content (%s).\n  needle: %q\n%s",
				c.tool, c.why, c.needle, truncate(got))
		}
	}
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
