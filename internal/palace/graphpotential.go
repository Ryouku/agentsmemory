package palace

import (
	"context"
	"sort"
)

// GraphViabilityBar is the share of drawers that must yield two or more entities
// for a frequency-based extractor to be worth putting on the write path.
//
// Pre-registered in ADR-016 BEFORE the measurement, and it is a real decision
// procedure rather than a formality: below it, wiring the extractor into every
// write buys a per-write cost and leaves the graph empty for a subtler reason
// than it is empty now, and the ADR withdraws that task instead of shipping it
// and hoping. The risk it guards is specific — mining feeds the extractor long
// repetitive transcripts, and agents file short deliberate notes, so a rule that
// needs a term TWICE may almost never fire on what agents actually write.
const GraphViabilityBar = 0.20

// WingGraphPotential is what one wing's derived graph would hold if every drawer
// in it were run through the extractor now.
type WingGraphPotential struct {
	Wing        string   `json:"wing"`
	Drawers     int      `json:"drawers"`
	WithAny     int      `json:"with_any_entity"`
	WithTwo     int      `json:"with_two_entities"`
	Hallways    int      `json:"hallways_derivable"`
	TopEntities []string `json:"top_entities"`
}

// GraphReport answers "is the derived graph reachable on this corpus?" without
// changing anything.
//
// It exists because the graph is empty on every palace populated through the
// agent write path — 0 of 359 drawers carried an entity, measured 2026-08-21 —
// and the obvious fix is to run the extractor on write. Whether that fix WORKS
// is a property of the corpus, not of the code, so it is measured on the corpus
// before the code is written.
type GraphReport struct {
	Drawers  int                  `json:"drawers"`
	WithAny  int                  `json:"with_any_entity"`
	WithTwo  int                  `json:"with_two_entities"`
	Hallways int                  `json:"hallways_derivable"`
	Wings    []WingGraphPotential `json:"wings"`
}

// ViableShare is the fraction of drawers that would carry two or more entities —
// the number the bar is set against. A hallway needs a PAIR co-occurring in one
// drawer, so a drawer with one entity contributes nothing to the graph.
func (r GraphReport) ViableShare() float64 {
	if r.Drawers == 0 {
		return 0
	}
	return float64(r.WithTwo) / float64(r.Drawers)
}

// Viable reports whether the measured share clears the pre-registered bar.
func (r GraphReport) Viable() bool { return r.ViableShare() >= GraphViabilityBar }

// GraphPotential runs the extractor over every drawer WITHOUT storing anything
// and reports what the derived graph would hold.
//
// Read-only on purpose. This is the measurement a decision is taken on, and a
// measurement that also mutates cannot be re-run to check itself.
func (s *Service) GraphPotential(ctx context.Context, teamID string) (GraphReport, error) {
	var report GraphReport
	byWing := map[string]*WingGraphPotential{}
	// Per wing: how many drawers each entity PAIR co-occurred in, which is exactly
	// what computeHallwaysForWing counts, so the projected number is the real one
	// rather than a guess at it.
	pairs := map[string]map[entityPair]int{}
	freqs := map[string]map[string]int{}

	const page = 500
	for offset := 0; ; offset += page {
		drawers, err := s.repo.List(ctx, teamID, "", "", page, offset)
		if err != nil {
			return GraphReport{}, err
		}
		if len(drawers) == 0 {
			break
		}
		for _, d := range drawers {
			w, ok := byWing[d.Wing]
			if !ok {
				w = &WingGraphPotential{Wing: d.Wing}
				byWing[d.Wing] = w
				pairs[d.Wing] = map[entityPair]int{}
				freqs[d.Wing] = map[string]int{}
			}
			report.Drawers++
			w.Drawers++

			ents := dedupePreserve(extractEntities(d.Content))
			for _, e := range ents {
				freqs[d.Wing][e]++
			}
			if len(ents) >= 1 {
				report.WithAny++
				w.WithAny++
			}
			if len(ents) < 2 {
				continue
			}
			report.WithTwo++
			w.WithTwo++
			for i := 0; i < len(ents); i++ {
				for j := i + 1; j < len(ents); j++ {
					a, b := ents[i], ents[j]
					if a == b {
						continue
					}
					if a > b {
						a, b = b, a
					}
					pairs[d.Wing][entityPair{a, b}]++
				}
			}
		}
	}

	for wing, w := range byWing {
		for _, count := range pairs[wing] {
			if count >= hallwayMinCount {
				w.Hallways++
			}
		}
		report.Hallways += w.Hallways
		// The most frequent candidates, so noise is visible rather than inferred:
		// a graph of a thousand hallways between meaningless tokens is worse than
		// no graph, and only looking at the names shows which one this is.
		type ef struct {
			name string
			n    int
		}
		var top []ef
		for name, n := range freqs[wing] {
			top = append(top, ef{name, n})
		}
		sort.Slice(top, func(a, b int) bool {
			if top[a].n != top[b].n {
				return top[a].n > top[b].n
			}
			return top[a].name < top[b].name
		})
		for i := 0; i < len(top) && i < 10; i++ {
			w.TopEntities = append(w.TopEntities, top[i].name)
		}
		report.Wings = append(report.Wings, *w)
	}
	sort.Slice(report.Wings, func(a, b int) bool { return report.Wings[a].Drawers > report.Wings[b].Drawers })
	return report, nil
}
