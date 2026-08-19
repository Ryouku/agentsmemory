package palace

import (
	"context"
	"testing"
)

// mustAddOne files a memory expected to fit one chunk and returns that drawer.
// The temporal tests reason about individual drawers, so a silent multi-chunk
// split would invalidate them — hence the count assertion on top of mustAdd.
func mustAddOne(t *testing.T, svc *Service, team string, in AddInput) Drawer {
	t.Helper()
	drawers := mustAdd(t, svc, team, in)
	if len(drawers) != 1 {
		t.Fatalf("add %q: expected 1 drawer, got %d", in.Content, len(drawers))
	}
	return drawers[0]
}

// TestOlderNeighborPicksOlderDifferentSourceHit pins the three filters that make
// a temporal pair honest. The fake embedder maps bytes to dimensions, so near-
// identical contents are near-identical vectors — the distractors below are all
// CLOSER to the target than the correct answer, and each must lose to a filter:
// the same-source sibling (same session, not a superseded fact), the newer-dated
// note (a correction is not superseded by its own future), and the undated one
// (no chronology, so "older" is unprovable).
func TestOlderNeighborPicksOlderDifferentSourceHit(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-temporal"

	newer := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/cache.md", ContentDate: "2026-03-01",
		Content: "cache ttl is ninety seconds now"})
	// Same source in another room (same room would trigger the add-time source
	// purge and replace the target instead of coexisting with it).
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "incidents", SourceFile: "notes/cache.md", ContentDate: "2025-01-05",
		Content: "cache ttl is ninety seconds now."})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/later.md", ContentDate: "2026-05-01",
		Content: "cache ttl is ninety seconds today"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/undated.md",
		Content: "cache ttl is ninety second notes"})
	want := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/old.md", ContentDate: "2025-06-10",
		Content: "cache ttl was thirty seconds"})

	got, ok, err := svc.OlderNeighbor(ctx, team, newer, 50)
	if err != nil {
		t.Fatalf("OlderNeighbor: %v", err)
	}
	if !ok {
		t.Fatalf("expected an older neighbour, got none")
	}
	if got.ID != want.ID {
		t.Fatalf("picked %q (source %q, date %q), want the older different-source drawer %q",
			got.ID, got.SourceFile, got.ContentDate, want.ID)
	}
}

// TestOlderNeighborReportsNoPairInsteadOfFabricating: a dated drawer whose only
// neighbours are undated or newer has nothing it supersedes, and the honest
// answer is ok=false — not an error, and never a pair conjured from whatever is
// nearest.
func TestOlderNeighborReportsNoPairInsteadOfFabricating(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-temporal-none"

	newer := mustAddOne(t, svc, team, AddInput{Wing: "wing_beta", Room: "decisions", SourceFile: "notes/limit.md", ContentDate: "2026-02-01",
		Content: "request limit is two hundred per minute"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_beta", Room: "decisions", SourceFile: "notes/other.md",
		Content: "request limit is two hundred per minute roughly"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_beta", Room: "decisions", SourceFile: "notes/future.md", ContentDate: "2026-07-01",
		Content: "request limit is five hundred per minute"})

	got, ok, err := svc.OlderNeighbor(ctx, team, newer, 50)
	if err != nil {
		t.Fatalf("OlderNeighbor: %v", err)
	}
	if ok {
		t.Fatalf("fabricated a pair with %q (source %q, date %q); corpus holds nothing dated older", got.ID, got.SourceFile, got.ContentDate)
	}
}

// TestOlderNeighborRejectsUndatedTarget: "older than nothing" is undefined, so
// asking on behalf of an undated drawer is a caller bug that must fail loudly
// rather than quietly return no pair.
func TestOlderNeighborRejectsUndatedTarget(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-temporal-undated"

	d := mustAddOne(t, svc, team, AddInput{Wing: "wing_beta", Room: "decisions", SourceFile: "notes/x.md",
		Content: "the retry budget is three attempts"})
	if _, _, err := svc.OlderNeighbor(ctx, team, d, 50); err == nil {
		t.Fatalf("expected an error for an undated target drawer, got none")
	}
}

// TestDatedDrawersFiltersUndated: the temporal eval samples only drawers whose
// chronology is known, and the filter belongs in the query — a page of undated
// drawers would leave the eval with nothing to pair.
func TestDatedDrawersFiltersUndated(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-dated"

	dated := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/a.md", ContentDate: "2025-11-20",
		Content: "the queue drains every five minutes"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/b.md",
		Content: "the queue is backed by redis streams"})
	mustAddOne(t, svc, team, AddInput{Wing: "wing_other", Room: "decisions", SourceFile: "notes/c.md", ContentDate: "2025-12-01",
		Content: "the worker pool holds eight workers"})

	got, err := svc.DatedDrawers(ctx, team, "wing_acme", 100)
	if err != nil {
		t.Fatalf("DatedDrawers: %v", err)
	}
	if len(got) != 1 || got[0].ID != dated.ID {
		t.Fatalf("expected exactly the dated wing_acme drawer %q, got %+v", dated.ID, got)
	}
}

// TestOlderNeighborStaysInWing pins the wing confinement the doc comment
// promises: a superseded fact and its correction belong to one project, so an
// older, semantically CLOSER drawer in another wing must lose to a farther
// eligible neighbour in the target's own wing.
func TestOlderNeighborStaysInWing(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-wingscope"

	newer := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/deploy.md", ContentDate: "2026-04-01",
		Content: "deploys run from the release branch now"})
	// Closest possible older text — but in another wing, so it must not pair.
	mustAddOne(t, svc, team, AddInput{Wing: "wing_beta", Room: "decisions", SourceFile: "notes/other.md", ContentDate: "2025-02-02",
		Content: "deploys run from the release branch now."})
	inWing := mustAddOne(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "notes/old.md", ContentDate: "2025-03-03",
		Content: "deploys used to run from the trunk branch"})

	got, ok, err := svc.OlderNeighbor(ctx, team, newer, 50)
	if err != nil {
		t.Fatalf("OlderNeighbor: %v", err)
	}
	if !ok || got.ID != inWing.ID {
		t.Fatalf("expected the same-wing older drawer %q, got ok=%v id=%q", inWing.ID, ok, got.ID)
	}
}
