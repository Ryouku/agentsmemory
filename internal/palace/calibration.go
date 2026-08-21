package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
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

// CanaryPair is one fixed (query, document) probe scored through the configured
// reranker at calibration time, with the spread observed across repeats.
//
// MaxDeviation is DERIVED from the instrument rather than typed: it is the widest
// gap seen across the repeats on this machine. Deterministic inference yields
// zero, and therefore an exact-match check at startup — which is the right answer
// when it is true and a false alarm when it is not, so it must be measured rather
// than assumed either way.
type CanaryPair struct {
	Query        string  `json:"query"`
	Document     string  `json:"document"`
	Mean         float64 `json:"mean"`
	MaxDeviation float64 `json:"max_deviation"`
}

// Calibration is the operating point: two thresholds, the evidence behind them,
// and a fingerprint of the instrument that produced them.
//
// It is a FILE rather than configuration on purpose. The tool writes it; an
// operator still has to point the server at it. A bad calibration therefore
// cannot become production behaviour on its own.
type Calibration struct {
	// ID is a short content hash naming this operating point in telemetry, so
	// rows judged under two different calibrations are never pooled as one
	// population. Stamped by Save.
	ID string `json:"id"`

	// Both thresholds are pointers because ABSENT is not zero. Zero is an
	// ordinary score on every scale this carries, and the collapse fails OPEN: a
	// nil answer_at means "do not gate", a 0.0 answer_at means "answer everything
	// above zero", which on an unbounded scale is most of the corpus.
	AnswerAt    *float64 `json:"answer_at"`
	RefuseBelow *float64 `json:"refuse_below"`

	// The targets are recorded beside the results because the achievable grid is
	// discrete: a chosen threshold clears its target rather than meeting it, and
	// without both numbers a reader takes the request for the result.
	AnswerRecallTarget float64 `json:"answer_recall_target"`
	RefuseAllowance    int     `json:"refuse_allowance"`
	AchievedRecall     float64 `json:"achieved_recall"`

	Reachable   int `json:"reachable"`
	Absent      int `json:"absent"`
	Unreachable int `json:"unreachable"`

	GatePassed   bool    `json:"gate_passed"`
	RefusalRate  float64 `json:"refusal_rate"`
	RefusalLower float64 `json:"refusal_lower"`
	RefusalBar   float64 `json:"refusal_bar"`

	// RerankModel is operator-declared, not detected: Reranker.Rerank returns
	// floats and does not say what produced them.
	RerankModel string `json:"rerank_model"`
	// Profile is the ranking configuration in force, because a threshold
	// calibrated under one fusion mode and applied under another is calibrated on
	// nothing.
	Profile string `json:"profile"`

	Canary []CanaryPair `json:"canary"`
	// ScoresBounded records whether the canary scores landed in (0,1) or ranged
	// unbounded — INFERRED from the instrument at calibration time and then
	// FIXED, never re-guessed on load, because the same reranker read through two
	// dialects produces two different scales for one threshold.
	ScoresBounded bool `json:"scores_bounded"`
}

// Save writes the calibration, stamping ID from its content first.
//
// Pointer receiver so the ID lands on the CALLER's value, not on a copy. The ID
// is what names this operating point in telemetry, and a caller that saved a
// calibration and still holds an empty ID cannot label the rows it then judges —
// which is exactly the pooling this field exists to prevent.
func (c *Calibration) Save(path string) error {
	c.ID = c.contentID()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode calibration: %w", err)
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// LoadCalibration reads a calibration, refusing anything it cannot parse.
//
// A corrupt file must NOT decode to a zero Calibration: its nil thresholds read
// as "do not gate", so a silent failure would switch the gate off while every
// report said it was on.
func LoadCalibration(path string) (Calibration, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Calibration{}, fmt.Errorf("read calibration: %w", err)
	}
	var c Calibration
	if err := json.Unmarshal(b, &c); err != nil {
		return Calibration{}, fmt.Errorf("decode calibration %s: %w", path, err)
	}
	return c, nil
}

// contentID hashes everything that defines the operating point, with ID itself
// zeroed so the hash does not depend on a previous hash.
//
// VALUE receiver deliberately, unlike Save: it blanks ID to compute the hash, and
// doing that through a pointer would clear the caller's field as a side effect of
// reading it.
func (c Calibration) contentID() string {
	c.ID = ""
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// canaryRepeats is how many times each probe is scored. Five is enough to see a
// spread without turning calibration into a benchmark; the number matters less
// than that it is greater than one, because a single reading cannot show drift at
// all and would record every instrument as perfectly deterministic.
const canaryRepeats = 5

// canaryProbes are the fixed (query, document) pairs the fingerprint is built
// from. Deliberately short, self-contained and about nothing in any corpus: the
// fingerprint identifies the INSTRUMENT, so a probe that depended on stored
// content would change meaning whenever the content did.
//
// The pairs span the range on purpose — one where the document plainly answers the
// query, one where it is on-topic and does not, one unrelated. A fingerprint taken
// only at the top of the scale cannot tell a model that agrees about good matches
// and disagrees about everything else.
var canaryProbes = []struct{ Query, Document string }{
	{"how do I rotate the signing key", "To rotate the signing key, run the rotate command and restart each node."},
	{"how do I rotate the signing key", "Signing keys are stored in the vault; this note covers how they are generated."},
	{"how do I rotate the signing key", "The kitchen roster for August is pinned in the hallway."},
}

// ScoreCanary probes the reranker and derives the fingerprint's tolerance from
// what it observes.
//
// The tolerance is the WIDEST gap seen across repeats, never a typed epsilon. A
// constant here would be a guess about someone else's hardware: too tight and a
// healthy reranker is declared broken on a busy host, too loose and a genuinely
// different model passes as the calibrated one — which is the failure that
// matters, since it applies a threshold calibrated for a scale that no longer
// exists.
//
// A deterministic reranker therefore yields zero, and an exact-match check at
// startup. That is the correct answer when it is true, which is why it is measured
// rather than floored to something "safe".
//
// A reranker that cannot score returns an error rather than an empty fingerprint:
// an empty one would match any future instrument, and the check would pass on
// nothing.
func ScoreCanary(ctx context.Context, r Reranker) ([]CanaryPair, error) {
	if r == nil {
		return nil, fmt.Errorf("no reranker configured; a calibration without a fingerprint " +
			"cannot be checked against the instrument that produced it")
	}
	docs := make([]string, len(canaryProbes))
	for i, p := range canaryProbes {
		docs[i] = p.Document
	}
	// One query per probe rather than one call for all of them: the probes share a
	// query here, but a cross-encoder scores the (query, document) pair, and
	// batching them under one query would silently change what is being measured
	// if the probe list ever gained a second question.
	runs := make([][]float64, 0, canaryRepeats)
	for i := 0; i < canaryRepeats; i++ {
		scores, err := r.Rerank(ctx, canaryProbes[0].Query, docs)
		if err != nil {
			return nil, fmt.Errorf("score canary probe (repeat %d): %w", i+1, err)
		}
		if len(scores) != len(docs) {
			return nil, fmt.Errorf("canary repeat %d returned %d scores for %d documents",
				i+1, len(scores), len(docs))
		}
		runs = append(runs, scores)
	}

	out := make([]CanaryPair, len(canaryProbes))
	for i, p := range canaryProbes {
		lo, hi, sum := math.Inf(1), math.Inf(-1), 0.0
		for _, run := range runs {
			v := run[i]
			sum += v
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		out[i] = CanaryPair{
			Query:    p.Query,
			Document: p.Document,
			Mean:     sum / float64(len(runs)),
			// the observed spread, which is 0 for a deterministic instrument
			MaxDeviation: hi - lo,
		}
	}
	return out, nil
}

// ScoresLookBounded infers whether the reranker's scale is (0,1) or unbounded,
// from the canary scores rather than from configuration.
//
// Reranker.Rerank returns floats and does not report which dialect decoded them,
// so the scale is not knowable any other way — and the same model read through two
// dialects gives two different scales for one threshold.
//
// Inferred once at calibration time and then recorded, never re-derived on load:
// a calibration whose scale was guessed again later could disagree with the
// thresholds stored beside it.
func ScoresLookBounded(pairs []CanaryPair) bool {
	for _, p := range pairs {
		if p.Mean < 0 || p.Mean > 1 {
			return false
		}
	}
	return len(pairs) > 0
}

// CurvePoint is one threshold and what it costs: how much of what the palace CAN
// answer it still answers, and how much of what it cannot it correctly refuses.
type CurvePoint struct {
	Threshold      float64
	AnswerRecall   float64 // over reachable-answerable rows
	CorrectRefusal float64 // over verified-absent rows
	Answered       int
	Refused        int
}

// RiskCoverageCurve walks every threshold the data supports and reports the
// exchange rate between answering and refusing.
//
// The two boundaries RecommendThresholds returns are points ON this curve. They
// are what an operator ships; the curve is what tells them whether shipping is
// possible at all. A gate verdict of FAIL says the chosen point misses the bar and
// cannot say whether ANY point would clear it — and those are different findings.
// "This threshold is wrong" sends you to retune. "No threshold clears both bars on
// this corpus" sends you to change the signal, the corpus or the declared targets.
//
// Thresholds are the observed scores, plus one below the lowest and one above the
// highest so the degenerate ends are present: answer-everything and
// refuse-everything are the two points that bound what any tuning can achieve, and
// a curve that omits them hides its own limits.
func RiskCoverageCurve(rows []CalibrationRow) []CurvePoint {
	var answerable, absent []float64
	for _, r := range rows {
		if !r.Scored || r.Population == PopUnreachable {
			continue
		}
		if r.Population == PopAbsent {
			absent = append(absent, r.Score)
		} else {
			answerable = append(answerable, r.Score)
		}
	}
	if len(answerable) == 0 || len(absent) == 0 {
		return nil
	}
	seen := map[float64]bool{}
	var thresholds []float64
	add := func(v float64) {
		if !seen[v] {
			seen[v] = true
			thresholds = append(thresholds, v)
		}
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range append(append([]float64(nil), answerable...), absent...) {
		add(s)
		if s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	// The degenerate ends, placed just outside the observed range rather than at
	// infinity so they print as numbers a reader can compare with the rest.
	span := hi - lo
	if span == 0 {
		span = 1
	}
	add(lo - span/100)
	add(hi + span/100)
	sort.Float64s(thresholds)

	out := make([]CurvePoint, 0, len(thresholds))
	for _, t := range thresholds {
		answered := 0
		for _, s := range answerable {
			if s >= t {
				answered++
			}
		}
		refused := 0
		for _, s := range absent {
			if s < t {
				refused++
			}
		}
		out = append(out, CurvePoint{
			Threshold:      t,
			AnswerRecall:   float64(answered) / float64(len(answerable)),
			CorrectRefusal: float64(refused) / float64(len(absent)),
			Answered:       answered,
			Refused:        refused,
		})
	}
	return out
}

// ViablePoint returns the threshold that clears BOTH declared bars while
// answering as much as possible, or nil when no such threshold exists.
//
// nil is the answer that matters. A gate that only ever reports its own operating
// point leaves the reader unable to distinguish a bad choice of threshold from a
// corpus on which the design cannot work — and the second is a finding about the
// system, not about the tuning.
//
// Among the thresholds that clear both bars it returns the LOWEST, which is the
// one that answers most: the bars are floors to satisfy, not quantities to
// maximise, so exceeding the refusal bar by refusing more than necessary would
// trade away answers nobody asked to lose.
func ViablePoint(curve []CurvePoint, answerRecall, refusalBar float64) *CurvePoint {
	for i := range curve {
		p := curve[i]
		if p.AnswerRecall >= answerRecall && p.CorrectRefusal >= refusalBar {
			return &p
		}
	}
	return nil
}
