package palace

import (
	"context"
	"testing"
)

// TestEveryWritePathAttachesTheDerivedEdge is the class audit for ADR-036 T6.
//
// T6 attached a containment edge in Service.Add and proved it there. That proved
// ONE path. The repository's own characteristic defect is a capability that is
// finished and unreachable, and the shape it takes here is a capability reachable
// on the path somebody tested and no other — so the question is not "does the
// edge attach" but "which write paths attach it".
//
// Three write paths exist and two of them did not, both found by cross-checking
// rather than by reading T6:
//
//   - Service.Add's DEFERRED-EMBEDDING branch returns early, before the
//     attachment. A memory filed while the embedder is down is exactly the one a
//     later session most needs to find, and it was a permanent orphan.
//   - AbsorbDrawers (ADR-035's import path, merged 2026-08-26) writes through
//     SaveUnembedded directly and never goes through Add at all. A whole imported
//     dataset would have been unreachable by traversal.
//
// This test enumerates the paths rather than testing one, so a fourth write path
// added later fails here instead of quietly filing orphans.
func TestEveryWritePathAttachesTheDerivedEdge(t *testing.T) {
	const team = "t-orphan"

	for _, tc := range []struct {
		name string
		file func(t *testing.T, ctx context.Context, svc *Service) string
	}{
		{
			"Service.Add with an embedder",
			func(t *testing.T, ctx context.Context, svc *Service) string {
				res, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the normal filing path"})
				if err != nil {
					t.Fatalf("add: %v", err)
				}
				return res.Drawers[0].ID
			},
		},
		{
			"Service.Add with the embedder down (deferred)",
			func(t *testing.T, ctx context.Context, svc *Service) string {
				// The vector index is deferred; the TEXT is still filed, and the
				// text is the memory. It must still be reachable.
				res, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "filed while the embedder was unreachable"})
				if err != nil {
					t.Fatalf("add deferred: %v", err)
				}
				if !res.PendingEmbedding {
					t.Fatal("this case is meant to exercise the deferred path and did not; the test would prove nothing")
				}
				return res.Drawers[0].ID
			},
		},
		{
			"AbsorbDrawers (the import path)",
			func(t *testing.T, ctx context.Context, svc *Service) string {
				n, err := svc.AbsorbDrawers(ctx, team, []ImportDrawer{{
					Wing: "wing_acme", Room: "decisions", Content: "a row that arrived through import",
				}})
				if err != nil {
					t.Fatalf("absorb: %v", err)
				}
				if n != 1 {
					t.Fatalf("absorbed %d, want 1", n)
				}
				// The import path derives its own ids, so the id is recovered the
				// way any reader would: by listing what is in the room.
				ids, err := svc.repo.IDsBySource(ctx, team, "wing_acme", "decisions", "")
				if err != nil || len(ids) == 0 {
					t.Fatalf("could not find the imported drawer: %v (%d ids)", err, len(ids))
				}
				return ids[len(ids)-1]
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc := newTestService(t)
			if tc.name == "Service.Add with the embedder down (deferred)" {
				svc = newTestServiceWith(t, brokenEmbedder{})
			}

			id := tc.file(t, ctx, svc)

			// Reachability, not the presence of a row: walk out from the room node
			// and require the drawer to be found.
			q, err := svc.KGQuery(ctx, team, KGQueryInput{
				Entity: DerivedEdgeSubject("wing_acme", "decisions"), Direction: "outgoing",
			})
			if err != nil {
				t.Fatalf("traverse from the room node: %v", err)
			}
			for _, f := range q.Facts {
				if f.Object == id {
					return
				}
			}
			t.Errorf("a drawer filed through this path is not reachable from its room node; %d edges out, none naming it — it is an orphan", len(q.Facts))
		})
	}
}

// TestTheFactArmActuallyScores is the rung-4 check T1 was missing.
//
// T1 proved the arm is DECLARED and REGISTERED, and four separate gates agreed.
// None of them asked whether the eval ever scores it. It did not: with no branch
// in evalCase's dispatch, the arm fell to `default`, where serviceForArm returns
// nil and the case is bypassed with ReasonOff. So the arm appeared in every
// table, passed every registration gate, and was structurally incapable of
// producing a number — the repository's characteristic defect, in the task whose
// whole purpose is to be the instrument.
//
// The lesson generalises past this arm: "is it registered" and "does it run" are
// different questions, and a registration gate answers only the first.
func TestTheFactArmActuallyScores(t *testing.T) {
	ctx := context.Background()
	const team = "t-armscores"
	svc := newTestService(t)

	filed, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: "the ledger service owns invoice numbering"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.KGAdd(ctx, team, "ledger service", "owns", "invoice numbering", "", "", "", "", filed.Drawers[0].ID); err != nil {
		t.Fatalf("kgadd: %v", err)
	}
	if _, err := svc.BackfillEntityLabels(ctx, team); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	gold := CanonicalFact("ledger service", "owns", "invoice numbering")
	rep, err := svc.EvaluateWith(ctx, team, []EvalCase{{
		Query: "who owns invoice numbering", Wing: "wing_acme",
		Category: CatFact, ExpectTriple: gold,
	}}, 10, EvalOptions{Arms: []string{string(ArmFactRetrieval)}}, nil)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	var m *EvalMetrics
	for i := range rep.Arms {
		if rep.Arms[i].Arm == ArmFactRetrieval {
			m = &rep.Arms[i]
		}
	}
	if m == nil {
		t.Fatal("the fact arm produced no row at all; it is registered and never scored")
	}
	if len(m.Ranks) == 0 {
		t.Fatal("the fact arm produced no ranks; every case was bypassed, so the arm can never report a number")
	}
	if m.Ranks[0] == 0 {
		t.Errorf("the gold fact was not found by the arm that exists to find it; ranks=%v", m.Ranks)
	}

	// And the rate it reports agrees with those ranks, so the two numbers the
	// instrument publishes cannot drift apart.
	if got := FactAnswerRateFrom(m.Ranks); got.Answered != 1 || got.Cases != 1 {
		t.Errorf("answerable rate %s disagrees with ranks %v", got, m.Ranks)
	}
}

// TestATripleIDIsNotAStableGold pins WHY the eval corpus is keyed on the
// canonical subject|predicate|object rather than on a triple id.
func TestATripleIDIsNotAStableGold(t *testing.T) {
	// The id hashes validFrom and recordedAt, so the same fact recorded at two
	// different moments has two different ids. A corpus keyed on ids decays
	// silently: cases simply begin to miss, which reads as retrieval getting
	// worse rather than as the gold going stale.
	a := tripleID("s", "p", "o", "2024-01-01", "2026-08-26T10:00:00Z")
	b := tripleID("s", "p", "o", "2024-01-01", "2026-08-26T10:00:01Z")
	if a == b {
		t.Fatal("triple ids are stable after all — if this is now true, the canonical-fact gold could be simplified")
	}
	if CanonicalFact("s", "p", "o") != CanonicalFact("s", "p", "o") {
		t.Error("the canonical fact is not stable, which is the only property it exists for")
	}
}
