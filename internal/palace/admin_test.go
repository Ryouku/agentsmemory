package palace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

func TestMergeWing(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "old-a", Room: "r", Content: "a memory about cats that clears the floor easily"})
	mustAdd(t, svc, team, AddInput{Wing: "old-b", Room: "s", Content: "another memory about dogs that clears the floor too"})

	res, err := svc.MergeWing(ctx, team, []string{"old-a", "old-b"}, "merged")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Drawers != 2 {
		t.Fatalf("expected 2 drawers relabeled, got %d", res.Drawers)
	}

	moved, _ := svc.List(ctx, team, "merged", "", 50, 0)
	if len(moved) != 2 {
		t.Fatalf("merged wing should hold both drawers, got %d", len(moved))
	}
	for _, w := range []string{"old-a", "old-b"} {
		left, _ := svc.List(ctx, team, w, "", 50, 0)
		if len(left) != 0 {
			t.Fatalf("source wing %q should be empty after merge, got %d", w, len(left))
		}
	}

	// Idempotent / self-merge: merging the target into itself changes nothing.
	again, err := svc.MergeWing(ctx, team, []string{"merged"}, "merged")
	if err != nil || again.Drawers != 0 {
		t.Fatalf("self-merge should be a no-op, got %+v err=%v", again, err)
	}
}

// seedWing fills a wing with everything a wing can own — drawers and closets (via
// mine, which writes both) plus a hallway — so a delete has all four record kinds
// to purge rather than only the easy one.
func seedWing(t *testing.T, svc *Service, team, wing string) {
	t.Helper()
	ctx := context.Background()
	content := strings.Repeat("Postgres stores the source of truth. The cache layer fronts it. ", 30)
	if _, err := svc.Mine(ctx, team, MineInput{Content: content, Wing: wing, Room: "notes", Source: wing + ".md"}); err != nil {
		t.Fatalf("mine %s: %v", wing, err)
	}
	if err := svc.repo.ReplaceWingHallways(ctx, team, wing, []Hallway{{
		ID: wing + "-h1", TeamID: team, Wing: wing, EntityA: "Postgres", EntityB: "cache",
		CoOccurrence: 2, Rooms: []string{"notes"}, Label: "seeded",
	}}); err != nil {
		t.Fatalf("seed hallway in %s: %v", wing, err)
	}
}

func TestDeleteWing(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	seedWing(t, svc, team, "doomed")
	seedWing(t, svc, team, "keeper")
	// A tunnel spanning both wings: deleting one end must take the link with it,
	// while the wing at the far end survives untouched.
	if _, err := svc.CreateTunnel(ctx, team, TunnelInput{
		SourceWing: "doomed", SourceRoom: "notes",
		TargetWing: "keeper", TargetRoom: "notes",
		Label: "shared storage",
	}, "2026-08-19T00:00:00Z"); err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	before, err := svc.repo.CountWing(ctx, team, "doomed")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if before.Drawers == 0 || before.Closets == 0 || before.Hallways != 1 || before.Tunnels != 1 {
		t.Fatalf("seed did not populate every record kind: %+v", before)
	}

	res, err := svc.DeleteWing(ctx, team, "doomed", "doomed")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res != before {
		t.Fatalf("delete should report exactly what the wing held: got %+v want %+v", res, before)
	}

	empty, err := svc.repo.CountWing(ctx, team, "doomed")
	if err != nil {
		t.Fatalf("recount: %v", err)
	}
	if (empty != DeleteWingResult{Wing: "doomed"}) {
		t.Fatalf("wing should hold nothing after delete, got %+v", empty)
	}

	// The sibling wing keeps its drawers, closets and hallway — only the tunnel it
	// shared with the deleted wing is gone, because a tunnel needs both endpoints.
	kept, err := svc.repo.CountWing(ctx, team, "keeper")
	if err != nil {
		t.Fatalf("count keeper: %v", err)
	}
	if kept.Drawers == 0 || kept.Closets == 0 || kept.Hallways != 1 {
		t.Fatalf("surviving wing was damaged: %+v", kept)
	}
	if kept.Tunnels != 0 {
		t.Fatalf("the shared tunnel should be gone, got %d", kept.Tunnels)
	}

	// Vectors go with the rows. Only "keeper" is left, so each namespace must hold
	// exactly its records and nothing more — a search index still carrying the
	// deleted wing's points would keep scoring candidates that have no drawer.
	assertPointCount(t, svc, team, kept.Drawers)
	assertPointCount(t, svc, closetNamespace(team), kept.Closets)

	// Idempotent: deleting an absent wing removes nothing and is not an error, so a
	// re-run after a partial failure is safe.
	again, err := svc.DeleteWing(ctx, team, "doomed", "doomed")
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if (again != DeleteWingResult{Wing: "doomed"}) {
		t.Fatalf("second delete should be a no-op, got %+v", again)
	}
}

// assertPointCount checks a vector namespace holds exactly want points. The test
// store is SQLite, the source of truth, so it can enumerate what it holds.
func assertPointCount(t *testing.T, svc *Service, namespace string, want int64) {
	t.Helper()
	sot, ok := svc.vectors.(store.SourceOfTruth)
	if !ok {
		t.Fatalf("test vector store cannot enumerate points")
	}
	pts, err := sot.AllPoints(context.Background(), namespace)
	if err != nil {
		t.Fatalf("all points in %q: %v", namespace, err)
	}
	if int64(len(pts)) != want {
		t.Fatalf("namespace %q holds %d points, want %d", namespace, len(pts), want)
	}
}

func TestDeleteWingRefusesWithoutMatchingConfirmation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	seedWing(t, svc, team, "doomed")
	before, err := svc.repo.CountWing(ctx, team, "doomed")
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	for _, confirm := range []string{"", "doomedd", "Doomed", "yes", "doomed2"} {
		res, err := svc.DeleteWing(ctx, team, "doomed", confirm)
		if !errors.Is(err, ErrConfirmMismatch) {
			t.Fatalf("confirm %q should be refused with ErrConfirmMismatch, got %+v err=%v", confirm, res, err)
		}
		// The refusal must name the blast radius — that is the point of the guard.
		if !strings.Contains(err.Error(), "drawers") {
			t.Fatalf("refusal should report what it would have deleted, got %q", err)
		}
	}

	after, err := svc.repo.CountWing(ctx, team, "doomed")
	if err != nil {
		t.Fatalf("recount: %v", err)
	}
	if after != before {
		t.Fatalf("a refused delete must change nothing: before %+v after %+v", before, after)
	}

	// Surrounding whitespace is tolerated, because SanitizeName trims the wing name
	// too: a pasted "doomed " names the same wing, so it must confirm the same wing.
	if _, err := svc.DeleteWing(ctx, team, "doomed", "  doomed  "); err != nil {
		t.Fatalf("a whitespace-padded confirmation names the same wing: %v", err)
	}
}

func TestMemoriesFiledAway(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	empty, err := svc.MemoriesFiledAway(ctx, team)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if empty.Count != 0 || empty.Message != "No memories filed yet" {
		t.Fatalf("empty palace summary wrong: %+v", empty)
	}

	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a filed memory long enough to be a drawer"})
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "s", Content: "a second filed memory long enough as well"})

	res, err := svc.MemoriesFiledAway(ctx, team)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if res.Count != 2 || res.Wings != 1 || res.Rooms != 2 {
		t.Fatalf("summary counts wrong: %+v", res)
	}
	if res.LastFiledAt == "" {
		t.Fatalf("expected a last_filed_at, got empty")
	}
}
