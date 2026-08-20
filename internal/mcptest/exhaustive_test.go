package mcptest_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestEveryToolIsExercisedEndToEnd is the gate this ADR exists for.
//
// Measured 2026-08-20 before any of this landed: 41 tools registered, 39 named
// in no test file, and no test anywhere that drove a handler. The catalogue
// gates already in the tree prove the count is honest and the names do not
// collide; neither proves a tool works when called.
//
// Two properties make it hard to satisfy dishonestly:
//
// The tool list comes from the RUNNING server, so a tool added without a
// scenario fails without anyone remembering to update a list. And coverage is
// counted from the calls a scenario actually MADE — the harness records them —
// so a scenario cannot claim a tool it never invoked.
func TestEveryToolIsExercisedEndToEnd(t *testing.T) {
	registered, err := mcptest.New(t).ListTools(t)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(registered) == 0 {
		t.Fatal("the running server advertises no tools — this check has stopped checking anything")
	}

	covered := map[string]bool{}
	for _, sc := range scenarios {
		for _, name := range runScenario(t, sc) {
			covered[name] = true
		}
	}
	exempt := map[string]string{}
	for _, u := range unobservable {
		exempt[u.Tool] = u.Needs
	}

	var missing []string
	for _, name := range registered {
		if covered[name] || exempt[name] != "" {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)

	// A RATCHET, not a skip, and not a permanent red.
	//
	// The honest state today is 38 of 41 uncovered, and asserting zero would put
	// the suite red for every unrelated task until T3 lands — at which point
	// somebody deletes the gate rather than the gap. Skipping it is worse: a
	// skipped gate is decoration that reads as coverage.
	//
	// So the count is pinned EXACTLY. It fails if coverage regresses, and it also
	// fails when coverage improves without the number being lowered — otherwise
	// the ceiling drifts upward silently and the ratchet stops ratcheting. Every
	// scenario added takes this number down; ADR-008 T3 takes it to 0, and the
	// constant is deleted with the last one.
	const uncoveredCeiling = 38

	switch {
	case len(missing) > uncoveredCeiling:
		t.Errorf("coverage REGRESSED: %d of %d registered tool(s) are exercised by no scenario "+
			"and named in no exemption, up from %d:\n  %s\n\nA tool nobody calls end to end has "+
			"only the evidence that the code looks right — the same evidence the four unreachable "+
			"capabilities in this repo had.",
			len(missing), len(registered), uncoveredCeiling, strings.Join(missing, "\n  "))
	case len(missing) < uncoveredCeiling:
		t.Errorf("coverage improved to %d uncovered (ceiling says %d) — lower uncoveredCeiling to "+
			"%d in the same commit, or the ratchet stops ratcheting and the next regression hides "+
			"under the old headroom.", len(missing), uncoveredCeiling, len(missing))
	default:
		t.Logf("%d of %d tool(s) still exercised by no scenario:\n  %s",
			len(missing), len(registered), strings.Join(missing, "\n  "))
	}
}

// TestScenariosObserveAnEffect: a scenario that makes one call proves the handler
// was reached and nothing else. Every defect found by hand this week reported
// success and was wrong about what it had done — a chunk-0-only update, a delete
// that orphaned child chunks, an anchor list that cleared instead of refusing.
// Only a second, observing call can see that, so a one-call scenario is rejected
// structurally rather than by review.
func TestScenariosObserveAnEffect(t *testing.T) {
	for _, sc := range scenarios {
		calls := runScenario(t, sc)
		if len(calls) < 2 {
			t.Errorf("scenario %q made %d call(s). A scenario must act and then OBSERVE, or it "+
				"asserts only that a call returned", sc.Name, len(calls))
		}
	}
}

// TestScenariosOnlyClaimToolsTheyCall stops the Tools field from becoming a list
// kept beside the truth — the failure mode this repo hit twice this week, once
// with an exclusion list keyed by arm name and once with a gate that scanned only
// const declarations.
func TestScenariosOnlyClaimToolsTheyCall(t *testing.T) {
	for _, sc := range scenarios {
		actual := map[string]bool{}
		for _, name := range runScenario(t, sc) {
			actual[name] = true
		}
		for _, claimed := range sc.Tools {
			if !actual[claimed] {
				t.Errorf("scenario %q claims to exercise %s and never calls it", sc.Name, claimed)
			}
		}
	}
}

// TestUnobservableListNamesADependency keeps the exemption list from becoming a
// parking space. "Hard to test" is a reason to write a scenario; "needs a live
// Qdrant" is a reason a scenario cannot exist here.
func TestUnobservableListNamesADependency(t *testing.T) {
	allowed := []string{"qdrant", "tei", "oauth", "ollama", "reranker", "network"}
	for _, u := range unobservable {
		if strings.TrimSpace(u.Needs) == "" {
			t.Errorf("%s is exempt with no dependency named", u.Tool)
			continue
		}
		ok := false
		for _, a := range allowed {
			if strings.Contains(strings.ToLower(u.Needs), a) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s is exempt for %q, which names no external dependency — that is a reason "+
				"to write a scenario, not a reason one cannot exist", u.Tool, u.Needs)
		}
		if strings.TrimSpace(u.Why) == "" {
			t.Errorf("%s is exempt with no explanation", u.Tool)
		}
	}
}

// runScenario runs one scenario against a fresh harness and returns the tools it
// actually called.
func runScenario(t *testing.T, sc mcptest.Scenario) []string {
	t.Helper()
	h := mcptest.New(t)
	sc.Run(t, h)
	return h.Called()
}
