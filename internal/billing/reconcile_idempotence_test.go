package billing

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestReconcileDoesNotRevertAnOperatorDowngrade reproduces B1 from the PR #96
// review: `set-plan` writes only teams.plan_id, and applyActivated's only
// re-delivery guard is "the subscription row says canceled". After an operator
// downgrades, the row still reads active and the provider order is still PAID, so
// the next pass re-applies it — forever, with a routine "1 activated" in the log.
//
// A webhook fires once; a poll fires every interval. That difference is why the
// stateless-per-pass design was safe for Stripe and is not safe here.
func TestReconcileDoesNotRevertAnOperatorDowngrade(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_b1", Status: "ACTIVE", Frequency: "MONTHLY", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)}, FromAccountSlug: "jane",
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")
	ctx := context.Background()

	if _, err := r.ReconcileOnce(ctx); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got == tenant.FreePlanID {
		t.Fatal("first pass did not activate")
	}

	// The operator downgrades, exactly as the ADR's rollback and the reconciler's
	// own "left for manual attribution with set-plan" log line instruct.
	if err := svc.plans.SetTeamPlan(ctx, teamID, tenant.FreePlanID); err != nil {
		t.Fatalf("set-plan: %v", err)
	}

	if _, err := r.ReconcileOnce(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("the operator's downgrade was reverted by the next reconcile pass: plan = %q", got)
	}
}

// TestReconcileDoesNotGrantARecurringPlanForAOneOff reproduces B1's corollary: a
// ONETIME contribution to a recurring tier is PAID forever on the provider, and
// providerOrder.Frequency is decoded and read by nothing — so a single payment
// grants Pro for as long as the collective exists.
func TestReconcileDoesNotGrantARecurringPlanForAOneOff(t *testing.T) {
	r, svc, gdb, teamID := newReconcileEnv(t, func(teamID string) []providerOrder {
		return []providerOrder{{
			ID: "or_b1b", Status: "PAID", Frequency: "ONETIME", TierSlug: "pro-monthly",
			Tags: []string{intentTag(teamID)},
		}}
	})
	recordIntent(t, gdb, teamID, "pro_monthly", "jane@example.com")

	rep, err := r.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if rep.Activated != 0 {
		t.Errorf("a ONETIME contribution activated a recurring plan: %+v", rep)
	}
	if got := teamPlanID(t, svc.subs.db, teamID); got != tenant.FreePlanID {
		t.Fatalf("a one-off payment granted a recurring plan: %q", got)
	}
}

// TestEmailFallbackRefusesAnAmbiguousMatch reproduces B2: MatchByEmail is scoped to
// (email, plan) across EVERY workspace and ordered created_at DESC, so with one
// email and two workspaces the payment lands on whichever clicked Upgrade last.
//
// This needs no attacker to be a real defect — one person with a personal and a team
// workspace, clicking both and paying once, is an ordinary support ticket. It is
// worse with one, because tenant.CreateUserWithPassword performs no email
// verification, so an address can be registered by someone who does not own it.
//
// And it is the PRIMARY channel in practice, not the fallback: if the tag does not
// survive the hosted checkout — still unproven — this is the only path that resolves.
func TestEmailFallbackRefusesAnAmbiguousMatch(t *testing.T) {
	_, _, _, gdb, _ := newTestEnv(t)
	intents := NewIntentRepo(gdb)
	ctx := context.Background()

	// Two workspaces, one email, both with an open intent for the same plan.
	for _, team := range []string{"victim-team", "attacker-team"} {
		if err := intents.Record(ctx, CheckoutIntent{
			TeamID: team, PlanCode: "pro_monthly",
			Tag: intentTag(team), Email: "shared@example.com",
		}); err != nil {
			t.Fatalf("record intent for %s: %v", team, err)
		}
	}

	// The contribution carries no tag, so the email channel decides. It must refuse:
	// "when neither resolves, the answer is we do not know" is the file's own stated
	// principle, and this is a case it currently resolves by guessing.
	if got, err := intents.MatchByEmail(ctx, "shared@example.com", "pro_monthly"); err == nil {
		t.Fatalf("an ambiguous email matched workspace %q; two workspaces share that address and nothing ties the intent to the payer", got.TeamID)
	}
}
