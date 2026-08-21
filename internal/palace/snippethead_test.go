package palace

import (
	"strings"
	"testing"
	"unicode/utf8"
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

// TestSnippetSurvivesRunesThatChangeLengthWhenLowercased pins a crash, not a
// quality regression.
//
// The clipped-term completion took a BYTE index from strings.Index over the
// lowercased window and used it to slice the ORIGINAL window. strings.ToLower
// maps runes one-for-one but not bytes: U+023A (Ⱥ, two bytes) lowercases to
// U+2C65 (ⱥ, three bytes), so the lowered window is longer than the original and
// an index near its end runs off the end of the original.
//
// That is a panic on the path EVERY search result takes — one memory containing
// such a character, plus a query term landing late in the window, takes down the
// whole request. Found by review, not by any gate: every existing snippet test
// used ASCII, where byte and rune indexes agree.
func TestSnippetSurvivesRunesThatChangeLengthWhenLowercased(t *testing.T) {
	// Each of these lowercases to a DIFFERENT number of bytes than it occupies.
	for _, r := range []string{"Ⱥ", "Ɫ", "Ᵽ", "Ɽ", "K", "Ω"} {
		content := strings.Repeat(r, 200) + " budget must be short and the tail continues past the window"
		for _, maxChars := range []int{20, 60, 120, 199, 200, 201} {
			for _, q := range []string{"budget", "tail continues", strings.ToLower(r), "window past"} {
				got := Snippet(content, q, maxChars) // must not panic
				if !utf8.ValidString(got) {
					t.Errorf("Snippet(%q…, %q, %d) returned invalid UTF-8", r, q, maxChars)
				}
				if got == "" {
					t.Errorf("Snippet(%q…, %q, %d) returned nothing", r, q, maxChars)
				}
				if h := SnippetWithHead(content, q, maxChars, true); !utf8.ValidString(h) {
					t.Errorf("SnippetWithHead(%q…, %q, %d) returned invalid UTF-8", r, q, maxChars)
				}
			}
		}
	}
}

// TestSnippetDoesNotEndMidWord is the test the previous attempt at this did not
// have, and could not have had: its repair searched for the clipped term INSIDE
// the chosen window, and strings.Index only matches a term wholly contained in
// it — so the shift condition (termEnd > end) was unreachable from the day it
// was written. Deleting the entire block left the whole package suite green.
//
// The symptom it was written for survived it verbatim: a 50-rune window over
// this content returned "…rerank budget must be sh…", which is the failure the
// real-query measurement named — the right drawer at rank 1 with the answer cut
// out of the text.
func TestSnippetDoesNotEndMidWord(t *testing.T) {
	const content = "the pool is fifty and the rerank budget must be shorter than any client waits, " +
		"or the fail-open path is unreachable in practice and nobody finds out"

	for _, maxChars := range []int{20, 30, 40, 50, 60, 80, 120} {
		got := Snippet(content, "budget shorter", maxChars)
		body := strings.Trim(got, "…")
		if body == "" {
			t.Fatalf("maxChars=%d returned nothing", maxChars)
		}
		// The window ends mid-word when the last rune of the body is a word rune
		// AND the rune that followed it in the content is too.
		if strings.HasSuffix(got, "…") {
			last := []rune(body)[len([]rune(body))-1]
			idx := strings.Index(content, body)
			if idx < 0 {
				t.Fatalf("maxChars=%d: %q is not a substring of the content", maxChars, body)
			}
			next := []rune(content[idx+len(body):])
			if len(next) > 0 && isWordRune(last) && isWordRune(next[0]) {
				t.Errorf("maxChars=%d cut a word in half: %q (next rune %q)", maxChars, got, string(next[0]))
			}
		}
	}
}

// TestSnippetShiftKeepsAMatchInView guards the fix against reproducing the bug
// it fixes. Completing a trailing word moves the window RIGHT, and a window that
// moves right can push the match off its left edge — which is the same failure
// ("the answer is not in the returned text") wearing the opposite sign.
func TestSnippetShiftKeepsAMatchInView(t *testing.T) {
	const term = "needle"
	for _, lead := range []int{0, 3, 17, 40, 91} {
		for _, maxChars := range []int{12, 20, 40, 90} {
			content := strings.Repeat("a ", lead) + term + " tailwordthatiscertainlylong " + strings.Repeat("b ", 120)
			got := Snippet(content, term, maxChars)
			if !strings.Contains(strings.ToLower(got), term) && maxChars >= len(term)+2 {
				t.Errorf("lead=%d maxChars=%d: the window no longer contains %q: %q", lead, maxChars, term, got)
			}
		}
	}
}

// TestSnippetScoresEveryPositionWhateverTheWindowSize: the window chooser
// advanced by a fixed stride of 40, so with a window narrower than the stride
// the positions between candidates were never scored at all. A match at rune 21
// with maxChars=10 returned the opening of the content and no match.
func TestSnippetScoresEveryPositionWhateverTheWindowSize(t *testing.T) {
	for _, at := range []int{5, 21, 39, 44, 77, 130} {
		content := strings.Repeat("x", at) + " needle " + strings.Repeat("y", 200)
		for _, maxChars := range []int{10, 16, 30, 39} {
			got := Snippet(content, "needle", maxChars)
			if !strings.Contains(got, "needle") {
				t.Errorf("match at rune %d, maxChars=%d: %q does not contain it", at, maxChars, got)
			}
		}
	}
}

// TestSnippetWithHeadDoesNotRepeatTheHead: the head was prepended whenever the
// body window did not start at rune 0, including when the body window started
// only a little past it — so runes 40..120 of a memory were delivered twice,
// inside a budget whose whole point is that context is expensive.
func TestSnippetWithHeadDoesNotRepeatTheHead(t *testing.T) {
	content := strings.Repeat("a", 300) + " MATCH here " + strings.Repeat("b", 300)
	got := SnippetWithHead(content, "match", 400, true)
	if !strings.Contains(got, "MATCH") {
		t.Fatalf("the match is not in the snippet: %q", got)
	}
	if n := strings.Count(got, " … "); n > 0 && !strings.Contains(got, "…"+strings.Repeat("a", 10)) {
		// A joined snippet is fine; a joined snippet whose two halves overlap is not.
		head, body, _ := strings.Cut(got, " … ")
		head = strings.TrimSuffix(head, " ")
		body = strings.TrimPrefix(body, "…")
		if len(head) > 8 && strings.Contains(body, head[len(head)-8:]) {
			t.Errorf("the head and the body overlap — content delivered twice: %q", got)
		}
	}
	if len([]rune(strings.ReplaceAll(strings.Trim(got, "…"), " … ", ""))) > 400 {
		t.Errorf("the snippet is longer than its budget: %d runes", len([]rune(got)))
	}
}
