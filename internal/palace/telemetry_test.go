package palace

import (
	"context"
	"errors"
	"testing"
)

// TestRerankedRecordsWhatHappened.
//
// `Reranked` was `boolToInt(s.rerank != nil)` — whether a cross-encoder is
// CONFIGURED, not whether one ran. With RERANK_WEIGHT=0 and a rerank URL set,
// applyRerankWith returns before scoring anything, the cross-encoder is never
// invoked, and every search event still claimed it was. ADR-001 calibrates its
// abstention threshold from these rows, so the lie is not cosmetic: it is
// training data for a gate.
//
// The same state also makes the pool widen the vector fetch for nothing —
// candidateK grows to rerankPool because a reranker exists, and then nothing
// cross-encodes it.
func TestRerankedRecordsWhatHappened(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	mustAdd(t, svc, "team-tel", AddInput{Wing: "w", Room: "r", Content: "the retry budget is three attempts"})

	// A reranker IS configured, and the weight hands it none of the decision.
	rr := &fakeReranker{}
	svc = svc.WithReranker(rr, 50).WithRerankWeight(0)

	if _, err := svc.Search(ctx, "team-tel", SearchQuery{Query: "retry budget", Limit: 5}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if rr.called != 0 {
		t.Fatalf("the cross-encoder was invoked %d time(s) at weight 0", rr.called)
	}

	ev := lastSearchEvent(t, svc, "team-tel")
	if ev.Reranked != 0 {
		t.Errorf("the search event claims reranking (Reranked=%d) that never happened — "+
			"ADR-001 calibrates its abstention threshold on these rows", ev.Reranked)
	}
}

// TestRerankedIsTrueWhenItActuallyRan is the other half. A test that only checks
// the false case passes when Reranked is hardcoded to 0.
func TestRerankedIsTrueWhenItActuallyRan(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	mustAdd(t, svc, "team-tel2", AddInput{Wing: "w", Room: "r", Content: "the retry budget is three attempts"})

	rr := &fakeReranker{}
	svc = svc.WithReranker(rr, 50).WithRerankWeight(0.5)

	if _, err := svc.Search(ctx, "team-tel2", SearchQuery{Query: "retry budget", Limit: 5}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if rr.called == 0 {
		t.Fatal("the cross-encoder never ran at weight 0.5, so this test cannot observe the true case")
	}
	if ev := lastSearchEvent(t, svc, "team-tel2"); ev.Reranked != 1 {
		t.Errorf("reranking ran and the event does not record it (Reranked=%d)", ev.Reranked)
	}
}

// TestRerankPoolDoesNotWidenTheFetchForNothing: with the weight at zero nothing
// will be cross-encoded, so paying for a wider vector fetch and a bigger GetMany
// join buys exactly nothing on every search.
func TestRerankPoolDoesNotWidenTheFetchForNothing(t *testing.T) {
	if candidateKFor(5, true, 50, 0) != candidateKFor(5, false, 50, 0) {
		t.Error("a configured-but-disabled reranker still widens the candidate fetch")
	}
	if candidateKFor(5, true, 50, 0.5) != 50 {
		t.Error("an ACTIVE reranker must still widen the fetch to its pool — that is where the " +
			"accuracy comes from")
	}
}

// lastSearchEvent reads the newest recorded search for a team. Telemetry has no
// read API beyond the aggregates, and a test that asserted on an aggregate could
// not tell "one reranked search" from "two searches, one reranked".
func lastSearchEvent(t *testing.T, svc *Service, teamID string) searchEventRow {
	t.Helper()
	var row searchEventRow
	if err := svc.repo.db.Model(&searchEventRow{}).
		Where("team_id = ?", teamID).
		Order("created_at DESC, id DESC").
		First(&row).Error; err != nil {
		t.Fatalf("read search event: %v", err)
	}
	return row
}

// TestDegradedRerankIsNotRecordedAsAPass.
//
// A reranker that errors fails OPEN — the page falls back to the fused order,
// which is correct behaviour and deliberately so. But the search then did NOT
// rerank, and recording that it did is the same lie as the weight-0 case, on a
// path that fires exactly when something is wrong. This palace has published an
// eval table with a silently degraded reranker in it before.
func TestDegradedRerankIsNotRecordedAsAPass(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	mustAdd(t, svc, "team-deg", AddInput{Wing: "w", Room: "r", Content: "the retry budget is three attempts"})

	svc = svc.WithReranker(&fakeReranker{err: errRerankProbe}, 50).WithRerankWeight(0.5)
	if _, err := svc.Search(ctx, "team-deg", SearchQuery{Query: "retry budget", Limit: 5}); err != nil {
		t.Fatalf("search must fail open, not error: %v", err)
	}
	if ev := lastSearchEvent(t, svc, "team-deg"); ev.Reranked != 0 {
		t.Errorf("a search whose reranker failed recorded Reranked=%d — the row says a "+
			"cross-encoder ranked this page and none did", ev.Reranked)
	}
}

var errRerankProbe = errors.New("reranker unavailable")
