package mcpserver

import "testing"

// Rung-3 proofs for ADR-036.
//
// Every fact bound by docs/specs/2026-08-26-a-recall-that-answers.md is bound to
// a test in package palace, and no test in package palace can observe an MCP
// render site or a tool registration. A cold review found that gap: five of the
// ADR's tasks claim "mutation: delete the render line and the test goes red"
// while naming only palace tests, which would stay green because they call the
// service directly — exactly what a caller that was never wired also does.
//
// These are the tests that go red when the render or registration line is
// deleted. They belong beside catalog_test.go and hitview_test.go, which exist
// for the same reason.

// TestKGQueryResultRendersResolutionState is ADR-036 T2's rung-3 proof: the
// absence-vs-failure signal must reach the tool RESULT, not merely the Go
// struct. A field a handler sets and no renderer emits is invisible to every
// agent, and no behavioural test can see that.
func TestKGQueryResultRendersResolutionState(t *testing.T) {
	t.Fatal("ADR-036 T2 not implemented: am_kg_query's rendered result distinguishes an unresolved entity or predicate from a real empty match")
}

// TestSearchResultRendersFactsAndTheSiblingPointer is ADR-036 T3's rung-3 proof.
func TestSearchResultRendersFactsAndTheSiblingPointer(t *testing.T) {
	t.Fatal("ADR-036 T3 not implemented: am_search's rendered result carries the in-wing fact block, the derivable sibling wings, and the unlocatable count")
}

// TestSearchResultRendersTheCorrectionMark is ADR-036 T5's rung-3 proof.
func TestSearchResultRendersTheCorrectionMark(t *testing.T) {
	t.Fatal("ADR-036 T5 not implemented: a superseded record's correction edge and replacement id appear in the rendered hit")
}

// TestAddDrawerResultReportsItsEdge is ADR-036 T6's rung-3 proof. T6 promised
// this field while naming no mcpserver file at all.
func TestAddDrawerResultReportsItsEdge(t *testing.T) {
	t.Fatal("ADR-036 T6 not implemented: am_add_drawer's rendered result says whether the drawer has an edge and whether it was derived")
}

// TestEntryPointToolIsRegisteredAndDiscoverable is ADR-036 T7's rung-3 proof:
// the tool must appear in the catalogue with its arguments, not merely exist.
func TestEntryPointToolIsRegisteredAndDiscoverable(t *testing.T) {
	t.Fatal("ADR-036 T7 not implemented: the entry-point tool is registered and appears in the catalogue with its arguments")
}

// TestBootstrapToolIsRegisteredAndDiscoverable is ADR-036 T8's rung-3 proof. A
// bootstrap nobody can find is the 13-call protocol it was written to replace.
func TestBootstrapToolIsRegisteredAndDiscoverable(t *testing.T) {
	t.Fatal("ADR-036 T8 not implemented: the bootstrap tool is registered and appears in the catalogue with its arguments")
}
