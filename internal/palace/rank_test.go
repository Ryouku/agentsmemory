package palace

import (
	"context"
	"reflect"
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
