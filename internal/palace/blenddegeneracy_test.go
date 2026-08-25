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
