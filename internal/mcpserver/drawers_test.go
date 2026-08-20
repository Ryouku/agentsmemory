package mcpserver

import "testing"

// TestParseAnchorListSeparatesEmptyFromUnreadable pins the one distinction that
// makes the replace path safe.
//
// parseAnchors is tolerant by design and the rationale is good where it was
// written: an unreadable entry means "no anchors added" and the memory is worth
// more than its anchor. At a REPLACE the same empty result means "delete the
// anchors this memory already has" — so tolerance inverts into data loss, and
// `code_anchors: {…}` instead of `[{…}]` is an ordinary mistake for an LLM
// caller. An unknown recorded as a definite negative is the shape this
// repository has already fixed at the read end; this is it at the write end.
func TestParseAnchorListSeparatesEmptyFromUnreadable(t *testing.T) {
	deliberate := []any{}
	if got, readable := parseAnchorList(deliberate); !readable || len(got) != 0 {
		t.Errorf("a genuine empty list must read as readable-and-empty, got readable=%v len=%d — "+
			"otherwise a deliberate clear becomes impossible", readable, len(got))
	}

	for name, raw := range map[string]any{
		"object instead of a list": map[string]any{"path": "a.go", "snippet": "x"},
		"string instead of a list": "internal/a.go",
		"nil":                      nil,
		"number":                   float64(3),
	} {
		if _, readable := parseAnchorList(raw); readable {
			t.Errorf("%s read as a valid list — at the replace site that clears the memory's "+
				"anchors and reports success", name)
		}
	}

	// Individual malformed ENTRIES stay tolerant: the list was readable, so the
	// caller's intent is clear, and dropping one bad entry is not data loss.
	mixed := []any{
		map[string]any{"path": "a.go", "snippet": "func A() {}"},
		map[string]any{"path": "", "snippet": "no path"},
		"not an object",
	}
	got, readable := parseAnchorList(mixed)
	if !readable {
		t.Fatal("a list with some bad entries is still a list")
	}
	if len(got) != 1 {
		t.Errorf("kept %d entries, want 1 — the good one", len(got))
	}
}
