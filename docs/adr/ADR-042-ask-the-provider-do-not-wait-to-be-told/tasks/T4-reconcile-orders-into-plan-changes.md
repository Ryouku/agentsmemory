# Task ADR-042-T4: Turn an order into the plan change the webhook would have made

**Depends-on:** T2, T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `Reconciler`, `Reconciler.ReconcileOnce(ctx) (ReconcileReport, error)`
**Consumes:** `IntentRepo` + `intentTag(teamID)` (T2), `orderSource` + `providerOrder` (T3)
**Data dependency:** needs ONE real contribution through the live hosted checkout, to confirm the `tags` value set by T2 arrives on `Order.tags`. The unit gate below is hermetic and does NOT prove this; the sign-off line must record the Open Collective order id the tag was read back from, and which channel attributed it (tag or email).

## Goal

Map each incoming order onto the existing `providerEvent` vocabulary, attribute it to a workspace,
and apply it through the existing `applyActivated` / `applyCanceled` — never a second plan-flip path.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/billing/reconcile.go` | add | The mapper and the reconcile pass. |
| `internal/billing/billing.go` | edit | Expose `applyActivated`/`applyCanceled` to the reconciler within the package; add nothing public. |
| `internal/billing/reconcile_test.go` | add | Mapping, attribution, idempotency and refusal tests. |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestReconcileMapsOrderStatusToEventKind`,
   `TestReconcileAttributesByTagOnlyWithAMatchingIntent`,
   `TestReconcileLeavesAnUnattributableOrderAlone`, `TestReconcileIsIdempotent`. Confirm RED.
2. Map status → kind, exhaustively over the 14 values read from the live enum 2026-08-28:
   `ACTIVE`, `PAID` → `eventActivated`; `CANCELLED`, `EXPIRED`, `REFUNDED`, `REJECTED`, `ERROR` →
   `eventCanceled`; `NEW`, `PENDING`, `PROCESSING`, `REQUIRE_CLIENT_CONFIRMATION`, `DISPUTED`,
   `IN_REVIEW`, `PAUSED` → `eventIgnored`. Write the mapping as a table keyed by the enum string and
   default UNKNOWN statuses to `eventIgnored` plus a log line — a status Open Collective adds later
   must never be silently read as a cancellation.
3. Resolve the plan code from `TierLegacyID` using the configured tier→plan map, not from the
   amount. An unknown tier is `eventIgnored` with a log line.
4. Attribute in this order, and stop at the first hit: (a) a `tags` value that matches a
   `billing_checkout_intents` row for that plan; (b) `FromAccountEmail` matching an intent row's
   email; (c) NOTHING — leave the order alone, log it once with its order id, and continue. A tag
   with no matching intent is NOT attribution.
5. Build the `providerEvent` and call the existing `applyActivated` / `applyCanceled`. Set
   `subscriptionID` from the order id so `applyCanceled`'s lookup key works, and populate
   `CurrentPeriodEnd` from `NextChargeDate`.
6. Return a `ReconcileReport` carrying counts (seen, activated, canceled, ignored, unattributed) so
   the caller can log a number rather than a silence.
7. Confirm GREEN.

## Acceptance

```bash
go test ./internal/billing/ -run 'TestReconcile' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t4-new.out && \
grep -q "^ok" /tmp/adr042-t4-new.out && \
go build ./... && go vet ./... && go test ./internal/billing/ ./internal/web/... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestReconcileMapsOrderStatusToEventKind` | `internal/billing/reconcile_test.go` | All 14 known statuses map as specified, and an unknown status maps to `eventIgnored` rather than to a cancellation | — |
| `TestReconcileAttributesByTagOnlyWithAMatchingIntent` | `internal/billing/reconcile_test.go` | A tag with no recorded intent does NOT upgrade the workspace it names; the same order DOES activate once the intent exists | — |
| `TestReconcileAttributesByEmailWhenTheTagIsAbsent` | `internal/billing/reconcile_test.go` | The email fallback attributes when no tag survived — the ADR's answer if `tags` does not round-trip | — |
| `TestReconcileLeavesAnUnattributableOrderAlone` | `internal/billing/reconcile_test.go` | An order with no tag and an unknown email changes no plan and is counted as unattributed | — |
| `TestReconcileIgnoresAContributionOutsideOurTiers` | `internal/billing/reconcile_test.go` | An ordinary donation (no tier) is not treated as a purchase even when it carries a valid tag | — |
| `TestReconcileIsIdempotent` | `internal/billing/reconcile_test.go` | Three passes leave one subscription row and one plan value, and `nextChargeDate` populates `CurrentPeriodEnd` | — |
| `TestReconcileDoesNotResurrectACanceledSubscription` | `internal/billing/reconcile_test.go` | A stale `ACTIVE` for an order already recorded canceled does not re-upgrade — the existing `applyActivated` guard holds on this path too | — |
| `TestReconcileReturnsTheReadError` | `internal/billing/reconcile_test.go` | A failing order source is an error, not a quiet pass with nothing to do | — |

**Deviation from the ADR's step 2, taken on the ADR's own advice:** `ERROR` is mapped to
`eventIgnored`, not `eventCanceled`. The ADR's Risks section flagged this exact call and said the
failure mode of ignoring (a workspace keeps Pro slightly too long) is strictly safer than the failure
mode of cancelling (a paying customer downgraded mid-retry, which is much harder to notice). Recorded
here because the ADR's Decision text still lists `REJECTED` and `ERROR` together.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above |
| 2 — something selects it | Nothing until T5 constructs and drives it. This task must not be read as "activation works" |
| 3 — the caller can discover it | `n/a: no declared interface` — package-internal |
| 4 — it is used | `ReconcileReport` counts, logged by T5 |

## Mutation Log

- 2026-08-28 · 366dd22* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Accepts a tag without requiring a recorded CheckoutIntent to corroborate it. The tag rides in a user-controlled URL, so this lets anyone credit a payment to any workspace. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · 366dd22* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Makes a status OpenCollective adds after this was written downgrade every workspace holding such an order — the silent mass-downgrade the default exists to prevent. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · 366dd22* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Stops requiring the contribution to name one of our sellable tiers, so a 5 EUR one-off donation would activate a 50 EUR/month plan. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · eec2269* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Accepts a tag without requiring a recorded CheckoutIntent to corroborate it, letting anyone credit a payment to any workspace. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · eec2269* · mutant killed · exit 1 · `internal/billing/reconcile.go` · Makes a status OpenCollective adds later downgrade every workspace holding such an order. · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79

## Invariants

- The plan flip happens ONLY through `applyActivated` / `applyCanceled`. No second write path.
- A tag alone never activates; an intent row must corroborate it.
- An unknown or new order status never downgrades anyone.
- Reconciliation is idempotent: it runs every interval forever and must converge.

## Risks

- The status mapping is a judgement about someone else's state machine. `ERROR` → `eventCanceled` is
  the one to challenge: it is mapped as cancel because an errored order is not a paid one, but if it
  turns out to be a transient state a workspace would be downgraded mid-retry. Named here so the
  reviewer sees it; if unsure, move `ERROR` to `eventIgnored` — the failure mode of ignoring is a
  workspace that keeps Pro slightly too long, which is strictly safer than one that loses it wrongly.
- Email matching is weak if a contributor pays under a different email. It is the fallback, not the
  primary, and its failure lands in the unattributed bucket rather than on a wrong workspace.

## Stop Condition

If the real contribution shows `tags` absent AND `fromAccount` email unreadable with the token's
permission, stop: both attribution channels are gone and the ADR's Decision needs revisiting before
more code is written.

## Out of Scope

- The periodic driver and config — T5.
- Distinguishing `DISPUTED` / `IN_REVIEW` from `eventIgnored` (deferred: Follow-ups, ADR-042).

## Verification Log
- 2026-08-28 · 366dd22* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
- 2026-08-28 · eec2269* · exit 0 · `go test ./internal/billing/ -run 'TestReconcile' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \ …` · acceptance-sha256:8640ad4200592ff8c3bfa110ea7da8fb3e4f338d58f7592f9ace2de253f21f79
