package palace

import (
	"strings"
	"testing"
)

// TestSnippetKeepsTheIdentityOfChunkZero: the first line of a memory says what it
// IS. A window centred on the match is right; discarding the identity to get
// there is not.
//
// Measured 2026-08-21 against real production queries: three pages returned a
// snippet beginning mid-sentence in the middle of a memory, and the agent read a
// fragment with no way to tell what it belonged to. Retrieval had already
// succeeded in every one of those — the right drawer was on the page, at rank 1 —
// and the payload threw the answer away. That whole class was the LARGEST failure
// mode, ahead of both synthesis and ranking.
func TestSnippetKeepsTheIdentityOfChunkZero(t *testing.T) {
	head := "2026-08-21 DECISION — the closet prior is retired by default. "
	filler := strings.Repeat("padding padding padding padding. ", 40)
	tail := "The rule is that a budget must be shorter than any client will wait."
	content := head + filler + tail

	got := SnippetWithHead(content, "budget shorter than any client", 300, true)

	if !strings.HasPrefix(got, "2026-08-21 DECISION") {
		t.Errorf("the snippet does not begin with the memory's identity line:\n  %.90q\n"+
			"  An agent reading this cannot tell what memory it came from.", got)
	}
	if !strings.Contains(got, "shorter than any client") {
		t.Errorf("the snippet lost the match it was centred on:\n  %.120q", got)
	}
	if len([]rune(got)) > 340 {
		t.Errorf("the snippet is %d runes for a 300 budget — the head must come out of the "+
			"budget, not on top of it", len([]rune(got)))
	}
}

// TestSnippetLeavesLaterChunksAlone: only chunk 0 has an identity at offset zero.
// Prepending the head of chunk 3 would prepend the middle of a sentence, which is
// the defect this fix is about, applied to the wrong place.
func TestSnippetLeavesLaterChunksAlone(t *testing.T) {
	content := strings.Repeat("continuation text that began in an earlier chunk. ", 30) +
		"the answer lives here at the end"
	withHead := SnippetWithHead(content, "answer lives here", 200, false)
	plain := Snippet(content, "answer lives here", 200)
	if withHead != plain {
		t.Errorf("a non-head chunk was treated as one:\n  with=%.60q\n  plain=%.60q", withHead, plain)
	}
}

// TestSnippetHeadDoesNotCrowdOutTheMatch: with a small budget the head must not
// consume so much that the matched passage no longer fits.
func TestSnippetHeadDoesNotCrowdOutTheMatch(t *testing.T) {
	content := "IDENTITY LINE. " + strings.Repeat("x ", 200) + "the needle is here"
	got := SnippetWithHead(content, "needle is here", 100, true)
	if !strings.Contains(got, "needle") {
		t.Errorf("at a 100-char budget the head crowded out the match entirely:\n  %.110q", got)
	}
}

// TestSnippetFindsAMatchInTheFinalWindow: a memory's conclusions live at its end.
//
// The window loop advanced while start+maxChars <= len(content), so the last
// window was never scored: for a 433-rune memory at a 50-rune window it stopped
// at 360-410 and a match at 415 was invisible. The chooser then fell back to
// offset 0 and returned the opening — which reads like a plausible snippet, so
// nothing looked wrong. Measured against real queries, this was the mechanism
// behind the largest failure mode: the right drawer at rank 1, and the answer not
// in the text the agent receives.
func TestSnippetFindsAMatchInTheFinalWindow(t *testing.T) {
	for _, budget := range []int{50, 120, 400} {
		content := "opening line that is not the answer. " +
			strings.Repeat("filler filler filler. ", 60) +
			"THE CONCLUSION: raise the budget before the pool."
		got := Snippet(content, "conclusion raise the budget", budget)
		if !strings.Contains(got, "CONCLUSION") {
			t.Errorf("budget %d: the match sits in the last %d runes and the snippet returned "+
				"the opening instead:\n  %.90q", budget, budget, got)
		}
	}
}
