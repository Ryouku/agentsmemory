package palace

import (
	"context"
	"testing"
)

// findFact returns the first fact matching predicate+object, or nil.
func findFact(facts []KGFact, predicate, object string) *KGFact {
	for i := range facts {
		if facts[i].Predicate == predicate && facts[i].Object == object {
			return &facts[i]
		}
	}
	return nil
}

func TestKGAddQueryDedup(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	res, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.TripleID == "" {
		t.Fatal("expected a triple id")
	}

	// Dedup: re-adding the identical current fact returns the same id, no duplicate.
	res2, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if res2.TripleID != res.TripleID {
		t.Fatalf("dedup should return the existing id: %s vs %s", res2.TripleID, res.TripleID)
	}

	// Query outgoing: predicate is normalized to works_at; current is true.
	q, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Direction: "both"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	f := findFact(q.Facts, "works_at", "Acme")
	if f == nil {
		t.Fatalf("expected Alice works_at Acme, got %+v", q.Facts)
	}
	if !f.Current || f.Direction != "outgoing" {
		t.Fatalf("fact should be current+outgoing: %+v", *f)
	}

	// Incoming from the object side resolves the subject name.
	in, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Acme", Direction: "incoming"})
	if err != nil {
		t.Fatalf("query incoming: %v", err)
	}
	if g := findFact(in.Facts, "works_at", "Acme"); g == nil || g.Subject != "Alice" {
		t.Fatalf("incoming should show Alice as subject, got %+v", in.Facts)
	}
}

func TestKGInvalidateAndAsOf(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	// After invalidation the fact is historical, not current.
	res, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Direction: "both"})
	f := findFact(res.Facts, "works_at", "Acme")
	if f == nil || f.Current || f.ValidTo != "2025-06-01" {
		t.Fatalf("fact should be ended 2025-06-01: %+v", res.Facts)
	}

	// as_of mid-window: in effect.
	mid, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2024-06-01", Direction: "both"})
	if findFact(mid.Facts, "works_at", "Acme") == nil {
		t.Fatal("fact should be in effect as of 2024-06-01")
	}
	// as_of after the end: not in effect.
	after, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2025-12-01", Direction: "both"})
	if findFact(after.Facts, "works_at", "Acme") != nil {
		t.Fatal("fact should NOT be in effect as of 2025-12-01 (ended)")
	}
	// as_of before the start: not in effect.
	before, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2023-01-01", Direction: "both"})
	if findFact(before.Facts, "works_at", "Acme") != nil {
		t.Fatal("fact should NOT be in effect as of 2023-01-01 (not yet started)")
	}

	// Supersede flow: after invalidation a new current fact can be added.
	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("post-invalidate add: %v", err)
	}
	now, _ := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", AsOf: "2025-12-01", Direction: "outgoing"})
	if findFact(now.Facts, "works_at", "Globex") == nil {
		t.Fatalf("the new current fact should be in effect: %+v", now.Facts)
	}
}

// TestEndedFactIsAbsentFromCurrentQuery is ADR-026 T1's gate.
//
// It asserts ABSENCE, which is the only assertion that can fail when the filter is
// removed. The pre-ADR-026 tool returned every fact ever recorded and tagged the
// dead ones current:false, so a test that merely queried and found its fact passed
// identically before and after — the behaviour under test was the reader's
// discipline, not the server's. Delete the status wiring and this goes red on the
// current branch; the `all` branch below is what stops it from passing by returning
// nothing at all.
func TestEndedFactIsAbsentFromCurrentQuery(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", ""); err != nil {
		t.Fatalf("add ended-to-be: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "Alice", "works at", "Globex", "2025-06-01", "", "", "", ""); err != nil {
		t.Fatalf("add survivor: %v", err)
	}
	if _, _, err := svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01"); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	current, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Status: KGStatusCurrent})
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if findFact(current.Facts, "works_at", "Acme") != nil {
		t.Fatalf("the retracted fact must not be returned at status=current: %+v", current.Facts)
	}
	if findFact(current.Facts, "works_at", "Globex") == nil {
		t.Fatalf("the open-ended fact must survive status=current: %+v", current.Facts)
	}

	ended, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Status: KGStatusEnded})
	if err != nil {
		t.Fatalf("ended: %v", err)
	}
	if findFact(ended.Facts, "works_at", "Acme") == nil {
		t.Fatalf("status=ended is the audit direction and must return the retracted fact: %+v", ended.Facts)
	}
	if findFact(ended.Facts, "works_at", "Globex") != nil {
		t.Fatalf("status=ended must not return open-ended facts: %+v", ended.Facts)
	}

	all, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice", Status: KGStatusAll})
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if findFact(all.Facts, "works_at", "Acme") == nil || findFact(all.Facts, "works_at", "Globex") == nil {
		t.Fatalf("status=all must return both: %+v", all.Facts)
	}

	// An omitted status is status=all until T4 flips it. Pinned so the flip is a
	// visible test change rather than a silent one.
	def, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "Alice"})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(def.Facts) != len(all.Facts) {
		t.Fatalf("the service default must be %q: got %d facts, all returns %d", KGStatusAll, len(def.Facts), len(all.Facts))
	}
}

func TestKGStatsAndTimeline(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	_, _ = svc.KGAdd(ctx, team, "Alice", "works at", "Acme", "2024-01-01", "", "", "", "")
	_, _ = svc.KGAdd(ctx, team, "Bob", "knows", "Alice", "", "", "", "", "")
	_, _, _ = svc.KGInvalidate(ctx, team, "Alice", "works at", "Acme", "2025-06-01")

	stats, err := svc.KGStats(ctx, team)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Triples != 2 || stats.CurrentFacts != 1 || stats.ExpiredFacts != 1 {
		t.Fatalf("stats wrong: %+v", stats)
	}
	if stats.Entities != 3 { // Alice, Acme, Bob
		t.Fatalf("expected 3 entities, got %d", stats.Entities)
	}

	tl, label, err := svc.KGTimeline(ctx, team, "Alice")
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if label != "Alice" || len(tl) < 2 {
		t.Fatalf("timeline for Alice should include both facts, got %d (%s)", len(tl), label)
	}
}

func TestKGValidation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Inverted interval is rejected.
	if _, err := svc.KGAdd(ctx, team, "A", "rel", "B", "2025-01-01", "2024-01-01", "", "", ""); err == nil {
		t.Fatal("inverted validity interval should be rejected")
	}
	// Malformed date is rejected.
	if _, err := svc.KGAdd(ctx, team, "A", "rel", "B", "2024-13-40", "", "", "", ""); err == nil {
		t.Fatal("malformed date should be rejected")
	}
	// Empty subject is rejected.
	if _, err := svc.KGAdd(ctx, team, "  ", "rel", "B", "", "", "", "", ""); err == nil {
		t.Fatal("empty subject should be rejected")
	}
	// Bad direction is rejected.
	if _, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "A", Direction: "sideways"}); err == nil {
		t.Fatal("invalid direction should be rejected")
	}
	// So is a status outside the vocabulary — a typo must not silently widen the
	// query back to every fact, which is the failure the default flip exists to end.
	if _, err := svc.KGQuery(ctx, team, KGQueryInput{Entity: "A", Status: "expired"}); err == nil {
		t.Fatal("invalid status should be rejected")
	}
}
