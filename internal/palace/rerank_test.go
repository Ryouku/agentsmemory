package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// keywordReranker is a stand-in cross-encoder: a document scores high when it
// contains the keyword, low when it does not. That is enough to prove the
// reranked order — not the fused order — decides the page, without a live TEI.
type keywordReranker struct {
	keyword  string
	calls    int
	gotQuery string
	gotDocs  []string
}

func (r *keywordReranker) Rerank(_ context.Context, query string, docs []string) ([]float64, error) {
	r.calls++
	r.gotQuery = query
	r.gotDocs = append([]string(nil), docs...)
	scores := make([]float64, len(docs))
	for i, d := range docs {
		if strings.Contains(strings.ToLower(d), r.keyword) {
			scores[i] = 0.99
		} else {
			scores[i] = 0.01
		}
	}
	return scores, nil
}

// brokenReranker is every failure mode the endpoint can present as one type: it
// always errors, exactly as a 500, a timeout or a refused connection would.
type brokenReranker struct{ calls int }

func (r *brokenReranker) Rerank(context.Context, string, []string) ([]float64, error) {
	r.calls++
	return nil, errors.New("rerank: connection refused")
}

// seedRerankCorpus files four drawers whose lexical overlap with the query is
// the OPPOSITE of their real relevance, so a passing test cannot be explained by
// the hybrid ranker accidentally agreeing with the reranker.
func seedRerankCorpus(t *testing.T, svc *Service, ctx context.Context, team string) {
	t.Helper()
	for _, d := range []struct{ room, content string }{
		{"decisions", "goose goose goose migrations run at boot from db/migrations"},
		{"decisions", "stripe stripe webhook secret comes from STRIPE_WEBHOOK_SECRET"},
		{"architecture", "the closet boost is a ranking signal, never a gate"},
		{"architecture", "caddy fronts the service and terminates TLS"},
	} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: d.room, Content: d.content}); err != nil {
			t.Fatalf("seed %q: %v", d.content, err)
		}
	}
}

// TestSearchRerankDecidesTheOrder is the feature: when a reranker is configured
// its score, not the fused hybrid score, orders the page. The corpus is seeded so
// the "closet" drawer is NOT the hybrid winner for this query — if the reranked
// order did not win, this drawer would not be first.
func TestSearchRerankDecidesTheOrder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-rerank"
	seedRerankCorpus(t, svc, ctx, team)

	rr := &keywordReranker{keyword: "closet"}
	svc.WithReranker(rr, 10)

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "goose migrations at boot", Limit: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search returned no hits")
	}
	if rr.calls != 1 {
		t.Errorf("reranker called %d times, want exactly 1", rr.calls)
	}
	if !strings.Contains(hits[0].Drawer.Content, "closet") {
		t.Errorf("top hit = %q, want the reranker's pick (the closet drawer)", hits[0].Drawer.Content)
	}
	if hits[0].RerankScore != 0.99 {
		t.Errorf("top hit RerankScore = %v, want 0.99 reported", hits[0].RerankScore)
	}
	// The fused score must still be reported: the point of keeping both is being
	// able to see when the two rankings disagree.
	if hits[0].Score == 0 {
		t.Error("top hit Score = 0, want the fused score still reported alongside")
	}
}

// TestSearchRerankFailsOpen is the availability guarantee. A reranker that is
// down must cost ordering quality, never recall: the search still answers, on
// the hybrid order, with no rerank score claimed.
func TestSearchRerankFailsOpen(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-failopen"
	seedRerankCorpus(t, svc, ctx, team)

	// Baseline: the same search with no reranker at all.
	want, err := svc.Search(ctx, team, SearchQuery{Query: "goose migrations at boot", Limit: 4})
	if err != nil {
		t.Fatalf("baseline Search: %v", err)
	}

	rr := &brokenReranker{}
	svc.WithReranker(rr, 10)
	got, err := svc.Search(ctx, team, SearchQuery{Query: "goose migrations at boot", Limit: 4})
	if err != nil {
		t.Fatalf("Search with a broken reranker returned an error, want fail-open: %v", err)
	}
	if rr.calls != 1 {
		t.Errorf("reranker called %d times, want exactly 1", rr.calls)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hits, want the baseline's %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Drawer.ID != want[i].Drawer.ID {
			t.Errorf("hit %d = %q, want baseline order %q", i, got[i].Drawer.Content, want[i].Drawer.Content)
		}
		if got[i].RerankScore != 0 {
			t.Errorf("hit %d claims RerankScore %v after a failed rerank", i, got[i].RerankScore)
		}
	}
}

// TestSearchRerankPoolBoundsTheCall pins the cost control: only the top `pool`
// fused candidates are cross-encoded, and everything past the pool keeps its
// fused position with no score claimed.
func TestSearchRerankPoolBoundsTheCall(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-pool"
	seedRerankCorpus(t, svc, ctx, team)

	var gotDocs int
	svc.WithReranker(rerankFunc(func(_ context.Context, _ string, docs []string) ([]float64, error) {
		gotDocs = len(docs)
		return make([]float64, len(docs)), nil
	}), 2)

	if _, err := svc.Search(ctx, team, SearchQuery{Query: "boot", Limit: 4}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotDocs != 2 {
		t.Errorf("cross-encoded %d docs, want the pool's 2", gotDocs)
	}
}

// TestSearchRerankContextFeedsOnlyTheCrossEncoder pins the SearchQuery.Context
// contract: it sharpens re-ranking and must never silently widen the query that
// retrieval already embedded.
func TestSearchRerankContextFeedsOnlyTheCrossEncoder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-ctx"
	seedRerankCorpus(t, svc, ctx, team)
	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "w", Room: "decisions",
		Content: "migrations evidence must stay selected by the user query " +
			strings.Repeat("neutral material with no query vocabulary. ", 55) +
			" AUDIT_CONTEXT_MARKER belongs to caller background, not evidence selection",
	}); err != nil {
		t.Fatalf("seed long memory: %v", err)
	}

	rr := &keywordReranker{keyword: "closet"}
	svc.WithReranker(rr, 10)
	if _, err := svc.Search(ctx, team, SearchQuery{Query: "migrations", Limit: 2}); err != nil {
		t.Fatalf("Search without context: %v", err)
	}
	withoutContext := documentContaining(t, rr.gotDocs, "migrations evidence must stay selected")

	if _, err := svc.Search(ctx, team, SearchQuery{
		Query:   "migrations",
		Context: "AUDIT_CONTEXT_MARKER",
		Limit:   2,
	}); err != nil {
		t.Fatalf("Search with context: %v", err)
	}
	if !strings.Contains(rr.gotQuery, "migrations") || !strings.Contains(rr.gotQuery, "AUDIT_CONTEXT_MARKER") {
		t.Errorf("rerank query = %q, want the query and the context", rr.gotQuery)
	}
	withContext := documentContaining(t, rr.gotDocs, "migrations evidence must stay selected")
	if withContext != withoutContext {
		t.Errorf("Context changed the memory evidence document:\nwithout: %q\nwith:    %q", withoutContext, withContext)
	}
}

func documentContaining(t *testing.T, docs []string, marker string) string {
	t.Helper()
	for _, doc := range docs {
		if strings.Contains(doc, marker) {
			return doc
		}
	}
	t.Fatalf("no reranker document contains %q: %#v", marker, docs)
	return ""
}

// rerankFunc adapts a plain function to Reranker for tests that only care about
// what the service passed in.
type rerankFunc func(context.Context, string, []string) ([]float64, error)

func (f rerankFunc) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	return f(ctx, query, docs)
}
