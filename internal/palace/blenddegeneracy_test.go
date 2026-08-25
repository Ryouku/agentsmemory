package palace

import (
	"math"
	"testing"
)

// twoCandidateDisagreement is the fixture the whole task turns on: two
// candidates where the fused order and the cross-encoder order are opposed, and
// the cross-encoder is as certain as it can express.
//
// ranked is fused-descending by contract, so candidate 0 has the better fused
// score. The scores say the opposite, at the extremes of the logit range.
func twoCandidateDisagreement() ([]HybridScore, []float64) {
	return []HybridScore{{Index: 0, Fused: 0.9}, {Index: 1, Fused: 0.1}}, []float64{-5.0, +5.0}
}

// TestServedBlendTiesOnATwoCandidatePool pins TODAY's behaviour, and it is meant
// to pass on the unmodified tree.
//
// It exists so the fixture is known to contain the defect. A later test asserting
// that the cross-encoder decides this page is worthless if nobody first
// established that it currently does not — that is the difference between a
// regression test and a test that has never been able to fail.
//
// Measured on the deployed stack 2026-08-25 (search_id 67a15fa4242b33ec190e8c7c):
// a served page returned two hits both carrying blended_score 0.5000 while the
// closest hit by cosine distance was placed last, with the trace reporting
// `am.search.rerank ran weight=0.5 pool=3`. This is that page, reduced.
func TestServedBlendTiesOnATwoCandidatePool(t *testing.T) {
	ranked, scores := twoCandidateDisagreement()

	out := BlendRerank(ranked, scores, DefaultRerankWeight)
	if len(out) != 2 {
		t.Fatalf("BlendRerank returned %d candidates, want 2", len(out))
	}
	if out[0].Blended != out[1].Blended {
		t.Fatalf("the fixture no longer ties at the served weight %.2f (%v vs %v) — either the "+
			"defect is fixed, in which case delete this test rather than editing it, or the fixture "+
			"stopped exercising it",
			DefaultRerankWeight, out[0].Blended, out[1].Blended)
	}
	if out[0].Index != 0 {
		t.Errorf("tie broken toward the cross-encoder's pick; today the stable sort keeps the fused "+
			"order, got index %d first", out[0].Index)
	}
	t.Logf("served blend at w=%.2f: blended=[%v %v] first=index%d — the cross-encoder asked for the "+
		"opposite and was discarded", DefaultRerankWeight, out[0].Blended, out[1].Blended, out[0].Index)
}

// TestLowSpreadIsAmplifiedByMinMax is the second, larger half of the same
// mechanism, and it is independent of the weight.
//
// min-max is scale-free: it maps the pool minimum to 0 and the maximum to 1
// whatever the distance between them. A cross-encoder that considers every
// candidate equivalent and one that is decisive therefore produce the SAME
// normalised range, and the weight is then applied to amplified noise exactly as
// it would be to a verdict. The `span == 0` guard cannot help — it fires only on
// exact equality, which floating-point logits do not produce.
func TestLowSpreadIsAmplifiedByMinMax(t *testing.T) {
	indifferent := []float64{0.5000, 0.4990, 0.4995} // spread 0.001
	decisive := []float64{5.0, -5.0, 0.0}            // spread 10.0

	gotIndifferent := normalizeScores(indifferent)
	gotDecisive := normalizeScores(decisive)

	rangeOf := func(v []float64) float64 {
		mn, mx := v[0], v[0]
		for _, x := range v {
			mn, mx = math.Min(mn, x), math.Max(mx, x)
		}
		return mx - mn
	}
	if r := rangeOf(gotIndifferent); r != 1 {
		t.Errorf("a 0.001 spread normalised to a range of %v, want 1 — the amplification this test "+
			"is about did not happen", r)
	}
	if rangeOf(gotIndifferent) != rangeOf(gotDecisive) {
		t.Errorf("indifferent and decisive inputs produced different ranges (%v vs %v); if that is "+
			"true the normaliser is no longer scale-free and this test should be deleted",
			rangeOf(gotIndifferent), rangeOf(gotDecisive))
	}
	t.Logf("spread 0.001 -> %v ; spread 10.0 -> %v — indistinguishable downstream",
		gotIndifferent, gotDecisive)
}

// TestSmallPoolArmsDisagree is the fixture's own guard.
//
// Three normalisers that all order this page identically would make the eval
// arms measure nothing, however carefully they were registered. So the assertion
// is not "sigmoid is better" — that is what the eval decides — but "these arms
// can be told apart on the case the defect lives in".
func TestSmallPoolArmsDisagree(t *testing.T) {
	ranked, scores := twoCandidateDisagreement()

	got := map[string][]float64{}
	first := map[string]int{}
	for _, norm := range []string{RerankNormMinMax, RerankNormSigmoid, RerankNormRank} {
		out := BlendRerankWith(ranked, scores, DefaultRerankWeight, norm)
		got[norm] = []float64{out[0].Blended, out[1].Blended}
		first[norm] = out[0].Index
		t.Logf("%-8s blended=[%.4f %.4f] first=index%d", norm, out[0].Blended, out[1].Blended, out[0].Index)
	}

	// min-max ties and therefore keeps the fused order; sigmoid must not, because
	// its values are pool-independent and cannot both land on 0.5.
	if got[RerankNormMinMax][0] != got[RerankNormMinMax][1] {
		t.Error("min-max no longer ties on this fixture; the arm comparison has lost its subject")
	}
	if first[RerankNormSigmoid] == first[RerankNormMinMax] {
		t.Errorf("sigmoid ordered this page the same way min-max did (index%d first) — a "+
			"maximally-disagreeing cross-encoder is still being discarded", first[RerankNormSigmoid])
	}
	// rank is expected to behave LIKE min-max here. It is registered to separate
	// the amplification half from the tie half, not to fix the tie, and pinning
	// that keeps the eval table honest about what each arm is for.
	if got[RerankNormRank][0] != got[RerankNormRank][1] {
		t.Logf("NOTE: rank normalisation no longer ties (%v) — update ADR-030, which predicts it does",
			got[RerankNormRank])
	}
}

// TestSigmoidPreservesConfidence is the second half, mirrored.
//
// normalizeScores maps a 0.001 spread and a 10.0 spread onto the same range;
// sigmoid must not, or it fixes the tie while leaving the larger defect intact.
func TestSigmoidPreservesConfidence(t *testing.T) {
	indifferent := normalizeSigmoid([]float64{0.5000, 0.4990, 0.4995})
	decisive := normalizeSigmoid([]float64{5.0, -5.0, 0.0})

	spread := func(v []float64) float64 {
		mn, mx := v[0], v[0]
		for _, x := range v {
			mn, mx = math.Min(mn, x), math.Max(mx, x)
		}
		return mx - mn
	}
	si, sd := spread(indifferent), spread(decisive)
	if si >= sd {
		t.Errorf("sigmoid gave an indifferent cross-encoder a spread of %v and a decisive one %v; "+
			"it is supposed to preserve magnitude, not erase it", si, sd)
	}
	if si > 0.01 {
		t.Errorf("a 0.001 logit spread became %v after sigmoid — too wide to read as indifference", si)
	}
	t.Logf("sigmoid: indifferent spread %.6f, decisive spread %.6f (min-max gives both 1.0)", si, sd)
}

// TestCrossEncoderDecidesATwoCandidatePool is the property T2 ships, and it is
// the test that was red before the default changed.
//
// It asserts the PROPERTY, never the constant. A test reading
// `blended == 0.5522` would go red on any future retune and green on a
// normalisation that reintroduced the tie at a different weight — it would pin
// the number and miss the behaviour. What has to stay true is that a
// cross-encoder which disagrees as strongly as it can express gets to change the
// order of a two-hit page.
//
// The pool size is the point. Of 648 reranked recalls in the deployed table on
// 2026-08-25, 45 returned zero or one hit and 114 returned two to four — so the
// small pool is not an edge case, it is roughly a quarter of real recalls, and it
// is the shape a tight wing or a narrow query produces.
func TestCrossEncoderDecidesATwoCandidatePool(t *testing.T) {
	ranked, scores := twoCandidateDisagreement()

	// Resolve the norm the way the served path does, so this test follows the
	// default rather than hard-coding a policy name beside it.
	served := (&Service{}).RerankNormName()
	if served == RerankNormMinMax {
		t.Fatalf("the served normaliser is %q; this test exists because min-max discards the "+
			"cross-encoder on a small pool, so it cannot pass while that is the default", served)
	}

	out := BlendRerankWith(ranked, scores, DefaultRerankWeight, served)
	if len(out) != 2 {
		t.Fatalf("BlendRerankWith returned %d candidates, want 2", len(out))
	}
	if out[0].Blended == out[1].Blended {
		t.Fatalf("the served blend still ties on an opposed two-candidate pool (%v); the "+
			"cross-encoder's verdict is being discarded", out[0].Blended)
	}
	if out[0].Index != 1 {
		t.Errorf("candidate 1 has by far the stronger cross-encoder score (%v vs %v) and did not "+
			"lead: got index %d first, blended=[%v %v]",
			scores[1], scores[0], out[0].Index, out[0].Blended, out[1].Blended)
	}
}

// TestLowSpreadDoesNotBecomeSignal is the other half of the same default.
//
// An indifferent cross-encoder must not outvote a decisive fused score. Under
// min-max it could: a 0.001 logit spread is stretched to the full [0,1] range, so
// noise arrives at the blend wearing the same clothes as a verdict.
func TestLowSpreadDoesNotBecomeSignal(t *testing.T) {
	// THREE candidates, not two, and that is forced rather than stylistic: on a
	// two-element pool min-max ties an opposed pair instead of flipping it, so the
	// control below could not discriminate and the test would have been asserting
	// nothing. Found by running it — the first version logged its own NOTE.
	//
	// Candidate 0 is clearly best on the fused axis. The cross-encoder mildly
	// prefers candidate 1, by 0.001 — an amount that means nothing.
	ranked := []HybridScore{{Index: 0, Fused: 0.9}, {Index: 1, Fused: 0.5}, {Index: 2, Fused: 0.1}}
	scores := []float64{0.4990, 0.5000, 0.4995}

	served := (&Service{}).RerankNormName()
	out := BlendRerankWith(ranked, scores, DefaultRerankWeight, served)
	if out[0].Index != 0 {
		t.Errorf("a 0.001 cross-encoder preference overturned a decisive fused score: got index %d "+
			"first, blended=[%v %v]. Noise is being read as signal.",
			out[0].Index, out[0].Blended, out[1].Blended)
	}

	// And the control: under min-max the same input DOES flip, which is what makes
	// this test worth running rather than a restatement of the sort order.
	mm := BlendRerankWith(ranked, scores, DefaultRerankWeight, RerankNormMinMax)
	if mm[0].Index == out[0].Index {
		t.Logf("NOTE: min-max no longer flips on this fixture (blended=[%v %v]); the control has "+
			"stopped discriminating and the fixture needs revisiting", mm[0].Blended, mm[1].Blended)
	} else {
		t.Logf("control: min-max puts index%d first on the same input — a 0.001 cross-encoder "+
			"preference was amplified to the full range and won", mm[0].Index)
	}
}
