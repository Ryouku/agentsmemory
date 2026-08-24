package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestScenarioSecondPartySeesASharedWing and its negative counterpart are
// deliberately a pair, and neither is worth much alone.
//
// A test showing that what A wrote is visible to B passes with wing scoping
// removed entirely — it is satisfied by a server that shows everything to
// everyone. A test showing that A's private wing is invisible to B passes with
// search broken. Only both together say "the scope is where it should be", which
// is the property SEARCH_SCOPE and wingFor exist for and which nothing has ever
// tested.
func TestScenarioSecondPartySeesASharedWing(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	a.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_shared", "room": "decisions",
		"content": "the deploy runs from the release branch, decided by the alpha team",
	})

	got := b.MustCall(t, "am_search", map[string]any{
		"query": "which branch does the deploy run from", "wing": "wing_shared", "limit": 5,
	})
	if !strings.Contains(got, "release branch") {
		t.Fatalf("the second party could not read a wing both share — a shared wing is the whole "+
			"mechanism for one project's sessions to reach each other:\n%s", got)
	}
}

func TestScenarioSecondPartyDoesNotSeeAnotherWing(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	a.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_alpha", "room": "decisions",
		"content": "alpha rotates its signing key every ninety days",
	})

	// B names no wing, so its registration's wing applies. Alpha's memory must
	// not answer beta's question: that is what keeps one project's decisions from
	// being acted on in another, and it is the failure the wings exist to prevent.
	got := b.MustCall(t, "am_search", map[string]any{
		"query": "how often is the signing key rotated", "limit": 5,
	})
	if strings.Contains(got, "rotates its signing key") {
		t.Fatalf("another project's memory answered this party's question with no wing named — "+
			"scoped recall is not holding:\n%s", got)
	}
}

// TestScenarioCrossWingRecallReachesTheOtherParty pins the escape hatch the team
// actually relies on for cross-project questions, and which nothing tests.
//
// Scoping without a way out is a silo: an infrastructure decision explains an
// application's deploy behaviour, and a session that cannot ask across wings
// re-derives it. wing:"*" is that way out.
func TestScenarioCrossWingRecallReachesTheOtherParty(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	a.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_alpha", "room": "decisions",
		"content": "alpha rotates its signing key every ninety days",
	})

	got := b.MustCall(t, "am_search", map[string]any{
		"query": "how often is the signing key rotated", "wing": "*", "limit": 5,
	})
	if !strings.Contains(got, "rotates its signing key") {
		t.Fatalf(`wing:"*" did not reach another wing, so a cross-project question cannot be `+
			"asked at all and scoped recall has become a silo:\n%s", got)
	}
}

// TestScenarioHandoffReachesBAndNotC is the three-party case, and the third
// party is the point.
//
// Measured 2026-08-20: six drawers of real findings were filed into wings no
// session resolves to, and nobody noticed for two days because every check was
// made from the SENDER's side, where the write had succeeded. Delivery is only
// observable from the recipient. Isolation is only observable from a bystander.
// One scenario, two observers.
func TestScenarioHandoffReachesBAndNotC(t *testing.T) {
	parties := mcptest.Parties(t, "wing_alpha", "wing_beta", "wing_gamma")
	a, b, c := parties[0], parties[1], parties[2]

	// Beta must already exist as a wing, or the handoff guard refuses — which is
	// itself the behaviour ADR-005 shipped, and is asserted separately below.
	b.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_beta", "room": "decisions", "content": "beta keeps its own decisions here",
	})

	a.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_beta", "room": "inbox",
		"content": "finding from alpha: beta's migration runner retries forever on a locked table",
	})

	// The recipient must find it.
	got := b.MustCall(t, "am_list_drawers", map[string]any{
		"wing": "wing_beta", "room": "inbox", "limit": 10,
	})
	if !strings.Contains(got, "migration runner retries forever") {
		t.Fatalf("the handoff did not reach the recipient's inbox — this is exactly the failure "+
			"that went unnoticed for two days because it was only ever checked from the sender:\n%s", got)
	}

	// The bystander must not.
	if got := c.MustCall(t, "am_list_drawers", map[string]any{
		"wing": "wing_gamma", "room": "inbox", "limit": 10,
	}); strings.Contains(got, "migration runner retries forever") {
		t.Errorf("a bystander's inbox received another party's handoff:\n%s", got)
	}
	if got := c.MustCall(t, "am_search", map[string]any{
		"query": "migration runner retries forever on a locked table", "limit": 5,
	}); strings.Contains(got, "migration runner retries forever") {
		t.Errorf("a bystander's scoped search surfaced another party's handoff:\n%s", got)
	}
}

// TestScenarioListDrawersHonoursTheRegistrationWing.
//
// Found by an independent review that ran the mutations rather than reasoning
// about them: am_list_drawers took its wing verbatim from the argument with no
// fallback, so a call naming no wing enumerated EVERY wing. am_search has
// resolved this correctly since scoping landed; list did not, and nothing
// compared them.
//
// The sharp end is that am_status's own hint recommended exactly the leaking
// call — "read it with am_list_drawers(room: \"inbox\")" — so an agent following
// the wake-up advice read other projects' inboxes. A scoped read that one
// enumeration route ignores is not scoped.
func TestScenarioListDrawersHonoursTheRegistrationWing(t *testing.T) {
	exerciseListDrawersRegistrationWing(t, mcptest.NewWithWing(t, "wing_beta"))
}

// TestScenarioHandoffIntoAnUnknownWingIsRefusedForEveryone: a refusal must leave
// nothing behind. A guard that rejects the caller while the write half-lands is
// worse than no guard, because the sender believes nothing happened.
func TestScenarioHandoffIntoAnUnknownWingIsRefusedForEveryone(t *testing.T) {
	a, b := mcptest.Pair(t, "wing_alpha", "wing_beta")

	msg := a.MustRefuse(t, "am_add_drawer", map[string]any{
		"wing": "wing_to-beta", "room": "inbox", "content": "a finding filed at the wrong wing",
	})
	if !strings.Contains(msg, "confirm_new_wing") {
		t.Errorf("the refusal does not name the way forward:\n%s", msg)
	}

	// Nothing may have landed, seen from the other side as well as the sender's.
	if got := b.MustCall(t, "am_search", map[string]any{
		"query": "a finding filed at the wrong wing", "wing": "*", "limit": 10,
	}); strings.Contains(got, "a finding filed at the wrong wing") {
		t.Errorf("a refused write is retrievable, so the refusal was reported and not performed:\n%s", got)
	}
}

// TestScenarioAnotherWorkspaceSeesNothing pins the OUTER boundary.
//
// Wings partition projects inside one workspace; the workspace is the tenancy
// boundary those wings sit inside, and nothing tested it. A review made the
// consequence concrete by running the mutation: dropping `team_id` from
// Repo.List left every scenario green, because the harness had one tenant by
// construction and no assertion could tell "scoped to my workspace" from
// "scoped to the whole database".
//
// Both clients here use the SAME wing name deliberately. If the only thing
// keeping them apart were the wing filter, this would pass while tenancy was
// broken — the wing must not be able to stand in for the team.
func TestScenarioAnotherWorkspaceSeesNothing(t *testing.T) {
	mine, theirs := mcptest.Tenants(t, "wing_shared_name")

	mine.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_shared_name", "room": "decisions",
		"content": "TENANT-SECRET the signing key lives in the vault under prod/app",
	})

	for tool, args := range map[string]map[string]any{
		"am_search":       {"query": "where does the signing key live", "wing": "*", "limit": 20},
		"am_list_drawers": {"wing": "*", "limit": 50},
		"am_get_taxonomy": {},
		"am_list_wings":   {},
	} {
		if got := theirs.MustCall(t, tool, args); contains(got, "TENANT-SECRET") {
			t.Errorf("%s leaked another WORKSPACE's memory — wings partition projects inside a "+
				"workspace, and the workspace is the boundary they sit inside:\n%s", tool, got)
		}
	}

	// And the owner must still see it, or the test passes on a broken read.
	if got := mine.MustCall(t, "am_list_drawers", map[string]any{"wing": "*", "limit": 50}); !contains(got, "TENANT-SECRET") {
		t.Errorf("the owning workspace cannot see its own memory:\n%s", got)
	}
}
