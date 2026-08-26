package palace

import (
	"strings"
	"testing"
)

// TestSelectArmsKeepsDeclaredOrderAndReportsWhatItDropped.
//
// The filter exists because COST is what stops a question being asked twice: 54
// cases against 36 arms took ~50 minutes at the shipped rerank pool and ~110 at
// pool 20, and two runs on 2026-08-26 were abandoned unfinished — an unfinished
// run yields nothing at all.
//
// Order is asserted because TestEvalArmsKeepProductionLast requires the arm that
// scores the SERVED path to be last in the table. Following the caller's pattern
// order instead would break that in a report rather than in a test, where nobody
// would see it.
func TestSelectArmsKeepsDeclaredOrderAndReportsWhatItDropped(t *testing.T) {
	all := []EvalArm{ArmVector, ArmHybrid, ArmRRF, rerankArm(0.25), rerankArm(0.5), ArmProduction}

	kept, dropped := selectArms(all, []string{"production (Search)", "vector"})
	if dropped != 4 {
		t.Errorf("dropped = %d, want 4 — a silent drop count reads as 'covered everything'", dropped)
	}
	if len(kept) != 2 || kept[0] != ArmVector || kept[1] != ArmProduction {
		t.Errorf("kept = %v, want [vector, production] IN DECLARED ORDER regardless of the order "+
			"the patterns were given; production must stay last", kept)
	}
}

// TestSelectArmsMatchesSweptFamiliesByPrefix.
//
// The swept families are minted at run time — rerankArm, bm25Arm and recencyArm
// build their names with fmt.Sprintf — so a caller cannot name them without
// knowing the sweep's current values. Prefix matching is what lets "rerank
// blend" select the whole weight sweep and keep selecting it when the sweep
// changes.
func TestSelectArmsMatchesSweptFamiliesByPrefix(t *testing.T) {
	all := []EvalArm{ArmVector, rerankArm(0.25), rerankArm(0.5), rerankArm(1.0), ArmProduction}

	kept, _ := selectArms(all, []string{"rerank blend"})
	if len(kept) != 3 {
		t.Fatalf("prefix 'rerank blend' kept %d arms (%v), want the whole sweep of 3", len(kept), kept)
	}
	for _, a := range kept {
		if !strings.HasPrefix(string(a), "rerank blend") {
			t.Errorf("prefix match admitted %q, which is not in the family", a)
		}
	}
}

// TestSelectArmsMatchingNothingIsCaughtByTheCaller: the filter reports zero, and
// Evaluate is what refuses.
//
// This is THE case worth testing, and it is this repository's oldest lesson
// pointed at a new surface: "a filter matching nothing exits 0". A `-run` filter
// that matches no tests prints a cheerful summary and exits 0, which is how
// every TDD task once passed its own gate with none of the work done. An --arms
// filter that matched nothing would do the same thing in a shape nobody has been
// bitten by yet — a full report, a clean exit, and an empty table that reads as
// "these arms scored nothing" rather than "no arm ran".
//
// selectArms itself returns empty rather than erroring, because a pure predicate
// should not decide policy; Evaluate turns the empty result into a refusal.
func TestSelectArmsMatchingNothingIsCaughtByTheCaller(t *testing.T) {
	all := []EvalArm{ArmVector, ArmHybrid, ArmProduction}

	kept, dropped := selectArms(all, []string{"an arm that does not exist"})
	if len(kept) != 0 {
		t.Fatalf("kept = %v for a pattern matching nothing, want empty", kept)
	}
	if dropped != len(all) {
		t.Errorf("dropped = %d, want all %d — the count is what tells the caller nothing matched",
			dropped, len(all))
	}
}
