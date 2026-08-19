package palace

import (
	"context"
	"fmt"
	"sort"
)

// Retrieval evaluation: does recall actually return the memory that answers the
// question, and does each ranking stage earn its place?
//
// It lives inside this package because an honest ablation has to run the REAL
// ranking code — rankHybrid, the closet boost, the cross-encoder — rather than a
// reimplementation that agrees with itself. Nothing here is exposed over MCP: an
// eval knob on the production search API is a knob that eventually gets set in
// production.
//
// Every arm scores the SAME candidate pool from one vector search per query, so
// the table measures ordering rather than the noise of re-running retrieval.

// EvalArm is one ranking configuration under test.
type EvalArm string

const (
	// ArmVector is nearest-neighbour order alone — the baseline everything else
	// has to beat to justify existing.
	ArmVector EvalArm = "vector"
	// ArmHybrid adds the lexical BM25 half of the fusion.
	ArmHybrid EvalArm = "hybrid"
	// ArmHybridCloset adds the closet boost.
	ArmHybridCloset EvalArm = "hybrid+closet"
	// ArmReranked is production: fusion, then the cross-encoder over the top K.
	ArmReranked EvalArm = "hybrid+closet+rerank"
)

// EvalCase is one labelled question: the query, and the drawer that should come
// back for it.
type EvalCase struct {
	Query  string
	Expect string // drawer id
	Wing   string // optional scope, mirroring how the query would really be run
}

// EvalMetrics is one arm's score over a case set.
//
// MRR is the headline: recall@1 is brittle on a small corpus and recall@5 saturates,
// while the reciprocal rank moves whenever an arm shifts the right answer up or
// down at all — which is exactly what a ranking change does.
type EvalMetrics struct {
	Arm      EvalArm
	Cases    int
	Recall1  int
	Recall5  int
	MRR      float64
	NotFound int // the expected drawer was not in the candidate pool at all
}

// Recall1Pct / Recall5Pct render the counts as percentages of cases.
func (m EvalMetrics) Recall1Pct() float64 { return pct(m.Recall1, m.Cases) }
func (m EvalMetrics) Recall5Pct() float64 { return pct(m.Recall5, m.Cases) }

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// EvalReport is the full table plus the per-case detail a human needs to tell a
// bad query from a bad ranking.
type EvalReport struct {
	Arms    []EvalMetrics
	Details []EvalCaseResult
}

// EvalCaseResult is where each arm put the expected drawer for one query. Rank 0
// means "not in the pool"; a case that is 0 everywhere is a retrieval miss, not a
// ranking one, and usually means the generated question shares no vocabulary with
// its own source.
type EvalCaseResult struct {
	Query string
	Ranks map[EvalArm]int
}

// Evaluate scores every arm over the cases. poolSize is how many neighbours the
// vector search fetches per query; it bounds every arm equally, so a memory
// outside the pool is unreachable for all of them (counted as NotFound).
func (s *Service) Evaluate(ctx context.Context, teamID string, cases []EvalCase, poolSize int) (EvalReport, error) {
	if poolSize <= 0 {
		poolSize = 50
	}
	arms := []EvalArm{ArmVector, ArmHybrid, ArmHybridCloset}
	if s.reranker != nil {
		arms = append(arms, ArmReranked)
	}
	byArm := map[EvalArm]*EvalMetrics{}
	for _, a := range arms {
		byArm[a] = &EvalMetrics{Arm: a}
	}
	report := EvalReport{}

	for _, c := range cases {
		ranks, err := s.evalCase(ctx, teamID, c, arms, poolSize)
		if err != nil {
			return EvalReport{}, err
		}
		report.Details = append(report.Details, EvalCaseResult{Query: c.Query, Ranks: ranks})
		for _, a := range arms {
			m := byArm[a]
			m.Cases++
			switch r := ranks[a]; {
			case r == 0:
				m.NotFound++
			default:
				m.MRR += 1 / float64(r)
				if r == 1 {
					m.Recall1++
				}
				if r <= 5 {
					m.Recall5++
				}
			}
		}
	}
	for _, a := range arms {
		m := byArm[a]
		if m.Cases > 0 {
			m.MRR /= float64(m.Cases)
		}
		report.Arms = append(report.Arms, *m)
	}
	return report, nil
}

// evalCase runs one query through every arm and returns the 1-based rank of the
// expected drawer per arm (0 = absent).
func (s *Service) evalCase(ctx context.Context, teamID string, c EvalCase, arms []EvalArm, poolSize int) (map[EvalArm]int, error) {
	vec, err := s.embed.EmbedOne(ctx, c.Query)
	if err != nil {
		return nil, fmt.Errorf("embed eval query: %w", err)
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, poolSize, searchFilter(SearchQuery{Wing: c.Wing}))
	if err != nil {
		return nil, fmt.Errorf("eval vector search: %w", err)
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return nil, fmt.Errorf("load eval candidates: %w", err)
	}

	// One candidate list, ordered by vector distance — the input every arm
	// re-orders. Building it once is what makes the comparison fair.
	type candidate struct {
		id       string
		content  string
		distance float64
		source   string
	}
	var pool []candidate
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			continue
		}
		pool = append(pool, candidate{id: d.ID, content: d.Content, distance: distanceFromScore(h.Score), source: d.SourceFile})
	}

	docs := make([]string, len(pool))
	dists := make([]float64, len(pool))
	for i, p := range pool {
		docs[i], dists[i] = p.content, p.distance
	}
	boosts := make([]float64, len(pool))
	closetBoosts := s.closetBoosts(ctx, teamID, vec)
	for i, p := range pool {
		boosts[i] = closetBoosts[p.source]
	}

	out := map[EvalArm]int{}
	for _, arm := range arms {
		var ordered []int // indices into pool, best first
		switch arm {
		case ArmVector:
			idx := make([]int, len(pool))
			for i := range idx {
				idx[i] = i
			}
			sort.SliceStable(idx, func(a, b int) bool { return pool[idx[a]].distance < pool[idx[b]].distance })
			ordered = idx
		case ArmHybrid:
			for _, r := range rankHybrid(c.Query, docs, dists, nil) {
				ordered = append(ordered, r.Index)
			}
		case ArmHybridCloset:
			for _, r := range rankHybrid(c.Query, docs, dists, boosts) {
				ordered = append(ordered, r.Index)
			}
		case ArmReranked:
			hitsForRank := make([]SearchHit, 0, len(pool))
			for _, r := range rankHybrid(c.Query, docs, dists, boosts) {
				hitsForRank = append(hitsForRank, SearchHit{Drawer: Drawer{ID: pool[r.Index].id, Content: pool[r.Index].content}})
			}
			for _, h := range s.crossEncode(ctx, c.Query, hitsForRank) {
				for i, p := range pool {
					if p.id == h.Drawer.ID {
						ordered = append(ordered, i)
						break
					}
				}
			}
		}
		poolIDs := make([]string, len(pool))
		for i, p := range pool {
			poolIDs[i] = p.id
		}
		out[arm] = rankOf(poolIDs, ordered, c.Expect)
	}
	return out, nil
}

// rankOf returns the 1-based position of the expected id in an ordering, or 0
// when it is absent — which is the signal that the memory never made the pool,
// a retrieval miss rather than a ranking one.
func rankOf(ids []string, ordered []int, expect string) int {
	for rank, idx := range ordered {
		if idx < 0 || idx >= len(ids) {
			continue
		}
		if ids[idx] == expect {
			return rank + 1
		}
	}
	return 0
}
