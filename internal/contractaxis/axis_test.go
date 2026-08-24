package contractaxis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)

func TestNoRegisteredAxesFailsInsteadOfPassingVacuously(t *testing.T) {
	report := Evaluate(context.Background(), testNow)

	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "<registry>", Item: "*", Case: "*", Contract: UniverseContract})
}

func TestAnEmptyUniverseFailsInsteadOfPassingVacuously(t *testing.T) {
	axis := completeAxis("empty")
	axis.Universe = func(context.Context) ([]string, error) { return nil, nil }

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "empty", Item: "*", Case: "*", Contract: UniverseContract})
}

func TestEveryItemRequiresAnExplicitNonEmptyCaseUniverse(t *testing.T) {
	tests := map[string]Cases{
		"nil":       nil,
		"empty":     func(context.Context, string) ([]string, error) { return nil, nil },
		"blank":     func(context.Context, string) ([]string, error) { return []string{" "}, nil },
		"duplicate": func(context.Context, string) ([]string, error) { return []string{"http", "http"}, nil },
		"reserved":  func(context.Context, string) ([]string, error) { return []string{"*"}, nil },
		"error": func(context.Context, string) ([]string, error) {
			return nil, errors.New("case registry unavailable")
		},
	}
	for name, cases := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("cases")
			axis.Cases = cases

			report := Evaluate(context.Background(), testNow, axis)
			assertStatus(t, report, Fail)
			assertContract(t, report, UniverseContract)
		})
	}
}

func TestEveryNamedCaseGetsAFreshObservation(t *testing.T) {
	axis := completeAxis("paths")
	called := map[string]bool{}
	observations := map[*Observation]bool{}
	axis.Cases = func(context.Context, string) ([]string, error) {
		return []string{"http", "cli"}, nil
	}
	axis.Probe = func(_ context.Context, _, caseID string, observation *Observation) error {
		called[caseID] = true
		if observations[observation] {
			t.Fatalf("observation was reused for case %q", caseID)
		}
		observations[observation] = true
		if caseID == "http" {
			observation.RecordBinding()
			observation.RecordPositive()
			observation.RecordNegative()
		}
		return nil
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	if got := report.Axes[0].Cases; got != 2 {
		t.Fatalf("case count = %d, want 2", got)
	}
	if len(called) != 2 || !called["http"] || !called["cli"] {
		t.Fatalf("probed cases = %v", called)
	}
	for _, contract := range []Contract{BindingContract, PositiveContract, NegativeContract} {
		assertResidual(t, report, ResidualKey{Axis: "paths", Item: "item", Case: "cli", Contract: contract})
	}
}

func TestAxisIdentifiersMustBeNonEmptyAndUnique(t *testing.T) {
	for name, axes := range map[string][]Axis{
		"empty":     {completeAxis(" ")},
		"reserved":  {completeAxis("<registry>")},
		"duplicate": {completeAxis("same"), completeAxis("same")},
	} {
		t.Run(name, func(t *testing.T) {
			report := Evaluate(context.Background(), testNow, axes...)
			assertStatus(t, report, Fail)
			if len(report.Axes) != 1 || report.Axes[0].Axis != "<registry>" {
				t.Fatalf("invalid registry report = %+v", report)
			}
		})
	}
}

func TestUniverseItemIdentifiersCannotUseStructuralSentinels(t *testing.T) {
	axis := completeAxis("items")
	axis.Universe = func(context.Context) ([]string, error) { return []string{"*"}, nil }

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "items", Item: "*", Case: "*", Contract: UniverseContract})
}

func TestResidualKeyEscapesPathSegments(t *testing.T) {
	key := ResidualKey{Axis: "axis/name", Item: "item%name", Case: "case/name", Contract: PositiveContract}
	if got, want := key.String(), "axis%2Fname/item%25name/case%2Fname/positive"; got != want {
		t.Fatalf("residual key = %q, want %q", got, want)
	}
}

func TestAUniverseItemNeedsBindingAndBothObservations(t *testing.T) {
	axis := completeAxis("tools")
	axis.Universe = func(context.Context) ([]string, error) { return []string{"am_new"}, nil }
	axis.Probe = func(context.Context, string, string, *Observation) error {
		return nil
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	for _, contract := range []Contract{BindingContract, PositiveContract, NegativeContract} {
		assertResidual(t, report, ResidualKey{Axis: "tools", Item: "am_new", Case: "default", Contract: contract})
	}
}

func TestClaimedCoverageCannotPassWithoutRecordedCalls(t *testing.T) {
	claimed := map[string]bool{"item": true}
	axis := completeAxis("claims")
	axis.Probe = func(_ context.Context, item, _ string, _ *Observation) error {
		if !claimed[item] {
			return errors.New("missing claim")
		}
		return nil
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	for _, contract := range []Contract{BindingContract, PositiveContract, NegativeContract} {
		assertResidual(t, report, ResidualKey{Axis: "claims", Item: "item", Case: "default", Contract: contract})
	}
}

func TestAProbeErrorDoesNotBecomeNegativeEvidence(t *testing.T) {
	axis := completeAxis("probe")
	axis.Probe = func(context.Context, string, string, *Observation) error {
		return errors.New("outer server did not start")
	}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "probe", Item: "item", Case: "default", Contract: ProbeContract})
}

func TestAValidExceptionOwnsButDoesNotHideAResidual(t *testing.T) {
	axis := completeAxis("external")
	axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
		observation.RecordBinding()
		observation.RecordPositive()
		return nil
	}
	axis.Exceptions = []Exception{{
		Key:       ResidualKey{Axis: "external", Item: "item", Case: "default", Contract: NegativeContract},
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

func TestInvalidExceptionDoesNotPoisonLaterValidSameKey(t *testing.T) {
	axis := completeAxis("poison")
	axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
		observation.RecordBinding()
		observation.RecordPositive()
		return nil
	}
	key := ResidualKey{Axis: "poison", Item: "item", Case: "default", Contract: NegativeContract}
	valid := Exception{
		Key: key, Kind: ExternalDependency, Owner: "storage",
		Reason:    "the refusal is visible only against a live Qdrant namespace",
		Reference: "issue-123", Expires: testNow.Add(24 * time.Hour),
	}
	invalid := Exception{
		Key: key, Kind: ExternalDependency, Owner: "",
		Reason: "missing owner", Reference: "issue-123", Expires: testNow.Add(24 * time.Hour),
	}
	axis.Exceptions = []Exception{invalid, valid}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	residual := findResidual(t, report, key)
	if !residual.Excepted {
		t.Fatalf("valid exception after an invalid same-key row was not applied: %+v", residual)
	}
	assertContract(t, report, ExceptionContract)
}

func TestAStaleExceptionFails(t *testing.T) {
	axis := completeAxis("stale")
	axis.Exceptions = []Exception{{
		Key:             ResidualKey{Axis: "stale", Item: "item", Case: "default", Contract: NegativeContract},
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
			Key: ResidualKey{Axis: "bad", Item: "item", Case: "default", Contract: NegativeContract}, Kind: ExternalDependency,
			Reason: "live service", Reference: "issue-1", Expires: testNow.Add(time.Hour),
		},
		"expired": {
			Key: ResidualKey{Axis: "bad", Item: "item", Case: "default", Contract: NegativeContract}, Kind: ExternalDependency,
			Owner: "ops", Reason: "live service", Reference: "issue-1", Expires: testNow.Add(-time.Hour),
		},
		"unknown kind": {
			Key: ResidualKey{Axis: "bad", Item: "item", Case: "default", Contract: NegativeContract}, Kind: "awkward",
			Owner: "ops", Reason: "live service", Reference: "issue-1", Expires: testNow.Add(time.Hour),
		},
		"two lifetimes": {
			Key: ResidualKey{Axis: "bad", Item: "item", Case: "default", Contract: NegativeContract}, Kind: ExternalDependency,
			Owner: "ops", Reason: "live service", Reference: "issue-1", Expires: testNow.Add(time.Hour), PermanentReason: "forever",
		},
	}

	for name, exception := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("bad")
			axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
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
	axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
		observation.RecordBinding()
		observation.RecordPositive()
		return nil
	}
	invalidKey := ResidualKey{Axis: "nested", Item: "item", Case: "default", Contract: NegativeContract}
	syntheticKey := ResidualKey{Axis: "nested", Item: invalidKey.String(), Case: "*", Contract: ExceptionContract}
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

func TestInstrumentFailuresCannotBeExcepted(t *testing.T) {
	tests := map[string]func(*Axis) ResidualKey{
		"universe": func(axis *Axis) ResidualKey {
			axis.Universe = func(context.Context) ([]string, error) { return nil, nil }
			return ResidualKey{Axis: axis.ID, Item: "*", Case: "*", Contract: UniverseContract}
		},
		"probe": func(axis *Axis) ResidualKey {
			axis.Probe = func(context.Context, string, string, *Observation) error {
				return errors.New("outer surface unavailable")
			}
			return ResidualKey{Axis: axis.ID, Item: "item", Case: "default", Contract: ProbeContract}
		},
	}
	for name, breakInstrument := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("instrument")
			key := breakInstrument(&axis)
			axis.Exceptions = []Exception{{
				Key: key, Kind: ExternalDependency, Owner: "test-infra", Reason: "attempted mask",
				Reference: "ADR-024", Expires: testNow.Add(time.Hour),
			}}

			report := Evaluate(context.Background(), testNow, axis)
			assertStatus(t, report, Fail)
			assertResidual(t, report, key)
			assertContract(t, report, ExceptionContract)
		})
	}
}

func TestMutationExceptionsAreOnlyForUnsupportedPlatforms(t *testing.T) {
	key := ResidualKey{Axis: "platform", Item: "*", Case: "*", Contract: MutantContract}
	for name, kind := range map[string]ExceptionKind{
		"unsupported": UnsupportedPlatform,
		"external":    ExternalDependency,
	} {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("platform")
			axis.Mutants = nil
			axis.MutationError = fmt.Errorf("%w: fixture platform", ErrMutationUnsupported)
			axis.Exceptions = []Exception{{
				Key: key, Kind: kind, Owner: "contract-axis", Reason: "native containment is unavailable",
				Reference: "ADR-024", Expires: testNow.Add(time.Hour),
			}}

			report := Evaluate(context.Background(), testNow, axis)
			if kind == UnsupportedPlatform {
				assertStatus(t, report, Partial)
				return
			}
			assertStatus(t, report, Fail)
			assertContract(t, report, ExceptionContract)
		})
	}
}

func TestRatchetsCannotOwnInstrumentFailures(t *testing.T) {
	axis := completeAxis("broken-ratchet")
	axis.Maturity = Ratchet
	axis.Universe = func(context.Context) ([]string, error) { return nil, nil }
	axis.Ratchet = []RatchetObligation{testRatchet(ResidualKey{
		Axis: "broken-ratchet", Item: "*", Case: "*", Contract: UniverseContract,
	})}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertContract(t, report, RatchetContract)
}

func TestARatchetNamesTheExactResidualsAndNeverLooksComplete(t *testing.T) {
	negative := ResidualKey{Axis: "ratchet", Item: "item", Case: "default", Contract: NegativeContract}
	axis := completeAxis("ratchet")
	axis.Maturity = Ratchet
	axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
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
	negative := ResidualKey{Axis: "ratchet", Item: "item", Case: "default", Contract: NegativeContract}
	tests := map[string]func(*RatchetObligation){
		"ownerless": func(obligation *RatchetObligation) { obligation.Owner = "" },
		"expired":   func(obligation *RatchetObligation) { obligation.Expires = testNow.Add(-time.Minute) },
		"stale":     func(obligation *RatchetObligation) { obligation.Key.Item = "absent" },
	}
	for name, breakObligation := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("ratchet")
			axis.Maturity = Ratchet
			axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
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
	axis.Ratchet = []RatchetObligation{testRatchet(ResidualKey{Axis: "ratchet", Item: "item", Case: "default", Contract: NegativeContract})}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertContract(t, report, RatchetContract)
}

func TestARatchetFailsOnARegressionHiddenBesideKnownDebt(t *testing.T) {
	axis := completeAxis("ratchet")
	axis.Maturity = Ratchet
	axis.Probe = func(_ context.Context, _, _ string, observation *Observation) error {
		observation.RecordPositive()
		return nil
	}
	axis.Ratchet = []RatchetObligation{testRatchet(ResidualKey{Axis: "ratchet", Item: "item", Case: "default", Contract: NegativeContract})}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{Axis: "ratchet", Item: "item", Case: "default", Contract: BindingContract})
	assertContract(t, report, RatchetContract)
}

func TestAdvisoryEvidenceMayBeIncompleteButTheInstrumentMustWork(t *testing.T) {
	axis := completeAxis("advice")
	axis.Maturity = Advisory
	axis.Probe = func(context.Context, string, string, *Observation) error { return nil }
	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Advice)

	axis.Universe = func(context.Context) ([]string, error) { return nil, nil }
	report = Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
}

func TestMutationEvidenceNeedsEveryStep(t *testing.T) {
	tests := map[string]func(*MutantEvidence){
		"axis":             func(m *MutantEvidence) { m.axis = "other" },
		"target":           func(m *MutantEvidence) { m.target.head = "other" },
		"patch provenance": func(m *MutantEvidence) { m.patchDigest = "" },
		"assertion":        func(m *MutantEvidence) { m.assertion = "" },
		"apply":            func(m *MutantEvidence) { m.applied = false },
		"compile":          func(m *MutantEvidence) { m.compiled = false },
		"kill":             func(m *MutantEvidence) { m.killed = false },
		"restore":          func(m *MutantEvidence) { m.restored = false },
	}
	for name, breakEvidence := range tests {
		t.Run(name, func(t *testing.T) {
			axis := completeAxis("mutant")
			broken := goodMutant("mutant")
			breakEvidence(&broken)
			axis.Mutants = []MutantEvidence{broken}
			report := Evaluate(context.Background(), testNow, axis)
			assertStatus(t, report, Fail)
			assertContract(t, report, MutantContract)
		})
	}
}

func TestAnAxisWideSelectorMutantIsRequired(t *testing.T) {
	axis := completeAxis("mutant-scope")
	itemMutant := goodMutant("mutant-scope")
	itemMutant.item = "item"
	itemMutant.caseID = "default"
	axis.Mutants = []MutantEvidence{itemMutant}

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{
		Axis: "mutant-scope", Item: "*", Case: "*", Contract: MutantContract,
	})
}

func TestMutationIdentifiersMustBeUnique(t *testing.T) {
	axis := completeAxis("mutant-ids")
	axis.Mutants = append(axis.Mutants, goodMutant("mutant-ids"))

	report := Evaluate(context.Background(), testNow, axis)
	assertStatus(t, report, Fail)
	assertResidual(t, report, ResidualKey{
		Axis: "mutant-ids", Item: "wire-cut", Case: "*", Contract: MutantContract,
	})
}

func TestReportOrderAndLabelsAreStable(t *testing.T) {
	b := completeAxis("b")
	b.Probe = func(context.Context, string, string, *Observation) error { return nil }
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
	if !strings.Contains(text, "OPEN b/item/default/binding") {
		t.Fatalf("residual identity missing from report:\n%s", text)
	}
	for _, want := range []string{
		"MUTANT wire-cut VERIFIED axis=a item=* case=*",
		`repo="/repo"`,
		`head=0123456789abcdef patch=abcdef paths=["selector.go"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("successful mutation provenance omitted %q:\n%s", want, text)
		}
	}
}

func TestWriteReportDoesNotVerifyMutantFromAnotherAxis(t *testing.T) {
	wrongAxis := goodMutant("other")
	wrongHead := goodMutant("a")
	wrongHead.target = MutationTarget{repository: "/repo", head: "deadbeef"}
	report := Report{Status: Fail, Axes: []AxisReport{{
		Axis: "a", Status: Fail, Maturity: Enforced,
		MutationTarget: testMutationTarget,
		Mutants:        []MutantEvidence{wrongAxis, wrongHead},
	}}}
	var out bytes.Buffer
	if err := WriteReport(&out, report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "VERIFIED") {
		t.Fatalf("report printed VERIFIED for a mutant evaluateAxis would reject:\n%s", out.String())
	}
	if strings.Count(out.String(), "INVALID") != 2 {
		t.Fatalf("want both mutants INVALID:\n%s", out.String())
	}
}

func TestWriteReportDefensivelySortsByAxisItemCaseAndContract(t *testing.T) {
	report := Report{Status: Fail, Axes: []AxisReport{
		{Axis: "b", Status: Pass, Maturity: Enforced},
		{
			Axis: "a", Status: Fail, Maturity: Enforced,
			Residuals: []Residual{
				{Key: ResidualKey{Axis: "a", Item: "a/b", Case: "c/d", Contract: Contract("a")}, Detail: "second"},
				{Key: ResidualKey{Axis: "a", Item: "a", Case: "c", Contract: Contract("z")}, Detail: "first"},
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
	first := strings.Index(text, "OPEN a/a/c/z")
	second := strings.Index(text, "OPEN a/a%2Fb/c%2Fd/a")
	if axisA < 0 || axisB < 0 || axisA > axisB || first < 0 || second < 0 || first > second {
		t.Fatalf("report is not sorted by (axis, item, case, contract):\n%s", text)
	}
}

func completeAxis(id string) Axis {
	return Axis{
		ID:       id,
		Maturity: Enforced,
		Universe: func(context.Context) ([]string, error) { return []string{"item"}, nil },
		Cases:    func(context.Context, string) ([]string, error) { return []string{"default"}, nil },
		Probe: func(_ context.Context, _, _ string, observation *Observation) error {
			observation.RecordBinding()
			observation.RecordPositive()
			observation.RecordNegative()
			return nil
		},
		MutationTarget: testMutationTarget,
		Mutants:        []MutantEvidence{goodMutant(id)},
	}
}

var testMutationTarget = MutationTarget{repository: "/repo", head: "0123456789abcdef"}

func goodMutant(axis string) MutantEvidence {
	return MutantEvidence{
		id: "wire-cut", axis: axis, item: "*", caseID: "*", target: testMutationTarget,
		patchDigest: "abcdef", paths: []string{"selector.go"},
		compile:         commandString(Command{Name: "go", Args: []string{"test", "./...", "-run", "^$"}}),
		assertion:       commandString(Command{Name: "go", Args: []string{"test", "./...", "-run", "TestProductionPath"}}),
		expectedFailure: "production selector disconnected",
		applied:         true, compiled: true, killed: true, restored: true,
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
