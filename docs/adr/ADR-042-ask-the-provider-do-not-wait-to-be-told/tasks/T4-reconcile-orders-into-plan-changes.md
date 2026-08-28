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
go test ./internal/billing/ -run 'TestReconcileMapsOrderStatusToEventKind|TestReconcileAttributesByTagOnlyWithAMatchingIntent|TestReconcileLeavesAnUnattributableOrderAlone|TestReconcileIsIdempotent' -count=1 2>&1 | tee /tmp/adr042-t4-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t4-new.out && \
grep -q "^ok" /tmp/adr042-t4-new.out && \
go build ./... && go vet ./... && go test ./internal/billing/ ./internal/web/... -count=1
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestReconcileMapsOrderStatusToEventKind` | `internal/billing/reconcile_test.go` | All 14 known statuses map as specified, and an unknown status maps to `eventIgnored` rather than to a cancellation | — |
| `TestReconcileAttributesByTagOnlyWithAMatchingIntent` | `internal/billing/reconcile_test.go` | A tag naming team B with no intent row does NOT upgrade team B | — |
| `TestReconcileLeavesAnUnattributableOrderAlone` | `internal/billing/reconcile_test.go` | An order with no tag and an unknown email changes no plan and is counted as unattributed | — |
| `TestReconcileIsIdempotent` | `internal/billing/reconcile_test.go` | Running the same page twice leaves one subscription row and one plan value | — |
| `TestReconcileDoesNotResurrectACanceledSubscription` | `internal/billing/reconcile_test.go` | A late `ACTIVE` for an order already recorded canceled does not re-upgrade — the existing `billing.go:218-223` guard still holds on this path | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The five tests above |
| 2 — something selects it | Nothing until T5 constructs and drives it. This task must not be read as "activation works" |
| 3 — the caller can discover it | `n/a: no declared interface` — package-internal |
| 4 — it is used | `ReconcileReport` counts, logged by T5 |

## Mutation Log

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
