package palace

import (
	"context"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	got := tokenize("The LRU-cache, evicts! 2x x")
	// lowercased, split on non-word, tokens of length >= 2 only ("x" dropped).
	want := []string{"the", "lru", "cache", "evicts", "2x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize = %v, want %v", got, want)
	}
	if tokenize("") != nil {
		t.Fatal("empty text should tokenize to nil")
	}
}

func TestBM25ScoresPresenceBeatsAbsence(t *testing.T) {
	docs := []string{
		"the cache uses an lru eviction policy",
		"completely unrelated text about the weather",
	}
	scores := bm25Scores("lru cache eviction", docs)
	if scores[0] <= 0 {
		t.Fatalf("doc containing the query terms should score > 0, got %.3f", scores[0])
	}
	if scores[1] != 0 {
		t.Fatalf("doc with no query terms should score 0, got %.3f", scores[1])
	}
}

func TestBM25ScoresEmptyQueryOrCorpus(t *testing.T) {
	if got := bm25Scores("", []string{"anything"}); got[0] != 0 {
		t.Fatalf("empty query yields zero scores, got %.3f", got[0])
	}
	if got := bm25Scores("q", nil); len(got) != 0 {
		t.Fatalf("empty corpus yields no scores, got %d", len(got))
	}
}

// TestRankHybridLexicalPromotesOverVector pins the convex blend: a candidate with
// a WORSE vector distance but a strong lexical match must outrank one that is
// vector-closer yet lexically empty, because BM25 (weight 0.4) tips the sum.
//
//	A: distance 0.5 -> vecSim 0.5, bm25Norm 1.0 -> fused 0.6*0.5 + 0.4*1.0 = 0.70
//	B: distance 0.1 -> vecSim 0.9, bm25Norm 0.0 -> fused 0.6*0.9 + 0.4*0.0 = 0.54
func TestRankHybridLexicalPromotesOverVector(t *testing.T) {
	docs := []string{
		"the cache uses an lru eviction policy", // strong lexical match
		"a quiet meadow at dawn",                // no query terms
	}
	distances := []float64{0.5, 0.1}
	ranked := rankHybrid("lru cache eviction", docs, distances, nil)
	if ranked[0].Index != 0 {
		t.Fatalf("lexical match should rank first; got index %d (fused %.3f)", ranked[0].Index, ranked[0].Fused)
	}
	if ranked[0].BM25 <= 0 {
		t.Fatalf("top hit should carry a positive raw BM25, got %.3f", ranked[0].BM25)
	}
}

// TestRankHybridNoLexicalFallsBackToVector confirms that when no candidate matches
// the query lexically (all BM25 = 0), the order is pure vector — smallest distance
// first — so hybrid never does worse than vector-only on a lexical miss.
func TestRankHybridNoLexicalFallsBackToVector(t *testing.T) {
	docs := []string{"alpha text", "beta text", "gamma text"}
	distances := []float64{0.3, 0.1, 0.5}
	ranked := rankHybrid("zzz qqq", docs, distances, nil) // query terms appear in no doc
	gotOrder := []int{ranked[0].Index, ranked[1].Index, ranked[2].Index}
	want := []int{1, 0, 2} // by ascending distance: 0.1, 0.3, 0.5
	if !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("no-lexical order = %v, want vector order %v", gotOrder, want)
	}
}

// TestRankHybridClosetBoostLifts pins the closet signal: two candidates with
// equal vector distance and no lexical match are tied, but the one carrying a
// closet boost must rank first, and the boost is recorded on the result.
func TestRankHybridClosetBoostLifts(t *testing.T) {
	docs := []string{"alpha note", "beta note"}
	distances := []float64{0.5, 0.5}
	boosts := []float64{0.0, 0.40}
	ranked := rankHybrid("zzz qqq", docs, distances, boosts) // no lexical signal
	if ranked[0].Index != 1 {
		t.Fatalf("closet-boosted candidate should rank first, got index %d", ranked[0].Index)
	}
	if ranked[0].Boost != 0.40 {
		t.Fatalf("boost should be recorded on the result, got %v", ranked[0].Boost)
	}
}

// TestSearchAppliesClosetBoost is the end-to-end payoff: after mining a source,
// a search whose query matches that source's closet lifts the source's drawers
// with a visible ClosetBoost — the third frozen ranking signal, now wired.
func TestSearchAppliesClosetBoost(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	content := strings.Repeat("Kubernetes orchestrates the deployment pipeline. ", 20) +
		"\n\n# Kubernetes Pipeline\n\nWe deployed the pipeline to production successfully."
	if _, err := svc.Mine(ctx, team, MineInput{Content: content, Wing: "infra", Room: "ops", Source: "k8s"}); err != nil {
		t.Fatalf("mine: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "Kubernetes deployment pipeline", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected hits from the mined source")
	}
	var boosted bool
	for _, h := range hits {
		if h.ClosetBoost > 0 {
			boosted = true
		}
	}
	if !boosted {
		t.Fatal("expected a closet boost to be applied to the mined source's drawers")
	}
}

// TestSearchSurfacesBM25 is an end-to-end check that the hybrid path runs: the
// exact-phrase drawer is the top hit and its lexical BM25 component is populated.
func TestSearchSurfacesBM25(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "the cache uses an lru eviction policy"})
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "the button turns blue on hover"})

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "the cache uses an lru eviction policy"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if hits[0].Drawer.Content != "the cache uses an lru eviction policy" {
		t.Fatalf("exact phrase should top the hybrid ranking, got %q", hits[0].Drawer.Content)
	}
	if hits[0].BM25 <= 0 {
		t.Fatalf("top hybrid hit should carry a positive BM25, got %.3f", hits[0].BM25)
	}
}

// TestClosetBoostStrengthFadesWithDistance pins the fix for a real regression: a
// flat boost let one mediocre closet outrank every other signal, and this palace's
// eval measured recall@1 falling from 92% to 17% because of it.
//
// The distances are measured ones (see closetDistanceCap): a closet's own text,
// a genuinely related question, and an unrelated one.
func TestClosetBoostStrengthFadesWithDistance(t *testing.T) {
	cases := []struct {
		name     string
		distance float64
		wantMin  float64
		wantMax  float64
	}{
		{"the closet's own text", 0.114, 0.7, 1.0},
		{"a related question", 0.49, 0.05, 0.3},
		{"unrelated, just inside the old cap", 0.63, 0, 0},
		{"a cake recipe", 0.706, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := closetBoostStrength(c.distance)
			if got < c.wantMin || got > c.wantMax {
				t.Errorf("strength(%.3f) = %.3f, want between %.2f and %.2f", c.distance, got, c.wantMin, c.wantMax)
			}
		})
	}
}

// TestClosetBoostHasNoCliff: strength must reach zero exactly at the cap, so a
// hit either side of the boundary is not reordered by a step change.
func TestClosetBoostHasNoCliff(t *testing.T) {
	just := closetBoostStrength(closetDistanceCap - 0.001)
	if just <= 0 || just > 0.01 {
		t.Errorf("strength just inside the cap = %.4f, want a hair above zero", just)
	}
	if got := closetBoostStrength(closetDistanceCap); got != 0 {
		t.Errorf("strength at the cap = %.4f, want 0", got)
	}
}

// TestSnippetCentresOnTheQuery: the answer to a query is rarely in a memory's
// first paragraph, which is usually its heading — so a snippet cut from the
// front would routinely show the agent the wrong part and cost a second call.
func TestSnippetCentresOnTheQuery(t *testing.T) {
	content := strings.Repeat("preamble about unrelated setup. ", 20) +
		"the installer pins CLAUDE_CONFIG_DIR and the registration lands unread. " +
		strings.Repeat("trailing notes that do not matter. ", 20)

	got := Snippet(content, "installer pins CLAUDE_CONFIG_DIR", 200)
	if !strings.Contains(got, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("snippet missed the matching passage: %q", got)
	}
	if len([]rune(got)) > 210 {
		t.Errorf("snippet is %d runes, want ~200", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "…") {
		t.Error("a snippet taken from the middle must say text was removed before it")
	}
}

// TestSnippetLeavesShortContentAlone: most memories are already short, and
// truncating them would cost an id lookup for nothing.
func TestSnippetLeavesShortContentAlone(t *testing.T) {
	content := "a short memory"
	if got := Snippet(content, "memory", 400); got != content {
		t.Errorf("Snippet mangled short content: %q", got)
	}
}

// TestSnippetWithoutQueryTermsStillReturnsSomething: a query whose terms appear
// nowhere (or is all stop-length noise) must still yield a readable head rather
// than an empty string.
func TestSnippetWithoutQueryTermsStillReturnsSomething(t *testing.T) {
	content := strings.Repeat("some content that shares nothing with the query. ", 20)
	got := Snippet(content, "zzz", 100)
	if len(got) == 0 || !strings.HasSuffix(got, "…") {
		t.Errorf("want a truncated head, got %q", got)
	}
}

// TestRankRRFIgnoresScoreScale is the property RRF is chosen for: it ranks by
// position, so a retriever whose scores are unbounded (BM25) cannot swamp one
// whose scores are not (cosine). Multiplying every lexical score by a thousand
// must not change the fused order.
func TestRankRRFIgnoresScoreScale(t *testing.T) {
	docs := []string{
		"lru cache eviction policy explained",
		"unrelated notes about deployment",
		"cache eviction in the lru implementation",
	}
	distances := []float64{0.4, 0.2, 0.6}

	got := rankRRF("lru cache eviction", docs, distances, nil)
	order := []int{got[0].Index, got[1].Index, got[2].Index}

	// Same inputs with the lexical signal inflated: the documents are unchanged,
	// so BM25's own scale changes nothing about their ORDER, and RRF must agree.
	inflated := make([]string, len(docs))
	for i, d := range docs {
		inflated[i] = strings.Repeat(d+" ", 50)
	}
	again := rankRRF("lru cache eviction", inflated, distances, nil)
	if order[0] != again[0].Index {
		t.Errorf("RRF changed its winner when only score magnitude changed: %d then %d", order[0], again[0].Index)
	}
}

// TestRankRRFRewardsAgreement: a candidate both retrievers like should beat one
// that only a single retriever ranks first. That is the behaviour the fusion
// exists for, and the reason the smoothing constant flattens the very top.
func TestRankRRFRewardsAgreement(t *testing.T) {
	docs := []string{
		"cache eviction policy for the lru cache", // both like it
		"lru lru lru lru",                         // lexical only
		"a note about something else entirely",    // vector only
	}
	distances := []float64{0.25, 0.9, 0.2}

	got := rankRRF("lru cache eviction policy", docs, distances, nil)
	if got[0].Index != 0 {
		t.Errorf("agreed-on candidate ranked %d, want it first", got[0].Index)
	}
}

// TestLexicalCoverageSeesWhatBM25CanUse pins the quantity the adaptive weight is
// built on. A query whose terms appear in no candidate has nothing for BM25 to
// match — the cross-lingual case, where lexical fusion measured worse than vector
// alone — and a query whose terms appear in EVERY candidate cannot discriminate.
func TestLexicalCoverageSeesWhatBM25CanUse(t *testing.T) {
	docs := []string{
		"the installer pins CLAUDE_CONFIG_DIR on every subprocess",
		"the installer writes commands into the config dir",
		"unrelated notes about deployment windows",
	}

	// Distinctive terms present in some but not all: real signal.
	if got := LexicalCoverage("CLAUDE_CONFIG_DIR subprocess", docs); got < 0.9 {
		t.Errorf("coverage for distinctive matching terms = %.2f, want ~1", got)
	}
	// Nothing in common — the cross-lingual shape.
	if got := LexicalCoverage("kokie yra rezervacijų laiko juostos", docs); got != 0 {
		t.Errorf("coverage for terms absent from every candidate = %.2f, want 0", got)
	}
	// A term in every candidate discriminates nothing.
	if got := LexicalCoverage("installer", docs[:2]); got != 0 {
		t.Errorf("coverage for a term in every candidate = %.2f, want 0", got)
	}
}

// TestAdaptiveWeightCollapsesWithoutSignal: with no lexical overlap the fusion
// must fall back to the vector ranking rather than mixing in noise.
func TestAdaptiveWeightCollapsesWithoutSignal(t *testing.T) {
	docs := []string{"english note about batching", "another english note"}
	if w := adaptiveBM25Weight("lietuviškas klausimas apie rezervacijas", docs, 0.4); w != 0 {
		t.Errorf("adaptive weight with no shared vocabulary = %.2f, want 0", w)
	}
	if w := adaptiveBM25Weight("batching note", docs, 0.4); w <= 0 {
		t.Errorf("adaptive weight with shared vocabulary = %.2f, want > 0", w)
	}
}

// TestLexicalCoverageIDFDiscountsCommonTerms pins the confirmed review finding:
// a term in N-1 of N candidates must count as ~nothing, not as one full vote —
// the binary count read paraphrase queries as lexically informative and kept the
// lexical weight up exactly when BM25 was noise.
func TestLexicalCoverageIDFDiscountsCommonTerms(t *testing.T) {
	docs := []string{
		"deploy the batching service to the cluster",
		"deploy notes for the batching rollout",
		"deploy checklist for the batching gateway",
		"unrelated drawing of a cake recipe",
	}
	common := LexicalCoverageIDF("deploy batching", docs) // both terms in 3 of 4
	rare := LexicalCoverageIDF("cake", docs)              // one term in 1 of 4
	if common >= rare {
		t.Errorf("common-term coverage %.3f must be well below rare-term coverage %.3f", common, rare)
	}
	if binary := LexicalCoverage("deploy batching", docs); binary != 1.0 {
		t.Fatalf("precondition: the binary count reads the common terms as full signal, got %.3f", binary)
	}
}

// TestLexicalCoverageIDFCrossLanguageStaysZero: terms absent from every
// candidate carry no weight under either variant — the cross-language behaviour
// that motivated adaptive weighting must survive the IDF change.
func TestLexicalCoverageIDFCrossLanguageStaysZero(t *testing.T) {
	docs := []string{"reservation flow and payment gate", "payment provider timeout handling"}
	if c := LexicalCoverageIDF("lietuviškas klausimas apie mokėjimus", docs); c != 0 {
		t.Errorf("all-absent terms must yield coverage 0, got %.3f", c)
	}
}

// orderOf returns just the candidate indices from a ranked page, which is what
// the identity tests compare: two fusions agree when they order the page the
// same way, not when they produce the same numbers.
func orderOf(page []HybridScore) []int {
	out := make([]int, len(page))
	for i, h := range page {
		out[i] = h.Index
	}
	return out
}

// TestLexNormPageMaxGivesTheWinnerFullWeight pins the defect ADR-002 exists for.
//
// Dividing by the page maximum means the best candidate on the page scores a
// perfect 1.0 lexically however weak its match actually is — the normaliser has
// no idea what a good BM25 score looks like, only what the best one here was. On
// a page where every candidate matches one common term and misses the rare one,
// page-max still awards full lexical weight; an anchored normaliser, which
// divides by what the query COULD have scored, does not.
func TestLexNormPageMaxGivesTheWinnerFullWeight(t *testing.T) {
	// "cache" appears in no candidate, so it contributes nothing to any raw
	// score while still counting toward the ceiling. "eviction" is in all three,
	// so its IDF is near the floor. Every match here is weak by construction.
	query := "cache eviction"
	docs := []string{
		"eviction eviction happens twice here",
		"eviction happens once here",
		"eviction here",
	}

	raw, ceiling := bm25ScoresAndCeiling(query, docs)
	pm := lexNormPageMax(raw, ceiling)
	an := lexNormCeiling(raw, ceiling)

	if pm[0] != 1.0 {
		t.Fatalf("page-max should hand the winner exactly 1.0, got %.6f", pm[0])
	}
	if an[0] >= 0.1 {
		t.Fatalf("anchored norm should report this weak winner as weak, got %.6f", an[0])
	}
	// The winner is weak in absolute terms: that is the whole point.
	if raw[0] >= 0.5 {
		t.Fatalf("fixture no longer has a weak winner: raw %.6f", raw[0])
	}
}

// TestLexNormAnchoredIgnoresWhichCandidateWon pins the narrow property anchoring
// actually buys: winner-independence, not candidate-set independence.
//
// The fixture holds every pool statistic fixed by construction. The edited
// candidate keeps the same query-term SET and the same token count and changes
// only term frequency, so N, every df, every idf and avgdl are untouched — which
// makes the other candidates' raw scores bit-identical across the edit. Their
// anchored contributions must therefore be bit-identical too, while their
// page-max contributions shrink, because the divisor moved under them.
func TestLexNormAnchoredIgnoresWhichCandidateWon(t *testing.T) {
	const query = "cache eviction"
	// before[0] and after[0] both contain {cache, eviction} and both hold four
	// tokens; only the frequencies differ.
	before := []string{
		"cache eviction alpha beta",
		"cache cache eviction eviction gamma delta",
		"eviction only here",
	}
	after := []string{
		"cache cache cache eviction",
		"cache cache eviction eviction gamma delta",
		"eviction only here",
	}

	rawBefore, ceilBefore := bm25ScoresAndCeiling(query, before)
	rawAfter, ceilAfter := bm25ScoresAndCeiling(query, after)

	// The fixture is only meaningful if the edit actually moved the winner, and
	// only valid if it moved nothing else. Assert both before trusting anything.
	if rawBefore[1] <= rawBefore[0] {
		t.Fatalf("fixture: candidate 1 should start as the lexical winner (%.6f vs %.6f)", rawBefore[1], rawBefore[0])
	}
	if rawAfter[0] <= rawAfter[1] {
		t.Fatalf("fixture: the edit should make candidate 0 the winner (%.6f vs %.6f)", rawAfter[0], rawAfter[1])
	}
	if ceilBefore != ceilAfter {
		t.Fatalf("fixture: the ceiling moved (%.9f -> %.9f); the edit changed a pool statistic", ceilBefore, ceilAfter)
	}
	for _, i := range []int{1, 2} {
		if rawBefore[i] != rawAfter[i] {
			t.Fatalf("fixture: sibling %d's raw score moved (%.9f -> %.9f); the edit changed df or avgdl", i, rawBefore[i], rawAfter[i])
		}
	}

	anBefore := lexNormCeiling(rawBefore, ceilBefore)
	anAfter := lexNormCeiling(rawAfter, ceilAfter)
	pmBefore := lexNormPageMax(rawBefore, ceilBefore)
	pmAfter := lexNormPageMax(rawAfter, ceilAfter)

	for _, i := range []int{1, 2} {
		if anBefore[i] != anAfter[i] {
			t.Errorf("anchored contribution of sibling %d changed when another candidate won: %.9f -> %.9f", i, anBefore[i], anAfter[i])
		}
		if !(pmAfter[i] < pmBefore[i]) {
			t.Errorf("page-max contribution of sibling %d should shrink when a stronger winner arrives: %.9f -> %.9f", i, pmBefore[i], pmAfter[i])
		}
	}
}

// lexVocab is the small shared vocabulary the property tests draw pages from.
// It is deliberately small so query terms land in some candidates and miss
// others, which is the regime where the normalisers actually differ.
var lexVocab = []string{"cache", "eviction", "policy", "index", "shard", "replica", "quorum", "vector"}

// randomLexPage builds one candidate page: a two-term query, three to six
// candidates of varying length, and a cosine distance for each.
func randomLexPage(rng *rand.Rand) (query string, docs []string, distances []float64) {
	n := 3 + rng.Intn(4)
	docs = make([]string, n)
	distances = make([]float64, n)
	for i := range docs {
		terms := make([]string, 3+rng.Intn(6))
		for j := range terms {
			terms[j] = lexVocab[rng.Intn(len(lexVocab))]
		}
		docs[i] = strings.Join(terms, " ")
		distances[i] = rng.Float64() * 2
	}
	query = lexVocab[rng.Intn(len(lexVocab))] + " " + lexVocab[rng.Intn(len(lexVocab))]
	return query, docs, distances
}

// TestLexNormCeilingEqualsPageMaxAtTheRescaledWeight pins the identity between
// the two normalisers, and pins the WRONG one out.
//
// With no boost the fused score is a convex blend, so anchoring is a rescaling
// of the lexical weight and nothing more: ceiling at w orders a page exactly as
// page-max does at w' = w·a/(1 − w + w·a), where a = maxBM25/C. The first draft
// of ADR-002 claimed w' = w·a, which is the same only when a = 1 or w = 0. This
// test is what keeps the corrected algebra honest, so it asserts the equality
// AND asserts that the naive rescaling disagrees somewhere.
func TestLexNormCeilingEqualsPageMaxAtTheRescaledWeight(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	weights := []float64{0.15, 0.3, 0.45, 0.6, 0.8}

	naiveDisagreed := false
	pages := 0
	for trial := 0; trial < 200; trial++ {
		query, docs, distances := randomLexPage(rng)

		raw, ceiling := bm25ScoresAndCeiling(query, docs)
		var maxBM25 float64
		for _, s := range raw {
			if s > maxBM25 {
				maxBM25 = s
			}
		}
		if maxBM25 == 0 || ceiling == 0 {
			continue // no lexical signal: both normalisers are identically zero
		}
		pages++
		a := maxBM25 / ceiling
		if a >= 1 {
			t.Fatalf("a = maxBM25/C must be < 1, got %.6f", a)
		}

		for _, w := range weights {
			anchored := orderOf(rankFused(query, docs, distances, nil, 1-w, w, lexNormCeiling))

			wr := w * a / (1 - w + w*a)
			if got := orderOf(rankFused(query, docs, distances, nil, 1-wr, wr, lexNormPageMax)); !reflect.DeepEqual(got, anchored) {
				t.Fatalf("trial %d w=%.2f: page-max at the rescaled weight %.6f ordered %v, anchored ordered %v", trial, w, wr, got, anchored)
			}
			naive := w * a
			if got := orderOf(rankFused(query, docs, distances, nil, 1-naive, naive, lexNormPageMax)); !reflect.DeepEqual(got, anchored) {
				naiveDisagreed = true
			}
		}
	}
	if pages < 50 {
		t.Fatalf("fixture generated only %d usable pages; the property is barely exercised", pages)
	}
	if !naiveDisagreed {
		t.Fatal("w' = w·a never disagreed with the anchored ordering — the test cannot tell the corrected identity from the wrong one")
	}
}

// TestLexNormBoostHasNoEquivalentPageMaxWeight pins where the identity above
// stops holding: it needs the fused score to be a convex blend, and an additive
// closet boost is not rescaled along with the weights.
//
// The claim has to be stated carefully, and the first version of this test
// stated it wrongly. "No page-max weight reproduces the anchored ordering" is
// false on any single small page: four candidates admit only twenty-four
// orderings, so some w' matches by coincidence roughly two pages in three. The
// real difference is per-page EXISTENCE. Without a boost every page has a
// reproducing weight, and the previous test names it in closed form. With a
// boost, pages exist for which no weight anywhere in [0,1] works — which is what
// makes anchoring a different ranking rather than a reparametrisation of this
// one.
//
// So the assertion is on both populations at once: boost-free pages always have
// a reproducing weight, boosted pages sometimes do not.
func TestLexNormBoostHasNoEquivalentPageMaxWeight(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const w = 0.4

	var boostedWithout, plainWithout, pages int
	for trial := 0; trial < 300; trial++ {
		query, docs, distances := randomLexPage(rng)
		raw, ceiling := bm25ScoresAndCeiling(query, docs)
		var maxBM25 float64
		for _, s := range raw {
			if s > maxBM25 {
				maxBM25 = s
			}
		}
		if maxBM25 == 0 || ceiling == 0 {
			continue
		}
		pages++

		boosts := make([]float64, len(docs))
		for i := range boosts {
			if rng.Intn(2) == 0 {
				boosts[i] = rng.Float64() * 0.3
			}
		}
		if !somePageMaxWeightReproduces(query, docs, distances, boosts, w) {
			boostedWithout++
		}
		if !somePageMaxWeightReproduces(query, docs, distances, nil, w) {
			plainWithout++
		}
	}

	if pages < 100 {
		t.Fatalf("fixture generated only %d usable pages; the property is barely exercised", pages)
	}
	if plainWithout != 0 {
		t.Errorf("%d of %d boost-free pages had no reproducing page-max weight; without a boost the rescaled weight always exists", plainWithout, pages)
	}
	if boostedWithout == 0 {
		t.Errorf("every one of %d boosted pages had a reproducing page-max weight; with a boost the anchored ranking is supposed to leave the page-max family", pages)
	}
}

// somePageMaxWeightReproduces reports whether any page-max weight on a fine grid
// orders the page exactly as the anchored normaliser does at w.
func somePageMaxWeightReproduces(query string, docs []string, distances, boosts []float64, w float64) bool {
	anchored := orderOf(rankFused(query, docs, distances, boosts, 1-w, w, lexNormCeiling))
	for step := 0; step <= 1000; step++ {
		wp := float64(step) / 1000
		if reflect.DeepEqual(orderOf(rankFused(query, docs, distances, boosts, 1-wp, wp, lexNormPageMax)), anchored) {
			return true
		}
	}
	return false
}

// TestLexNormPageMaxIsTodaysArithmetic pins that naming the normaliser changed
// no behaviour: page-max reproduces the raw/maxBM25 fusion this code shipped
// before ADR-002, recomputed here by hand rather than by calling the same
// function under a different name.
func TestLexNormPageMaxIsTodaysArithmetic(t *testing.T) {
	const query = "cache eviction policy"
	docs := []string{
		"cache eviction policy described at length in this note",
		"cache policy",
		"eviction eviction eviction",
		"unrelated prose about something else entirely",
	}
	distances := []float64{0.8, 0.35, 0.6, 0.2}
	boosts := []float64{0, 0.25, 0, 0.25}
	const w = 0.4

	// The pre-change arithmetic, written out.
	raw := bm25Scores(query, docs)
	var maxBM25 float64
	for _, s := range raw {
		if s > maxBM25 {
			maxBM25 = s
		}
	}
	want := make([]HybridScore, len(docs))
	for i := range docs {
		norm := 0.0
		if maxBM25 > 0 {
			norm = raw[i] / maxBM25
		}
		want[i] = HybridScore{Index: i, Fused: (1-w)*vecSimFromDistance(distances[i]) + w*norm + boosts[i]}
	}
	sort.SliceStable(want, func(a, b int) bool { return want[a].Fused > want[b].Fused })

	got := rankHybridWeighted(query, docs, distances, boosts, w)
	if !reflect.DeepEqual(orderOf(got), orderOf(want)) {
		t.Fatalf("page-max ordering %v differs from the pre-change arithmetic %v", orderOf(got), orderOf(want))
	}
	for i := range got {
		if math.Abs(got[i].Fused-want[i].Fused) > 1e-12 {
			t.Fatalf("rank %d fused %.12f, pre-change arithmetic %.12f", i, got[i].Fused, want[i].Fused)
		}
	}
}

// TestLexNormSaturatingCompressesTheTop pins the saturating transform's
// contract, which no other test reaches — it is registered as an arm by a later
// task, so without this it would ship as code nothing asserts anything about.
//
// The property that distinguishes it from the ceiling transform is what it does
// to strong matches: both agree that weak is weak, but where the ceiling stays
// proportional all the way up, this one flattens, so the gap between a good
// match and an excellent one narrows.
func TestLexNormSaturatingCompressesTheTop(t *testing.T) {
	const ceiling = 4.0
	half := lexNormSaturatingKappa * ceiling
	raw := []float64{0, 0.1, half, 2 * half, 8 * half}

	got := lexNormSaturating(raw, ceiling)

	if got[0] != 0 {
		t.Errorf("a zero raw score must contribute zero, got %.6f", got[0])
	}
	if math.Abs(got[2]-0.5) > 1e-12 {
		t.Errorf("raw == kappa*C is the half-way point by definition, got %.6f", got[2])
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Errorf("saturating must stay strictly increasing: got[%d]=%.6f <= got[%d]=%.6f", i, got[i], i-1, got[i-1])
		}
		if got[i] >= 1 {
			t.Errorf("saturating must stay below 1, got[%d]=%.6f", i, got[i])
		}
	}

	// Compression: past the half-way point the saturating transform gives less
	// than the proportional one for the same raw score.
	prop := lexNormCeiling(raw, ceiling)
	for _, i := range []int{3, 4} {
		if got[i] >= prop[i] {
			t.Errorf("saturating should compress strong matches: got[%d]=%.6f >= ceiling[%d]=%.6f", i, got[i], i, prop[i])
		}
	}
}

// TestLexNormDegenerateInputsYieldZero pins the guard on every normaliser: a
// page with no lexical signal contributes zero to the fusion, never a NaN that
// would propagate silently into every fused score and sort the page at random.
//
// The guard is written against the divisor rather than against the reasoning for
// when the divisor can vanish, because that reasoning depends on the IDF formula
// and the formula is exactly the kind of thing a later task changes.
func TestLexNormDegenerateInputsYieldZero(t *testing.T) {
	norms := map[string]lexNorm{
		"page-max":   lexNormPageMax,
		"ceiling":    lexNormCeiling,
		"saturating": lexNormSaturating,
	}
	for name, norm := range norms {
		got := norm([]float64{0, 0, 0}, 0)
		if len(got) != 3 {
			t.Fatalf("%s returned %d values for 3 candidates", name, len(got))
		}
		for i, v := range got {
			if v != 0 {
				t.Errorf("%s with no lexical signal: candidate %d contributed %v, want 0", name, i, v)
			}
		}
		if empty := norm(nil, 0); len(empty) != 0 {
			t.Errorf("%s on an empty page returned %d values", name, len(empty))
		}
	}

	// The same through the fusion: a query whose terms appear nowhere must leave
	// the vector order untouched rather than producing NaN scores.
	docs := []string{"alpha beta", "gamma delta"}
	distances := []float64{0.4, 0.9}
	for name, norm := range norms {
		page := rankFused("nonexistent terminology", docs, distances, nil, 0.6, 0.4, norm)
		for _, h := range page {
			if math.IsNaN(h.Fused) {
				t.Fatalf("%s produced NaN on a page with no lexical signal", name)
			}
		}
		if page[0].Index != 0 {
			t.Errorf("%s: with no lexical signal the vector order should stand, got %v", name, orderOf(page))
		}
	}
}

// TestLexNormSaturatingBoundOnAchievableInput pins the bound that actually
// constrains the experiment, which is not the one the algebra suggests.
//
// TestLexNormSaturatingCompressesTheTop asserts only that the result stays below
// 1, and it can afford that loose bound because it feeds raw = 8·kappa·C = 4C —
// a value no real page can produce, since raw < C strictly. On achievable input
// the supremum is 2/3: raw/(raw + 0.5·C) < C/(1.5·C).
//
// That is not a curiosity, it is a fairness problem for the comparison ADR-002
// runs. Across bm25Sweep the saturating arm's lexical contribution tops out at
// two thirds of page-max's at the same weight — 0.40 against 0.60 at w=0.6 —
// so matching page-max at the top of the grid would need w = 0.9, outside it.
// Best-of-family is only a fair rule if the grid spans each family comparably,
// and for this family it is truncated at roughly two thirds of the range. On the
// identifier-query regime, where lexical fusion measured 1.000 against vector's
// 0.847, that is exactly the end of the range that matters.
//
// Recorded in ADR-002's Risks rather than fixed here: widening the sweep for one
// arm changes the experiment's design, which is the ADR's decision, not a test's.
func TestLexNormSaturatingBoundOnAchievableInput(t *testing.T) {
	const ceiling = 4.0
	// Sweep the whole achievable domain: raw ranges over (0, C).
	var max float64
	for i := 1; i < 1000; i++ {
		raw := ceiling * float64(i) / 1000
		got := lexNormSaturating([]float64{raw}, ceiling)[0]
		if got > max {
			max = got
		}
	}
	const sup = 2.0 / 3.0
	if max >= sup {
		t.Errorf("saturating reached %.6f on achievable input; the supremum is %.6f", max, sup)
	}
	if max < sup-0.01 {
		t.Errorf("saturating topped out at %.6f, well short of its %.6f supremum — the fixture no "+
			"longer sweeps the achievable range and the bound is untested", max, sup)
	}

	// And the gap against page-max at the same weight, which is what makes the
	// grid unfair rather than merely different.
	if pm := lexNormPageMax([]float64{ceiling * 0.999}, ceiling)[0]; pm <= max {
		t.Errorf("page-max reached %.6f and saturating %.6f; the truncation this test exists to "+
			"pin has gone away — check whether the sweep still needs its Risks note", pm, max)
	}
}
