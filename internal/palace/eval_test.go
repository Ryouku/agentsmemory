package palace

import (
	"context"
	"math"
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

	got, ok, err := svc.OlderNeighbor(ctx, team, newer, 50, 0)
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

	got, ok, err := svc.OlderNeighbor(ctx, team, newer, 50, 0)
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
	if _, _, err := svc.OlderNeighbor(ctx, team, d, 50, 0); err == nil {
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

	got, ok, err := svc.OlderNeighbor(ctx, team, newer, 50, 0)
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
	oc, err := svc.evalCaseResult(ctx, team, EvalCase{Query: query, Expect: gold, Wing: "infra"}, arms, 20)
	if err != nil {
		t.Fatalf("evalCase: %v", err)
	}
	ranks := oc.Ranks
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
		oc, err := svc.evalCaseResult(ctx, team, EvalCase{Query: query, Expect: gold, Wing: "infra"}, arms, 20)
		if err != nil {
			t.Fatalf("evalCase at scale %v: %v", scale, err)
		}
		return oc.Ranks
	}

	off, on := run(0), run(1)

	if off[ArmProduction] == on[ArmProduction] {
		t.Errorf("production ranked the gold at %d under both served scales; it is supposed to reflect what the server serves", off[ArmProduction])
	}
	if off[ArmHybridCloset] != on[ArmHybridCloset] {
		t.Errorf("hybrid+closet ranked the gold at %d served-off and %d served-on; the arm must measure the prior at full strength either way", off[ArmHybridCloset], on[ArmHybridCloset])
	}
}

// anchorFixture is T1's page-max fixture in eval clothing: the rare query term
// appears in no candidate and the common one in all, so every match is weak and
// page-max still awards its winner a perfect lexical score while an anchored
// normaliser does not. If the two normalisers cannot disagree here they cannot
// disagree anywhere.
func anchorFixture() (query string, docs []string, dists []float64) {
	// Two constraints, and the first version of this fixture met only one.
	//
	// The normalisers must differ: page-max divides by the best score on the
	// page and the anchored ones by what the query could have scored, so any
	// page whose winner falls short of the ceiling separates them.
	//
	// AND the adaptive arms need lexical coverage to work with. Coverage counts
	// query terms present in at least one candidate but not all; the earlier
	// fixture put "eviction" in every candidate and "cache" in none, so coverage
	// was zero, the adaptive weight collapsed to zero, and the lexical term was
	// multiplied away — making page-max and anchored identical for a reason that
	// had nothing to do with the normaliser. Here "cache" is in one candidate
	// and "eviction" in two of three, so both terms count.
	return "cache eviction", []string{
		"cache eviction happens twice here eviction",
		"eviction happens once here",
		"unrelated prose about something else entirely",
	}, []float64{0.55, 0.5, 0.45}
}

// TestAnchoredArmsRankDifferentlyFromPageMax is the behavioural check, and the
// only one here that can catch the failure that matters: an anchored arm
// falling through to the page-max branch of the dispatch.
//
// A registry test cannot catch it — the arm is registered either way, and the
// table would show two identical rows reading as "the normaliser makes no
// difference". This follows TestLexicalIDFChangesWhatSearchReturns, whose
// predecessor asserted only that both modes returned results and passed happily
// while the flag was read by nothing at all.
func TestAnchoredArmsRankDifferentlyFromPageMax(t *testing.T) {
	// Every anchored arm, not just one. The first version of this test built only
	// anchoredArm(bm25Arm(0.4), …), so the four adaptive-anchored arms had no
	// behavioural check at all — and pointing their branches back at the
	// page-max rankers left the whole suite green. Found by review.
	bases := []EvalArm{ArmAdaptive, ArmAdaptiveIDF}
	for _, w := range bm25Sweep {
		if w != 0 {
			bases = append(bases, bm25Arm(w))
		}
	}
	for _, base := range bases {
		plain := fusionRankerFor(base, hybridBM25Weight)
		if plain == nil {
			t.Fatalf("%s has no ranker; the dispatch does not cover it", base)
			continue
		}
		checkAnchoredDiffers(t, base, plain)
	}
}

// checkAnchoredDiffers asserts each anchored counterpart of base scores the
// fixture differently from base itself.
func checkAnchoredDiffers(t *testing.T, base EvalArm, plain func(string, []string, []float64, []float64) []HybridScore) {
	t.Helper()
	query, docs, dists := anchorFixture()
	for _, norm := range anchoredNorms {
		arm := anchoredArm(base, norm.name)
		ranker := fusionRankerFor(arm, hybridBM25Weight)
		if ranker == nil {
			t.Errorf("%s has no ranker; it would fall through to the page-max branch", arm)
			continue
		}
		pageMax := plain(query, docs, dists, nil)
		got := ranker(query, docs, dists, nil)
		if len(got) != len(pageMax) {
			t.Fatalf("%s returned %d scores, page-max returned %d", arm, len(got), len(pageMax))
		}
		same := true
		for i := range got {
			if got[i].Fused != pageMax[i].Fused {
				same = false
			}
		}
		if same {
			t.Errorf("%s produced the same fused scores as %s on a fixture built to separate them — the arm is named but not wired", arm, base)
		}
	}
}

// TestAnchoredArmsCarryNoClosetPrior pins the resolution of a collision between
// two accepted ADRs.
//
// ADR-002 T2 was written to add BOOSTED anchored arms plus a `no-closet` control
// family, because anchoring inflates an additive boost and a single boost regime
// cannot separate that from a lexical-weighting effect. ADR-003 T1 then made the
// closet prior something an arm opts into by name and tagged closet variants of
// the sweep arms as permanently out of scope, which removes the confound at the
// source: no sweep arm is boosted, so there is one regime and nothing to
// control for. The families collapse into one, and this test is what keeps them
// collapsed — if a boosted anchored arm is ever added, it fails.
func TestAnchoredArmsCarryNoClosetPrior(t *testing.T) {
	closet := []float64{0.1, 0.2, 0.3}
	for _, arm := range evalArms(EvalOptions{Contextual: true}, true) {
		if !strings.Contains(string(arm), "anchored:") {
			continue
		}
		if got := armBoosts(arm, closet); got != nil {
			t.Errorf("%s carries the closet prior; anchored arms are one unboosted family", arm)
		}
	}
}

// TestAnchoredArmsCoverEveryNonzeroWeight pins the registry: every fusion arm
// whose lexical term can matter gets both anchored counterparts.
func TestAnchoredArmsCoverEveryNonzeroWeight(t *testing.T) {
	registered := map[EvalArm]bool{}
	for _, a := range evalArms(EvalOptions{Contextual: true}, true) {
		registered[a] = true
	}

	var want []EvalArm
	for _, w := range bm25Sweep {
		if w == 0 {
			continue
		}
		for _, norm := range anchoredNorms {
			want = append(want, anchoredArm(bm25Arm(w), norm.name))
		}
	}
	for _, adaptive := range []EvalArm{ArmAdaptive, ArmAdaptiveIDF} {
		for _, norm := range anchoredNorms {
			want = append(want, anchoredArm(adaptive, norm.name))
		}
	}
	if len(want) != 10 {
		t.Fatalf("expected 10 anchored arms from the sweep and the adaptive pair, computed %d", len(want))
	}
	for _, a := range want {
		if !registered[a] {
			t.Errorf("%s is not registered; its page-max counterpart has no anchored comparison", a)
		}
	}
}

// TestAnchoredArmsSkipWeightZero pins the one omission: at w=0 the lexical term
// is multiplied by zero, so the normaliser cannot change the order and the row
// would duplicate `fusion bm25=0.00` while reading as a finding.
func TestAnchoredArmsSkipWeightZero(t *testing.T) {
	for _, a := range evalArms(EvalOptions{Contextual: true}, true) {
		for _, norm := range anchoredNorms {
			if a == anchoredArm(bm25Arm(0), norm.name) {
				t.Errorf("%s is registered; at zero lexical weight the normaliser cannot matter", a)
			}
		}
	}
}

// TestEvalArmsKeepProductionLast pins the ordering invariant eval.go states in a
// comment and nothing enforced: every fusion arm is scored before the arm that
// exercises the code agents actually call.
//
// The comment says production "runs LAST and always" and that has never been
// quite true — ArmContextual and the reranked family are appended after it, and
// writing this test is how that surfaced. What the ordering is really for is the
// comparison: production is the reality check on the fusion arms above it, so
// what must hold is that no fusion arm appears below it. The comment now says
// that instead of the stronger thing it could not back.
func TestEvalArmsKeepProductionLast(t *testing.T) {
	for _, rerankReady := range []bool{false, true} {
		arms := evalArms(EvalOptions{Contextual: true}, rerankReady)
		prod := -1
		for i, a := range arms {
			if a == ArmProduction {
				prod = i
			}
		}
		if prod < 0 {
			t.Fatalf("rerankReady=%v: the production arm is not registered at all", rerankReady)
		}
		for i, a := range arms[prod+1:] {
			if fusionRankerFor(a, hybridBM25Weight) != nil {
				t.Errorf("rerankReady=%v: fusion arm %s is scored at %d, after production at %d", rerankReady, a, prod+1+i, prod)
			}
		}
		if prod == 0 {
			t.Errorf("rerankReady=%v: production is the FIRST arm; it has nothing to be the reality check on", rerankReady)
		}
	}
}

// TestEvalArmNamesAreUnique pins that two arms never collide on a name. The
// report is keyed by arm name, so a collision does not error — it silently
// overwrites a row, and the table looks complete.
func TestEvalArmNamesAreUnique(t *testing.T) {
	for _, rerankReady := range []bool{false, true} {
		seen := map[EvalArm]int{}
		for _, a := range evalArms(EvalOptions{Contextual: true}, rerankReady) {
			seen[a]++
			if seen[a] > 1 {
				t.Errorf("rerankReady=%v: arm name %q is registered %d times; the later row overwrites the earlier", rerankReady, a, seen[a])
			}
		}
	}
}

// TestEveryRegisteredArmIsScorable pins that a registered arm reaches the
// scoring path its name implies, which is a different question from whether it
// is registered at all.
//
// The trap is specific and this task walked into it: the anchored arms were
// added to the registry before evalCase knew about them, so they fell through
// the switch to the `default` branch — the one that scores the RERANKED family —
// and would have been scored as reranked arms under their fusion names. Nothing
// would have failed. armreach_test.go checks registration and is syntactic by
// design; the report is keyed by arm name and takes whatever it is given.
//
// Every arm is therefore either score fusion (fusionRankerFor returns a ranker)
// or one of the named exceptions below, each of which evalCase handles in its
// own case. A new arm that is neither fails here.
func TestEveryRegisteredArmIsScorable(t *testing.T) {
	notFusion := map[EvalArm]string{
		ArmVector:       "nearest-neighbour order, no fusion at all",
		ArmRRF:          "reciprocal rank fusion, its own case",
		ArmRRFReranked:  "RRF then the cross-encoder, its own case and its own scores",
		ArmContextual:   "scores a different candidate set, not a different ranker",
		ArmProduction:   "goes through Search, which is the point of it",
		ArmHybridRerank: "fusion then the cross-encoder, scored in the rerank branch",
		ArmReranked:     "fusion then the cross-encoder, scored in the rerank branch",
	}
	for _, w := range rerankSweep {
		notFusion[rerankArm(w)] = "a rerank blend weight, scored in the rerank branch"
	}
	for _, b := range recencySweep {
		notFusion[recencyArm(b)] = "fusion plus a date reorder, scored in its own branch — the " +
			"fusion seam carries no date and must not grow one for a single arm"
	}

	for _, arm := range evalArms(EvalOptions{Contextual: true}, true) {
		if fusionRankerFor(arm, hybridBM25Weight) != nil {
			if why, listed := notFusion[arm]; listed {
				t.Errorf("%s is listed as not-fusion (%s) yet fusionRankerFor returns a ranker for it; it would be scored twice over by two different rules", arm, why)
			}
			continue
		}
		if _, listed := notFusion[arm]; !listed {
			t.Errorf("%s is registered but is neither score fusion nor a listed exception — it falls through to the rerank branch and is scored under a name that does not describe it", arm)
		}
	}
}

// TestRerankedArmsUseThePoolTheirNameClaims pins the two-pool selection, which
// is one line and was invisible to the entire suite.
//
// evalCase fetches cross-encoder scores for a closet-on pool and a closet-off
// pool, and each reranked arm picks one by whether armBoosts hands it a slice.
// Inverting that single condition — so every reranked arm reads the wrong pool —
// used to pass all tests, because no fixture in this package configures a
// reranker at all, so the branch was dead as far as testing was concerned. That
// is this repository's named defect exactly: the classifier had a test, the
// CONSUMER of the classifier did not.
//
// The assertion is directional rather than "the two arms differ". Inverting the
// condition swaps which arm reads which pool, so both arms still differ from
// each other and a difference test would pass under the bug. What cannot survive
// the swap is WHICH arm benefits: the closet boost lifts the mined source, and
// the gold is in it, so the closet-on arm must rank it at least as well.
func TestRerankedArmsUseThePoolTheirNameClaims(t *testing.T) {
	ctx := context.Background()
	const team = "team-1"
	svc := newTestService(t).WithClosetBoost(0).WithReranker(&fakeReranker{}, DefaultRerankPool)

	query, gold := closetFixture(t, svc, team)
	arms := []EvalArm{ArmHybridRerank, ArmReranked}
	oc, err := svc.evalCaseResult(ctx, team, EvalCase{Query: query, Expect: gold, Wing: "infra"}, arms, 20)
	if err != nil {
		t.Fatalf("evalCase: %v", err)
	}
	ranks, degraded := oc.Ranks, oc.Degraded
	if degraded {
		t.Fatal("the fake reranker failed; this test would then be comparing two copies of the fused order")
	}
	if ranks[ArmHybridRerank] == 0 || ranks[ArmReranked] == 0 {
		t.Fatalf("fixture: the gold never made the pool (hybrid+rerank %d, hybrid+closet+rerank %d)", ranks[ArmHybridRerank], ranks[ArmReranked])
	}
	if ranks[ArmReranked] > ranks[ArmHybridRerank] {
		t.Errorf("hybrid+closet+rerank ranked the gold at %d and hybrid+rerank at %d; the gold is in the MINED source, so the closet-on pool cannot be the worse of the two — the arms are reading each other's pools",
			ranks[ArmReranked], ranks[ArmHybridRerank])
	}
	if ranks[ArmReranked] == ranks[ArmHybridRerank] {
		t.Errorf("both reranked arms ranked the gold at %d; the fixture is not separating the two pools, so this test cannot see the selection it exists to check", ranks[ArmReranked])
	}
}

// TestAnchoredNormNamesMatchTheirTransforms binds each label in anchoredNorms to
// the function it claims, which nothing else does.
//
// Swapping the two entries in that table — `ceiling` pointing at
// lexNormSaturating and vice versa — turned ZERO tests red across the whole
// package before this existed. Every other anchored test asks only whether an
// arm differs from page-max, and both transforms do, so both survive the swap.
// The consequence is not academic: ADR-002 exists to compare the two, and T3's
// evidence run would publish one transform's numbers under the other's name.
//
// The binding is behavioural rather than by identity, because Go cannot compare
// function values. Each transform is pinned by the property that distinguishes
// it: dividing by a constant is linear in raw, so equal raw ratios give equal
// normalised ratios; the saturating transform is strictly concave, so the
// normalised value grows slower than raw does. An unrecognised name fails, so a
// third transform cannot be added without saying which of the two it behaves
// like — or getting a case of its own.
func TestAnchoredNormNamesMatchTheirTransforms(t *testing.T) {
	const ceiling = 4.0
	raw := []float64{1.0, 2.0}

	for _, n := range anchoredNorms {
		got := n.norm(raw, ceiling)
		if got[0] <= 0 {
			t.Fatalf("%s: fixture produced no signal to compare", n.name)
		}
		ratio := got[1] / got[0] // raw doubled; what did the transform do?
		switch n.name {
		case "ceiling":
			// Proportional: dividing by a constant preserves ratios exactly.
			if math.Abs(ratio-2) > 1e-12 {
				t.Errorf("%q is not the proportional transform: doubling raw scaled the result by %.6f, want 2 — the label is wired to the wrong function", n.name, ratio)
			}
		case "saturating":
			// Strictly concave: doubling raw gives strictly less than double.
			if !(ratio > 1 && ratio < 2) {
				t.Errorf("%q is not the saturating transform: doubling raw scaled the result by %.6f, want strictly between 1 and 2 — the label is wired to the wrong function", n.name, ratio)
			}
		default:
			t.Errorf("anchoredNorms carries %q, which this test has no property for; a normaliser nothing pins can be swapped for another and every test stays green", n.name)
		}
	}
	if len(anchoredNorms) != 2 {
		t.Errorf("anchoredNorms holds %d transforms; add its distinguishing property above before registering it", len(anchoredNorms))
	}
}

// TestEvalCaseFetchesOnlyThePoolsItsArmsRead counts cross-encoder passes,
// because nothing else can see one going missing.
//
// BlendRerank returns the fused order unchanged when handed no scores, which is
// the right thing at runtime and the reason a skipped pass is invisible: the arm
// still produces an ordering, still gets a rank, and still prints a row headed
// by the reranker's name. This palace has already published one full table of
// "reranked" numbers that were the hybrid order.
//
// So the assertion is on the call count, not on the ranking. It pins both
// directions of the fetch rule at once — a pool that no requested arm reads is
// not fetched, and a pool that one does read is.
func TestEvalCaseFetchesOnlyThePoolsItsArmsRead(t *testing.T) {
	ctx := context.Background()
	const team = "team-1"

	for _, tc := range []struct {
		name  string
		arms  []EvalArm
		wants int
	}{
		{"no reranked arm asks for nothing", []EvalArm{ArmHybrid, ArmHybridCloset}, 0},
		{"closet-off arm reads the plain pool only", []EvalArm{ArmHybridRerank}, 1},
		{"closet-on arm reads the closet pool only", []EvalArm{ArmReranked}, 1},
		{"a sweep arm reads the plain pool", []EvalArm{rerankArm(rerankSweep[0])}, 1},
		{"both arms read both pools", []EvalArm{ArmHybridRerank, ArmReranked}, 2},
		// rrf+rerank takes its OWN pass, because RRF changes the order the head
		// is taken in. It is a third call this table did not cover, and its
		// failure is not folded into rerankFailed — so replacing it with a nil
		// slice made rrf+rerank print as plain rrf under a reranker's name, with
		// no degraded warning and nothing red. Found by review.
		{"rrf+rerank reads its own pool", []EvalArm{ArmRRFReranked}, 1},
		{"rrf+rerank does not borrow the fusion pools", []EvalArm{ArmRRFReranked, ArmReranked}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := &fakeReranker{}
			svc := newTestService(t).WithClosetBoost(0).WithReranker(rr, DefaultRerankPool)
			query, gold := closetFixture(t, svc, team)
			if _, err := svc.evalCaseResult(ctx, team, EvalCase{Query: query, Expect: gold, Wing: "infra"}, tc.arms, 20); err != nil {
				t.Fatalf("evalCase: %v", err)
			}
			if rr.called != tc.wants {
				t.Errorf("the cross-encoder ran %d time(s), want %d — a pass that is skipped degrades to the fused order and still prints a reranked row", rr.called, tc.wants)
			}
		})
	}
}

// TestCandidateUnionPoolsTheClosetHead pins that the judged pool is blind to the
// decision this ADR is taking.
//
// CandidateUnion builds the candidate set a human or a model judges to produce
// real-query qrels. Every ranker it pooled was closet-OFF, so a memory that only
// the curation prior would surface could never be judged relevant — and the
// resulting qrels would then be used to show the prior does not help. That is
// the conclusion being assumed by the instrument. Adding the closet-on head
// costs one more ordering over the same candidates and removes the bias.
//
// It also pins the two properties that keep the pool honest: a drawer appears
// exactly once however many heads nominate it, and the order carries no signal
// about which ranker liked what.
func TestCandidateUnionPoolsTheClosetHead(t *testing.T) {
	ctx := context.Background()
	const team = "team-1"
	svc := newTestService(t).WithClosetBoost(0)

	query, gold := closetFixture(t, svc, team)

	pooled, err := svc.CandidateUnion(ctx, team, query, "infra", 1, 20)
	if err != nil {
		t.Fatalf("CandidateUnion: %v", err)
	}
	if len(pooled) == 0 {
		t.Fatal("the union pooled nothing")
	}

	seen := map[string]int{}
	for _, d := range pooled {
		seen[d.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("drawer %s appears %d times; the union must dedupe across heads", id, n)
		}
	}
	if seen[gold] == 0 {
		t.Errorf("the gold from the MINED source is not in the judged pool — with perArm=1 only the closet-boosted head promotes it, so a judge could never mark it relevant")
	}
	for i := 1; i < len(pooled); i++ {
		if pooled[i-1].ID > pooled[i].ID {
			t.Fatalf("the pool is not sorted by id at %d; any other order leaks which ranker proposed a candidate", i)
		}
	}
}

// TestSupersessionRanksDistractorInSamePool pins that the superseded version is
// scored through the SAME ordering as the gold, and — the part a review caught
// before this was written — that it is resolved to a MEMORY id the way the gold
// is.
//
// The pool is keyed by memory id, and the gold reaches that key through a Get
// plus ParentID. Rank a raw distractor DRAWER id against memory ids and every
// multi-chunk distractor scores as never-retrieved: Vacuous inflates, every
// stale-above rate is flattered, and nothing fails. So the fixture's distractor
// is deliberately multi-chunk — a single-chunk one passes either way and would
// have let the bug ship behind a green suite.
func TestSupersessionRanksDistractorInSamePool(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Long enough to chunk, so the distractor's id is a CHILD whose ParentID is
	// what the pool is keyed by.
	stale := strings.Repeat("The retention window is thirty days and applies to every tenant. ", 30)
	staleRes, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "policy-v1", Content: stale})
	if err != nil {
		t.Fatalf("add stale: %v", err)
	}
	if len(staleRes.Drawers) < 2 {
		t.Fatalf("fixture: the distractor must be multi-chunk to exercise the resolution, got %d chunk(s)", len(staleRes.Drawers))
	}
	current := strings.Repeat("The retention window is ninety days and applies to every tenant. ", 30)
	curRes, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "policy-v2", Content: current})
	if err != nil {
		t.Fatalf("add current: %v", err)
	}

	// A CHILD chunk id on both sides, which is what a generated case carries.
	child := staleRes.Drawers[len(staleRes.Drawers)-1].ID
	arms := []EvalArm{ArmHybrid, ArmHybridCloset}
	res, err := svc.evalCaseResult(ctx, team, EvalCase{
		Query: "retention window", Expect: curRes.Drawers[0].ID, Distractor: child,
		Wing: "w", Category: CatTemporal,
	}, arms, 20)
	if err != nil {
		t.Fatalf("evalCase: %v", err)
	}

	if res.DistractorPoolRank == 0 {
		t.Fatal("the superseded version was scored as never retrieved — a multi-chunk distractor " +
			"whose drawer id was never resolved to its parent memory looks exactly like this, and " +
			"it makes every stale-above rate look better than it is")
	}
	for _, a := range arms {
		if _, ok := res.DistractorRanks[a]; !ok {
			t.Errorf("%s has no distractor rank; it must be read from the same ordering that produced its gold rank", a)
		}
	}
}

// TestSupersessionRanksScopePerArm pins that the report names the population each
// arm's number was measured over.
//
// Three arms answer a different question by construction. A pool-scoped arm
// re-orders the shared candidate set. ArmProduction is scored over the page
// Search actually returns, which is at most DefaultSearchLimit long after the
// distance gate — so "the distractor was not above the gold" can mean "it was not
// on the page at all". ArmContextual retrieves from its own namespace entirely.
// Printing those three as one column would be the same error as reading an arm's
// zero as "outside the pool".
func TestSupersessionRanksScopePerArm(t *testing.T) {
	for arm, want := range map[EvalArm]SupersessionScope{
		ArmHybrid:       ScopePool,
		ArmHybridCloset: ScopePool,
		ArmRRF:          ScopePool,
		ArmReranked:     ScopePool,
		ArmProduction:   ScopePage,
		ArmContextual:   ScopeOwnIndex,
	} {
		if got := supersessionScope(arm); got != want {
			t.Errorf("supersessionScope(%s) = %q, want %q", arm, got, want)
		}
	}
	for _, arm := range evalArms(EvalOptions{Contextual: true}, true) {
		if supersessionScope(arm) == "" {
			t.Errorf("%s has no supersession scope — its number would be printed beside arms "+
				"measuring a different population", arm)
		}
	}
}

// TestRecencyArmPrefersNewerWithinBand pins the arm's whole behaviour: inside a
// band of fused score it prefers the newer memory, and outside it changes
// nothing.
//
// The band matters more than the preference. A recency prior that reorders
// across large score gaps is not a tie-break, it is a different ranker — it would
// promote a recent irrelevance over an older exact answer. So the test asserts
// both halves: two candidates within the band swap, and a candidate far below
// stays below however new it is.
//
// The baseline is unboosted ArmHybrid. The task originally said ArmHybridCloset,
// which was true when it was written and is now a guaranteed red test —
// TestArmBoostsDimension errors when any arm outside the two closet-named ones
// receives boosts.
func TestRecencyArmPrefersNewerWithinBand(t *testing.T) {
	query := "retention window"
	docs := []string{
		"the retention window is thirty days", // older
		"the retention window is ninety days", // newer, near-identical score
		"retention window retention window retention window unrelated filler",
	}
	dists := []float64{0.30, 0.31, 0.90}
	dates := []string{"2024-01-01", "2026-01-01", "2026-06-01"}

	base := rankHybrid(query, docs, dists, nil)
	got := reorderByRecency(base, dates, 0.05)

	if len(got) != len(base) {
		t.Fatalf("recency reorder returned %d scores, want %d", len(got), len(base))
	}
	// The newer of the two near-tied candidates must come first.
	posOf := func(page []HybridScore, idx int) int {
		for i, h := range page {
			if h.Index == idx {
				return i
			}
		}
		return -1
	}
	if posOf(got, 1) >= posOf(got, 0) {
		t.Errorf("the newer of two candidates inside the band did not move ahead: order %v", orderOf(got))
	}
	// The far-below candidate must not be promoted by being newest.
	if posOf(got, 2) != len(got)-1 {
		t.Errorf("a candidate outside the band was reordered by date: order %v — a recency prior "+
			"that crosses large score gaps is a different ranker, not a tie-break", orderOf(got))
	}
	// And a zero band must be a no-op, or the sweep has no baseline.
	if !reflect.DeepEqual(orderOf(reorderByRecency(base, dates, 0)), orderOf(base)) {
		t.Error("a zero band must leave the order untouched")
	}
}

// TestRecencyArmLeavesUndatedInPlace pins the rule that keeps the arm honest:
// absence of a date is not evidence of being old.
//
// An undated memory is neither promoted nor demoted. Treating "" as very old
// would push every un-dated memory down a ranking on no evidence at all, and
// most memories in a real palace carry no content date.
func TestRecencyArmLeavesUndatedInPlace(t *testing.T) {
	query := "retention window"
	docs := []string{
		"the retention window is thirty days",
		"the retention window is ninety days",
	}
	dists := []float64{0.30, 0.31}

	base := rankHybrid(query, docs, dists, nil)
	// Neither dated: nothing to compare, so nothing moves.
	if got := reorderByRecency(base, []string{"", ""}, 0.05); !reflect.DeepEqual(orderOf(got), orderOf(base)) {
		t.Errorf("two undated candidates were reordered: %v, want %v", orderOf(got), orderOf(base))
	}
	// One dated, one not: the undated one is not demoted for lacking a date.
	got := reorderByRecency(base, []string{"", "2026-01-01"}, 0.05)
	if !reflect.DeepEqual(orderOf(got), orderOf(base)) {
		t.Errorf("an undated candidate moved against a dated one: %v, want %v — absence of a date "+
			"is not evidence of being old, and most memories carry none", orderOf(got), orderOf(base))
	}
	// An unparseable date behaves the same as none.
	if got := reorderByRecency(base, []string{"not-a-date", "2026-01-01"}, 0.05); !reflect.DeepEqual(orderOf(got), orderOf(base)) {
		t.Errorf("an unparseable date was treated as old: %v", orderOf(got))
	}
}

// TestRecencyArmReordersThroughEvalCase pins the ARM, not the helper.
//
// reorderByRecency has unit tests, and they pass whether or not evalCase ever
// calls it. Collapsing the band to zero inside the dispatch branch — so the arm
// is registered, scored, and prints a row that is byte-identical to plain
// hybrid — left the whole suite green. That is the same shape as an anchored arm
// falling through to the page-max branch: the component is tested, the selection
// is not, and the table reports one arm's numbers under another's name.
//
// The fixture puts the correction and the superseded version close enough in
// fused score to sit inside the band, and dates them a year apart.
func TestRecencyArmReordersThroughEvalCase(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// The OLDER memory is the better lexical match — it repeats the query's terms
	// — so plain hybrid ranks it first. That is the case the arm exists for, and a
	// fixture where hybrid already puts the newer one on top cannot separate the
	// two arms at all: the first version of this test did exactly that and passed
	// with the reorder disabled.
	old, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "policy-v1",
		Content:     "retention window policy retention window policy thirty days",
		ContentDate: "2024-01-01"})
	if err != nil {
		t.Fatalf("add old: %v", err)
	}
	newer, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", SourceFile: "policy-v2",
		Content: "retention window policy ninety days", ContentDate: "2026-01-01"})
	if err != nil {
		t.Fatalf("add new: %v", err)
	}

	band := recencySweep[len(recencySweep)-1]
	arms := []EvalArm{ArmHybrid, recencyArm(band)}
	oc, err := svc.evalCaseResult(ctx, team, EvalCase{
		Query: "retention window policy", Expect: newer.Drawers[0].ID,
		Distractor: old.Drawers[0].ID, Wing: "w", Category: CatTemporal,
	}, arms, 20)
	if err != nil {
		t.Fatalf("evalCase: %v", err)
	}

	recency := arms[1]
	if oc.Ranks[ArmHybrid] == 0 || oc.Ranks[recency] == 0 {
		t.Fatalf("fixture: both arms must rank the gold (hybrid %d, recency %d)", oc.Ranks[ArmHybrid], oc.Ranks[recency])
	}
	if oc.Ranks[ArmHybrid] != 2 {
		t.Fatalf("fixture: plain hybrid must rank the NEWER gold second, or there is nothing for "+
			"the recency arm to fix — got %d", oc.Ranks[ArmHybrid])
	}
	if oc.Ranks[recency] != 1 {
		t.Errorf("%s ranked the newer gold at %d, want 1 — two candidates inside the band, and the "+
			"arm did not prefer the newer one. Registered and dispatched is not the same as "+
			"reordering: collapsing the band to zero makes this arm a byte-identical copy of "+
			"hybrid under a different name, and only this assertion can see that",
			recency, oc.Ranks[recency])
	}
}

// TestOlderNeighborFloorRejectsDistantPair pins the filter the other three lack.
//
// The three existing filters say what a pair must NOT be — not itself, not the
// same source, not newer. None says what it must BE. So "nearest older
// neighbour" degrades into "the least unrelated older memory in this wing", and
// on a sparse wing that is a pair of unrelated memories presented to a judge as a
// supersession. The ceiling makes the claim positive: close enough that one
// plausibly restates the other.
//
// A distance of 0 keeps the old behaviour, so the existing tests pass a
// permissive value and stay meaningful rather than being silently re-scoped.
func TestOlderNeighborFloorRejectsDistantPair(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	newer := mustAddOne(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "current",
		Content: "The retention window is ninety days for every tenant.", ContentDate: "2026-01-01"})
	// Strictly older, different source, and about something else entirely.
	mustAddOne(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "unrelated",
		Content: "Kubernetes schedules pods onto nodes using taints and tolerations.", ContentDate: "2024-01-01"})

	// Permissive: the old behaviour, which accepts it.
	if _, ok, err := svc.OlderNeighbor(ctx, team, newer, 50, 0); err != nil || !ok {
		t.Fatalf("with no ceiling the distant neighbour must still be accepted (ok=%v err=%v) — "+
			"otherwise this test is not measuring the ceiling", ok, err)
	}
	// With a tight ceiling it must be rejected rather than offered as a pair.
	if _, ok, err := svc.OlderNeighbor(ctx, team, newer, 50, 0.01); err != nil {
		t.Fatalf("OlderNeighbor with a ceiling: %v", err)
	} else if ok {
		t.Error("a strictly-older, different-source neighbour about an unrelated subject was " +
			"accepted as a supersession pair — without a ceiling, 'nearest older neighbour' is a " +
			"claim about the corpus's sparsity, not about the two memories")
	}
}
