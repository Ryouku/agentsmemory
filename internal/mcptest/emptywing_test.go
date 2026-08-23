package mcptest_test

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
)

// TestScenarioAnEmptyWingSaysSoOnTheWire drives the real handler, because the
// unit test for emptyWingNote passes whether or not anything attaches its output
// to the response. Removing the attachment from the handler left every unit test
// green — the function was correct and reached nothing, which is this
// repository's signature defect.
func TestScenarioAnEmptyWingSaysSoOnTheWire(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_real")
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "a memory that exists in the real wing",
	})

	// A wing nobody has ever filed into.
	out := h.MustCall(t, "am_search", map[string]any{
		"query": "anything at all", "wing": "wing_that_never_existed",
	})
	if !strings.Contains(out, `"count":0`) {
		t.Fatalf("expected an empty page from a wing that holds nothing: %s", out)
	}
	if !strings.Contains(out, "note") {
		t.Errorf("an empty page from a NON-EXISTENT wing carried no note:\n  %s\n"+
			"  It is byte-identical to a genuine miss, and an agent concludes the memory does not "+
			"exist rather than that it named the wrong wing.", out)
	}
	if !strings.Contains(out, "wing_real") {
		t.Errorf("the note does not name a wing that does hold memories: %s", out)
	}

	// And a real miss against a populated wing stays quiet.
	quiet := h.MustCall(t, "am_search", map[string]any{
		"query": "zzzz nothing will match this qqqq", "wing": "wing_real", "max_distance": 0.01,
	})
	if strings.Contains(quiet, `"note"`) {
		t.Errorf("a genuine miss against a populated wing produced a note — a warning on every "+
			"empty page is a warning nobody reads:\n  %s", quiet)
	}
}

// TestScenarioRerankedIsAlwaysStated: rerank_score is omitempty, so its absence
// once meant four different things. The bool that disambiguates it must be
// serialised even when false, or three of the four merge again.
func TestScenarioRerankedIsAlwaysStated(t *testing.T) {
	h := mcptest.NewWithWing(t, "wing_rr")
	h.MustCall(t, "am_add_drawer", map[string]any{
		"room": "decisions", "content": "the cross-encoder is not configured in this harness",
	})
	out := h.MustCall(t, "am_search", map[string]any{"query": "cross-encoder configured"})
	if !strings.Contains(out, `"reranked"`) {
		t.Errorf("a hit carries no `reranked` key with no reranker configured:\n  %s\n"+
			"  Absence then means 'no reranker', 'weight 0', 'below the pool cutoff' and 'scored "+
			"0.0' all at once, which is the ambiguity this field exists to remove.", out)
	}
}
