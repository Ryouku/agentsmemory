package palace

import (
	"sort"
	"strings"
)

// minRegionRunes is the smallest a region may be.
//
// Below it a fragment carries no sentence and costs the join marker that
// announces it, so a budget spent on many of them is worse than one passage. It
// is also what keeps a matching-in-one-place memory returning exactly what it
// returns today.
const minRegionRunes = 100

// Region is one part of a memory that matched, verbatim.
//
// Text is a slice of the memory and nothing else: ADR-019 refuses to put
// generated prose on the read path, because am_add_drawer promises text is
// "stored exactly, never summarised" and a summary an agent acts on is that
// promise broken at the other end. Score is how many distinct query terms fall
// inside, and Start is the rune offset so a caller can tell where in the memory
// it came from.
type Region struct {
	Text  string `json:"text"`
	Score int    `json:"terms_matched"`
	Start int    `json:"start"`
}

// SnippetRegions returns every part of a memory that matched, verbatim,
// position-ordered and non-overlapping, within maxChars total.
//
// It exists because the single-window chooser cannot do better. Its score
// SATURATES — a window ranks by how many distinct query terms fall inside, so
// once a window holds the terms every other window holding them ties — and ties
// resolve to the earliest position. Measured 2026-08-21 across nine real queries:
// the chosen window started within 130 runes of the memory's beginning 7 times,
// was tied by a later window the agent never saw 5 times, and was beaten by a
// later window 0 times. In a corpus whose memories open with a header line
// carrying the date, project and subject, the opening ties by construction and
// the body is never shown.
//
// Position order and not score order: an agent can sort by score itself, and it
// cannot un-jumble prose.
func SnippetRegions(content, query string, maxChars int) []Region {
	return snippetRegions(content, query, maxChars, 0, false)
}

// snippetRegions is SnippetRegions with an optional ceiling on how many
// passages may share the budget. A zero ceiling preserves the public function's
// behavior. Ranking also asks for distinct-term coverage because a cross-encoder
// must receive every clause of a compound question before it receives another
// passage repeating terms already represented. Agent-visible regions retain
// their existing score-first behavior.
func snippetRegions(content, query string, maxChars, maxRegions int, coverTerms bool) []Region {
	if maxChars <= 0 {
		maxChars = DefaultSnippetChars
	}
	runes := []rune(content)
	terms := tokenize(query)
	if coverTerms {
		terms = distinctTerms(terms)
	}
	if len(runes) == 0 {
		return nil
	}
	// Short enough to show whole, or nothing to match on: one region, which is
	// exactly what the caller already receives.
	if len(runes) <= maxChars || len(terms) == 0 {
		end := len(runes)
		if end > maxChars {
			end = maxChars
		}
		return []Region{{Text: string(runes[:end]), Start: 0}}
	}

	lower := []rune(strings.ToLower(content))

	// How many regions the budget can hold at a size worth reading, and how big
	// each is. One region is the floor: a memory that matched in one place must not
	// have its budget shredded into fragments.
	//
	// want*size <= maxChars by construction, which is why no clamp follows: the
	// only way a region grows past its size is the word-boundary extension below,
	// and that checks the remaining budget before taking it. A clamp here would be
	// a branch no input reaches, which reads as a guard and is not one.
	want := max(maxChars/minRegionRunes, 1)
	if maxRegions > 0 {
		want = min(want, maxRegions)
	}
	size := maxChars / want

	candidates := snippetCandidates(runes, lower, terms, size)
	// Best first, and among equals the earliest — so the selection is
	// deterministic and a re-run of the same query returns the same page.
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].Terms != candidates[b].Terms {
			return candidates[a].Terms > candidates[b].Terms
		}
		return candidates[a].Start < candidates[b].Start
	})

	var picked []windowCandidate
	if coverTerms {
		picked = coverageCandidates(candidates, lower, terms, want, size)
	} else {
		for _, c := range candidates {
			if c.Terms == 0 {
				break // nothing below here matched anything; padding with misses helps nobody
			}
			if len(picked) >= want {
				break
			}
			if !nearRegion(c, picked, size) {
				picked = append(picked, c)
			}
		}
	}
	if len(picked) == 0 {
		// Terms exist but no window contains one. The opening is the honest
		// fallback and is what the caller receives today.
		return []Region{{Text: string(runes[:maxChars]), Start: 0}}
	}

	sort.SliceStable(picked, func(a, b int) bool { return picked[a].Start < picked[b].Start })

	out := make([]Region, 0, len(picked))
	spent := 0
	for _, p := range picked {
		// Align the region to its MATCH rather than to the candidate boundary.
		//
		// A candidate wins because a term falls somewhere inside it, which routinely
		// leaves tens of runes of preceding filler and cuts the sentence the term
		// belongs to. The answer almost always FOLLOWS the term — "the pool ships at
		// ten BECAUSE…", "measured at twenty two seconds" — so the window is slid to
		// begin just before the first match and carry what comes after it.
		start := p.Start
		if at := firstMatchAt(lower, p.Start, p.End, terms); at >= 0 {
			lead := size / 6 // a little context before the term, not a paragraph of it
			start = at - lead
			if start < p.Start {
				start = p.Start
			}
			if start+size > len(runes) {
				start = len(runes) - size
			}
			if start < 0 {
				start = 0
			}
		}
		end := start + (p.End - p.Start)
		if end > len(runes) {
			end = len(runes)
		}
		p.Start = start
		if end <= start {
			break
		}
		// Do not hand back a word cut in half, for the same reason the single
		// window does not: the sentence an agent needs continues past the cut.
		if grow := wordTail(runes, end); grow > 0 && spent+(end-start)+grow <= maxChars {
			end += grow
		}
		out = append(out, Region{Text: string(runes[start:end]), Score: p.Terms, Start: start})
		spent += end - start
	}
	return out
}

// coverageCandidates selects a new query term before another occurrence of a
// term already represented. Once every term reachable within the budget is
// covered, score order resumes so repeated occurrences can still contribute
// separate evidence — important when one subject term introduces several
// distant premises and conclusions.
func coverageCandidates(candidates []windowCandidate, lower []rune, terms []string, want, size int) []windowCandidate {
	picked := make([]windowCandidate, 0, want)
	covered := make(map[string]bool, len(terms))
	for len(picked) < want {
		best := -1
		bestFresh := -1
		for i, c := range candidates {
			if c.Terms == 0 || nearRegion(c, picked, size) {
				continue
			}
			fresh := 0
			window := string(lower[c.Start:c.End])
			for _, term := range terms {
				if !covered[term] && strings.Contains(window, term) {
					fresh++
				}
			}
			// candidates are already score-first and position-second. Keeping the
			// first tie preserves those deterministic tie-breakers.
			if fresh > bestFresh {
				best, bestFresh = i, fresh
			}
		}
		if best < 0 {
			break
		}
		chosen := candidates[best]
		picked = append(picked, chosen)
		window := string(lower[chosen.Start:chosen.End])
		for _, term := range terms {
			if strings.Contains(window, term) {
				covered[term] = true
			}
		}
	}
	return picked
}

// nearRegion enforces one region per neighbourhood, not merely non-overlap.
// Candidate windows advance by much less than their width, so without this the
// highest-scoring choices are usually several views of one paragraph.
func nearRegion(candidate windowCandidate, picked []windowCandidate, size int) bool {
	for _, p := range picked {
		if candidate.Start < p.End+size && p.Start < candidate.End+size {
			return true
		}
	}
	return false
}

func distinctTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		if !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
	}
	return out
}

// firstMatchAt is the rune offset of the earliest query term inside [from,to),
// or -1 when none is there. lower is the lowercased content, aligned with the
// original one rune to one.
func firstMatchAt(lower []rune, from, to int, terms []string) int {
	win := string(lower[from:to])
	best := -1
	for _, t := range terms {
		if b := strings.Index(win, t); b >= 0 {
			at := from + len([]rune(win[:b]))
			if best < 0 || at < best {
				best = at
			}
		}
	}
	return best
}

// MemoryIdentity is a memory's own first line, bounded — what the author wrote to
// say what this memory IS.
//
// It is the "summary" a hit carries, and it is not written by a model. This
// repository's convention is that a memory opens with its date, project and
// subject (SnippetHeadChars exists because of it), so the line the author already
// wrote is a better label than anything that could be generated from the text —
// and it costs nothing and cannot be wrong.
func MemoryIdentity(content string) string {
	line := content
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	runes := []rune(line)
	if len(runes) <= SnippetHeadChars {
		return strings.TrimRight(line, " ")
	}
	cut := SnippetHeadChars
	// Back off to a word boundary rather than slicing a word in half.
	for cut > 0 && isWordRune(runes[cut-1]) && cut < len(runes) && isWordRune(runes[cut]) {
		cut--
	}
	if cut == 0 {
		cut = SnippetHeadChars
	}
	return strings.TrimRight(string(runes[:cut]), " ")
}
