package palace

import (
	"math"
	"sort"
)

// CalibrationRow is one labelled observation the risk–coverage curve is fitted
// over: which population the case belongs to, the score the SERVED gate would
// compare, and whether anything actually produced that score.
//
// Scored is separate from Score because a zero is an ordinary value on every
// scale this can carry — a cross-encoder logit, a distance gap — so "no score"
// cannot be encoded as one without a case nothing measured being read as a case
// that measured zero.
type CalibrationRow struct {
	Population string
	Score      float64
	Scored     bool
}

// Thresholds is the recommendation, with everything needed to judge how much it
// should be trusted.
//
// Both boundaries are pointers: a threshold that could not be derived is ABSENT,
// and absent is not zero. Zero is an ordinary score, so a nil-vs-0 confusion here
// would silently ship a bar that answers everything.
type Thresholds struct {
	// AnswerAt is the highest threshold still holding answer-recall at or above
	// the declared target over reachable-answerable rows.
	AnswerAt *float64
	// RefuseBelow is the highest threshold at which at most `allowance`
	// reachable-answerable rows fall below it. A COUNT rather than a rate,
	// because at these sample sizes the achievable recall grid is coarse enough
	// that two different rate targets select the same threshold and the band
	// collapses to nothing.
	RefuseBelow *float64
	// AchievedRecall is what AnswerAt actually delivers, which is not the target
	// it was asked for: the grid is discrete, so the chosen threshold clears the
	// target rather than meeting it. Printed beside the target so nobody reads
	// the request as the result.
	AchievedRecall float64
	// BandEmpty reports that the two boundaries coincide, so there is no score
	// range where the system would say "I am not sure" rather than answering or
	// refusing. It is a real outcome on overlapping data, not an error.
	BandEmpty bool

	Reachable   int // rows the curve was fitted over
	Absent      int // verified-absent rows
	Unreachable int // counted and EXCLUDED; reported so a thin sample is visible
}

// RecommendThresholds derives both boundaries from one curve over the labelled
// rows.
//
// Unreachable-answerable rows are excluded from every rate and counted
// separately. Their gold never entered the retrieved pool, so no threshold could
// have surfaced them: scoring them as answerable drags the bar down to rescue
// cases that were never rescuable, which is a retrieval fact wearing a ranking
// result's clothes.
//
// Candidate thresholds are the observed scores themselves rather than a grid over
// the range. A threshold between two observations behaves identically to the
// lower one on this sample, so a finer grid would invent precision the data does
// not carry.
func RecommendThresholds(rows []CalibrationRow, answerRecall float64, refuseAllowance int) Thresholds {
	var out Thresholds
	var answerable []float64
	for _, r := range rows {
		switch {
		case r.Population == PopUnreachable:
			out.Unreachable++
		case !r.Scored:
			// nothing measured this row; it has no score to place
		case r.Population == PopAbsent:
			out.Absent++
		default:
			out.Reachable++
			answerable = append(answerable, r.Score)
		}
	}
	if len(answerable) == 0 {
		return out
	}
	sort.Float64s(answerable)

	candidates := append([]float64(nil), answerable...)
	for _, r := range rows {
		if r.Scored && r.Population == PopAbsent {
			candidates = append(candidates, r.Score)
		}
	}
	sort.Float64s(candidates)

	n := float64(len(answerable))
	// recallAt is the share of answerable rows scoring at or above t. It is
	// non-increasing in t, which is what makes RefuseBelow <= AnswerAt hold
	// rather than be asserted.
	recallAt := func(t float64) float64 {
		kept := 0
		for _, s := range answerable {
			if s >= t {
				kept++
			}
		}
		return float64(kept) / n
	}
	belowAt := func(t float64) int {
		c := 0
		for _, s := range answerable {
			if s < t {
				c++
			}
		}
		return c
	}

	for i := len(candidates) - 1; i >= 0; i-- {
		t := candidates[i]
		if out.AnswerAt == nil && recallAt(t) >= answerRecall {
			v := t
			out.AnswerAt = &v
			out.AchievedRecall = recallAt(t)
		}
		if out.RefuseBelow == nil && belowAt(t) <= refuseAllowance {
			v := t
			out.RefuseBelow = &v
		}
		if out.AnswerAt != nil && out.RefuseBelow != nil {
			break
		}
	}
	// The band must never invert. ADR-001 T2 states that refuse_below <= answer_at
	// "follows from recall being non-increasing in the threshold" — it does not,
	// and this is where that shows. The two boundaries answer to DIFFERENT
	// budgets: answer_at to a recall RATE, refuse_below to an absolute COUNT. They
	// agree only while the allowance stays within what the rate permits, i.e.
	// allowance <= (1 - answerRecall) * n. Below that sample size the count is the
	// looser of the two and lifts refuse_below above answer_at, which would put
	// the middle verdict where the system should simply be answering.
	//
	// Clamped rather than reported-and-shipped, because an inverted band is not a
	// finding about the corpus — it is the allowance being incoherent with the
	// target at this n, and the safe reading is that there is no band at all.
	if out.AnswerAt != nil && out.RefuseBelow != nil && *out.RefuseBelow >= *out.AnswerAt {
		v := *out.AnswerAt
		out.RefuseBelow = &v
		out.BandEmpty = true
	}
	return out
}

// RefusalGate is the ADR's declared go/no-go, as an answer rather than a reading.
//
// It compares the 90% Wilson LOWER BOUND on the correct-refusal rate against the
// bar, never the point estimate. At the sample sizes here the estimate carries
// roughly ±0.11 at one standard error, so a gate on the estimate passes on noise
// about half the time it sits near the bar — a verdict with the authority of a
// measurement and the reliability of a coin.
//
// An empty sample returns false with n=0: undefined, not failing-with-confidence.
func RefusalGate(n, refused int, bar float64) (pass bool, rate, lower float64, cases int) {
	if n <= 0 {
		return false, 0, 0, 0
	}
	rate = float64(refused) / float64(n)
	lower = wilsonLower(refused, n, zFor90)
	return lower >= bar, rate, lower, n
}

// zFor90 is the one-sided-equivalent z for a 90% two-sided Wilson interval. Named
// rather than inlined so the confidence level is legible at the call site: this
// ADR compares against a 90% bound while EvalReport's intervals are 95%, and two
// different levels sharing an unlabelled constant is how they get conflated.
const zFor90 = 1.6448536269514722

// wilsonLower is the lower end of a Wilson score interval at the given z.
//
// Extracted from WilsonInterval so both confidence levels share one derivation:
// a second copy at a different z is a second chance to get the algebra wrong, in
// a function whose whole value is that the algebra is right.
func wilsonLower(successes, n int, z float64) float64 {
	if n <= 0 {
		return 0
	}
	p := float64(successes) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	centre := (p + z*z/(2*nf)) / denom
	spread := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	if lo := centre - spread; lo > 0 {
		return lo
	}
	return 0
}
