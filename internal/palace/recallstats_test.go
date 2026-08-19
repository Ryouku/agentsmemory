package palace

import (
	"context"
	"testing"
	"time"
)

// TestRecallStatsSeparatesAnsweredFromAsked is the measurement that matters: a
// palace that is written to constantly while every recall comes back empty looks,
// from drawer counts alone, exactly like one that is working.
func TestRecallStatsSeparatesAnsweredFromAsked(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-stats"

	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the scheduler plans in two tiers so a long job cannot starve a short one"})
	mustAdd(t, svc, team, AddInput{Wing: "wing_beta", Room: "decisions", Content: "scan results are stored per audit run, not per page"})

	// One answered search per wing, plus one that finds nothing.
	if _, err := svc.Search(ctx, team, SearchQuery{Query: "two-tier planning", Wing: "wing_acme"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, err := svc.Search(ctx, team, SearchQuery{Query: "scan results per audit run", Wing: "wing_beta"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, err := svc.Search(ctx, team, SearchQuery{Query: "kubernetes ingress annotations", Wing: "wing_acme", MaxDistance: 0.01}); err != nil {
		t.Fatalf("search: %v", err)
	}

	stats, err := svc.RecallStats(ctx, team, time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if stats.Searches != 3 {
		t.Errorf("searches = %d, want 3", stats.Searches)
	}
	if stats.Answered != 2 {
		t.Errorf("answered = %d, want 2", stats.Answered)
	}
	if stats.AnsweredPct() != 67 {
		t.Errorf("answered pct = %d, want 67", stats.AnsweredPct())
	}
	if stats.Writes != 2 {
		t.Errorf("writes = %d, want 2", stats.Writes)
	}

	byWing := map[string]WingRecall{}
	for _, w := range stats.Wings {
		byWing[w.Wing] = w
	}
	acme, ok := byWing["wing_acme"]
	if !ok {
		t.Fatalf("wing_acme missing from %v", stats.Wings)
	}
	if acme.Searches != 2 || acme.Answered != 1 || acme.Drawers != 1 {
		t.Errorf("wing_acme = %+v; want 2 searches, 1 answered, 1 drawer", acme)
	}
	if acme.AnsweredPct() != 50 {
		t.Errorf("wing_acme answered pct = %d, want 50", acme.AnsweredPct())
	}

	// The unanswered query is the actionable output: it names a memory the team
	// went looking for and does not have.
	if len(stats.Unanswered) != 1 || stats.Unanswered[0] != "kubernetes ingress annotations" {
		t.Errorf("unanswered = %v; want the kubernetes query", stats.Unanswered)
	}
}

// TestRecallStatsShowsWriteOnlyWings: a wing that is filled and never read is the
// pattern most worth surfacing, and a search-only report would hide it entirely.
func TestRecallStatsShowsWriteOnlyWings(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-writeonly"

	mustAdd(t, svc, team, AddInput{Wing: "wing_unused", Room: "decisions", Content: "nobody ever asks about this"})

	stats, err := svc.RecallStats(ctx, team, time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if len(stats.Wings) != 1 || stats.Wings[0].Wing != "wing_unused" {
		t.Fatalf("wings = %+v; want wing_unused present", stats.Wings)
	}
	if w := stats.Wings[0]; w.Drawers != 1 || w.Searches != 0 {
		t.Errorf("wing_unused = %+v; want 1 drawer, 0 searches", w)
	}
}
