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

	stats, err := svc.RecallStats(ctx, team, "", time.Hour, 10)
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

// TestRecallStatsFiltersEverySectionByWing prevents a scoped report from
// leaking another project's raw unanswered query or aggregate topology.
func TestRecallStatsFiltersEverySectionByWing(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-scoped-stats"

	mustAdd(t, svc, team, AddInput{Wing: "wing_alpha", Room: "private", Content: "alpha private release ritual"})
	mustAdd(t, svc, team, AddInput{Wing: "wing_beta", Room: "private", Content: "beta private release ritual"})
	recordUnanswered(svc, team, "wing_alpha", "ALPHA-UNANSWERED-MARKER", time.Now().Add(-time.Minute))
	recordUnanswered(svc, team, "wing_beta", "BETA-UNANSWERED-MARKER", time.Now())

	stats, err := svc.RecallStats(ctx, team, "wing_beta", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if stats.Searches != 1 || stats.Writes != 1 {
		t.Errorf("scoped totals = %d searches, %d writes; want 1 and 1", stats.Searches, stats.Writes)
	}
	if len(stats.Wings) != 1 || stats.Wings[0].Wing != "wing_beta" || stats.Wings[0].Drawers != 1 {
		t.Errorf("scoped wings = %+v; want only wing_beta", stats.Wings)
	}
	if len(stats.Unanswered) != 1 || stats.Unanswered[0] != "BETA-UNANSWERED-MARKER" {
		t.Errorf("scoped unanswered = %v; want only beta marker", stats.Unanswered)
	}
	if len(stats.Suggestions) != 1 || stats.Suggestions[0].Wing != "wing_beta" {
		t.Errorf("scoped suggestions = %+v; want only wing_beta", stats.Suggestions)
	}
}

// TestRecallStatsShowsWriteOnlyWings: a wing that is filled and never read is the
// pattern most worth surfacing, and a search-only report would hide it entirely.
func TestRecallStatsShowsWriteOnlyWings(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-writeonly"

	mustAdd(t, svc, team, AddInput{Wing: "wing_unused", Room: "decisions", Content: "nobody ever asks about this"})

	stats, err := svc.RecallStats(ctx, team, "", time.Hour, 10)
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

// recordUnanswered files one no-hit search event directly through recordSearch,
// with an explicit timestamp so ordering assertions do not race the clock the
// way events recorded through Search (which stamps "now", at second resolution)
// would.
func recordUnanswered(svc *Service, team, wing, query string, at time.Time) {
	svc.repo.recordSearch(context.Background(), searchEventRow{
		TeamID:    team,
		Wing:      wing,
		Query:     query,
		Hits:      0,
		CreatedAt: at.UTC().Format(time.RFC3339),
	})
}

// TestSuggestionsCollapseParaphrases is the flywheel's core promise: five
// phrasings of the same missing memory must read as ONE suggestion with a count,
// while genuinely different topics — even ones sharing a token — stay apart.
func TestSuggestionsCollapseParaphrases(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-suggest"
	now := time.Now()

	// Three phrasings of one ask: reordered, elaborated, and plain. The newest
	// (recorded closest to now) supplies the representative query.
	recordUnanswered(svc, team, "wing_acme", "kubernetes ingress annotations", now.Add(-1*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "how do kubernetes ingress annotations work", now.Add(-2*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "annotations for kubernetes ingress", now.Add(-3*time.Minute))
	// Distinct topics: zero shared tokens, and one that shares only "kubernetes"
	// (1/3 overlap, under the 60% bar) — the near-miss that a sloppier rule merges.
	recordUnanswered(svc, team, "wing_acme", "sqlite fts5 tokenizer choice", now.Add(-4*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "kubernetes pod restarts", now.Add(-5*time.Minute))
	// An answered search must never become a suggestion, whatever its query.
	svc.repo.recordSearch(ctx, searchEventRow{
		TeamID: team, Wing: "wing_acme", Query: "kubernetes ingress annotations",
		Hits: 2, TopScore: 0.9, CreatedAt: now.Add(-30 * time.Second).UTC().Format(time.RFC3339),
	})

	stats, err := svc.RecallStats(ctx, team, "", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if len(stats.Suggestions) != 3 {
		t.Fatalf("suggestions = %+v; want 3 (paraphrases collapsed, topics apart)", stats.Suggestions)
	}
	top := stats.Suggestions[0]
	if top.Query != "kubernetes ingress annotations" || top.Times != 3 || top.Wing != "wing_acme" {
		t.Errorf("top suggestion = %+v; want the kubernetes ask, 3x, wing_acme", top)
	}
	// Equal counts fall back to recency: the sqlite ask is newer than pod restarts.
	if stats.Suggestions[1].Query != "sqlite fts5 tokenizer choice" || stats.Suggestions[1].Times != 1 {
		t.Errorf("second suggestion = %+v; want sqlite ask, 1x", stats.Suggestions[1])
	}
	if stats.Suggestions[2].Query != "kubernetes pod restarts" || stats.Suggestions[2].Times != 1 {
		t.Errorf("third suggestion = %+v; want pod restarts, 1x", stats.Suggestions[2])
	}
}

// TestSuggestionsAreWingScoped: "write this memory" is only actionable with a
// WHERE, so the same ask against two wings is two suggestions, and an unscoped
// ask is named as such rather than merged into either.
func TestSuggestionsAreWingScoped(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-wings"
	now := time.Now()

	recordUnanswered(svc, team, "wing_acme", "redis eviction policy", now.Add(-1*time.Minute))
	recordUnanswered(svc, team, "wing_beta", "redis eviction policy", now.Add(-2*time.Minute))
	recordUnanswered(svc, team, "", "redis eviction policy", now.Add(-3*time.Minute))

	stats, err := svc.RecallStats(ctx, team, "", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if len(stats.Suggestions) != 3 {
		t.Fatalf("suggestions = %+v; want 3 (one per wing, unscoped separate)", stats.Suggestions)
	}
	wings := map[string]bool{}
	for _, s := range stats.Suggestions {
		if s.Times != 1 {
			t.Errorf("suggestion %+v; want times 1 — wings must not merge", s)
		}
		wings[s.Wing] = true
	}
	for _, want := range []string{"wing_acme", "wing_beta", "(unscoped)"} {
		if !wings[want] {
			t.Errorf("no suggestion for %s in %+v", want, stats.Suggestions)
		}
	}
}

// TestSuggestionsEmptyTelemetry: a palace nobody searched has nothing to suggest,
// and the report must stay silent rather than invent work.
func TestSuggestionsEmptyTelemetry(t *testing.T) {
	svc := newTestService(t)
	stats, err := svc.RecallStats(context.Background(), "team-empty", "", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	if len(stats.Suggestions) != 0 {
		t.Errorf("suggestions = %+v; want none for empty telemetry", stats.Suggestions)
	}
	if len(stats.SuggestionLines(3)) != 0 {
		t.Errorf("suggestion lines = %v; want none", stats.SuggestionLines(3))
	}
}

// TestSuggestionsOrderCountThenRecency pins the ranking contract: most-asked
// first, and among equals the gap somebody hit most recently.
func TestSuggestionsOrderCountThenRecency(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-order"
	now := time.Now()

	// B asked three times (all old), A twice (newest last-ask), C twice (older
	// last-ask): expect B, A, C.
	recordUnanswered(svc, team, "wing_acme", "goose migration ordering", now.Add(-40*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "goose migration ordering", now.Add(-35*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "goose migration ordering", now.Add(-30*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "chromem reconcile on boot", now.Add(-20*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "chromem reconcile on boot", now.Add(-1*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "qdrant payload indexes", now.Add(-25*time.Minute))
	recordUnanswered(svc, team, "wing_acme", "qdrant payload indexes", now.Add(-10*time.Minute))

	stats, err := svc.RecallStats(ctx, team, "", time.Hour, 10)
	if err != nil {
		t.Fatalf("recall stats: %v", err)
	}
	var got []string
	for _, s := range stats.Suggestions {
		got = append(got, s.Query)
	}
	want := []string{"goose migration ordering", "chromem reconcile on boot", "qdrant payload indexes"}
	if len(got) != len(want) {
		t.Fatalf("suggestions = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("suggestions = %v; want %v (count first, then recency)", got, want)
		}
	}
	if stats.Suggestions[0].Times != 3 || stats.Suggestions[1].Times != 2 || stats.Suggestions[2].Times != 2 {
		t.Errorf("times = %d/%d/%d; want 3/2/2", stats.Suggestions[0].Times, stats.Suggestions[1].Times, stats.Suggestions[2].Times)
	}
}

// TestSuggestionLinesTransportFormat pins the "  write: " prefix the Stop hook
// greps for, and the cap that keeps the report a nudge instead of a dump.
func TestSuggestionLinesTransportFormat(t *testing.T) {
	stats := RecallStats{Suggestions: []MemorySuggestion{
		{Query: "kubernetes ingress annotations", Times: 3, Wing: "wing_acme", LastAsked: "2026-01-01T10:00:00Z"},
		{Query: "sqlite fts5 tokenizer choice", Times: 1, Wing: "wing_beta", LastAsked: "2026-01-01T09:00:00Z"},
		{Query: "redis eviction policy", Times: 1, Wing: "wing_beta", LastAsked: "2026-01-01T08:00:00Z"},
	}}
	lines := stats.SuggestionLines(2)
	if len(lines) != 2 {
		t.Fatalf("lines = %v; want the cap respected at 2", lines)
	}
	if lines[0] != "  write: 3x kubernetes ingress annotations [wing_acme]" {
		t.Errorf("line[0] = %q; the hook greps this exact shape", lines[0])
	}
	if lines[1] != "  write: 1x sqlite fts5 tokenizer choice [wing_beta]" {
		t.Errorf("line[1] = %q", lines[1])
	}
}
