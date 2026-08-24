package contractaxis

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)

func TestNoRegisteredAxesFailsInsteadOfPassingVacuously(t *testing.T) {
	report := Evaluate(context.Background(), testNow)

	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "<registry>", Item: "*", Contract: UniverseContract})
}

func TestAnEmptyUniverseFailsInsteadOfPassingVacuously(t *testing.T) {
	axis := completeAxis("empty")
	axis.Universe = func(context.Context) ([]string, error) { return nil, nil }

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "empty", Item: "*", Contract: UniverseContract})
}

func TestAUniverseItemNeedsBindingAndBothObservations(t *testing.T) {
	axis := completeAxis("tools")
	axis.Universe = func(context.Context) ([]string, error) { return []string{"am_new"}, nil }
	axis.Probe = func(context.Context, string, *Observation) error {
		return nil
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	for _, contract := range []Contract{BindingContract, PositiveContract, NegativeContract} {
		assertResidual(t, report, ResidualKey{Axis: "tools", Item: "am_new", Contract: contract})
	}
}

func TestClaimedCoverageCannotPassWithoutRecordedCalls(t *testing.T) {
	claimed := map[string]bool{"item": true}
	axis := completeAxis("claims")
	axis.Probe = func(_ context.Context, item string, _ *Observation) error {
		if !claimed[item] {
			return errors.New("missing claim")
		}
		return nil
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	for _, contract := range []Contract{BindingContract, PositiveContract, NegativeContract} {
		assertResidual(t, report, ResidualKey{Axis: "claims", Item: "item", Contract: contract})
	}
}

func TestAProbeErrorDoesNotBecomeNegativeEvidence(t *testing.T) {
	axis := completeAxis("probe")
	axis.Probe = func(context.Context, string, *Observation) error {
		return errors.New("outer server did not start")
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "probe", Item: "item", Contract: ProbeContract})
}

func TestAValidExceptionOwnsButDoesNotHideAResidual(t *testing.T) {
	axis := completeAxis("external")
	axis.Probe = func(_ context.Context, _ string, observation *Observation) error {
		observation.RecordBinding()
		observation.RecordPositive()
		return nil
	}
	axis.Exceptions = []Exception{{
		Key:       ResidualKey{Axis: "external", Item: "item", Contract: NegativeContract},
		Kind:      ExternalDependency,
		Owner:     "storage",
		Reason:    "the refusal is visible only against a live Qdrant namespace",
		Reference: "issue-123",
		Expires:   testNow.Add(24 * time.Hour),
	}}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Partial)
	residual := findResidual(t, report, axis.Exceptions[0].Key)
	if !residual.Excepted || residual.Exception == nil {
		t.Fatalf("expected visible owned residual, got %+v", residual)
	}
}

func TestAStaleExceptionFails(t *testing.T) {
	axis := completeAxis("stale")
	axis.Exceptions = []Exception{{
		Key:             ResidualKey{Axis: "stale", Item: "item", Contract: NegativeContract},
		Kind:            NonProduction,
		Owner:           "test-infra",
		Reason:          "fixture-only item",
		Reference:       "ADR-024",
		PermanentReason: "the item is compiled only into tests",
	}}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertContract(t, report, ExceptionContract)
}

func TestInvalidExceptionsFail(t *testing.T) {
	tests := map[string]Exception{
		"ownerless": {
			Key: ResidualKey{Axis: "bad", Item: "item", Contract: NegativeContract}, Kind: ExternalDependency,
			Reason: "live service", Reference: "issue-1", Expires: testNow.Add(time.Hour),
		},
		"expired": {
			Key: ResidualKey{Axis: "bad", Item: "item", Contract: NegativeContract}, Kind: ExternalDependency,
			Owner: "ops", Reason: "live service", Reference: "issue-1", Expires: testNow.Add(-time.Hour),
		},
		"unknown kind": {
			Key: ResidualKey{Axis: "bad", Item: "item", Contract: NegativeContract}, Kind: "awkward",
			Owner: "ops", Reason: "live service", Reference: "issue-1", Expires: testNow.Add(time.Hour),
		},
		"two lifetimes": {
			Key: ResidualKey{Axis: "bad", Item: "item", Contract: NegativeContract}, Kind: ExternalDependency,
			Owner: "ops", Reason: "live service", Reference: "issue-1", Expires: testNow.Add(time.Hour), PermanentReason: "forever",
		},
	}

	for name, exception := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("bad")
			axis.Probe = func(_ context.Context, _ string, observation *Observation) error {
				observation.RecordBinding()
				observation.RecordPositive()
				return nil
			}
			axis.Exceptions = []Exception{exception}
			report := Evaluate(context.Background(), testNow, axis)
			assertStatus(t, report, Fail)
			assertContract(t, report, ExceptionContract)
		})
	}
}

func TestAnInvalidExceptionCannotBeExceptedByAnotherException(t *testing.T) {
	axis := completeAxis("nested")
	axis.Probe = func(_ context.Context, _ string, observation *Observation) error {
		observation.RecordBinding()
		observation.RecordPositive()
		return nil
	}
	invalidKey := ResidualKey{Axis: "nested", Item: "item", Contract: NegativeContract}
	syntheticKey := ResidualKey{Axis: "nested", Item: invalidKey.String(), Contract: ExceptionContract}
	axis.Exceptions = []Exception{
		{
			Key: invalidKey, Kind: ExternalDependency,
			Reason: "owner deliberately omitted", Reference: "issue-1", Expires: testNow.Add(time.Hour),
		},
		{
			Key: syntheticKey, Kind: ExternalDependency, Owner: "nobody",
			Reason: "attempt to hide validator output", Reference: "issue-2", Expires: testNow.Add(time.Hour),
		},
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	for _, residual := range report.Axes[0].Residuals {
		if residual.Key.Contract == ExceptionContract && residual.Excepted {
			t.Fatalf("validation residual was excepted: %+v", residual)
		}
	}
}

func TestARatchetNamesTheExactResidualsAndNeverLooksComplete(t *testing.T) {
	negative := ResidualKey{Axis: "ratchet", Item: "item", Contract: NegativeContract}
	axis := completeAxis("ratchet")
	axis.Maturity = Ratchet
	axis.Probe = func(_ context.Context, _ string, observation *Observation) error {
		observation.RecordBinding()
		observation.RecordPositive()
		return nil
	}
	axis.Ratchet = []RatchetObligation{testRatchet(negative)}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Partial)
	if report.Complete() {
		t.Fatal("a ratchet with a residual reported complete")
	}
	var out bytes.Buffer
	if err := WriteReport(&out, report); err != nil {
		t.Fatalf("write ratchet report: %v", err)
	}
	for _, want := range []string{"RATCHET", "owner=contract-axis", "reference=ADR-024", "expires=", "reason=known migration debt"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("ratchet report omitted %q:\n%s", want, out.String())
		}
	}
}

func TestInvalidRatchetObligationsFail(t *testing.T) {
	negative := ResidualKey{Axis: "ratchet", Item: "item", Contract: NegativeContract}
	tests := map[string]func(*RatchetObligation){
		"ownerless": func(obligation *RatchetObligation) { obligation.Owner = "" },
		"expired":   func(obligation *RatchetObligation) { obligation.Expires = testNow.Add(-time.Minute) },
		"stale":     func(obligation *RatchetObligation) { obligation.Key.Item = "absent" },
	}
	for name, breakObligation := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("ratchet")
			axis.Maturity = Ratchet
			axis.Probe = func(_ context.Context, _ string, observation *Observation) error {
				observation.RecordBinding()
				observation.RecordPositive()
				return nil
			}
			obligation := testRatchet(negative)
			breakObligation(&obligation)
			axis.Ratchet = []RatchetObligation{obligation}
			report := Evaluate(context.Background(), testNow, axis)
			assertStatus(t, report, Fail)
			assertContract(t, report, RatchetContract)
		})
	}
}

func TestARatchetFailsOnImprovementUntilPromoted(t *testing.T) {
	axis := completeAxis("ratchet")
	axis.Maturity = Ratchet
	axis.Ratchet = []RatchetObligation{testRatchet(ResidualKey{Axis: "ratchet", Item: "item", Contract: NegativeContract})}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertContract(t, report, RatchetContract)
}

func TestARatchetFailsOnARegressionHiddenBesideKnownDebt(t *testing.T) {
	axis := completeAxis("ratchet")
	axis.Maturity = Ratchet
	axis.Probe = func(_ context.Context, _ string, observation *Observation) error {
		observation.RecordPositive()
		return nil
	}
	axis.Ratchet = []RatchetObligation{testRatchet(ResidualKey{Axis: "ratchet", Item: "item", Contract: NegativeContract})}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "ratchet", Item: "item", Contract: BindingContract})
	assertContract(t, report, RatchetContract)
}

func TestAdvisoryEvidenceMayBeIncompleteButTheInstrumentMustWork(t *testing.T) {
	axis := completeAxis("advice")
	axis.Maturity = Advisory
	axis.Probe = func(context.Context, string, *Observation) error { return nil }
	axis.Mutants = nil
	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Advice)

	axis.Universe = func(context.Context) ([]string, error) { return nil, nil }
	report = Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
}

func TestMutationEvidenceNeedsEveryStep(t *testing.T) {
	tests := map[string]func(*MutantEvidence){
		"assertion": func(m *MutantEvidence) { m.assertion = "" },
		"apply":     func(m *MutantEvidence) { m.applied = false },
		"compile":   func(m *MutantEvidence) { m.compiled = false },
		"kill":      func(m *MutantEvidence) { m.killed = false },
		"restore":   func(m *MutantEvidence) { m.restored = false },
	}
	for name, breakEvidence := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("mutant")
			broken := goodMutant()
			breakEvidence(&broken)
			axis.Mutants = []MutantEvidence{broken}
			report := Evaluate(context.Background(), testNow, axis)
			assertStatus(t, report, Fail)
			assertContract(t, report, MutantContract)
		})
	}
}

func TestReportOrderAndLabelsAreStable(t *testing.T) {
	b := completeAxis("b")
	b.Probe = func(context.Context, string, *Observation) error { return nil }
	a := completeAxis("a")
	report := Evaluate(context.Background(), testNow, b, a)

	var out bytes.Buffer
	if err := WriteReport(&out, report); err != nil {
		t.Fatalf("write report: %v", err)
	}
	text := out.String()
	if !strings.HasPrefix(text, "CONTRACT AXES: FAIL\na: PASS") {
		t.Fatalf("axes were not sorted or labelled:\n%s", text)
	}
	if !strings.Contains(text, "OPEN b/item/binding") {
		t.Fatalf("residual identity missing from report:\n%s", text)
	}
}

func TestWriteReportDefensivelySortsByAxisItemAndContract(t *testing.T) {
	report := Report{Status: Fail, Axes: []AxisReport{
		{Axis: "b", Status: Pass, Maturity: Enforced},
		{
			Axis: "a", Status: Fail, Maturity: Enforced,
			Residuals: []Residual{
				{Key: ResidualKey{Axis: "a", Item: "a/b", Contract: Contract("a")}, Detail: "second"},
				{Key: ResidualKey{Axis: "a", Item: "a", Contract: Contract("z")}, Detail: "first"},
			},
		},
	}}

	var out bytes.Buffer
	if err := WriteReport(&out, report); err != nil {
		t.Fatalf("write report: %v", err)
	}
	text := out.String()
	axisA := strings.Index(text, "a: FAIL")
	axisB := strings.Index(text, "b: PASS")
	first := strings.Index(text, "OPEN a/a/z")
	second := strings.Index(text, "OPEN a/a/b/a")
	if axisA < 0 || axisB < 0 || axisA > axisB || first < 0 || second < 0 || first > second {
		t.Fatalf("report is not sorted by (axis, item, contract):\n%s", text)
	}
}

func completeAxis(id string) Axis {
	return Axis{
		ID:       id,
		Maturity: Enforced,
		Universe: func(context.Context) ([]string, error) { return []string{"item"}, nil },
		Probe: func(_ context.Context, _ string, observation *Observation) error {
			observation.RecordBinding()
			observation.RecordPositive()
			observation.RecordNegative()
			return nil
		},
		Mutants: []MutantEvidence{goodMutant()},
	}
}

func goodMutant() MutantEvidence {
	return MutantEvidence{
		id: "wire-cut", assertion: "TestProductionPath", applied: true,
		compiled: true, killed: true, restored: true,
	}
}

func testRatchet(key ResidualKey) RatchetObligation {
	return RatchetObligation{
		Key: key, Owner: "contract-axis", Reason: "known migration debt",
		Reference: "ADR-024", Expires: testNow.Add(7 * 24 * time.Hour),
	}
}

func assertStatus(t *testing.T, report Report, want Status) {
	t.Helper()
	if report.Status != want {
		var out bytes.Buffer
		_ = WriteReport(&out, report)
		t.Fatalf("status = %s, want %s\n%s", report.Status, want, out.String())
	}
}

func assertResidual(t *testing.T, report Report, key ResidualKey) {
	t.Helper()
	_ = findResidual(t, report, key)
}

func findResidual(t *testing.T, report Report, key ResidualKey) Residual {
	t.Helper()
	for _, axis := range report.Axes {
		for _, residual := range axis.Residuals {
			if residual.Key == key {
				return residual
			}
		}
	}
	var out bytes.Buffer
	_ = WriteReport(&out, report)
	t.Fatalf("missing residual %s\n%s", key.String(), out.String())
	return Residual{}
}

func assertContract(t *testing.T, report Report, contract Contract) {
	t.Helper()
	for _, axis := range report.Axes {
		for _, residual := range axis.Residuals {
			if residual.Key.Contract == contract {
				return
			}
		}
	}
	var out bytes.Buffer
	_ = WriteReport(&out, report)
	t.Fatalf("missing %s residual\n%s", contract, out.String())
}
