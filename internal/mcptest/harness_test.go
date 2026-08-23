package mcptest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestHarnessObservesAWriteThroughARead is the harness proving itself.
//
// Measured 2026-08-20: 41 tools registered, 39 named in no test file, and zero
// tests that drive a tool handler at all. Every property the product rests on —
// a write is readable, a delete removes, a scope holds — rested on the code
// looking right, which is the same evidence the four capabilities this repo
// shipped unreachable also had.
//
// The round trip is the point. Asserting that am_add_drawer returned no error
// proves the handler was reached and nothing else; three defects found by hand
// this week (a chunk-0-only update, a delete that orphaned child chunks, an
// anchor list that cleared on malformed input) all reported success and were
// wrong about what they had done. Only a second call can see that.
func TestHarnessObservesAWriteThroughARead(t *testing.T) {
	h := mcptest.New(t)

	h.MustCall(t, "am_add_drawer", map[string]any{
		"wing": "wing_harness", "room": "decisions",
		"content": "the harness drives the transport an agent uses, not the handler directly",
	})

	got := h.MustCall(t, "am_search", map[string]any{
		"query": "harness drives the transport", "wing": "wing_harness", "limit": 5,
	})
	if !strings.Contains(got, "the harness drives the transport") {
		t.Fatalf("a write was not observable through a read — the harness cannot see effects, so "+
			"every scenario built on it would pass vacuously:\n%s", got)
	}
}

// TestHarnessFailsOnAnEmptyCatalogue: a harness that stands up a server with no
// tools would let every later scenario "pass" by calling nothing. The harness
// must refuse to hand out a client in that state rather than be trusted not to
// reach it.
func TestHarnessFailsOnAnEmptyCatalogue(t *testing.T) {
	tools, err := mcptest.New(t).ListTools(t)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("the harness served an empty catalogue")
	}
	for _, want := range []string{"am_status", "am_add_drawer", "am_search"} {
		if !containsName(tools, want) {
			t.Errorf("catalogue is missing %s, so the harness is not serving the real registry", want)
		}
	}
}

// TestCatalogueGuardRejectsAnEmptyServer drives the guard itself.
//
// TestHarnessFailsOnAnEmptyCatalogue asserts on what a real server returns and
// therefore passes whether or not the guard exists — measured: disarming the
// guard left the whole package green, because no test can produce a toolless
// server to trip it. The rule is only falsifiable once it takes the list as an
// argument.
func TestCatalogueGuardRejectsAnEmptyServer(t *testing.T) {
	if err := mcptest.UsableCatalogue(nil, nil); err == nil {
		t.Error("an empty catalogue was accepted — every scenario would then pass by calling nothing")
	}
	if err := mcptest.UsableCatalogue(nil, errors.New("transport down")); err == nil {
		t.Error("a failed listing was accepted as a usable catalogue")
	}
	if err := mcptest.UsableCatalogue([]string{"am_status"}, nil); err != nil {
		t.Errorf("a non-empty catalogue was rejected: %v", err)
	}
}

func containsName(tools []string, want string) bool {
	for _, n := range tools {
		if n == want {
			return true
		}
	}
	return false
}
