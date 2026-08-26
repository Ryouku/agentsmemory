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
