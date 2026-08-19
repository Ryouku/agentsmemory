package palace

import (
	"strings"
	"testing"
)

// TestBootstrapSeparatesSignalFromNoise: a large real difference must exclude
// zero, and pure noise must not — otherwise the intervals are decoration.
func TestBootstrapSeparatesSignalFromNoise(t *testing.T) {
	// Arm A finds everything at rank 1; arm B misses half outright.
	a := []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	b := []int{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0}
	if d := PairedDelta(a, b); d.Contains(0) {
		t.Errorf("a decisive difference produced an interval containing zero: %v", d)
	}

	// Identical arms: the delta must be exactly zero-width around zero.
	if d := PairedDelta(a, a); !d.Contains(0) || d.Lo != 0 || d.Hi != 0 {
		t.Errorf("identical arms produced a nonzero delta: %v", d)
	}

	// One flipped case out of twelve is the kind of gap this repo previously
	// ranked arms on. The interval must refuse to.
	c := []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2}
	if d := PairedDelta(a, c); !d.Contains(0) {
		t.Errorf("a one-case gap at n=12 excluded zero: %v — this is the over-reading the stats exist to stop", d)
	}
}

// TestBootstrapIsReproducible: same inputs, same interval — a report that
// changes between runs of identical cases reads as broken.
func TestBootstrapIsReproducible(t *testing.T) {
	ranks := []int{1, 2, 0, 3, 1, 1, 0, 2, 1, 5, 1, 1}
	first, second := BootstrapMRR(ranks), BootstrapMRR(ranks)
	if first != second {
		t.Errorf("two runs over identical ranks: %v then %v", first, second)
	}
	if first.Lo >= first.Hi {
		t.Errorf("degenerate interval %v for varied ranks", first)
	}
}

// TestEvaluateFailsLoudOnStaleGold pins the adversarial-review finding: a case
// whose drawer was purged by a re-mine must stop the run and say why, not score
// as an all-arm retrieval miss that the pool diagnosis then misattributes.
func TestEvaluateFailsLoudOnStaleGold(t *testing.T) {
	svc := newTestService(t)
	const team = "team-stale"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a real memory so the corpus is not empty"})

	_, err := svc.Evaluate(t.Context(), team,
		[]EvalCase{{Query: "anything", Expect: "purged-drawer-id-that-no-longer-exists"}}, 10, nil)
	if err == nil {
		t.Fatal("a stale gold id must fail the run, not silently score as a miss")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("the error must name the cause: %v", err)
	}
}
