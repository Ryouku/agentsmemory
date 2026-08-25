package palace

import "testing"

// TestHitCarriesTheScoreItWasOrderedBy: the page is ordered by Blended, and
// Blended is what comes out.
//
// The fixture is built so the two orders DISAGREE, which is the only version of
// this test worth running. `BlendRerank` combines a pool-normalised rerank score
// with a pool-normalised fused score at the served weight, so on most inputs the
// cross-encoder dominates and the blended order coincides with the rerank order
// — a fixture like that passes identically whether the implementation sorts on
// the blend or just hands back the rerank score under a new name.
//
// Measured on the live stack 2026-08-25, both cases occur in production. With
// query context the logits spread 0.480 → 0.178 and the orders coincide; without
// it they sat within 0.07 of each other (0.273, 0.108, 0.041, 0.042, 0.064) and
// the fused component decided — which was reported as a suspected reranker bug
// and is the blend working as ADR-024 argues it should. This test pins the case
// that made it look like a bug.
func TestHitCarriesTheScoreItWasOrderedBy(t *testing.T) {
	// THREE candidates, not two, and that is forced rather than stylistic:
	// normalizeScores is min-max over the pool, so any two-element fixture
	// normalises to exactly {0, 1} on BOTH axes and ties at weight 0.5 — the
	// first attempt at this test produced 0.5 against 0.5 and could not have
	// distinguished anything. A degenerate fixture is the failure mode this whole
	// task is about, met while writing the test for it.
	//
	// fusedNorm  = [1.0, 0.0, 0.5]
	// rerankNorm = [0.6, 1.0, 0.0]
	// blended@.5 = [0.8, 0.5, 0.25]  -> candidate 0 leads
	// rerank alone                   -> candidate 1 leads
	ranked := []HybridScore{
		{Index: 0, Fused: 1.00},
		{Index: 1, Fused: 0.00},
		{Index: 2, Fused: 0.50},
	}
	scores := []float64{0.60, 1.00, 0.00}

	out := BlendRerank(ranked, scores, 0.5)
	if len(out) != 3 {
		t.Fatalf("BlendRerank returned %d candidates, want 3", len(out))
	}

	// Guard the fixture itself: if the rerank order and the blended order agreed,
	// this test could not tell a correct implementation from one that returns the
	// rerank score under another name, however it were worded.
	rerankWinner := 1 // scores[1] is the highest cross-encoder score
	if out[0].Index == rerankWinner {
		t.Fatalf("the fixture no longer disagrees: the rerank winner is also the blended winner, so "+
			"this test cannot fail for the reason it exists. out=%+v", out)
	}

	if out[0].Index != 0 {
		t.Errorf("the page is not ordered by Blended: candidate 0 has the stronger fused score and "+
			"should lead at weight 0.5, got index %d first (%+v)", out[0].Index, out[0])
	}
	for i := 1; i < len(out); i++ {
		if out[i-1].Blended <= out[i].Blended {
			t.Errorf("Blended is not descending across the page at %d: %v then %v — the value the sort "+
				"used is not the value carried out", i, out[i-1].Blended, out[i].Blended)
		}
	}
	for i, h := range out {
		if h.Blended == 0 {
			t.Errorf("candidate %d carries no Blended value, so the number that decided its position "+
				"is unavailable to anything downstream: %+v", i, h)
		}
		if !h.Reranked {
			t.Errorf("candidate %d is in the scored pool and not marked Reranked: %+v", i, h)
		}
	}
}
