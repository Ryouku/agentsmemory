package palace

import (
	"fmt"
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
