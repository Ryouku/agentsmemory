package palace

import (
	"context"
	"sort"
)

// FactBlock is what a recall returns BESIDE its drawer hits: the facts it could
// place in the searched wing, the sibling wings it could not, and a count of the
// ones it could not place at all.
//
// It sits beside the hits rather than among them because F-9 pins that drawer
// selection and order are unchanged. A fact block that reordered the page would
// make every measurement of this decision a measurement of two changes at once.
type FactBlock struct {
	// Facts are IN-WING only. Nothing here ever describes another wing.
	Facts []KGFact `json:"facts,omitempty"`
	// ElsewhereWings names the wings that hold matches this recall did not
	// return, so an agent can go and query them. Names only — never content.
	ElsewhereWings []string `json:"elsewhere_wings,omitempty"`
	// Unlocatable counts matches whose wing could not be derived at all. On
	// today's corpus this is the MAJORITY case: 90 of 196 triples resolve to a
	// drawer (measured 2026-08-26). Dropping it would make a recall silent about
	// most of what it found, which is indistinguishable from "nothing is filed" —
	// the failure this whole decision exists to remove.
	Unlocatable int `json:"unlocatable,omitempty"`
}

// Empty reports whether the block says nothing at all. A block with no facts but
// a sibling wing or an unlocatable count is NOT empty: it is a recall telling an
// agent where else to look, which is the point.
func (b FactBlock) Empty() bool {
	return len(b.Facts) == 0 && len(b.ElsewhereWings) == 0 && b.Unlocatable == 0
}

// factEntityMatches is how many nearest entity labels a question is expanded to.
// Small on purpose: each match costs a triple lookup, and a question that means
// five different entities is a question, not a lookup.
const factEntityMatches = 5

// factsFor answers a question with FACTS, wing-resolved into three states.
//
// This is the seam ADR-036 exists to open. kg_triples and kg_entities appear zero
// times in service.go, memory_search.go and rank.go, so a recall consulted none of
// the graph: 196 triples with provenance and validity windows, reachable only by
// spelling an entity exactly right.
//
// Every placement decision goes through WingPolicy rather than a filter written
// here, because F-19 requires one rule across four response paths and four
// filters that agree today diverge on the path nobody tested.
func (s *Service) factsFor(ctx context.Context, teamID, wing string, vec []float32, loaded map[string]Drawer) (FactBlock, error) {
	var block FactBlock
	if len(vec) == 0 {
		return block, nil
	}

	matches, err := s.entityMatches(ctx, teamID, vec, factEntityMatches)
	if err != nil {
		return block, err
	}
	if len(matches) == 0 {
		return block, nil
	}

	// Collect the candidate facts by walking out from each matched entity.
	var candidates []KGFact
	for _, m := range matches {
		q, err := s.KGQuery(ctx, teamID, KGQueryInput{
			Entity: m.ID, Direction: "both", Status: KGStatusCurrent,
		})
		if err != nil {
			return block, err
		}
		candidates = append(candidates, q.Facts...)
	}
	if len(candidates) == 0 {
		return block, nil
	}

	// Resolve provenance from the rows the search ALREADY loaded, and query only
	// for the ids it did not. A fact's provenance often points at a drawer that
	// is on the page — a derived containment edge always does — so re-reading it
	// would fetch a row this recall is already holding.
	//
	// TestCandidateWideningDoesNotRefetchRows caught exactly that: it counts the
	// statements that resolve one drawer row during a search and refuses a second.
	wings := map[string]string{}
	var missing []string
	for _, f := range candidates {
		id := f.SourceDrawerID
		if id == "" {
			continue
		}
		if d, ok := loaded[id]; ok {
			wings[id] = d.Wing
			continue
		}
		if _, done := wings[id]; !done {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fetched, err := s.repo.WingsForDrawers(ctx, teamID, missing)
		if err != nil {
			return block, err
		}
		for id, w := range fetched {
			wings[id] = w
		}
	}
	policy := NewWingPolicy(wing, func(_ context.Context, id string) (string, bool) {
		w, ok := wings[id]
		return w, ok
	})

	elsewhere := map[string]bool{}
	seen := map[string]bool{}
	for _, f := range candidates {
		key := f.Subject + "\x00" + f.Predicate + "\x00" + f.Object
		if seen[key] {
			continue
		}
		seen[key] = true

		// A derived edge is PLUMBING, not an answer. T6 attaches
		// room:<wing>/<room> —holds→ <drawer id> so a drawer is reachable by
		// traversal; returning that to someone who asked a question would answer
		// "who owns invoice numbering" with "a room contains a drawer".
		//
		// This is the marker from 00028 earning its keep on the first read path
		// that consumes it: without it, server-inferred structure and authored
		// knowledge are one population and the block cannot tell them apart.
		if f.Derived {
			continue
		}

		placement, w := policy.Place(ctx, f.SourceDrawerID)
		switch {
		case policy.MayReturnContent(placement):
			block.Facts = append(block.Facts, f)
		case placement == PlacementForeign:
			elsewhere[w] = true
		default:
			block.Unlocatable++
		}
	}

	for w := range elsewhere {
		block.ElsewhereWings = append(block.ElsewhereWings, w)
	}
	// Sorted so the pointer is stable between identical recalls; an unstable list
	// reads as a changing answer.
	sort.Strings(block.ElsewhereWings)
	return block, nil
}
