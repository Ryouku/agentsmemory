package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

// TestWindowReportNamesTheChosenWindow: the window the report marks as chosen
// must be the one Snippet actually returns.
//
// The whole point of this report is to decide whether the answers agents miss
// are in windows the chooser discarded. A report built on a re-implementation of
// the scoring loop measures the re-implementation, and would answer that question
// about code nobody runs.
func TestWindowReportNamesTheChosenWindow(t *testing.T) {
	const content = "the rerank pool ships at ten because a cross encoder is linear in pool size. " +
		"an unrelated paragraph about cache invalidation and ttl defaults sits in the middle here. " +
		"the budget must be shorter than any client waits, or the fail-open path is unreachable."
	const query = "rerank pool"

	report := palace.WindowReport(content, query, 60)
	if len(report.Windows) < 2 {
		t.Fatalf("only %d window(s) reported for a %d-rune memory at a 60-rune window — "+
			"the report cannot say anything about discarded windows if it does not have any",
			len(report.Windows), len([]rune(content)))
	}

	var chosen *palace.WindowScore
	for i := range report.Windows {
		if report.Windows[i].Chosen {
			if chosen != nil {
				t.Fatal("two windows are marked chosen; the chooser returns exactly one")
			}
			chosen = &report.Windows[i]
		}
	}
	if chosen == nil {
		t.Fatal("no window is marked chosen, so the report cannot say what was discarded relative to what")
	}

	// EXACTLY what Snippet delivers, not merely overlapping it. The chooser shifts
	// its window to avoid cutting a word, so the candidate it started from and the
	// window the caller receives differ by a few runes — and a report whose
	// "chosen" text is not what the caller saw is answering about a window nobody
	// got. An overlap test cannot tell those apart.
	got := palace.Snippet(content, query, 60)
	body := strings.Trim(got, "…")
	if chosen.Text != body {
		t.Errorf("the report's chosen window is not the one Snippet returns.\n  report:  %q\n  Snippet: %q",
			chosen.Text, body)
	}
}

// TestWindowReportCoversTheWholeMemory: every rune of the memory falls inside at
// least one reported window.
//
// A measurement with blind spots is worthless exactly where the answer might be:
// if the report never looks at runes 900-1100, it cannot tell you the answer was
// there, and it will read as "the answer is in no window".
func TestWindowReportCoversTheWholeMemory(t *testing.T) {
	content := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa. ", 30)

	// Narrow windows as well as wide ones. The chooser's stride is capped at half
	// the window precisely so candidates overlap; at a FIXED stride of 40 a
	// 20-rune window leaves 20 runes unscored between every pair, and an answer
	// sitting in one of those gaps would be reported as "in no window" — the
	// verdict that withdraws this whole ADR. A coverage check run only at a wide
	// window cannot see that.
	for _, maxChars := range []int{16, 20, 40, 80, 200} {
		report := palace.WindowReport(content, "gamma theta", maxChars)
		runes := len([]rune(content))
		covered := make([]bool, runes)
		for _, w := range report.Windows {
			for i := w.Start; i < w.End && i < runes; i++ {
				covered[i] = true
			}
		}
		for i, ok := range covered {
			if !ok {
				t.Fatalf("maxChars=%d: rune %d of %d is in no reported window — the report has a blind "+
					"spot, and an answer sitting there would be counted as 'in no window'",
					maxChars, i, runes)
			}
		}
	}
}

var _ = bytes.MinRead
