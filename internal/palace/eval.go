package palace

import (
	"context"
	"fmt"
	"sort"
	"time"
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
	// ArmReranked is production: fusion, then the cross-encoder over the top K,
	// blended at the configured weight.
	ArmReranked EvalArm = "hybrid+closet+rerank"
	// ArmRRF fuses the same two retrievers by RANK instead of by weighted score —
	// the candidate for replacing an inherited 0.6/0.4 split with something that
	// needs no tuning at all.
	ArmRRF EvalArm = "rrf"
	// ArmRRFReranked is RRF with the cross-encoder on top, so the fusion choice
	// and the rerank choice can be read independently.
	ArmRRFReranked EvalArm = "rrf+rerank"
	// ArmContextual retrieves from an index built with each chunk carrying a
	// little of its parent's context, then ranks it exactly like ArmHybridCloset —
	// so the delta is the EMBEDDING, not the ranking.
	ArmContextual EvalArm = "contextual chunks"
)

// rerankSweep are the blend weights the eval tries alongside production, so how
// much the cross-encoder should decide is answered by measurement rather than by
// whoever last had an opinion. 1.0 is the old behaviour: the cross-encoder
// overwrites the fused order completely.
var rerankSweep = []float64{0.25, 0.5, 0.75, 1.0}

// bm25Sweep is how much the LEXICAL half counts. 0.0 is vector-only, 0.4 is the
// inherited default. It is swept because a real corpus has already shown the
// default can be worse than not fusing at all: BM25 rewards shared vocabulary,
// and in a large palace many memories share a query's words without answering it.
var bm25Sweep = []float64{0.0, 0.2, 0.4, 0.6}

// bm25Arm names a swept fusion arm.
func bm25Arm(w float64) EvalArm { return EvalArm(fmt.Sprintf("fusion bm25=%.2f", w)) }

// rerankArm names a swept arm.
func rerankArm(w float64) EvalArm { return EvalArm(fmt.Sprintf("rerank blend w=%.2f", w)) }

// Eval categories, borrowed from the agent-memory benchmarks (LoCoMo and its
// descendants) because the axes they separate are the ones a single-category
// eval silently averages over.
//
// The point of the split is that a system can be excellent at one and useless at
// another: finding the note that states a fact is a different problem from
// finding the CURRENT version of a fact that was later corrected, and both are
// different from knowing when the palace simply does not hold the answer.
const (
	// CatSingle: one memory answers the question outright.
	CatSingle = "single"
	// CatCrossLingual: the question is in one language, the memory in another.
	// This palace is bilingual, and the embedder's multilingual claim has never
	// been tested on it.
	CatCrossLingual = "crosslingual"
	// CatTemporal: a fact was later corrected or superseded, and recall must
	// prefer the version that is still true.
	CatTemporal = "temporal"
	// CatAbsent: the palace does NOT hold the answer, and the right behaviour is
	// to return nothing. Untested until now, which means max_distance was folklore.
	CatAbsent = "absent"
)

// EvalCase is one labelled question: the query, the drawer that should come back
// for it, and what kind of question it is.
type EvalCase struct {
	Query    string
	Expect   string // drawer id; empty for CatAbsent, where any hit is a false positive
	Wing     string // optional scope, mirroring how the query would really be run
	Category string // one of the Cat* values; empty is treated as CatSingle
}

// category returns the case's category, defaulting to single-hop.
func (c EvalCase) category() string {
	if c.Category == "" {
		return CatSingle
	}
	return c.Category
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

	// ByCategory holds the same counts per question kind, because an average over
	// categories hides the failure that matters: a system can be perfect on
	// single-hop and blind on temporal, and the mean looks fine.
	ByCategory map[string]*CategoryMetrics
}

// CategoryMetrics is one arm's record within one category.
type CategoryMetrics struct {
	Cases    int
	Recall1  int
	Recall5  int
	MRR      float64
	NotFound int
	// FalsePositives counts CatAbsent cases where something was returned anyway.
	// It is the only metric here that a higher score makes WORSE.
	FalsePositives int
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

	// GoldRerank and AbsentRerank are the top-1 CROSS-ENCODER scores for the two
	// kinds of question. They exist because the distance distributions overlap —
	// so cosine cannot answer "do I know this?" — and a cross-encoder score is
	// the one number in the pipeline that was trained on exactly that question
	// rather than on similarity.
	GoldRerank   []float64
	AbsentRerank []float64

	// GoldDistances and AbsentDistances are the top-1 cosine distances for
	// answerable and unanswerable questions. They exist because max_distance —
	// the gate that decides when the palace should admit it knows nothing — was
	// inherited folklore, and the only way to set it honestly is to look at where
	// the two distributions actually sit.
	GoldDistances   []float64
	AbsentDistances []float64
}

// EvalCaseResult is where each arm put the expected drawer for one query. Rank 0
// means "not in the pool"; a case that is 0 everywhere is a retrieval miss, not a
// ranking one, and usually means the generated question shares no vocabulary with
// its own source.
type EvalCaseResult struct {
	Query    string
	Category string
	Ranks    map[EvalArm]int
}

// Evaluate scores every arm over the cases. poolSize is how many neighbours the
// vector search fetches per query; it bounds every arm equally, so a memory
// outside the pool is unreachable for all of them (counted as NotFound).
// Progress reports how far a run has got. An eval that prints nothing for
// several minutes is indistinguishable from one that has hung — which is exactly
// how the first one read — so Evaluate reports each case as it lands.
type Progress func(done, total int, query string, elapsed time.Duration)

// EvalOptions turn on arms that need something built first.
type EvalOptions struct {
	// Contextual adds the contextual-chunk arm. The index must already exist
	// (BuildContextualIndex); the arm is skipped rather than silently empty when
	// it does not.
	Contextual bool
}

func (s *Service) Evaluate(ctx context.Context, teamID string, cases []EvalCase, poolSize int, progress Progress) (EvalReport, error) {
	return s.EvaluateWith(ctx, teamID, cases, poolSize, EvalOptions{}, progress)
}

// EvaluateWith is Evaluate with the optional arms.
func (s *Service) EvaluateWith(ctx context.Context, teamID string, cases []EvalCase, poolSize int, opts EvalOptions, progress Progress) (EvalReport, error) {
	if poolSize <= 0 {
		poolSize = 50
	}
	arms := []EvalArm{ArmVector, ArmHybrid, ArmHybridCloset, ArmRRF}
	for _, w := range bm25Sweep {
		arms = append(arms, bm25Arm(w))
	}
	if opts.Contextual {
		arms = append(arms, ArmContextual)
	}
	if s.rerank != nil {
		arms = append(arms, ArmRRFReranked)
		arms = append(arms, ArmReranked)
		for _, w := range rerankSweep {
			arms = append(arms, rerankArm(w))
		}
	}
	byArm := map[EvalArm]*EvalMetrics{}
	for _, a := range arms {
		byArm[a] = &EvalMetrics{Arm: a, ByCategory: map[string]*CategoryMetrics{}}
	}
	report := EvalReport{}

	for i, c := range cases {
		started := time.Now()
		ranks, topDistance, topRerank, err := s.evalCase(ctx, teamID, c, arms, poolSize)
		if err != nil {
			return EvalReport{}, err
		}
		if progress != nil {
			progress(i+1, len(cases), c.Query, time.Since(started))
		}
		report.Details = append(report.Details, EvalCaseResult{Query: c.Query, Category: c.category(), Ranks: ranks})
		cat := c.category()
		for _, a := range arms {
			m := byArm[a]
			cm := m.ByCategory[cat]
			if cm == nil {
				cm = &CategoryMetrics{}
				m.ByCategory[cat] = cm
			}
			cm.Cases++

			// An absent case has no gold to rank: what is measured is whether the
			// system returned something confident anyway.
			if cat == CatAbsent {
				if topDistance >= 0 {
					report.AbsentDistances = append(report.AbsentDistances, topDistance)
				}
				continue
			}

			m.Cases++
			switch r := ranks[a]; {
			case r == 0:
				m.NotFound++
				cm.NotFound++
			default:
				m.MRR += 1 / float64(r)
				cm.MRR += 1 / float64(r)
				if r == 1 {
					m.Recall1++
					cm.Recall1++
				}
				if r <= 5 {
					m.Recall5++
					cm.Recall5++
				}
			}
		}
		if cat == CatAbsent {
			if topRerank != 0 {
				report.AbsentRerank = append(report.AbsentRerank, topRerank)
			}
		} else {
			if topDistance >= 0 {
				report.GoldDistances = append(report.GoldDistances, topDistance)
			}
			if topRerank != 0 {
				report.GoldRerank = append(report.GoldRerank, topRerank)
			}
		}
	}
	for _, a := range arms {
		m := byArm[a]
		if m.Cases > 0 {
			m.MRR /= float64(m.Cases)
		}
		for _, cm := range m.ByCategory {
			if cm.Cases > 0 {
				cm.MRR /= float64(cm.Cases)
			}
		}
		report.Arms = append(report.Arms, *m)
	}
	return report, nil
}

// evalCase runs one query through every arm and returns the 1-based rank of the
// expected drawer per arm (0 = absent).
func (s *Service) evalCase(ctx context.Context, teamID string, c EvalCase, arms []EvalArm, poolSize int) (map[EvalArm]int, float64, float64, error) {
	vec, err := s.embed.EmbedOne(ctx, c.Query)
	if err != nil {
		return nil, -1, 0, fmt.Errorf("embed eval query: %w", err)
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, poolSize, searchFilter(SearchQuery{Wing: c.Wing}))
	if err != nil {
		return nil, -1, 0, fmt.Errorf("eval vector search: %w", err)
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return nil, -1, 0, fmt.Errorf("load eval candidates: %w", err)
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

	// The nearest candidate's distance, whatever the arm: it is what a
	// max_distance gate would see, and the absent-case measurement needs it.
	topDistance := -1.0
	for _, p := range pool {
		if topDistance < 0 || p.distance < topDistance {
			topDistance = p.distance
		}
	}

	out := map[EvalArm]int{}
	topRerank := 0.0
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
		case ArmContextual:
			// A separate retrieval, because the arm's whole claim is about what
			// gets RETRIEVED. Ranking is the standard fusion so the delta cannot
			// be attributed to anything else.
			ctxHits, err := s.vectors.Search(ctx, contextualNamespace(teamID), vec, poolSize, searchFilter(SearchQuery{Wing: c.Wing}))
			if err != nil || len(ctxHits) == 0 {
				break // index not built for this team; the arm reports NotFound
			}
			ctxIDs := make([]string, len(ctxHits))
			for i, h := range ctxHits {
				ctxIDs[i] = h.ID
			}
			ctxRows, err := s.repo.GetMany(ctx, teamID, ctxIDs)
			if err != nil {
				return nil, -1, 0, fmt.Errorf("load contextual candidates: %w", err)
			}
			var ctxDocs []string
			var ctxDists []float64
			var ctxOrderIDs []string
			for _, h := range ctxHits {
				d, ok := ctxRows[h.ID]
				if !ok {
					continue
				}
				ctxDocs = append(ctxDocs, d.Content)
				ctxDists = append(ctxDists, distanceFromScore(h.Score))
				ctxOrderIDs = append(ctxOrderIDs, d.ID)
			}
			var ctxOrdered []int
			for _, r := range rankHybrid(c.Query, ctxDocs, ctxDists, nil) {
				ctxOrdered = append(ctxOrdered, r.Index)
			}
			out[arm] = rankOf(ctxOrderIDs, ctxOrdered, c.Expect)
			continue
		case ArmRRF:
			for _, r := range rankRRF(c.Query, docs, dists, boosts) {
				ordered = append(ordered, r.Index)
			}
		case ArmRRFReranked:
			fused := rankRRF(c.Query, docs, dists, boosts)
			hitsForRank := make([]SearchHit, len(pool))
			for i, p := range pool {
				hitsForRank[i] = SearchHit{Drawer: Drawer{ID: p.id, Content: p.content}}
			}
			for _, r := range s.applyRerankWith(ctx, c.Query, hitsForRank, fused, s.rerankWeight) {
				ordered = append(ordered, r.Index)
			}
		default:
			// A swept fusion arm: same pipeline, different lexical weight.
			if isBM25Arm := func() bool {
				for _, w := range bm25Sweep {
					if arm == bm25Arm(w) {
						for _, r := range rankHybridWeighted(c.Query, docs, dists, boosts, w) {
							ordered = append(ordered, r.Index)
						}
						return true
					}
				}
				return false
			}(); isBM25Arm {
				break
			}

			// Every reranked arm shares one path; only the blend weight differs.
			weight := s.rerankWeight
			for _, w := range rerankSweep {
				if arm == rerankArm(w) {
					weight = w
				}
			}
			// The cross-encoder refines the fused order, so it is handed the fused
			// SCORES too — reranking a list whose scores were dropped would blend
			// against zeroes and silently become the overwrite this measures.
			fused := rankHybrid(c.Query, docs, dists, boosts)
			hitsForRank := make([]SearchHit, len(pool))
			for i, p := range pool {
				hitsForRank[i] = SearchHit{Drawer: Drawer{ID: p.id, Content: p.content}}
			}
			for _, r := range s.applyRerankWith(ctx, c.Query, hitsForRank, fused, weight) {
				ordered = append(ordered, r.Index)
			}
		}
		poolIDs := make([]string, len(pool))
		for i, p := range pool {
			poolIDs[i] = p.id
		}
		out[arm] = rankOf(poolIDs, ordered, c.Expect)
	}
	return out, topDistance, topRerank, nil
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

// SampleDrawers returns a random sample of a team's drawers for eval question
// generation. It is a thin pass-through to the repo, exposed because the eval
// command lives outside this package and must not reach into the repository.
func (s *Service) SampleDrawers(ctx context.Context, teamID, wing string, n int) ([]Drawer, error) {
	return s.repo.ListRandom(ctx, teamID, wing, n)
}
