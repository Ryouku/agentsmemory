package palace

import (
	"fmt"
	"testing"
	"time"
)

// armFingerprint renders every knob serviceForArm can set. It is deliberately
// NOT RankingProfile(): that string omits recencyBand, so three recency arms
// fingerprinted identically to plain hybrid and the gate cried wolf on its
// first run. A gate with false alarms is one people learn to skip, and then it
// protects nothing.
func armFingerprint(s *Service) string {
	return fmt.Sprintf("fusionRRF=%v bm25Auto=%v bm25IDF=%v bm25Base=%.4f lexNorm=%s "+
		"closet=%.4f rerankWeight=%.4f rerankNorm=%s rerankPool=%d recencyBand=%.4f "+
		"retrieveK=%d evidence=%s",
		s.fusionRRF, s.bm25Auto, s.bm25IDF, s.bm25Base, s.lexNormName,
		s.closetBoostScale, s.rerankWeight, s.RerankNormName(), s.rerankPool,
		s.recencyBand, s.retrieveK, s.MemoryEvidenceSelectorName())
}

// TestEvalArmsAreDISTINCTCONFIGURATIONSNotJustDistinctNames is the gate that
// TestEvalArmNamesAreUnique cannot be: two arms can carry different names and
// the SAME knobs, and then a table prints two rows, identical to three decimal
// places, that a reader interprets as a replicated null.
//
// That is not hypothetical. serviceForArm's reset block cleared nine ranking
// knobs and not rerankNorm, so at the shipped default of sigmoid the arm named
// `rrf+rerank` — whose name promises the min-max baseline — WAS
// `rrf+rerank norm=sigmoid`. Both scored 0.708 on corpus B, and a sentence
// reading "sigmoid normalisation scores identically to min-max" was written
// into an evidence file and an ADR. Min-max had never run; the identity was the
// defect's signature, and it read as reassurance.
//
// This is the same shape armBoosts was written to fix, one field over. Its own
// comment records twelve arms that "promise a pure ranking comparison" while
// quietly carrying a curation prior, and a conclusion read off that table.
//
// The fingerprint is written out knob by knob rather than derived from the arm
// registry: a gate taking its expectation from the same switch it guards passes
// whatever that switch happens to do.
func TestEvalArmsAreDISTINCTCONFIGURATIONSNotJustDistinctNames(t *testing.T) {
	svc := newTestService(t).
		WithReranker(&fakeReranker{budget: 10 * time.Second}, 10).
		WithRerankWeight(0.5).
		WithRerankNorm(DefaultRerankNorm)

	// A sweep's endpoint can legitimately land on a named arm: the bm25 sweep at
	// 0.00 IS vector-only, at 0.40 IS hybrid, and the weight sweep at 0.50 IS the
	// served blend. These pairs are allowed because BOTH names state their knobs
	// honestly — a reader seeing `fusion bm25=0.40` beside `hybrid` can tell they
	// are one configuration. That is the opposite of the case this gate exists for,
	// where a name promised a normaliser the arm did not run. They do cost a
	// duplicate row and a duplicate scoring pass; if the sweeps are ever trimmed,
	// these are the rows to drop.
	allowed := map[EvalArm]EvalArm{
		"fusion bm25=0.00":    ArmVector,
		"fusion bm25=0.40":    ArmHybrid,
		"rerank blend w=0.50": ArmHybridRerank,
	}

	seen := map[string]EvalArm{}
	for _, arm := range evalArms(EvalOptions{Contextual: true}, true) {
		c := svc.serviceForArm(arm)
		if c == nil {
			continue // production and contextual retrieve on another path
		}
		profile := armFingerprint(c)
		if prior, dup := seen[profile]; dup {
			if allowed[arm] == prior || allowed[prior] == arm {
				continue
			}
			t.Errorf("arms %q and %q are the SAME configuration:\n  %s\n"+
				"Two names over one set of knobs put two identical rows in the table, "+
				"which reads as a replicated result and is actually one arm measured twice. "+
				"Either give the arm the knobs its name promises, or stop declaring it.",
				prior, arm, profile)
			continue
		}
		seen[profile] = arm
	}
}

// TestProductionShapedArmsReconstructTheSERVEDNormaliser is the other half of
// the arm-configuration contract, and it exists because the first fix for the
// min-max control broke it.
//
// That fix (e20890e) reset rerankNorm to min-max for EVERY cloned arm. It cured
// `rrf+rerank`, whose name every table in this corpus reads as the min-max
// control — and simultaneously forced min-max onto `hybrid+rerank`, documented
// as the closet-OFF reranked arm production actually serves, and onto every
// `rerank blend w=*` row, which exist to sweep the WEIGHT at a fixed normaliser.
// Under a served sigmoid those arms then measured a pipeline nobody runs, which
// is the same defect the fix was for, one arm family over. A cross-encoder
// spread of 0.500/0.501 that sigmoid preserves is stretched to 0/1 by min-max,
// so an opposed fused order can flip the winner and a weight conclusion is read
// off the wrong normaliser.
//
// The rule this pins: an arm that NAMES a normaliser has that one, the min-max
// control has min-max whatever the server is set to, and a production-shaped arm
// has the SERVED value — because that is what makes it production-shaped.
func TestProductionShapedArmsReconstructTheSERVEDNormaliser(t *testing.T) {
	for _, served := range []string{RerankNormSigmoid, RerankNormRank, RerankNormMinMax} {
		t.Run("served="+served, func(t *testing.T) {
			svc := newTestService(t).
				WithReranker(&fakeReranker{budget: 10 * time.Second}, 10).
				WithRerankWeight(0.5).
				WithRerankNorm(served)

			for _, tc := range []struct {
				arm  EvalArm
				want string
				why  string
			}{
				{ArmHybridRerank, served, "the closet-OFF reranked arm IS the served shape; pinning it to a fixed normaliser measures a pipeline nobody runs"},
				{ArmReranked, served, "production-shaped, closet on; same argument"},
				{ArmRRFReranked, RerankNormMinMax, "the min-max control every table in this corpus reads as the baseline — it must not drift with the server"},
				{ArmBlendSigmoid, RerankNormSigmoid, "the arm names its normaliser"},
				{ArmBlendRank, RerankNormRank, "the arm names its normaliser"},
			} {
				c := svc.serviceForArm(tc.arm)
				if c == nil {
					t.Fatalf("%s reconstructed no service", tc.arm)
				}
				if got := c.RerankNormName(); got != tc.want {
					t.Errorf("arm %q normaliser = %q, want %q\n  %s", tc.arm, got, tc.want, tc.why)
				}
			}
		})
	}
}
