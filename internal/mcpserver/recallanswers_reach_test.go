package mcpserver

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

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
	// A SOURCE check, deliberately. Rung 3 asks whether the intended caller can
	// DISCOVER the field, and only a source or schema check can answer that: a
	// behavioural test that reads the value passes whether or not the handler
	// emits it, because the test can reach the struct directly. That is exactly
	// what a caller which was never wired also does.
	keys := renderedKeysOf(t, "kg.go", "kg_query")
	for _, want := range []string{"resolution", "unresolved"} {
		if !keys[want] {
			t.Errorf("am_kg_query's rendered result has no %q key; the state is set on the Go struct and never reaches an agent", want)
		}
	}
}

// renderedKeysOf parses an mcpserver file and returns the string keys assigned
// into map[string]any results within the named tool's handler region.
//
// It is deliberately loose about WHERE in the handler a key is set — some are set
// in the literal and some conditionally afterwards — because the question is only
// "does this key ever reach the wire", and tightening it to the literal alone
// would report a conditionally-added key as missing.
func renderedKeysOf(t *testing.T, file, tool string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := string(src)
	i := strings.Index(body, strconv.Quote(tool))
	if i < 0 {
		t.Fatalf("%s: tool %q not found — this check has stopped checking anything", file, tool)
	}
	// Bound the region at the next tool registration so keys from a neighbouring
	// handler cannot satisfy this one.
	rest := body[i+len(tool):]
	if j := strings.Index(rest, "newTool("); j >= 0 {
		rest = rest[:j]
	}
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z_]+)":`).FindAllStringSubmatch(rest, -1) {
		keys[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`out\["([a-z_]+)"\]`).FindAllStringSubmatch(rest, -1) {
		keys[m[1]] = true
	}
	if len(keys) == 0 {
		t.Fatalf("%s: no rendered keys found for %q", file, tool)
	}
	return keys
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
