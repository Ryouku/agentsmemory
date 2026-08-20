package palace

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Statistical honesty for the eval.
//
// Every mistake this file exists to prevent has already been made in this repo:
// arms were ranked on MRR gaps of 0.05 at n=12, conclusions were drawn from
// tables whose differences were sampling noise, and nothing in the output said
// so. A number without an interval invites exactly that reading.
//
// The design is a PAIRED bootstrap: every arm answered the same questions, so
// resampling cases (not scores) preserves the pairing, and the interval on a
// DIFFERENCE between arms is far tighter than the intervals on the arms
// themselves. "Is A better than B?" is answered by the delta's interval
// containing zero, not by eyeballing two overlapping ranges.

// bootstrapIters is how many resamples build an interval. 2000 puts the Monte
// Carlo error well under the widths we print at these sample sizes.
const bootstrapIters = 2000

// bootstrapSeed fixes the resampling so two runs of the same case file print the
// same intervals — an eval that gives different intervals on identical inputs
// reads as broken, and reproducibility is the whole point of saved cases.
const bootstrapSeed = 42

// Interval is a 95% bootstrap confidence interval.
type Interval struct {
	Lo, Hi float64
}

// Contains reports whether v lies inside the interval.
func (i Interval) Contains(v float64) bool { return v >= i.Lo && v <= i.Hi }

// String renders the interval compactly for the report table.
func (i Interval) String() string { return fmt.Sprintf("[%.2f–%.2f]", i.Lo, i.Hi) }

// reciprocal maps a 1-based rank (0 = miss) to its reciprocal rank.
func reciprocal(rank int) float64 {
	if rank <= 0 {
		return 0
	}
	return 1 / float64(rank)
}

// BootstrapMRR builds the 95% interval for one arm's MRR over its per-case
// ranks.
func BootstrapMRR(ranks []int) Interval {
	n := len(ranks)
	if n == 0 {
		return Interval{}
	}
	rng := rand.New(rand.NewSource(bootstrapSeed))
	means := make([]float64, bootstrapIters)
	for b := range means {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += reciprocal(ranks[rng.Intn(n)])
		}
		means[b] = sum / float64(n)
	}
	sort.Float64s(means)
	return Interval{Lo: means[int(0.025*bootstrapIters)], Hi: means[int(0.975*bootstrapIters)-1]}
}

// PairedDelta builds the 95% interval for MRR(a) − MRR(b) over the SAME cases.
//
// Pairing is what makes small samples usable at all: the per-case difference
// cancels the difficulty of the question, so the interval measures the arms'
// disagreement rather than the questions' variance. An interval containing zero
// means the data cannot tell the arms apart — and the report says that instead
// of ranking them.
func PairedDelta(a, b []int) Interval {
	n := len(a)
	if n == 0 || n != len(b) {
		return Interval{}
	}
	diffs := make([]float64, n)
	for i := range diffs {
		diffs[i] = reciprocal(a[i]) - reciprocal(b[i])
	}
	rng := rand.New(rand.NewSource(bootstrapSeed))
	means := make([]float64, bootstrapIters)
	for bi := range means {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += diffs[rng.Intn(n)]
		}
		means[bi] = sum / float64(n)
	}
	sort.Float64s(means)
	return Interval{Lo: means[int(0.025*bootstrapIters)], Hi: means[int(0.975*bootstrapIters)-1]}
}

// ClosetCell is one category's preselected closet-versus-no-closet comparison,
// with every case it declined to use accounted for.
//
// The exclusion counts are not diagnostics. A delta computed over an unstated
// subset is a number nobody can check, and the whole point of preselecting this
// comparison is that the reader can see exactly what went into it.
type ClosetCell struct {
	Category string
	// Admitted is how many cases the delta is computed over.
	Admitted int
	// Unreachable is cases whose gold never entered the pool. No arm could have
	// ranked it, so the pair would contribute a zero for a retrieval reason.
	Unreachable int
	// NoGold is cases one of the two arms did not score, so they cannot be paired.
	NoGold int
	// DeltaMRR is the mean of (closet − no-closet) reciprocal ranks. Negative
	// means the prior costs.
	DeltaMRR float64
	// Interval is the 95% paired bootstrap interval on DeltaMRR.
	Interval Interval
	// DeltaRecall1 is closet's recall@1 minus no-closet's, over admitted cases.
	DeltaRecall1 float64
	// Moved is how many admitted cases the two arms ranked differently at all.
	// A delta near zero means something different when nothing moved than when
	// many cases moved and cancelled out.
	Moved int
}

// ClosetDelta computes the comparison ADR-003 is decided on: ArmHybridCloset
// against ArmHybrid, over one category, with the exclusions fixed in advance.
//
// It is deliberately dull. There is no threshold in it, no minimum case count,
// and no branch that depends on the sign of its own output — because the table's
// existing "vs best" verdicts compare each arm against a baseline chosen from
// the same table, which flatters whichever arm happened to win. A statistic that
// decides a default has to be named before the run and computed the same way
// whatever it returns.
//
// Sign convention: Δ = closet minus no-closet. Negative means the prior costs.
func ClosetDelta(report EvalReport, category string) ClosetCell {
	cell := ClosetCell{Category: category}
	var withCloset, without []int

	for _, d := range report.Details {
		if d.Category != category {
			continue
		}
		if category == CatAbsent {
			continue // no gold to rank; the delta is undefined, not zero
		}
		if d.PoolRank <= 0 {
			cell.Unreachable++
			continue
		}
		a, okA := d.Ranks[ArmHybridCloset]
		b, okB := d.Ranks[ArmHybrid]
		if !okA || !okB {
			cell.NoGold++
			continue
		}
		cell.Admitted++
		withCloset = append(withCloset, a)
		without = append(without, b)
		if a != b {
			cell.Moved++
		}
		if a == 1 {
			cell.DeltaRecall1++
		}
		if b == 1 {
			cell.DeltaRecall1--
		}
	}

	if cell.Admitted == 0 {
		return cell
	}
	var sum float64
	for i := range withCloset {
		sum += reciprocal(withCloset[i]) - reciprocal(without[i])
	}
	cell.DeltaMRR = sum / float64(cell.Admitted)
	cell.DeltaRecall1 /= float64(cell.Admitted)
	cell.Interval = PairedDelta(withCloset, without)
	return cell
}

// SupersessionCell is one arm's stale-above measurement over one population,
// with every case it declined to use accounted for.
type SupersessionCell struct {
	// Scope names the population this arm's number was measured over. Without it
	// a pool-scoped arm and a page-scoped one print side by side as if they had
	// answered the same question.
	Scope SupersessionScope
	// Cases is the denominator: non-vacuous cases only.
	Cases int
	// StaleAbove is how many of those put the superseded version above the
	// correction — including the cases where the correction was never retrieved
	// at all, which is the worst outcome and the one a bare `<` scores as a win.
	StaleAbove int
	// StaleAboveReachable is the same count restricted to cases whose correction
	// WAS retrieved, so a ranking failure can be told from a retrieval one.
	StaleAboveReachable int
	// CurrentUnreachable is how many cases never retrieved the correction.
	CurrentUnreachable int
	// StaleInPage is how many put the superseded version at rank 5 or better —
	// the cutoff Recall5 uses, and the page an agent actually sees.
	StaleInPage int
	// Vacuous is how many cases the superseded version never entered the pool
	// in, so no arm could have ranked it above anything.
	Vacuous int
}

// Rate is StaleAbove over the non-vacuous cases, or 0 when there are none.
func (c SupersessionCell) Rate() float64 {
	if c.Cases == 0 {
		return 0
	}
	return float64(c.StaleAbove) / float64(c.Cases)
}

// StaleAboveRate measures how often one arm put a superseded memory above the
// correction that replaced it.
//
// Two things it deliberately does not do. It does not read vacuity from the
// arm's own zero — that is a case-level property, taken from DistractorPoolRank,
// because an arm's 0 means "not in this ordering" and a vacuous case would then
// score as a success for every arm at once. And it does not treat a gold rank of
// 0 as a good position: 0 is the miss sentinel, so `distractor < gold` would
// score "the stale one came back and the correction did not" as a win, which is
// the exact failure the metric exists to catch.
func StaleAboveRate(cases []EvalCaseResult, arm EvalArm) SupersessionCell {
	var cell SupersessionCell
	for _, c := range cases {
		if c.DistractorRanks == nil {
			continue // the case names no superseded version
		}
		if c.DistractorPoolRank <= 0 {
			cell.Vacuous++
			continue
		}
		cell.Cases++
		gold, stale := c.Ranks[arm], c.DistractorRanks[arm]
		if gold == 0 {
			cell.CurrentUnreachable++
		}
		if stale > 0 && (gold == 0 || stale < gold) {
			cell.StaleAbove++
			if gold != 0 {
				cell.StaleAboveReachable++
			}
		}
		if stale > 0 && stale <= 5 {
			cell.StaleInPage++
		}
	}
	return cell
}

// WilsonInterval is the 95% score interval for a proportion.
//
// Not a bootstrap. Resampling a proportion by percentile returns [0,0] at zero
// successes and [1,1] at all of them — the two values a small corpus produces
// most often, and the two places a zero-width interval claims a certainty the
// sample cannot support. Wilson stays open at both ends and widens as n falls,
// which is the only reason to report an interval at all.
func WilsonInterval(successes, n int) Interval {
	if n <= 0 {
		return Interval{}
	}
	const z = 1.959963984540054 // 95%
	p := float64(successes) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	centre := (p + z*z/(2*nf)) / denom
	spread := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	lo, hi := centre-spread, centre+spread
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return Interval{Lo: lo, Hi: hi}
}

// fillSupersession attaches each arm's stale-above measurement, scoped, once the
// per-case details are complete.
//
// It runs after the per-case loop rather than inside it because the metric's
// denominator is a property of the whole population — the non-vacuous cases —
// and a running total cannot exclude a case it has not seen yet.
func fillSupersession(report *EvalReport) {
	for i := range report.Arms {
		arm := report.Arms[i].Arm
		cell := StaleAboveRate(report.Details, arm)
		cell.Scope = supersessionScope(arm)
		report.Arms[i].Supersession = cell
	}
}
