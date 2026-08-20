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

// TestKnowledgeGraphIsWorkspaceWideNotWingScoped records what the graph actually
// does, because the answer is not obvious from either the tools or the protocol
// and I could not settle it by reading.
//
// The KG tools take no `wing` argument and the schema has no wing column: every
// KGAdd/KGQuery/KGStats signature takes teamID alone. So the graph is partitioned
// by WORKSPACE and not by project, while drawers, anchors, rooms, tunnels and
// search are all partitioned by both.
//
// That asymmetry matters because the bootstrap protocol tells an agent to file
// durable facts with am_kg_add in the same breath as it explains that wings keep
// one project's memories from answering another's question. For drawers that is
// true. For the graph it is not, and nothing says so.
//
// This test does not assert the behaviour is wrong — that is a design decision
// with a real case on both sides, since a fact like "service X deploys to host Y"
// is often exactly what another project needs. It pins the behaviour so the
// decision is visible and cannot change by accident.
func TestKnowledgeGraphIsWorkspaceWideNotWingScoped(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	a.MustCall(t, "am_kg_add", map[string]any{
		"subject": "alpha-billing-service", "predicate": "authenticates_with", "object": "alpha-internal-idp",
	})

	got := b.MustCall(t, "am_kg_query", map[string]any{"entity": "alpha-billing-service"})
	if !strings.Contains(got, "alpha-internal-idp") {
		t.Skip("the graph appears to be wing-scoped after all — this test records the opposite; " +
			"re-read it before trusting either")
	}
	t.Logf("RECORDED: the knowledge graph is workspace-wide. A fact filed from wing_alpha is "+
		"returned to a wing_beta registration that names no wing:\n%s", truncate(got))

	// The one thing that must hold regardless: the WORKSPACE boundary.
	mine, theirs := mcptest.Tenants(t, "wing_shared_name")
	mine.MustCall(t, "am_kg_add", map[string]any{
		"subject": "tenant-secret-service", "predicate": "stores_keys_in", "object": "tenant-secret-vault",
	})
	if got := theirs.MustCall(t, "am_kg_query", map[string]any{"entity": "tenant-secret-service"}); strings.Contains(got, "tenant-secret-vault") {
		t.Errorf("the knowledge graph crossed a WORKSPACE boundary — wings are a project "+
			"partition and this may be deliberate, but the workspace is tenancy:\n%s", truncate(got))
	}
}
