package palace

import "testing"

// Bindings for docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md.
//
// ⚠ DELIBERATELY RED. These are TDD-red stubs for facts decided in the grill and
// not yet built; they fail with the assertion they will one day make, so a reader
// who runs them learns what is missing rather than that something broke. They
// live on their own branch until the ADR turns them green — a red test riding a
// branch that has to ship first blocks the thing it was never about.
//
// ⚠ AND A STUB THAT FAILS IS NOT A TEST THAT CAN FAIL. Each of these asserts
// nothing today. When it is implemented, the fact it binds must be provable by
// BREAKING the mechanism and watching this go red — a stub replaced by an
// assertion that passes against correct code and never fails against broken code
// has moved the tag from @spec to @implemented while proving nothing. That is the
// defect this repository has shipped repeatedly, and the reason these carry the
// warning rather than a TODO.

// notYetBuilt is the shape of a spec binding that has not been executed.
const notYetBuilt = "not built yet — %s"

func TestF1AHitIsDisclosedAboveTheFloor(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-1 (and UC1-S1): a recall discloses enough of each memory to be "+
		"acted on without a second call. Measured 2026-08-28: hits carried content_coverage "+
		"0.026-0.031, and partial content was acted on twice in one session before anyone "+
		"noticed. A hit below the floor is a defect, not a preview")
}

func TestF2FewerWholeNotMoreFragments(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-2 (and UC1-S2): when the budget cannot disclose every hit above "+
		"the floor, return FEWER memories whole rather than more as fragments, and report the "+
		"withheld count. A fragment that cannot be acted on has negative value; a memory never "+
		"received at least does not mislead")
}

func TestF3SupersededNeverOutranksItsCorrection(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-3 (and UC1-S3): a superseded memory is marked and never outranks "+
		"the record that superseded it. Measured 2026-08-28 on one query: the superseded record "+
		"at distance 0.334, its correction at 0.355 — the reader gets the wrong version first")
}

func TestF4AMemoryIsOneUnitToItsCaller(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-4 (and UC1-S4): a memory is returned as ONE unit; chunking is an "+
		"embedding-time detail that never reaches the read contract. ChunkSize = 1600 is sized "+
		"for the embedder (chunk.go:20) and leaked into what a caller receives. It may remain "+
		"the matching unit — this constrains delivery, not retrieval")
}

func TestF5CountingRuleIsAnArtifactBeforeCollection(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-5 (and UC2-S1): the counting rule — what a read is, and the window "+
		"it is attributed to — is a COMMITTED ARTIFACT fixed before collection, not a sentence "+
		"in a flow step. ADR-041 fixed its unit precisely and left its window to one clause, and "+
		"the resulting metric cannot see what the ADR exists to move")
}

func TestF5NoMechanismShipsBeforeItsBaseline(t *testing.T) {
	t.Fatalf(notYetBuilt, "F-6 (and UC2-S2): no mechanism ships before a baseline is recorded "+
		"under the published rule, and changing the rule INVALIDATES the baseline taken under "+
		"it — the way changing a fence invalidates its recorded evidence")
}
