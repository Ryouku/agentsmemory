package palace

import (
	"context"
	"reflect"
	"strings"
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

// closetFixture mines one source and files an unmined rival covering the same
// vocabulary, so a closet boost has something to lift the gold ABOVE. Without a
// rival every candidate carries the same boost and the ordering never moves,
// which would let the closet tests pass while measuring nothing.
func closetFixture(t *testing.T, svc *Service, team string) (query string, gold string) {
	t.Helper()
	ctx := context.Background()

	mined := strings.Repeat("Kubernetes orchestrates the deployment pipeline. ", 20) +
		"\n\n# Kubernetes Pipeline\n\nThe canary rollout guards the deployment pipeline."
	if _, err := svc.Mine(ctx, team, MineInput{Content: mined, Wing: "infra", Room: "ops", Source: "k8s-runbook"}); err != nil {
		t.Fatalf("mine: %v", err)
	}

	// The rival is filed, not mined, so it has no closet and takes no boost.
	rival, err := svc.Add(ctx, team, AddInput{
		Wing: "infra", Room: "ops", SourceFile: "notes",
		Content: "The canary rollout guards the deployment pipeline in staging as well.",
	})
	if err != nil {
		t.Fatalf("add rival: %v", err)
	}
	if len(rival.Drawers) == 0 {
		t.Fatal("rival drawer was not filed")
	}

	drawers, err := svc.List(ctx, team, "infra", "ops", 100, 0)
	if err != nil {
		t.Fatalf("list drawers: %v", err)
	}
	for _, d := range drawers {
		if d.SourceFile == "k8s-runbook" {
			return "canary rollout deployment pipeline", d.ID
		}
	}
	t.Fatal("no drawer from the mined source")
	return "", ""
}

// TestArmBoostsDimension pins the classification every rank call now goes
// through: an arm carries the closet prior only if its name says so.
//
// This is the defect the task exists for. evalCase built one boosts slice and
// handed it to fourteen arms, twelve of which never mention closets — so a
// decision about the lexical weight was read off a table that was silently
// measuring a curation prior at the same time. The test enumerates the REGISTERED
// arms rather than a hand-written list, so an arm added later is classified or
// the test fails.
func TestArmBoostsDimension(t *testing.T) {
	closet := []float64{0.1, 0.2, 0.3}
	carriers := map[EvalArm]bool{ArmHybridCloset: true, ArmReranked: true}

	arms := evalArms(EvalOptions{Contextual: true}, true)
	if len(arms) < 14 {
		t.Fatalf("expected the full arms list, got %d arms", len(arms))
	}
	seenCarrier := 0
	for _, arm := range arms {
		got := armBoosts(arm, closet)
		if carriers[arm] {
			seenCarrier++
			if !reflect.DeepEqual(got, closet) {
				t.Errorf("%s is named for the closet prior and must carry it, got %v", arm, got)
			}
			continue
		}
		if got != nil {
			t.Errorf("%s does not mention closets and must not carry the prior, got %v", arm, got)
		}
	}
	if seenCarrier != len(carriers) {
		t.Errorf("only %d of the %d closet-named arms are registered; the classification is untested for the rest", seenCarrier, len(carriers))
	}
}

// TestClosetArmMeasuresClosetsWhenServedPriorIsOff pins that an arm measures
// what its name says whatever the server is configured to serve.
//
// Before this task the closet slice came from s.closetBoosts, which returns
// nothing when the served scale is 0 — correct for Search, and wrong for an arm
// whose entire purpose is to show what the prior does. With CLOSET_BOOST=0 about
// to become the default, the arm that decides whether that was right would have
// measured a disabled prior against itself.
func TestClosetArmMeasuresClosetsWhenServedPriorIsOff(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t).WithClosetBoost(0)
	const team = "team-1"

	query, gold := closetFixture(t, svc, team)
	arms := []EvalArm{ArmHybrid, ArmHybridCloset}
	ranks, _, _, _, _, _, err := svc.evalCase(ctx, team, EvalCase{Query: query, Expect: gold, Wing: "infra"}, arms, 20)
	if err != nil {
		t.Fatalf("evalCase: %v", err)
	}
	if ranks[ArmHybrid] == 0 {
		t.Fatal("fixture: the gold never made the pool, so no arm can separate")
	}
	if ranks[ArmHybridCloset] == ranks[ArmHybrid] {
		t.Fatalf("hybrid+closet ranked the gold at %d, same as hybrid — the arm is not applying closet boosts at served scale 0", ranks[ArmHybridCloset])
	}
}

// TestProductionArmFollowsServedClosetScale pins the other half, and it is the
// opposite rule: the production arm exists to exercise what agents actually
// call, so it must track the SERVED scale rather than the arms' full-strength
// one. If both halves are not pinned, one fix breaks the other silently.
func TestProductionArmFollowsServedClosetScale(t *testing.T) {
	ctx := context.Background()
	const team = "team-1"
	arms := []EvalArm{ArmHybridCloset, ArmProduction}

	run := func(scale float64) map[EvalArm]int {
		t.Helper()
		svc := newTestService(t).WithClosetBoost(scale)
		query, gold := closetFixture(t, svc, team)
		ranks, _, _, _, _, _, err := svc.evalCase(ctx, team, EvalCase{Query: query, Expect: gold, Wing: "infra"}, arms, 20)
		if err != nil {
			t.Fatalf("evalCase at scale %v: %v", scale, err)
		}
		return ranks
	}

	off, on := run(0), run(1)

	if off[ArmProduction] == on[ArmProduction] {
		t.Errorf("production ranked the gold at %d under both served scales; it is supposed to reflect what the server serves", off[ArmProduction])
	}
	if off[ArmHybridCloset] != on[ArmHybridCloset] {
		t.Errorf("hybrid+closet ranked the gold at %d served-off and %d served-on; the arm must measure the prior at full strength either way", off[ArmHybridCloset], on[ArmHybridCloset])
	}
}
