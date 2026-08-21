package palace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCalibrationFingerprintRoundTrip pins that a calibration survives the disk
// unchanged, and — the part that matters — that an ABSENT threshold reads back
// absent rather than as zero.
//
// Zero is an ordinary score on every scale this file can carry: a cross-encoder
// logit sits either side of it, and a distance gap of zero means an indecisive
// page. So a calibration that could not derive a boundary and one that derived
// the boundary 0.0 are different facts, and JSON's habit of writing a missing
// number as 0 collapses them. The collapse is silent and it fails OPEN: a nil
// answer_at means "do not gate", while a 0.0 answer_at means "answer everything
// scoring above zero", and on an unbounded scale that is most of the corpus.
func TestCalibrationFingerprintRoundTrip(t *testing.T) {
	at, below := 1.25, -0.5
	full := Calibration{
		AnswerAt: &at, RefuseBelow: &below,
		AnswerRecallTarget: 0.95, RefuseAllowance: 1,
		AchievedRecall: 0.974,
		Reachable:      39, Absent: 20, Unreachable: 4,
		GatePassed: true, RefusalRate: 0.45, RefusalLower: 0.31, RefusalBar: 0.30,
		RerankModel: "some-cross-encoder",
		Profile:     "fusion=rrf lex=auto pool=10 weight=0.50",
		Canary: []CanaryPair{
			{Query: "q1", Document: "d1", Mean: 0.80, MaxDeviation: 0.0},
			{Query: "q2", Document: "d2", Mean: -1.20, MaxDeviation: 0.02},
		},
		ScoresBounded: false,
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "calibration.json")
	if err := full.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadCalibration(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.AnswerAt == nil || *got.AnswerAt != at {
		t.Errorf("answer_at round-tripped as %v, want %v", got.AnswerAt, at)
	}
	if got.RefuseBelow == nil || *got.RefuseBelow != below {
		t.Errorf("refuse_below round-tripped as %v, want %v", got.RefuseBelow, below)
	}
	if len(got.Canary) != 2 || got.Canary[1].MaxDeviation != 0.02 {
		t.Errorf("canary did not survive: %+v", got.Canary)
	}
	if got.ScoresBounded {
		t.Error("scores_bounded flipped to true — the dialect is INFERRED from the canary and " +
			"must not be re-guessed on load")
	}
	if got.ID == "" {
		t.Error("no ID — nothing names this operating point in telemetry, so rows judged under " +
			"two different calibrations pool as one population")
	}

	// The case the field types exist for.
	partial := Calibration{AnswerAt: nil, RefuseBelow: &below, Reachable: 39, Absent: 20}
	p2 := filepath.Join(dir, "partial.json")
	if err := partial.Save(p2); err != nil {
		t.Fatalf("save partial: %v", err)
	}
	back, err := LoadCalibration(p2)
	if err != nil {
		t.Fatalf("load partial: %v", err)
	}
	if back.AnswerAt != nil {
		t.Errorf("an ABSENT answer_at read back as %v — a threshold that could not be derived "+
			"is now a threshold of that value, and on an unbounded scale it answers everything",
			*back.AnswerAt)
	}

	// Two calibrations that differ must not share an ID, or telemetry cannot tell
	// which operating point produced a row.
	if full.ID == "" || partial.ID == "" {
		t.Fatal("Save did not stamp an ID")
	}
	if full.ID == partial.ID {
		t.Errorf("two different calibrations share the ID %q", full.ID)
	}
}

// TestLoadCalibrationRejectsGarbage pins that an unreadable or corrupt file is an
// ERROR rather than a zero-valued calibration.
//
// A zero Calibration has nil thresholds, which read as "do not gate" — so a
// corrupt file that loaded silently would disable the gate while reporting
// success. That is the failure mode where a safety mechanism is absent and
// everything says it is present.
func TestLoadCalibrationRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCalibration(bad); err == nil {
		t.Error("a corrupt calibration loaded without error; its nil thresholds would read as " +
			"'do not gate' and the gate would be silently off")
	}
	if _, err := LoadCalibration(filepath.Join(dir, "absent.json")); err == nil {
		t.Error("a missing calibration loaded without error")
	}
}

// driftingReranker returns a different score on each call, cycling through the
// supplied offsets. It stands in for a reranker whose output moves under load.
type driftingReranker struct {
	base    []float64
	offsets []float64
	call    int
}

func (r *driftingReranker) Rerank(_ context.Context, _ string, docs []string) ([]float64, error) {
	off := r.offsets[r.call%len(r.offsets)]
	r.call++
	out := make([]float64, len(docs))
	for i := range docs {
		out[i] = r.base[i%len(r.base)] + off
	}
	return out, nil
}

// TestCanaryToleranceIsMeasuredNotTyped pins the invariant that makes the
// fingerprint worth having: the tolerance comes from the INSTRUMENT, never from a
// constant in the source.
//
// A typed epsilon is a guess about someone else's hardware. Too tight and a
// healthy reranker is declared broken on a busy host; too loose and a genuinely
// different model passes as the calibrated one — which is the failure that
// matters, because it silently applies a threshold calibrated for a scale that no
// longer exists.
//
// A deterministic reranker must yield tolerance 0, and therefore an exact-match
// check at startup. That is the right answer when it is true, so it has to be
// measured rather than floored to some "safe" non-zero value.
func TestCanaryToleranceIsMeasuredNotTyped(t *testing.T) {
	ctx := context.Background()

	t.Run("deterministic yields zero", func(t *testing.T) {
		steady := &driftingReranker{base: []float64{0.8, -1.2}, offsets: []float64{0}}
		got, err := ScoreCanary(ctx, steady)
		if err != nil {
			t.Fatalf("score canary: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("no canary pairs scored — the fingerprint would check nothing")
		}
		for _, p := range got {
			if p.MaxDeviation != 0 {
				t.Errorf("pair %q got tolerance %v from a deterministic reranker; a non-zero "+
					"floor here is an epsilon typed in by another name", p.Query, p.MaxDeviation)
			}
		}
	})

	t.Run("drift is captured as the widest gap observed", func(t *testing.T) {
		// offsets span 0.30; the tolerance must be the observed spread, not an
		// average of it and not a constant.
		drifty := &driftingReranker{base: []float64{0.8, -1.2}, offsets: []float64{0, 0.1, -0.2, 0.05, 0}}
		got, err := ScoreCanary(ctx, drifty)
		if err != nil {
			t.Fatalf("score canary: %v", err)
		}
		for _, p := range got {
			if p.MaxDeviation < 0.29 || p.MaxDeviation > 0.31 {
				t.Errorf("pair %q tolerance %v; the repeats span 0.30, so a tolerance that is "+
					"not ~0.30 is not derived from what was observed", p.Query, p.MaxDeviation)
			}
		}
	})

	t.Run("a broken reranker is an error, not a zero fingerprint", func(t *testing.T) {
		if _, err := ScoreCanary(ctx, &fakeReranker{err: errors.New("unreachable")}); err == nil {
			t.Error("a failing reranker produced a fingerprint; an empty one would then match " +
				"any future instrument and the check would pass on nothing")
		}
	})
}
