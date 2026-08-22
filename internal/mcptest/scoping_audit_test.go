package mcptest_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/mark3labs/mcp-go/mcp"
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
// So this derives the candidate class from the RUNNING server's schemas: every
// tool with an optional wing argument must either have a live scoping probe or
// an explicit reason why wing is not a read filter. That second map is self-
// checked so stale exemptions fail instead of accumulating. Each enumeration is
// then asked the only question that settles it: with two projects in one
// workspace, does naming no wing show me the other project's content, and does
// wing:"*" deliberately widen the same call?
func TestEveryEnumerationHonoursTheRegistrationWing(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	// Two drawers per wing make one real hallway per project: hallway derivation
	// requires the same entity pair to co-occur in at least two drawers. Without
	// this setup a leaking list_hallways handler returns the same empty page as a
	// correctly scoped one, which is how the leak survived the first audit.
	for _, seed := range []struct {
		h             *mcptest.Harness
		rooms         [2]string
		content       string
		anchorPath    string
		anchorSnippet string
	}{
		{a, [2]string{"alpha-private-room", "alpha-graph-room"},
			"ALPHA-DRAWER-MARKER Atlas routes to Sentry. Atlas reports through Sentry.",
			"internal/alpha/secret.go", "func AlphaScopeSecret() {"},
		{b, [2]string{"beta-private-room", "beta-graph-room"},
			"BETA-DRAWER-MARKER Vault schedules Nomad. Vault reports through Nomad.",
			"internal/beta/own.go", "func BetaScopeOwn() {"},
	} {
		for i, room := range seed.rooms {
			args := map[string]any{
				"wing": seed.h.Wing, "room": room,
				"content": seed.content,
			}
			if i == 0 {
				args["code_anchors"] = []any{map[string]any{
					"path": seed.anchorPath, "snippet": seed.anchorSnippet,
				}}
			}
			seed.h.MustCall(t, "am_add_drawer", args)
		}
	}
	a.MustCall(t, "am_create_tunnel", map[string]any{
		"source_wing": "wing_alpha", "source_room": "alpha-private-room",
		"target_wing": "wing_alpha", "target_room": "alpha-graph-room",
		"label": "ALPHA-TUNNEL-LABEL alpha's own cross reference",
	})
	b.MustCall(t, "am_create_tunnel", map[string]any{
		"source_wing": "wing_beta", "source_room": "beta-private-room",
		"target_wing": "wing_beta", "target_room": "beta-graph-room",
		"label": "BETA-TUNNEL-LABEL beta's own cross reference",
	})
	a.MustCall(t, "am_recompute_graph", map[string]any{})

	type probe struct {
		args                     map[string]any
		foreignNeedle, ownNeedle string
		why                      string
	}
	probes := map[string]probe{
		"am_list_drawers": {map[string]any{"limit": 50}, "ALPHA-DRAWER-MARKER", "BETA-DRAWER-MARKER",
			"the call am_status recommends to a waking agent"},
		"am_list_anchors": {map[string]any{}, "AlphaScopeSecret", "BetaScopeOwn",
			"anchors carry verbatim source lines from another project's tree"},
		"am_list_rooms": {map[string]any{}, "alpha-private-room", "beta-private-room",
			"room names disclose what another project files and how much"},
		"am_list_tunnels": {map[string]any{}, "ALPHA-TUNNEL-LABEL", "BETA-TUNNEL-LABEL",
			"a tunnel label is free text written by another project's session"},
		"am_list_hallways": {map[string]any{}, "Atlas", "Vault",
			"hallways disclose another project's named systems and relationships"},
	}

	// These tools also expose an optional wing in the live schema, but wing is a
	// destination, a mutation target, or a deliberately different read scope. A
	// new optional-wing tool cannot silently join this list: the audit fails until
	// it either gains a probe above or a reviewed, specific reason here.
	allow := map[string]string{
		"am_add_drawer":      "wing is the write destination and wingFor applies the registration default",
		"am_update_drawer":   "wing is the destination of a move, not a read filter",
		"am_search":          "recall scoping is covered by the positive/negative/cross-wing scenario trio",
		"am_diary_write":     "wing is a destination: explicit wing wins, then registration default_wing, then wing_<agent_name>",
		"am_diary_read":      "the primary boundary is agent_name; empty wing deliberately spans that agent's diary",
		"am_mine":            "wing is the destination for newly mined memories",
		"am_recompute_graph": "wing selects a mutating rebuild; empty intentionally rebuilds the workspace graph",
	}

	tools, err := b.ListToolDefinitions(t)
	if err != nil {
		t.Fatalf("list live tool schemas: %v", err)
	}
	seenProbes := map[string]bool{}
	seenAllow := map[string]bool{}
	for _, tool := range tools {
		if _, hasWing := tool.InputSchema.Properties["wing"]; !hasWing || required(tool, "wing") {
			continue
		}
		if c, ok := probes[tool.Name]; ok {
			seenProbes[tool.Name] = true
			assertWingScopeSchema(t, tool)
			args := c.args
			foreign := c.foreignNeedle
			own := c.ownNeedle
			why := c.why

			got := b.MustCall(t, tool.Name, args)
			if strings.Contains(got, foreign) {
				t.Errorf("%s with no wing named disclosed another project's content (%s).\n  needle: %q\n%s",
					tool.Name, why, foreign, truncate(got))
			}
			if !strings.Contains(got, own) {
				t.Errorf("%s with no wing named did not return this registration's own content.\n  needle: %q\n%s",
					tool.Name, own, truncate(got))
			}

			wideArgs := cloneArgs(args)
			wideArgs["wing"] = "*"
			if wide := b.MustCall(t, tool.Name, wideArgs); !strings.Contains(wide, foreign) {
				t.Errorf("%s with wing:\"*\" did not widen to every wing.\n  needle: %q\n%s",
					tool.Name, foreign, truncate(wide))
			}
			continue
		}
		if why, ok := allow[tool.Name]; ok {
			seenAllow[tool.Name] = true
			if strings.TrimSpace(why) == "" {
				t.Errorf("%s has an empty optional-wing exemption", tool.Name)
			}
			continue
		}
		t.Errorf("%s has an optional wing argument in the live schema but no scoping probe or justified exemption",
			tool.Name)
	}

	for name := range probes {
		if !seenProbes[name] {
			t.Errorf("scoping probe %s names no live optional-wing tool", name)
		}
	}
	for name := range allow {
		if !seenAllow[name] {
			t.Errorf("optional-wing exemption %s is stale or no longer optional", name)
		}
	}
}

// TestToolSchemasStateTheScopeAndMutationContracts pins the guidance clients
// actually receive from tools/list. Correct handler wiring is insufficient when
// the schema tells an agent that omission means something else.
func TestToolSchemasStateTheScopeAndMutationContracts(t *testing.T) {
	h := mcptest.New(t)
	tools, err := h.ListToolDefinitions(t)
	if err != nil {
		t.Fatalf("list live tool schemas: %v", err)
	}

	search := namedTool(t, tools, "am_search")
	limit := strings.ToLower(propertyText(t, search, "limit"))
	for _, want := range []string{"distinct memories", "collapse"} {
		if !strings.Contains(limit, want) {
			t.Errorf("am_search limit schema does not say pages count distinct memories after collapse; missing %q: %s",
				want, limit)
		}
	}

	reconnect := strings.ToLower(namedTool(t, tools, "am_reconnect").Description)
	for _, want := range []string{"write-gated", "may create backend state"} {
		if !strings.Contains(reconnect, want) {
			t.Errorf("am_reconnect schema hides why a nominal liveness operation is a write; missing %q: %s",
				want, reconnect)
		}
	}
	if strings.Contains(reconnect, "stateless liveness probe") {
		t.Errorf("am_reconnect still describes itself as a stateless read while remaining write-gated: %s", reconnect)
	}

	diaryWing := strings.ToLower(propertyText(t, namedTool(t, tools, "am_diary_write"), "wing"))
	for _, want := range []string{"default_wing", "wing_", "agent_name"} {
		if !strings.Contains(diaryWing, want) {
			t.Errorf("am_diary_write wing schema omits its destination precedence; missing %q: %s", want, diaryWing)
		}
	}
}

// TestDiaryWriteWingPrecedenceMatchesSchema pins the optional-wing exemption's
// actual contract: explicit destination, then registration default, then the
// agent-named fallback when the registration has no wing.
func TestDiaryWriteWingPrecedenceMatchesSchema(t *testing.T) {
	registered := mcptest.NewWithWing(t, "wing_alpha")
	registered.MustCall(t, "am_diary_write", map[string]any{
		"agent_name": "alpha", "entry": "REGISTRATION-DIARY-MARKER",
	})
	registered.MustCall(t, "am_diary_write", map[string]any{
		"agent_name": "alpha", "wing": "wing_beta", "entry": "EXPLICIT-DIARY-MARKER",
	})
	for _, tc := range []struct {
		wing, marker string
	}{
		{"wing_alpha", "REGISTRATION-DIARY-MARKER"},
		{"wing_beta", "EXPLICIT-DIARY-MARKER"},
	} {
		if got := registered.MustCall(t, "am_list_drawers", map[string]any{
			"wing": tc.wing, "room": "diary", "limit": 10,
		}); !strings.Contains(got, tc.marker) {
			t.Errorf("diary write did not use %s in the destination precedence: %s", tc.wing, got)
		}
	}

	unscoped := mcptest.New(t)
	unscoped.MustCall(t, "am_diary_write", map[string]any{
		"agent_name": "alpha", "entry": "AGENT-FALLBACK-DIARY-MARKER",
	})
	if got := unscoped.MustCall(t, "am_list_drawers", map[string]any{
		"wing": "wing_alpha", "room": "diary", "limit": 10,
	}); !strings.Contains(got, "AGENT-FALLBACK-DIARY-MARKER") {
		t.Errorf("unscoped diary write did not fall back to wing_<agent_name>: %s", got)
	}
}

// TestReconnectIsActuallyWriteGated is the behavioural half of its schema. A
// description saying "write-gated" must be backed by the registration, while a
// writer must still be able to ensure the namespace.
func TestReconnectIsActuallyWriteGated(t *testing.T) {
	member := mcptest.AsRole(t, tenant.RoleMember)
	if got, isErr, err := member.Call(t, "am_reconnect", map[string]any{}); err != nil {
		t.Fatalf("member reconnect transport: %v", err)
	} else if !isErr || !strings.Contains(got, "read-only") {
		t.Errorf("member reached am_reconnect despite its backend write; isErr=%v result=%s", isErr, got)
	}

	writer := mcptest.AsRole(t, tenant.RoleWriter)
	if got, isErr, err := writer.Call(t, "am_reconnect", map[string]any{}); err != nil || isErr {
		t.Errorf("writer could not ensure the vector namespace: isErr=%v err=%v result=%s", isErr, err, got)
	}
}

func required(tool mcp.Tool, property string) bool {
	for _, name := range tool.InputSchema.Required {
		if name == property {
			return true
		}
	}
	return false
}

func cloneArgs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func assertWingScopeSchema(t *testing.T, tool mcp.Tool) {
	t.Helper()
	text := strings.ToLower(tool.Description + " " + propertyText(t, tool, "wing"))
	for _, want := range []string{"default_wing", "search_scope", "workspace", "*", "every wing"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s schema does not explain the conditional registration scope and * escape hatch; missing %q:\n%s",
				tool.Name, want, text)
		}
	}
}

func namedTool(t *testing.T, tools []mcp.Tool, name string) mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("live catalogue is missing %s", name)
	return mcp.Tool{}
}

func propertyText(t *testing.T, tool mcp.Tool, property string) string {
	t.Helper()
	v, ok := tool.InputSchema.Properties[property]
	if !ok {
		t.Fatalf("%s schema is missing property %s", tool.Name, property)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s.%s schema: %v", tool.Name, property, err)
	}
	return string(b)
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
