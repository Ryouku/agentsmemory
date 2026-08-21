package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestScenarioAMemberMayReadAndMayNotWrite drives the whole stack — real HTTP
// transport, real MCP client, real registration — as a least-privileged member.
//
// The unit test in internal/mcpserver proves the guard refuses. This proves the
// guard is REACHED: that a role resolved at the edge survives the transport, the
// context bridge and the registration, and arrives where the decision is made.
// Until this existed the server resolved a role for every call and enforced it on
// one tool of forty-one, and every end-to-end scenario ran as admin — so the
// enforcement could have been absent, or present and unreachable, and the suite
// would have looked identical.
func TestScenarioAMemberMayReadAndMayNotWrite(t *testing.T) {
	member := mcptest.AsRole(t, tenant.RoleMember)

	// Reading is untouched: a member is a full participant in recall.
	if out := member.MustCall(t, "am_status", map[string]any{}); !strings.Contains(out, "team_id") {
		t.Errorf("a member could not read am_status: %s", out)
	}
	if _, isErr, err := member.Call(t, "am_search", map[string]any{"query": "anything"}); err != nil || isErr {
		t.Errorf("a member was refused a read: isErr=%v err=%v", isErr, err)
	}

	// Writing is refused, and the refusal says why rather than failing obscurely.
	out, isErr, err := member.Call(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "a member should not be able to file this",
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !isErr {
		t.Fatalf("a member-role caller filed a drawer; the write guard is not reached over HTTP: %s", out)
	}
	if !strings.Contains(out, "read-only") {
		t.Errorf("the refusal does not tell the caller why it was refused: %s", out)
	}

	// And nothing was written — a refusal that still performs the write is worse
	// than no refusal, because it reports a failure the caller then works around.
	got, _, _ := member.Call(t, "am_search", map[string]any{"query": "a member should not be able to file this"})
	if strings.Contains(got, "a member should not be able to file this") {
		t.Error("the refused content is in the palace: the guard reported a refusal and wrote anyway")
	}
}

// TestScenarioAWriterMayWrite is the positive half. Without it, a guard that
// refused EVERY role would pass the test above and break the product.
func TestScenarioAWriterMayWrite(t *testing.T) {
	writer := mcptest.AsRole(t, tenant.RoleWriter)
	out, isErr, err := writer.Call(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "a writer files this",
	})
	if err != nil || isErr {
		t.Fatalf("a writer was refused a write: isErr=%v err=%v out=%s", isErr, err, out)
	}
	if found := writer.MustCall(t, "am_search", map[string]any{"query": "a writer files this"}); !strings.Contains(found, "a writer files this") {
		t.Errorf("the writer's drawer is not recallable: %s", found)
	}
}
