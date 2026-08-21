package palace

import "testing"

// TestGatedArmMatchesProductionForEveryConfiguration: the supersession gate must
// judge the arm production actually ranks with.
//
// It was a constant under a comment saying it "must change in the same commit
// that changes production ranking". ADR-014 changed production on two dimensions
// — fusion to rrf and the closet prior off — and the constant stayed at
// ArmReranked, which is linear plus a full closet prior: a pipeline nobody runs.
// Both arms appear in the report, so the lookup found one and reported nothing
// amiss. A rule enforced by remembering is a rule that eventually is not.
func TestGatedArmMatchesProductionForEveryConfiguration(t *testing.T) {
	for _, tc := range []struct {
		fusionRRF, closetOn, reranked bool
		want                          EvalArm
	}{
		{true, false, true, ArmRRFReranked},   // the shipped default with a reranker
		{true, false, false, ArmRRF},          // the shipped default without one
		{false, true, true, ArmReranked},      // what shipped BEFORE ADR-014
		{false, true, false, ArmHybridCloset}, // …without a reranker
		{false, false, true, ArmHybridRerank}, // linear with the prior retired
		{false, false, false, ArmHybrid},
	} {
		if got := gatedArmFor(tc.fusionRRF, tc.closetOn, tc.reranked); got != tc.want {
			t.Errorf("gatedArmFor(rrf=%v closet=%v rerank=%v) = %q, want %q",
				tc.fusionRRF, tc.closetOn, tc.reranked, got, tc.want)
		}
	}
}

// TestGatedArmIsARegisteredArm: an arm the gate names but the report never
// contains makes every verdict "no comparable arm", which reads as a corpus
// problem rather than a wiring one.
func TestGatedArmIsARegisteredArm(t *testing.T) {
	registered := map[EvalArm]bool{}
	for _, a := range evalArms(EvalOptions{Contextual: true}, true) {
		registered[a] = true
	}
	for _, combo := range []struct{ rrf, closet, rr bool }{
		{true, false, true}, {true, false, false}, {false, true, true},
		{false, true, false}, {false, false, true}, {false, false, false},
	} {
		arm := gatedArmFor(combo.rrf, combo.closet, combo.rr)
		if !registered[arm] {
			t.Errorf("gatedArmFor(%v,%v,%v) = %q, which evalArms never registers — every verdict "+
				"would report 'no comparable arm'", combo.rrf, combo.closet, combo.rr, arm)
		}
	}
}

// TestServiceReportsItsOwnGatedArm: a run against a non-default configuration
// must be gated on the arm IT serves, not on the shipped default.
func TestServiceReportsItsOwnGatedArm(t *testing.T) {
	svc := NewService(nil, nil, nil, 0)
	if got := svc.SupersessionGatedArmFor(); got != ArmHybridCloset {
		t.Errorf("a bare service (linear, full closet, no reranker) reported %q, want %q", got, ArmHybridCloset)
	}
	if got := svc.WithFusion("rrf").SupersessionGatedArmFor(); got != ArmRRF {
		t.Errorf("after WithFusion(rrf) the service reported %q, want %q", got, ArmRRF)
	}
	if got := svc.WithClosetBoost(0).SupersessionGatedArmFor(); got != ArmRRF {
		t.Errorf("closet off under rrf should still be %q, got %q", ArmRRF, got)
	}
}
