package palace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
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
	// ArmProduction goes through Service.Search itself — the code agents actually
	// call — rather than this file's reconstruction of it. It exists because an
	// eval that reimplements the pipeline can score well while production is
	// broken, and has: a mis-set rerank URL made every real search silently fall
	// back to hybrid while the eval's own arms looked fine.
	ArmProduction EvalArm = "production (Search)"
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

// ArmAdaptive picks the lexical weight per query from how much lexical signal
// that query actually has — the alternative to a constant nobody can set right
// for every palace.
const ArmAdaptive EvalArm = "fusion bm25=auto"

// ArmAdaptiveIDF is ArmAdaptive with each query term weighted by its IDF
// instead of counted once. The candidate to replace the binary coverage: a
// term in N-1 candidates reads as signal to the binary count and as ~nothing
// to this one.
const ArmAdaptiveIDF EvalArm = "fusion bm25=auto-idf"

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
	// CatReal: the query is one an agent actually ran against this palace,
	// replayed from the search_events telemetry. The gold is not a generator's
	// seed note but a judged set — every pooled candidate an LLM judge marked
	// relevant — which is what breaks the circularity of generated questions:
	// nothing about a real query was manufactured to suit any arm's feature.
	CatReal = "real"
	// CatAbsent: the palace does NOT hold the answer, and the right behaviour is
	// to return nothing. Untested until now, which means max_distance was folklore.
	CatAbsent = "absent"
)

// EvalCase is one labelled question: the query, the drawer that should come back
// for it, and what kind of question it is.
type EvalCase struct {
	Query  string
	Expect string // drawer id; empty for CatAbsent, where any hit is a false positive
	// ExpectAny lists drawer ids that each count as a correct answer — the
	// judged qrels of a CatReal case. A generated case has exactly one gold by
	// construction; a real query can be answered by several memories, and
	// scoring only one of them turns valid answers into retrieval errors.
	ExpectAny []string `json:",omitempty"`
	Wing      string   // optional scope, mirroring how the query would really be run
	Category  string   // one of the Cat* values; empty is treated as CatSingle
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

	// Ranks are the per-case 1-based ranks (0 = miss), aligned with the case
	// order, so intervals and paired comparisons can be computed after the fact —
	// including by a reader of the results file, not only by this process.
	Ranks []int

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

	// Warnings are conditions that changed what was measured — a degraded
	// reranker, a skipped arm. They are part of the result, because a table whose
	// caveats live only in scrollback gets quoted without them.
	Warnings []string

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
	// AllowDegraded lets the run continue without the reranked arms when the
	// configured reranker fails its preflight probe. The default is to REFUSE:
	// this eval has already once produced a full table of "reranked" numbers that
	// were silently the hybrid order, and a loud stop is the only reliable cure.
	AllowDegraded bool
}

// Evaluate scores every arm over the cases. poolSize is how many neighbours the
// vector search fetches per query; it bounds every arm equally, so a memory
// outside the pool is unreachable for all of them (counted as NotFound).
func (s *Service) Evaluate(ctx context.Context, teamID string, cases []EvalCase, poolSize int, progress Progress) (EvalReport, error) {
	return s.EvaluateWith(ctx, teamID, cases, poolSize, EvalOptions{}, progress)
}

// EvaluateWith is Evaluate with the optional arms.
func (s *Service) EvaluateWith(ctx context.Context, teamID string, cases []EvalCase, poolSize int, opts EvalOptions, progress Progress) (EvalReport, error) {
	if poolSize <= 0 {
		poolSize = 50
	}
	report := EvalReport{}

	// Preflight the reranker with ONE probe before scoring hundreds of cases
	// against it. A dead reranker degrades every reranked arm to the hybrid order
	// SILENTLY — this exact table has been published with that failure in it — so
	// the default is to stop and say what is wrong.
	if s.rerank != nil {
		// The probe mirrors a real call — pool-sized batch, chunk-sized documents —
		// because a token-sized probe has already passed while every real call
		// failed on the batch and sequence limits it never touched.
		probeDocs := make([]string, s.rerankPool)
		probeText := strings.Repeat("preflight document text sized like a real drawer chunk. ", 30)
		for i := range probeDocs {
			probeDocs[i] = probeText
		}
		if _, err := s.rerank.Rerank(ctx, "preflight probe", probeDocs); err != nil {
			if !opts.AllowDegraded {
				return EvalReport{}, fmt.Errorf(
					"the configured reranker failed its preflight (%w) — every reranked arm would silently score as plain hybrid, "+
						"which has already produced one wrong table too many. Fix it, or pass --allow-degraded to run without those arms", err)
			}
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("reranker preflight failed (%v): reranked arms were DROPPED, not silently degraded", err))
			s = s.withoutReranker()
		}
	}

	arms := []EvalArm{ArmVector, ArmHybrid, ArmHybridCloset, ArmRRF}
	for _, w := range bm25Sweep {
		arms = append(arms, bm25Arm(w))
	}
	arms = append(arms, ArmAdaptive, ArmAdaptiveIDF)
	// The reality check runs LAST and always: this is the arm that exercises the
	// code agents actually call. It went missing once already — built, documented,
	// and never appended — which an adversarial review caught and no table did.
	arms = append(arms, ArmProduction)
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

	degradedCases := 0
	for i, c := range cases {
		started := time.Now()
		ranks, topDistance, topRerank, scored, degraded, err := s.evalCase(ctx, teamID, c, arms, poolSize)
		if err != nil {
			return EvalReport{}, err
		}
		if degraded {
			degradedCases++
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

			// An absent case has no gold to rank; its distance evidence is taken
			// once per CASE below, not here — appending inside this loop copied it
			// once per arm and silently weighted the separation medians.
			if cat == CatAbsent {
				continue
			}

			m.Cases++
			m.Ranks = append(m.Ranks, ranks[a])
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
			if topDistance >= 0 {
				report.AbsentDistances = append(report.AbsentDistances, topDistance)
			}
			if scored {
				report.AbsentRerank = append(report.AbsentRerank, topRerank)
			}
		} else {
			if topDistance >= 0 {
				report.GoldDistances = append(report.GoldDistances, topDistance)
			}
			if scored {
				report.GoldRerank = append(report.GoldRerank, topRerank)
			}
		}
	}
	if degradedCases > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"the reranker failed mid-run on %d of %d case(s): reranked arms scored the HYBRID order there — treat their numbers as a floor, not a measurement", degradedCases, len(cases)))
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
// The rerankScored return says whether a cross-encoder actually scored the top
// candidate. It is a separate boolean rather than a test on the score, because
// the score's zero is only "unscored" by convention: a sigmoid backend never
// emits exactly 0, but a logit backend can, and the abstention data must not
// quietly drop the case that lands there.
func (s *Service) evalCase(ctx context.Context, teamID string, c EvalCase, arms []EvalArm, poolSize int) (ranksOut map[EvalArm]int, topDistanceOut, topRerankOut float64, rerankScored, degraded bool, errOut error) {
	vec, err := s.embed.EmbedOne(ctx, c.Query)
	if err != nil {
		return nil, -1, 0, false, false, fmt.Errorf("embed eval query: %w", err)
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, poolSize, searchFilter(SearchQuery{Wing: c.Wing}))
	if err != nil {
		return nil, -1, 0, false, false, fmt.Errorf("eval vector search: %w", err)
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return nil, -1, 0, false, false, fmt.Errorf("load eval candidates: %w", err)
	}

	// The gold is a MEMORY, not a chunk of one.
	//
	// A long memory is stored as several chunks and any of them answers the
	// question as far as the agent is concerned — it reads the memory, not the
	// slice. Scoring only the exact chunk marks a correct retrieval as a miss, and
	// unevenly: an arm that changes WHICH chunk surfaces (contextual chunking, for
	// one) is penalised precisely for doing its job. In this corpus every sampled
	// gold was a chunk of a multi-chunk memory, so the bias applied to every
	// number measured before this.
	goldSet := make(map[string]bool, 1+len(c.ExpectAny))
	for _, id := range append([]string{c.Expect}, c.ExpectAny...) {
		if id == "" { // CatAbsent cases have no gold to resolve
			continue
		}
		switch gold, err := s.repo.Get(ctx, teamID, id); {
		case err == nil:
			if gold.ParentID != "" {
				goldSet[gold.ParentID] = true
			} else {
				goldSet[gold.ID] = true
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			// A saved case can outlive its drawer: re-mining a source purges the
			// old ids and mints new ones. Swallowing that scored the dead case as
			// an all-arm retrieval miss — and the pool diagnosis then told the
			// operator to raise --pool, misdiagnosing stale case data as a
			// retrieval failure. Found by adversarial review, minutes after a
			// full re-mine had made it live.
			return nil, -1, 0, false, false, fmt.Errorf(
				"eval case %q: its expected drawer %s no longer exists — the corpus was re-mined since this case file was generated; regenerate the cases", c.Query, id)
		default:
			return nil, -1, 0, false, false, fmt.Errorf("load eval gold %s: %w", id, err)
		}
	}

	// One candidate list, ordered by vector distance — the input every arm
	// re-orders. Building it once is what makes the comparison fair.
	type candidate struct {
		id       string
		memory   string // the parent memory this chunk belongs to, or its own id
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
		memory := d.ID
		if d.ParentID != "" {
			memory = d.ParentID
		}
		pool = append(pool, candidate{id: d.ID, memory: memory, content: d.Content, distance: distanceFromScore(h.Score), source: d.SourceFile})
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

	// One cross-encoder call per case, shared by every arm that needs it. The
	// scores do not depend on the blend weight, and fetching them per arm made the
	// slowest step in the pipeline run six times over identical inputs — which is
	// why a twelve-case run grew from one minute a case to three.
	fusedForRerank := rankHybrid(c.Query, docs, dists, boosts)
	hitsForRerank := make([]SearchHit, len(pool))
	for i, p := range pool {
		hitsForRerank[i] = SearchHit{Drawer: Drawer{ID: p.id, Content: p.content}}
	}
	var rerankScores []float64
	rerankFailed := false
	if s.rerank != nil {
		if rerankScores = s.RerankScoresFor(ctx, c.Query, hitsForRerank, fusedForRerank); rerankScores == nil {
			rerankFailed = true
		}
	}

	out := map[EvalArm]int{}
	// The abstention gate's calibration data, taken from the production arm and
	// nowhere else.
	prodRerank, prodScored := 0.0, false
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
		case ArmProduction:
			// The real path, telemetry suppressed so an eval does not pollute the
			// palace's own recall statistics.
			page, err := s.Search(ctx, teamID, SearchQuery{
				Query: c.Query, Wing: c.Wing, Limit: MaxSearchLimit,
				// Production callers pass the default distance gate; omitting it
				// here would measure a search nobody actually runs.
				MaxDistance: DefaultMaxDistance, SkipTelemetry: true,
			})
			if err != nil {
				break // scored as a miss; the error itself surfaces via NotFound
			}
			// The abstention gate will run on THIS path, so its calibration data
			// has to come from here too. The reranked arm's top-1 can be a
			// different document — production fuses adaptively or by rank, that
			// arm always blends at a fixed weight — and a threshold calibrated on
			// one and applied to the other is calibrated on nothing.
			if len(page) > 0 {
				prodRerank, prodScored = page[0].RerankScore, page[0].Reranked
			}
			pageIDs := make([]string, len(page))
			pageOrder := make([]int, len(page))
			for i, h := range page {
				memory := h.Drawer.ID
				if h.Drawer.ParentID != "" {
					memory = h.Drawer.ParentID
				}
				pageIDs[i] = memory
				pageOrder[i] = i
			}
			out[arm] = rankOf(pageIDs, pageOrder, goldSet)
			continue
		case ArmContextual:
			// A separate retrieval, because the arm's whole claim is about what
			// gets RETRIEVED. Ranking is the standard fusion so the delta cannot
			// be attributed to anything else.
			ctxHits, err := s.vectors.Search(ctx, contextualNamespace(teamID), vec, poolSize, searchFilter(SearchQuery{Wing: c.Wing}))
			if err != nil {
				// A store failure is not "index not built", and scoring it as a
				// miss would let a dead backend read as a bad embedding.
				return nil, -1, 0, false, false, fmt.Errorf("contextual index search: %w", err)
			}
			if len(ctxHits) == 0 {
				break // index not built (or empty) for this scope: reported NotFound
			}
			ctxIDs := make([]string, len(ctxHits))
			for i, h := range ctxHits {
				ctxIDs[i] = h.ID
			}
			ctxRows, err := s.repo.GetMany(ctx, teamID, ctxIDs)
			if err != nil {
				return nil, -1, 0, false, false, fmt.Errorf("load contextual candidates: %w", err)
			}
			var ctxDocs []string
			var ctxDists []float64
			var ctxOrderIDs []string
			for _, h := range ctxHits {
				d, ok := ctxRows[h.ID]
				if !ok {
					continue
				}
				memory := d.ID
				if d.ParentID != "" {
					memory = d.ParentID
				}
				ctxDocs = append(ctxDocs, d.Content)
				ctxDists = append(ctxDists, distanceFromScore(h.Score))
				ctxOrderIDs = append(ctxOrderIDs, memory)
			}
			var ctxOrdered []int
			for _, r := range rankHybrid(c.Query, ctxDocs, ctxDists, nil) {
				ctxOrdered = append(ctxOrdered, r.Index)
			}
			out[arm] = rankOf(ctxOrderIDs, ctxOrdered, goldSet)
			continue
		case ArmRRF:
			for _, r := range rankRRF(c.Query, docs, dists, boosts) {
				ordered = append(ordered, r.Index)
			}
		case ArmRRFReranked:
			// RRF changes the ORDER the head is taken in, so it needs its own
			// scores — the one case where a second call is real information.
			rrfFused := rankRRF(c.Query, docs, dists, boosts)
			rrfScores := s.RerankScoresFor(ctx, c.Query, hitsForRerank, rrfFused)
			for _, r := range BlendRerank(rrfFused, rrfScores, s.rerankWeight) {
				ordered = append(ordered, r.Index)
			}
		case ArmAdaptive:
			for _, r := range rankHybridAdaptive(c.Query, docs, dists, boosts, hybridBM25Weight) {
				ordered = append(ordered, r.Index)
			}
		case ArmAdaptiveIDF:
			for _, r := range rankHybridAdaptiveIDF(c.Query, docs, dists, boosts, hybridBM25Weight) {
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
			// Scores were fetched once for this case; only the blend differs per
			// arm, so no arm pays for inference again.
			reranked := BlendRerank(fusedForRerank, rerankScores, weight)
			for _, r := range reranked {
				ordered = append(ordered, r.Index)
			}
		}
		poolIDs := make([]string, len(pool))
		for i, p := range pool {
			poolIDs[i] = p.memory
		}
		out[arm] = rankOf(poolIDs, ordered, goldSet)
	}
	// Production's score, or nothing. There is deliberately no fallback to the
	// fixed-weight reranked arm: substituting it would refill the distribution
	// with the very mismatch this measurement exists to avoid — that arm blends
	// at a constant weight and can top out on a different document. A case where
	// production returned no scored hit contributes nothing, which is honest.
	return out, topDistance, prodRerank, prodScored, rerankFailed, nil
}

// rankOf returns the 1-based position of the expected id in an ordering, or 0
// when it is absent — which is the signal that the memory never made the pool,
// a retrieval miss rather than a ranking one.
func rankOf(ids []string, ordered []int, expect map[string]bool) int {
	if len(expect) == 0 {
		return 0
	}
	for rank, idx := range ordered {
		if idx < 0 || idx >= len(ids) {
			continue
		}
		// ids are MEMORY ids here, so several candidates can carry the same one
		// (sibling chunks). The first position ANY relevant memory reaches is the
		// rank that matters: that is where the agent sees an answer. Generated
		// cases carry a single-member set; judged real-query cases carry every
		// memory the judge accepted.
		if expect[ids[idx]] {
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

// SampleSearchQueries exposes the telemetry sampler to the eval command, which
// lives outside this package and must not reach into the repository.
func (s *Service) SampleSearchQueries(ctx context.Context, teamID, wing string, n int) ([]string, error) {
	return s.repo.SampleSearchQueries(ctx, teamID, wing, n)
}

// DatedDrawers lists a team's drawers that carry a content date, optionally
// scoped to a wing — the population the temporal eval samples from, since a
// "later corrected" fact needs a chronology to be later than.
func (s *Service) DatedDrawers(ctx context.Context, teamID, wing string, limit int) ([]Drawer, error) {
	return s.repo.DatedDrawers(ctx, teamID, wing, limit)
}

// OlderNeighbor finds the distractor half of a temporal eval pair: the drawer
// semantically closest to d whose content date is non-empty and strictly older.
// Such a neighbour is the corpus's own record of the superseded version of d's
// fact, so a question about the fact's CURRENT state is answered correctly only
// when ranking puts d above it — which is exactly what CatTemporal measures.
//
// Hits from d's own source are skipped: chunks of one source are the same
// session split for embedding, not a fact that was later corrected, and pairing
// them would test chunk ordering rather than temporal preference. The skip
// requires a non-empty source, because a source-less drawer is a standalone
// memory (see Add) and two of them sharing "" proves nothing about their origin.
//
// Dates are normalized through findDate before comparing: Add stores
// content_date as the caller sent it, so a hand-filed drawer can carry
// "26 June 2026" — comparing that lexicographically against "2026-08-01" would
// order by spelling, not chronology. A date that does not normalize disqualifies
// the candidate; on the target it disqualifies the pair (ok=false), because the
// question "what is newer" cannot be answered about an unparseable date.
// ok=false means the corpus holds no such neighbour — the caller skips the
// drawer rather than fabricating a pair.
func (s *Service) OlderNeighbor(ctx context.Context, teamID string, d Drawer, poolSize int) (Drawer, bool, error) {
	if d.ContentDate == "" {
		// "Older than nothing" has no answer; the caller sampled the wrong
		// population (DatedDrawers is the right one), so say so rather than
		// silently returning no pair.
		return Drawer{}, false, fmt.Errorf("older neighbour of drawer %s: it has no content date, so \"older\" is undefined — sample dated drawers only", d.ID)
	}
	dDate := findDate(d.ContentDate)
	if dDate == "" {
		return Drawer{}, false, nil
	}
	if poolSize <= 0 {
		poolSize = 50
	}
	vec, err := s.embed.EmbedOne(ctx, d.Content)
	if err != nil {
		return Drawer{}, false, fmt.Errorf("embed drawer for temporal pairing: %w", err)
	}
	// The same retrieval seam evalCase uses, scoped to the drawer's own wing: a
	// superseded fact and its correction belong to one project, and a cross-wing
	// "pair" would be two projects coincidentally near in embedding space.
	hits, err := s.vectors.Search(ctx, teamID, vec, poolSize, searchFilter(SearchQuery{Wing: d.Wing}))
	if err != nil {
		return Drawer{}, false, fmt.Errorf("temporal pair search: %w", err)
	}
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return Drawer{}, false, fmt.Errorf("load temporal pair candidates: %w", err)
	}
	// Hits arrive best-first, so the first survivor of the filters is the
	// closest eligible neighbour.
	for _, h := range hits {
		cand, ok := rows[h.ID]
		if !ok {
			continue // orphan vector (row deleted) — skip, as search does
		}
		if cand.ID == d.ID {
			continue
		}
		if d.SourceFile != "" && cand.SourceFile == d.SourceFile {
			continue
		}
		candDate := findDate(cand.ContentDate)
		if candDate == "" || candDate >= dDate {
			continue
		}
		return cand, true, nil
	}
	return Drawer{}, false, nil
}

// withoutReranker returns a shallow copy of the service with the reranker
// removed, for a degraded eval run — the palace itself is untouched.
func (s *Service) withoutReranker() *Service {
	clone := *s
	clone.rerank = nil
	return &clone
}
