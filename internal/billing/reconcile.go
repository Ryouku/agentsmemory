package billing

import (
	"context"
	"errors"
	"log"
	"time"

	"gorm.io/gorm"
)

// Open Collective's OrderStatus enum, read in full from the live schema on
// 2026-08-28 (all fourteen values). They are grouped here by what they mean for a
// workspace's plan, and the grouping is the judgement in this file — the names are
// someone else's state machine, so each group says why.
//
// ACTIVATING: the money is real and the contribution is live. ACTIVE is a running
// recurring contribution; PAID is a completed one-off or a settled charge.
//
// CANCELLING: the contribution has stopped or been undone, so the workspace must
// return to Free. REJECTED and EXPIRED never became money; REFUNDED and CANCELLED
// stopped being money.
//
// IGNORED: everything in flight, under review, or paused. ⚠ These are deliberately
// NOT cancellations. The failure mode of ignoring a state is a workspace that keeps
// Pro slightly too long; the failure mode of cancelling on one is a paying customer
// downgraded mid-retry, which is strictly worse and much harder to notice.
// ERROR sits here for exactly that reason: an errored order is not a paid one, but
// it is also not evidence the customer left.
var ocStatusKind = map[string]eventKind{
	"ACTIVE":                      eventActivated,
	"PAID":                        eventActivated,
	"CANCELLED":                   eventCanceled,
	"EXPIRED":                     eventCanceled,
	"REFUNDED":                    eventCanceled,
	"REJECTED":                    eventCanceled,
	"NEW":                         eventIgnored,
	"PENDING":                     eventIgnored,
	"PROCESSING":                  eventIgnored,
	"REQUIRE_CLIENT_CONFIRMATION": eventIgnored,
	"DISPUTED":                    eventIgnored,
	"IN_REVIEW":                   eventIgnored,
	"PAUSED":                      eventIgnored,
	"ERROR":                       eventIgnored,
}

// kindForStatus maps a provider status onto our event vocabulary. An UNKNOWN status
// — one Open Collective adds after this was written — is ignored and logged, never
// treated as a cancellation: a new state name must not be able to silently downgrade
// paying workspaces.
func kindForStatus(status string) eventKind {
	if k, ok := ocStatusKind[status]; ok {
		return k
	}
	log.Printf("billing: unknown opencollective order status %q; ignoring (a new status must never be read as a cancellation)", status)
	return eventIgnored
}

// intentMatcher is the read half of the intent store, declared at the consumer so
// the reconciler depends on the two lookups it performs.
type intentMatcher interface {
	MatchByTag(ctx context.Context, tag, planCode string) (CheckoutIntent, error)
	MatchByEmail(ctx context.Context, email, planCode string) (CheckoutIntent, error)
}

// ReconcileReport counts what one pass saw. It exists so the caller can log a
// number rather than a silence: "0 orders" and "the call failed" must never produce
// the same log line, which is the same empty-reads-as-an-answer trap the order
// source guards at the transport level.
type ReconcileReport struct {
	Seen         int
	Activated    int
	Canceled     int
	Ignored      int
	Unattributed int
}

// Reconciler turns contributions read from the provider into plan changes. It owns
// no plan-flip logic of its own: it maps each order onto a providerEvent and hands
// it to the same applyActivated / applyCanceled the Stripe webhook uses, so there is
// exactly one implementation of "what a payment does to a workspace" (ADR-042).
type Reconciler struct {
	svc          *Service
	orders       orderSource
	intents      intentMatcher
	planByTierID map[int]string // Open Collective tier legacyId -> our plan code
}

// NewReconciler builds a Reconciler. planByTierID maps the provider's tier ids onto
// our sellable plan codes; an order naming a tier that is not in the map is ignored,
// because we cannot say what was bought.
func NewReconciler(svc *Service, orders orderSource, intents intentMatcher, planByTierID map[int]string) *Reconciler {
	return &Reconciler{svc: svc, orders: orders, intents: intents, planByTierID: planByTierID}
}

// ReconcileOnce reads every incoming contribution and applies the ones it can
// attribute. It is idempotent by construction — it re-reads the same orders every
// pass and converges on the same state, because the plan flip and the subscription
// upsert it delegates to are both idempotent.
//
// A failure to read is returned; a failure to apply ONE order is logged and the pass
// continues, because one unattributable or malformed contribution must not stop
// every other workspace from being activated.
func (r *Reconciler) ReconcileOnce(ctx context.Context) (ReconcileReport, error) {
	var rep ReconcileReport
	orders, err := r.orders.listOrders(ctx)
	if err != nil {
		return rep, err
	}
	rep.Seen = len(orders)
	for _, o := range orders {
		kind := kindForStatus(o.Status)
		if kind == eventIgnored {
			rep.Ignored++
			continue
		}
		planCode, ok := r.planByTierID[o.TierLegacyID]
		if !ok {
			// A contribution outside our sellable tiers — an ordinary donation. Not an
			// error, and not something to act on.
			rep.Ignored++
			continue
		}
		switch kind {
		case eventActivated:
			teamID, attributed := r.attribute(ctx, o, planCode)
			if !attributed {
				rep.Unattributed++
				continue
			}
			if err := r.svc.applyActivated(ctx, providerEvent{
				kind: eventActivated, teamID: teamID, planCode: planCode,
				customerID: o.FromAccountSlug, subscriptionID: o.ID,
			}); err != nil {
				log.Printf("billing: reconcile activate order %s: %v", o.ID, err)
				continue
			}
			// nextChargeDate is the paid-through date; recording it is what finally
			// populates a column that has existed and been written by nothing.
			if o.NextChargeDate != "" {
				r.recordPeriodEnd(ctx, teamID, o.NextChargeDate)
			}
			rep.Activated++
		case eventCanceled:
			// A cancellation needs no attribution: the order id is the stable key and
			// applyCanceled looks the workspace up by it. An id we never recorded is a
			// no-op there, which is the correct answer for someone else's contribution.
			if err := r.svc.applyCanceled(ctx, providerEvent{
				kind: eventCanceled, subscriptionID: o.ID,
			}); err != nil {
				log.Printf("billing: reconcile cancel order %s: %v", o.ID, err)
				continue
			}
			rep.Canceled++
		}
	}
	return rep, nil
}

// attribute decides which workspace a contribution belongs to, and refuses to guess.
//
// The order is deliberate. A `tags` value we put on the checkout URL is the primary
// channel, but a URL is user-controlled, so a tag ALONE is never attribution: it
// must resolve to a CheckoutIntent this server recorded for that plan. Without that
// corroboration anyone could tag a payment with someone else's workspace. The
// contributor's email is the fallback for when the tag does not survive the hosted
// checkout. When neither resolves, the answer is "we do not know" — the order is
// counted as unattributed, logged once, and left entirely alone for an operator to
// settle with set-plan.
func (r *Reconciler) attribute(ctx context.Context, o providerOrder, planCode string) (teamID string, ok bool) {
	for _, tag := range o.Tags {
		intent, err := r.intents.MatchByTag(ctx, tag, planCode)
		if err == nil {
			return intent.TeamID, true
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("billing: reconcile tag lookup for order %s: %v", o.ID, err)
		}
	}
	if o.FromAccountEmail != "" {
		intent, err := r.intents.MatchByEmail(ctx, o.FromAccountEmail, planCode)
		if err == nil {
			return intent.TeamID, true
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("billing: reconcile email lookup for order %s: %v", o.ID, err)
		}
	}
	log.Printf("billing: contribution %s (%s) matches no checkout intent; left for manual attribution with `set-plan`", o.ID, planCode)
	return "", false
}

// Run drives ReconcileOnce until the context is cancelled. It is the only loop in
// this server, so its failure behaviour is stated rather than assumed:
//
//   - A reconcile error is LOGGED and retried at the next tick, never fatal. A
//     payment provider being unreachable must not take the server down.
//   - A panic is recovered at the loop boundary for the same reason: one malformed
//     order must not kill the process.
//   - Every pass logs its counts. "0 orders" and "the call failed" are different
//     lines, because a silent zero is indistinguishable from a working system with
//     no customers — which is exactly the state this project is in today.
//   - The first pass runs immediately, so a restart picks up anything that arrived
//     while the process was down rather than waiting a full interval.
func (r *Reconciler) Run(ctx context.Context, every time.Duration) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("billing: reconcile loop panicked and stopped: %v; activation is manual until restart", v)
		}
	}()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		rep, err := r.ReconcileOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			log.Printf("billing: reconcile failed: %v (retrying in %s)", err, every)
		default:
			log.Printf("billing: reconciled %d order(s): %d activated, %d canceled, %d ignored, %d unattributed",
				rep.Seen, rep.Activated, rep.Canceled, rep.Ignored, rep.Unattributed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// recordPeriodEnd stores the paid-through date on the workspace's subscription. It
// is best-effort and deliberately separate from the plan flip: the plan being right
// matters, and a missing period-end is cosmetic until something reads it.
func (r *Reconciler) recordPeriodEnd(ctx context.Context, teamID, until string) {
	sub, err := r.svc.subs.ByTeam(ctx, teamID)
	if err != nil {
		return
	}
	sub.CurrentPeriodEnd = until
	if err := r.svc.subs.Upsert(ctx, sub); err != nil {
		log.Printf("billing: recording period end for team %s: %v", teamID, err)
	}
}
