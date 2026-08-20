package palace

import (
	"math"
	"strings"
	"testing"
)

// TestBootstrapSeparatesSignalFromNoise: a large real difference must exclude
// zero, and pure noise must not — otherwise the intervals are decoration.
func TestBootstrapSeparatesSignalFromNoise(t *testing.T) {
	// Arm A finds everything at rank 1; arm B misses half outright.
	a := []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	b := []int{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0}
	if d := PairedDelta(a, b); d.Contains(0) {
		t.Errorf("a decisive difference produced an interval containing zero: %v", d)
	}

	// Identical arms: the delta must be exactly zero-width around zero.
	if d := PairedDelta(a, a); !d.Contains(0) || d.Lo != 0 || d.Hi != 0 {
		t.Errorf("identical arms produced a nonzero delta: %v", d)
	}

	// One flipped case out of twelve is the kind of gap this repo previously
	// ranked arms on. The interval must refuse to.
	c := []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2}
	if d := PairedDelta(a, c); !d.Contains(0) {
		t.Errorf("a one-case gap at n=12 excluded zero: %v — this is the over-reading the stats exist to stop", d)
	}
}

// TestBootstrapIsReproducible: same inputs, same interval — a report that
// changes between runs of identical cases reads as broken.
func TestBootstrapIsReproducible(t *testing.T) {
	ranks := []int{1, 2, 0, 3, 1, 1, 0, 2, 1, 5, 1, 1}
	first, second := BootstrapMRR(ranks), BootstrapMRR(ranks)
	if first != second {
		t.Errorf("two runs over identical ranks: %v then %v", first, second)
	}
	if first.Lo >= first.Hi {
		t.Errorf("degenerate interval %v for varied ranks", first)
	}
}

// TestEvaluateFailsLoudOnStaleGold pins the adversarial-review finding: a case
// whose drawer was purged by a re-mine must stop the run and say why, not score
// as an all-arm retrieval miss that the pool diagnosis then misattributes.
func TestEvaluateFailsLoudOnStaleGold(t *testing.T) {
	svc := newTestService(t)
	const team = "team-stale"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a real memory so the corpus is not empty"})

	_, err := svc.Evaluate(t.Context(), team,
		[]EvalCase{{Query: "anything", Expect: "purged-drawer-id-that-no-longer-exists"}}, 10, nil)
	if err == nil {
		t.Fatal("a stale gold id must fail the run, not silently score as a miss")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("the error must name the cause: %v", err)
	}
}

// TestClosetDeltaExcludesUnreachableAndAbsentCases pins the admission rules, and
// pins that every exclusion is counted rather than quietly dropped.
//
// The comparison this ADR is decided on has to be preselected — hybrid+closet
// against hybrid, named before the run — because every "vs best" verdict the
// table prints picks its own baseline from the same data it is judging. Two
// kinds of case cannot contribute: one whose gold never entered the pool, since
// no arm could have ranked it and the delta would be zero for a retrieval reason
// rather than a ranking one; and an absent case, which has no gold at all.
func TestClosetDeltaExcludesUnreachableAndAbsentCases(t *testing.T) {
	report := EvalReport{Details: []EvalCaseResult{
		{Query: "reachable, closet wins", Category: CatSingle, PoolRank: 3,
			Ranks: map[EvalArm]int{ArmHybrid: 4, ArmHybridCloset: 1}},
		{Query: "reachable, closet loses", Category: CatSingle, PoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 1, ArmHybridCloset: 3}},
		{Query: "unreachable — gold never made the pool", Category: CatSingle, PoolRank: 0,
			Ranks: map[EvalArm]int{ArmHybrid: 0, ArmHybridCloset: 0}},
		{Query: "absent — no gold to rank", Category: CatAbsent, PoolRank: 0,
			Ranks: map[EvalArm]int{ArmHybrid: 0, ArmHybridCloset: 0}},
	}}

	cell := ClosetDelta(report, CatSingle)

	if cell.Admitted != 2 {
		t.Errorf("admitted %d cases, want the 2 reachable single-hop ones", cell.Admitted)
	}
	if cell.Unreachable != 1 {
		t.Errorf("counted %d unreachable, want 1 — an exclusion nobody can see is an exclusion nobody can check", cell.Unreachable)
	}
	if cell.Moved != 2 {
		t.Errorf("counted %d moved, want 2: both admitted cases were ranked differently by the two arms", cell.Moved)
	}
	// Δ = closet minus no-closet. Case one: 1/1 − 1/4 = +0.75. Case two:
	// 1/3 − 1/1 = −0.667. Mean = +0.0417.
	if math.Abs(cell.DeltaMRR-0.0416666) > 1e-4 {
		t.Errorf("ΔMRR = %.6f, want ≈ +0.041667 (closet minus no-closet over the two admitted cases)", cell.DeltaMRR)
	}
}

// TestClosetDeltaIsScopedToOneCategory pins that the statistic never pools
// categories. A paraphrase question and a real recorded query are different
// populations, and a delta averaged over both describes neither.
func TestClosetDeltaIsScopedToOneCategory(t *testing.T) {
	report := EvalReport{Details: []EvalCaseResult{
		{Query: "single", Category: CatSingle, PoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 2, ArmHybridCloset: 1}},
		{Query: "real one", Category: CatReal, PoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 1, ArmHybridCloset: 5}},
		{Query: "real two", Category: CatReal, PoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 1, ArmHybridCloset: 5}},
	}}

	single := ClosetDelta(report, CatSingle)
	real := ClosetDelta(report, CatReal)

	if single.Admitted != 1 || real.Admitted != 2 {
		t.Fatalf("admitted single=%d real=%d, want 1 and 2 — the categories are leaking into each other", single.Admitted, real.Admitted)
	}
	if !(single.DeltaMRR > 0) {
		t.Errorf("single-hop ΔMRR = %.4f, want positive; the closet arm ranked that case better", single.DeltaMRR)
	}
	if !(real.DeltaMRR < 0) {
		t.Errorf("real ΔMRR = %.4f, want negative; the closet arm ranked both those cases worse", real.DeltaMRR)
	}
}

// TestClosetDeltaCountsCasesNeitherArmScored pins the third exclusion: a case
// present in the category and reachable, but which one of the two arms never
// scored, cannot be paired and is reported rather than dropped.
func TestClosetDeltaCountsCasesNeitherArmScored(t *testing.T) {
	report := EvalReport{Details: []EvalCaseResult{
		{Query: "only one arm ran", Category: CatSingle, PoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 3}},
		{Query: "both ran", Category: CatSingle, PoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 3, ArmHybridCloset: 2}},
	}}

	cell := ClosetDelta(report, CatSingle)

	if cell.Admitted != 1 {
		t.Errorf("admitted %d, want 1 — a case only one arm scored cannot be paired", cell.Admitted)
	}
	if cell.NoGold != 1 {
		t.Errorf("counted %d unpairable, want 1", cell.NoGold)
	}
}

// TestStaleAboveRateExcludesVacuous pins the denominator.
//
// A case is vacuous when the superseded version never entered the pool at all:
// no arm could have ranked it above anything, so counting it as a success would
// credit every arm for a retrieval accident. Vacuity is a property of the CASE —
// two arms may order a distractor differently, but they cannot disagree about
// whether it was retrievable — which is why it is read from DistractorPoolRank
// and not from each arm's own zero.
func TestStaleAboveRateExcludesVacuous(t *testing.T) {
	cases := []EvalCaseResult{
		// stale above current: distractor at 1, gold at 3 → counts, and is a hit
		{Category: CatTemporal, PoolRank: 3, DistractorPoolRank: 1,
			Ranks: map[EvalArm]int{ArmHybrid: 3}, DistractorRanks: map[EvalArm]int{ArmHybrid: 1}},
		// current above stale → counts, not a hit
		{Category: CatTemporal, PoolRank: 1, DistractorPoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 1}, DistractorRanks: map[EvalArm]int{ArmHybrid: 2}},
		// vacuous: the superseded version never made the pool
		{Category: CatTemporal, PoolRank: 1, DistractorPoolRank: 0,
			Ranks: map[EvalArm]int{ArmHybrid: 1}, DistractorRanks: map[EvalArm]int{ArmHybrid: 0}},
	}

	got := StaleAboveRate(cases, ArmHybrid)
	if got.Vacuous != 1 {
		t.Errorf("counted %d vacuous, want 1 — an exclusion nobody can see is one nobody can check", got.Vacuous)
	}
	if got.Cases != 2 {
		t.Errorf("denominator %d, want 2 (the non-vacuous cases)", got.Cases)
	}
	if got.StaleAbove != 1 {
		t.Errorf("counted %d stale-above, want 1", got.StaleAbove)
	}
	if math.Abs(got.Rate()-0.5) > 1e-9 {
		t.Errorf("rate %.4f, want 0.5", got.Rate())
	}
}

// TestStaleAboveRateCountsUnreachableCurrent pins the sentinel that a bare `<`
// would get backwards.
//
// A gold rank of 0 means the CURRENT version was never retrieved. The distractor
// being retrieved while the correction is missing is the worst outcome this
// metric exists to measure, and `distractor < gold` scores it as a success
// because 0 sorts first. It counts as stale-above, and separately as
// CurrentUnreachable so the two are distinguishable.
func TestStaleAboveRateCountsUnreachableCurrent(t *testing.T) {
	cases := []EvalCaseResult{
		{Category: CatTemporal, PoolRank: 0, DistractorPoolRank: 2,
			Ranks: map[EvalArm]int{ArmHybrid: 0}, DistractorRanks: map[EvalArm]int{ArmHybrid: 2}},
	}
	got := StaleAboveRate(cases, ArmHybrid)
	if got.StaleAbove != 1 {
		t.Errorf("stale retrieved with the correction missing must count as stale-above, got %d", got.StaleAbove)
	}
	if got.CurrentUnreachable != 1 {
		t.Errorf("counted %d unreachable-current, want 1", got.CurrentUnreachable)
	}
	if got.StaleAboveReachable != 0 {
		t.Errorf("the reachable-only rate must exclude it, got %d", got.StaleAboveReachable)
	}
}

// TestStaleAboveRateWilsonNotBootstrap pins the interval's shape.
//
// This is a proportion, not a mean of reciprocal ranks, and resampling a
// proportion by percentile returns [0,0] at a rate of 0 and [1,1] at 1 — which
// are exactly the values a small corpus produces most often, and exactly where a
// zero-width interval is a lie. The Wilson score interval stays open at both
// ends.
func TestStaleAboveRateWilsonNotBootstrap(t *testing.T) {
	zero := WilsonInterval(0, 20)
	if zero.Lo != 0 {
		t.Errorf("Wilson lower bound at 0/20 = %.4f, want 0", zero.Lo)
	}
	if zero.Hi <= 0 {
		t.Errorf("Wilson upper bound at 0/20 = %.4f — a zero-width interval at zero successes "+
			"claims certainty 20 samples cannot support", zero.Hi)
	}
	one := WilsonInterval(20, 20)
	if one.Hi != 1 {
		t.Errorf("Wilson upper bound at 20/20 = %.4f, want 1", one.Hi)
	}
	if one.Lo >= 1 {
		t.Errorf("Wilson lower bound at 20/20 = %.4f — same lie at the other end", one.Lo)
	}
	// A wider interval for less data is the property that makes it worth having.
	if WilsonInterval(1, 5).Hi-WilsonInterval(1, 5).Lo <= WilsonInterval(20, 100).Hi-WilsonInterval(20, 100).Lo {
		t.Error("1/5 must yield a wider interval than 20/100 at the same point estimate")
	}
	if WilsonInterval(0, 0).Hi != 0 {
		t.Error("no samples must not produce an interval claiming anything")
	}
}
