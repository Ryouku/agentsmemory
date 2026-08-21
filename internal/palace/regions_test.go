package palace

import (
	"strings"
	"testing"
)

// A memory that names one subject three times in three different places, which is
// what a real session note looks like: a header, then the body returning to the
// same terms as it works through the material.
var threeRegionMemory = "2026-08-21 | rerank pool sizing | the header line naming the subject. " +
	strings.Repeat("filler about unrelated matters that mentions nothing of interest. ", 6) +
	"the rerank pool ships at ten because the cross encoder is linear in pool size. " +
	strings.Repeat("more filler, again about something else entirely and quite unrelated. ", 6) +
	"and finally the rerank pool was measured at twenty two seconds when it was fifty. "

// TestRegionsAreVerbatimSlicesOfTheMemory is the ADR's refusal made mechanical.
//
// ADR-019 declines to put generated prose on the read path, because
// am_add_drawer's contract promises text is "stored exactly, never summarised" and
// a summary an agent acts on is that promise broken at the other end. Every region
// must therefore be a slice of the memory — not trimmed, not normalised, not
// rewritten.
func TestRegionsAreVerbatimSlicesOfTheMemory(t *testing.T) {
	regions := SnippetRegions(threeRegionMemory, "rerank pool size", 400)
	if len(regions) == 0 {
		t.Fatal("no regions returned for a memory that matches in three places")
	}
	for i, r := range regions {
		if !strings.Contains(threeRegionMemory, r.Text) {
			t.Errorf("region %d is not a slice of the memory — something on this path is generated: %q", i, r.Text)
		}
		if got := string([]rune(threeRegionMemory)[r.Start : r.Start+len([]rune(r.Text))]); got != r.Text {
			t.Errorf("region %d says it starts at rune %d, but the memory holds %q there", i, r.Start, got)
		}
	}
}

// TestRegionsCoverEveryMatch: a memory matching in three places yields regions
// covering all three, in position order, without overlapping.
//
// Position order and not score order: an agent can sort by score itself and
// cannot un-jumble prose. Overlap matters because two regions sharing text spend
// the budget twice on the same words.
func TestRegionsCoverEveryMatch(t *testing.T) {
	regions := SnippetRegions(threeRegionMemory, "rerank pool size", 400)
	if len(regions) < 2 {
		t.Fatalf("a memory matching in three separate places produced %d region(s) — the whole point "+
			"is that the agent sees the places it matched, not the first one", len(regions))
	}
	for i := 1; i < len(regions); i++ {
		if regions[i].Start <= regions[i-1].Start {
			t.Errorf("regions are not in position order: region %d starts at %d, region %d at %d",
				i-1, regions[i-1].Start, i, regions[i].Start)
		}
		prevEnd := regions[i-1].Start + len([]rune(regions[i-1].Text))
		if regions[i].Start < prevEnd {
			t.Errorf("regions %d and %d overlap (%d..%d and %d..) — the budget is spent twice on the "+
				"same words", i-1, i, regions[i-1].Start, prevEnd, regions[i].Start)
		}
	}
	// The last mention, furthest from the opening, must be reachable — it is the
	// one the old single-window chooser could never show.
	var sawLate bool
	for _, r := range regions {
		if strings.Contains(r.Text, "twenty two seconds") {
			sawLate = true
		}
	}
	if !sawLate {
		t.Error("the match nearest the END of the memory is in no region; that is the one the " +
			"single-window chooser already could not reach, so nothing has changed for it")
	}
}

// TestRegionsKeepOneRegionWhole: a memory matching in one place returns ONE
// region. The budget must not be shredded when there is nothing to spread it over.
func TestRegionsKeepOneRegionWhole(t *testing.T) {
	one := "a memory that mentions the rerank pool exactly once and then talks at length about " +
		strings.Repeat("entirely unrelated matters with no bearing on the question asked. ", 12)
	regions := SnippetRegions(one, "rerank pool", 400)
	if len(regions) != 1 {
		t.Errorf("one matching place produced %d regions; a list of fragments is worse than a passage", len(regions))
	}
}

// TestRegionsRespectTheBudget: the caller's snippet_chars is a ceiling.
func TestRegionsRespectTheBudget(t *testing.T) {
	for _, budget := range []int{120, 200, 400, 800} {
		regions := SnippetRegions(threeRegionMemory, "rerank pool size measured", budget)
		total := 0
		for _, r := range regions {
			total += len([]rune(r.Text))
		}
		if total > budget {
			t.Errorf("budget %d: regions total %d runes — the caller's ceiling is not a suggestion",
				budget, total)
		}
	}
}

// TestIdentityIsTheMemorysOwnFirstLine: the identity is what the author wrote,
// bounded — not a summary, not the highest-scoring region, not a derivation.
func TestIdentityIsTheMemorysOwnFirstLine(t *testing.T) {
	got := MemoryIdentity(threeRegionMemory)
	if got == "" {
		t.Fatal("no identity returned")
	}
	if !strings.HasPrefix(threeRegionMemory, got) {
		t.Errorf("the identity is not the memory's own opening: %q", got)
	}
	if !strings.Contains(got, "2026-08-21") {
		t.Errorf("the identity does not carry the opening line an author wrote to say what this is: %q", got)
	}
	if len([]rune(got)) > SnippetHeadChars {
		t.Errorf("the identity is %d runes; it is bounded at %d so it stays a label",
			len([]rune(got)), SnippetHeadChars)
	}
	// A memory whose first line is longer than the bound is cut at a word.
	long := strings.Repeat("averylongopeningwithoutanynewline ", 20)
	id := MemoryIdentity(long)
	if len([]rune(id)) > SnippetHeadChars {
		t.Errorf("a long opening was not bounded: %d runes", len([]rune(id)))
	}
}

// TestContentIsUnchangedByRegions: `content` must stay byte-identical to what it
// is today. This task ADDS; anything that changes what existing readers see is a
// different decision, and every agent in the wild reads that field.
func TestContentIsUnchangedByRegions(t *testing.T) {
	for _, maxChars := range []int{80, 200, 400} {
		for _, isHead := range []bool{true, false} {
			got := SnippetWithHead(threeRegionMemory, "rerank pool size", maxChars, isHead)
			if got == "" {
				t.Fatalf("maxChars=%d isHead=%v: the snippet is empty", maxChars, isHead)
			}
			// It is still ONE window, not the regions joined.
			if strings.Count(got, " … ") > 1 {
				t.Errorf("maxChars=%d isHead=%v: content now looks like joined regions (%q) — "+
					"regions are a separate field and content keeps its meaning", maxChars, isHead, got)
			}
		}
	}
}

// TestRegionsAreOrderedByPositionNotScore: the strongest match may sit at the END
// of a memory, and a list that jumps backwards through prose reads as nonsense.
//
// An agent can sort by score itself — the score is in the field. It cannot
// un-jumble sentences. The first version of this test could not tell the two
// orderings apart because every region happened to score the same; this memory
// makes the best region the last one.
func TestRegionsAreOrderedByPositionNotScore(t *testing.T) {
	// One term early, all three terms late.
	mem := "an opening line that mentions the pool and nothing else at all. " +
		strings.Repeat("filler with no bearing on anything being asked about here. ", 8) +
		"the rerank pool size was settled at ten after the measurement. " +
		strings.Repeat("trailing filler that is also of no interest whatsoever here. ", 3)

	regions := SnippetRegions(mem, "rerank pool size", 400)
	if len(regions) < 2 {
		t.Fatalf("expected several regions, got %d", len(regions))
	}
	for i := 1; i < len(regions); i++ {
		if regions[i].Start <= regions[i-1].Start {
			t.Fatalf("regions are not in position order: %d then %d", regions[i-1].Start, regions[i].Start)
		}
	}
	// And the highest-scoring region really is a later one, so the assertion above
	// would fail under score ordering rather than passing by coincidence.
	best, bestAt := 0, -1
	for i, r := range regions {
		if r.Score > best {
			best, bestAt = r.Score, i
		}
	}
	if bestAt == 0 {
		t.Fatalf("this fixture no longer puts the best region later, so it cannot distinguish "+
			"position ordering from score ordering; scores are %v", regions)
	}
}
