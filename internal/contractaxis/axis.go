package contractaxis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Maturity states how an axis participates in a gate.
type Maturity string

const (
	// Enforced requires every residual to be closed or explicitly excepted.
	Enforced Maturity = "enforced"
	// Ratchet permits exactly the pre-registered residual keys and reports PARTIAL.
	Ratchet Maturity = "ratchet"
	// Advisory reports residuals without claiming that the axis is complete.
	Advisory Maturity = "advisory"
)

// Contract names one independently provable part of an axis item.
type Contract string

const (
	// UniverseContract means the production universe could not be enumerated honestly.
	UniverseContract Contract = "universe"
	// BindingContract means an item has no production selector or adapter binding.
	BindingContract Contract = "binding"
	// PositiveContract means selecting an item has no observed promised effect.
	PositiveContract Contract = "positive"
	// NegativeContract means the forbidden effect has not been observed absent.
	NegativeContract Contract = "negative"
	// ProbeContract means the observation itself failed before it could answer.
	ProbeContract Contract = "probe"
	// MutantContract means no compiling, killed, cleanly restored mutant protects the axis.
	MutantContract Contract = "mutant"
	// ExceptionContract means an exception is invalid, expired, or stale.
	ExceptionContract Contract = "exception"
	// RatchetContract means the actual residual identities differ from the ratchet.
	RatchetContract Contract = "ratchet"
)

// Observation records calls made while probing one universe item. The runner
// creates it so an adapter cannot substitute a precomputed passing value.
type Observation struct {
	bound    bool
	positive bool
	negative bool
}

// RecordBinding records that the production selector accepted the item.
func (o *Observation) RecordBinding() { o.bound = true }

// RecordPositive records that the promised outer effect was observed.
func (o *Observation) RecordPositive() { o.positive = true }

// RecordNegative records that the forbidden outer effect was observed absent.
func (o *Observation) RecordNegative() { o.negative = true }

// Probe observes one item through the production selector and outer surface.
type Probe func(ctx context.Context, item string, observation *Observation) error

// Universe enumerates the authoritative production item identifiers.
type Universe func(ctx context.Context) ([]string, error)

// MutantEvidence records one disposable mutation run.
type MutantEvidence struct {
	id        string
	assertion string
	applied   bool
	compiled  bool
	killed    bool
	restored  bool
	detail    string
}

// ID returns the mutation's stable identifier.
func (m MutantEvidence) ID() string { return m.id }

// Assertion returns the command that the mutant made fail.
func (m MutantEvidence) Assertion() string { return m.assertion }

// Verified reports whether the patch applied, compiled, was killed, and restored cleanly.
func (m MutantEvidence) Verified() bool {
	return m.applied && m.compiled && m.killed && m.restored && strings.TrimSpace(m.assertion) != ""
}

// Detail returns bounded diagnostic evidence from the mutation run.
func (m MutantEvidence) Detail() string { return m.detail }

// ExceptionKind classifies why a residual cannot currently be closed.
type ExceptionKind string

const (
	// ExternalDependency means the observation needs a named live dependency.
	ExternalDependency ExceptionKind = "external_dependency"
	// PolicyUndecided means product behaviour must be decided before it is tested.
	PolicyUndecided ExceptionKind = "policy_undecided"
	// UnsupportedPlatform means the item cannot run on the gate's platform.
	UnsupportedPlatform ExceptionKind = "unsupported_platform"
	// NonProduction means the item is derived but deliberately never ships.
	NonProduction ExceptionKind = "non_production"
)

// ResidualKey is the stable identity of one unmet contract.
type ResidualKey struct {
	Axis     string
	Item     string
	Contract Contract
}

// String returns the stable axis/item/contract identity used by ratchets.
func (k ResidualKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Axis, k.Item, k.Contract)
}

// Exception owns one residual until it can be closed.
type Exception struct {
	Key             ResidualKey
	Kind            ExceptionKind
	Owner           string
	Reason          string
	Reference       string
	Expires         time.Time
	PermanentReason string
}

// RatchetObligation owns one known residual while an axis is being closed.
type RatchetObligation struct {
	Key       ResidualKey
	Owner     string
	Reason    string
	Reference string
	Expires   time.Time
}

// Axis binds one derived universe to its production observations.
type Axis struct {
	ID         string
	Maturity   Maturity
	Universe   Universe
	Probe      Probe
	Mutants    []MutantEvidence
	Exceptions []Exception
	Ratchet    []RatchetObligation
}

// Residual describes one unmet contract and whether a valid exception owns it.
type Residual struct {
	Key        ResidualKey
	Detail     string
	Excepted   bool
	Exception  *Exception
	Obligation *RatchetObligation
}

// Status is the visible outcome of an axis evaluation.
type Status string

const (
	// Pass means no residual or exception remains.
	Pass Status = "PASS"
	// Partial means the gate may continue but the report is not complete.
	Partial Status = "PARTIAL"
	// Advice means the axis is diagnostic only and cannot claim enforcement.
	Advice Status = "ADVISORY"
	// Fail means the gate must stop.
	Fail Status = "FAIL"
)

// AxisReport is the deterministic result for one axis.
type AxisReport struct {
	Axis      string
	Maturity  Maturity
	Status    Status
	Universe  int
	Residuals []Residual
}

// Report is the complete, sorted result for every evaluated axis.
type Report struct {
	Status Status
	Axes   []AxisReport
}

// Complete reports whether every evaluated axis is fully enforced with no exception.
func (r Report) Complete() bool { return r.Status == Pass }

// Failed reports whether the result must fail a gate.
func (r Report) Failed() bool { return r.Status == Fail }

// Evaluate runs every axis and returns a stable complete report.
func Evaluate(ctx context.Context, now time.Time, axes ...Axis) Report {
	report := Report{Status: Pass}
	if len(axes) == 0 {
		report.Status = Fail
		report.Axes = []AxisReport{{
			Axis:     "<registry>",
			Maturity: Enforced,
			Status:   Fail,
			Residuals: []Residual{{
				Key:    ResidualKey{Axis: "<registry>", Item: "*", Contract: UniverseContract},
				Detail: "no contract axes are registered",
			}},
		}}
		return report
	}
	for _, axis := range axes {
		ar := evaluateAxis(ctx, now, axis)
		report.Axes = append(report.Axes, ar)
		report.Status = combineStatus(report.Status, ar.Status)
	}
	sort.Slice(report.Axes, func(i, j int) bool { return report.Axes[i].Axis < report.Axes[j].Axis })
	return report
}

func evaluateAxis(ctx context.Context, now time.Time, axis Axis) AxisReport {
	id := strings.TrimSpace(axis.ID)
	if id == "" {
		id = "<unnamed>"
	}
	ar := AxisReport{Axis: id, Maturity: axis.Maturity}
	residuals := map[ResidualKey]Residual{}
	add := func(item string, contract Contract, detail string) {
		key := ResidualKey{Axis: id, Item: item, Contract: contract}
		if _, exists := residuals[key]; !exists {
			residuals[key] = Residual{Key: key, Detail: detail}
		}
	}

	if axis.Maturity != Enforced && axis.Maturity != Ratchet && axis.Maturity != Advisory {
		add("*", UniverseContract, fmt.Sprintf("unknown maturity %q", axis.Maturity))
	}
	if axis.Universe == nil {
		add("*", UniverseContract, "universe callback is nil")
	} else {
		items, err := axis.Universe(ctx)
		if err != nil {
			add("*", UniverseContract, "enumerate universe: "+err.Error())
		} else {
			seen := map[string]bool{}
			for _, raw := range items {
				item := strings.TrimSpace(raw)
				if item == "" {
					add("<empty>", UniverseContract, "universe contains an empty item identifier")
					continue
				}
				if seen[item] {
					add(item, UniverseContract, "universe contains the item more than once")
					continue
				}
				seen[item] = true
			}
			ar.Universe = len(seen)
			if len(seen) == 0 {
				add("*", UniverseContract, "production universe is empty")
			}
			for _, item := range sortedKeys(seen) {
				if axis.Probe == nil {
					add(item, ProbeContract, "probe callback is nil")
					continue
				}
				observation := &Observation{}
				probeErr := axis.Probe(ctx, item, observation)
				if probeErr != nil {
					add(item, ProbeContract, probeErr.Error())
					continue
				}
				if !observation.bound {
					add(item, BindingContract, "item is not bound to a production selector")
				}
				if !observation.positive {
					add(item, PositiveContract, "promised outer effect was not observed")
				}
				if !observation.negative {
					add(item, NegativeContract, "forbidden outer effect was not observed absent")
				}
			}
		}
	}

	if len(axis.Mutants) == 0 {
		add("*", MutantContract, "axis has no mutation evidence")
	}
	for i, mutant := range axis.Mutants {
		item := strings.TrimSpace(mutant.id)
		if item == "" {
			item = fmt.Sprintf("<mutant-%d>", i+1)
		}
		var missing []string
		if strings.TrimSpace(mutant.assertion) == "" {
			missing = append(missing, "named assertion")
		}
		if !mutant.applied {
			missing = append(missing, "applied patch")
		}
		if !mutant.compiled {
			missing = append(missing, "compiling mutant")
		}
		if !mutant.killed {
			missing = append(missing, "killed assertion")
		}
		if !mutant.restored {
			missing = append(missing, "clean restoration")
		}
		if len(missing) > 0 {
			detail := "missing " + strings.Join(missing, ", ")
			if mutant.detail != "" {
				detail += ": " + mutant.detail
			}
			add(item, MutantContract, detail)
		}
	}

	applyExceptions(now, id, residuals, axis.Exceptions, add)
	applyRatchet(now, id, axis, residuals, add)
	ar.Residuals = residualSlice(residuals)
	ar.Status = statusFor(axis, ar.Residuals, add)
	if ar.Status == Fail && len(ar.Residuals) != len(residuals) {
		ar.Residuals = residualSlice(residuals)
	}
	return ar
}

func applyExceptions(now time.Time, axis string, residuals map[ResidualKey]Residual, exceptions []Exception, add func(string, Contract, string)) {
	base := residualKeys(residuals)
	var valid []int
	seen := map[ResidualKey]bool{}
	for i := range exceptions {
		ex := exceptions[i]
		problem := validateException(now, axis, ex)
		if problem == "" && seen[ex.Key] {
			problem = "exception is duplicated"
		}
		seen[ex.Key] = true
		_, exists := base[ex.Key]
		if problem == "" && !exists {
			problem = "exception is stale because its residual is absent"
		}
		if problem != "" {
			add(ex.Key.String(), ExceptionContract, problem)
			continue
		}
		valid = append(valid, i)
	}
	for _, i := range valid {
		ex := exceptions[i]
		residual := residuals[ex.Key]
		residual.Excepted = true
		residual.Exception = &exceptions[i]
		residuals[ex.Key] = residual
	}
}

func validateException(now time.Time, axis string, ex Exception) string {
	if ex.Key.Axis != axis || strings.TrimSpace(ex.Key.Item) == "" || ex.Key.Contract == "" {
		return "exception key does not name a residual on this axis"
	}
	if ex.Key.Contract == ExceptionContract || ex.Key.Contract == RatchetContract {
		return "exception cannot target validation residuals"
	}
	switch ex.Kind {
	case ExternalDependency, PolicyUndecided, UnsupportedPlatform, NonProduction:
	default:
		return fmt.Sprintf("unknown exception kind %q", ex.Kind)
	}
	if strings.TrimSpace(ex.Owner) == "" {
		return "exception has no owner"
	}
	if strings.TrimSpace(ex.Reason) == "" {
		return "exception has no concrete reason"
	}
	if strings.TrimSpace(ex.Reference) == "" {
		return "exception has no issue or ADR reference"
	}
	hasExpiry := !ex.Expires.IsZero()
	hasPermanent := strings.TrimSpace(ex.PermanentReason) != ""
	if hasExpiry == hasPermanent {
		return "exception must have exactly one expiry or permanent rationale"
	}
	if hasExpiry && !ex.Expires.After(now) {
		return "exception is expired"
	}
	return ""
}

func applyRatchet(now time.Time, axis string, definition Axis, residuals map[ResidualKey]Residual, add func(string, Contract, string)) {
	if definition.Maturity != Ratchet {
		if len(definition.Ratchet) > 0 {
			add("*", RatchetContract, "ratchet obligations require ratchet maturity")
		}
		return
	}
	if len(definition.Exceptions) > 0 {
		add("*", RatchetContract, "ratchet axes cannot also declare exceptions")
	}
	if len(definition.Ratchet) == 0 {
		add("*", RatchetContract, "ratchet has no owned residual obligations")
		return
	}

	base := residualKeys(residuals)
	seen := map[ResidualKey]bool{}
	var valid []int
	for i := range definition.Ratchet {
		obligation := definition.Ratchet[i]
		problem := validateRatchetObligation(now, axis, obligation)
		if problem == "" && seen[obligation.Key] {
			problem = "ratchet obligation is duplicated"
		}
		if problem == "" {
			seen[obligation.Key] = true
			if _, exists := base[obligation.Key]; !exists {
				problem = "ratchet obligation is stale because its residual is absent"
			}
		}
		if problem != "" {
			add(obligation.Key.String(), RatchetContract, problem)
			continue
		}
		valid = append(valid, i)
	}
	for _, i := range valid {
		obligation := definition.Ratchet[i]
		residual := residuals[obligation.Key]
		residual.Obligation = &definition.Ratchet[i]
		residuals[obligation.Key] = residual
	}
}

func validateRatchetObligation(now time.Time, axis string, obligation RatchetObligation) string {
	if obligation.Key.Axis != axis || strings.TrimSpace(obligation.Key.Item) == "" || obligation.Key.Contract == "" {
		return "ratchet key does not name a residual on this axis"
	}
	if obligation.Key.Contract == ExceptionContract || obligation.Key.Contract == RatchetContract {
		return "ratchet cannot own validation residuals"
	}
	if strings.TrimSpace(obligation.Owner) == "" {
		return "ratchet obligation has no owner"
	}
	if strings.TrimSpace(obligation.Reason) == "" {
		return "ratchet obligation has no concrete reason"
	}
	if strings.TrimSpace(obligation.Reference) == "" {
		return "ratchet obligation has no issue or ADR reference"
	}
	if obligation.Expires.IsZero() || !obligation.Expires.After(now) {
		return "ratchet obligation has no future expiry"
	}
	return ""
}

func statusFor(axis Axis, residuals []Residual, add func(string, Contract, string)) Status {
	var open []ResidualKey
	var excepted bool
	for _, residual := range residuals {
		if residual.Excepted {
			excepted = true
			continue
		}
		open = append(open, residual.Key)
	}
	sortResidualKeys(open)

	switch axis.Maturity {
	case Advisory:
		for _, key := range open {
			switch key.Contract {
			case UniverseContract, ProbeContract, ExceptionContract, RatchetContract:
				return Fail
			}
		}
		return Advice
	case Ratchet:
		for _, key := range open {
			if key.Contract == ExceptionContract || key.Contract == RatchetContract {
				return Fail
			}
		}
		expected := make([]ResidualKey, 0, len(axis.Ratchet))
		for _, obligation := range axis.Ratchet {
			expected = append(expected, obligation.Key)
		}
		sortResidualKeys(expected)
		actual := make([]ResidualKey, 0, len(open))
		for _, key := range open {
			if key.Contract != ExceptionContract && key.Contract != RatchetContract {
				actual = append(actual, key)
			}
		}
		if len(expected) == 0 || !sameResidualKeys(actual, expected) {
			add("*", RatchetContract, fmt.Sprintf("actual residuals %v differ from expected %v", keyStrings(actual), keyStrings(expected)))
			return Fail
		}
		return Partial
	case Enforced:
		if len(open) > 0 {
			return Fail
		}
		if excepted {
			return Partial
		}
		return Pass
	default:
		return Fail
	}
}

func residualSlice(residuals map[ResidualKey]Residual) []Residual {
	out := make([]Residual, 0, len(residuals))
	for _, residual := range residuals {
		out = append(out, residual)
	}
	sort.Slice(out, func(i, j int) bool { return lessResidualKey(out[i].Key, out[j].Key) })
	return out
}

func residualKeys(residuals map[ResidualKey]Residual) map[ResidualKey]bool {
	keys := make(map[ResidualKey]bool, len(residuals))
	for key := range residuals {
		keys[key] = true
	}
	return keys
}

func sortedKeys(items map[string]bool) []string {
	keys := make([]string, 0, len(items))
	for item := range items {
		keys = append(keys, item)
	}
	sort.Strings(keys)
	return keys
}

func sortResidualKeys(keys []ResidualKey) {
	sort.Slice(keys, func(i, j int) bool { return lessResidualKey(keys[i], keys[j]) })
}

func lessResidualKey(a, b ResidualKey) bool {
	if a.Axis != b.Axis {
		return a.Axis < b.Axis
	}
	if a.Item != b.Item {
		return a.Item < b.Item
	}
	return a.Contract < b.Contract
}

func sameResidualKeys(a, b []ResidualKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func keyStrings(keys []ResidualKey) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key.String())
	}
	return out
}

func combineStatus(a, b Status) Status {
	rank := map[Status]int{Pass: 0, Advice: 1, Partial: 2, Fail: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
