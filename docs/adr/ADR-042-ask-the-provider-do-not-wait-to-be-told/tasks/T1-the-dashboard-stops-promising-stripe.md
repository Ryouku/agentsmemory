# Task ADR-042-T1: Stop the dashboard promising a provider and a portal it does not have

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** none — corrects existing behaviour, adds no contract
**Consumes:** none
**Data dependency:** hermetic

## Goal

Under `BILLING_PROVIDER=opencollective`, stop rendering a "Manage your plan" button whose handler
can only fail, and stop telling the user their payment goes through Stripe and returns them to the
dashboard.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/handlers.go` | edit | `canManage` (line 231) gates on plan alone; a workspace with no `subscriptions` row must not be offered a portal. This line is what SELECTS the ManageCard — deleting the new condition is the mutation T1 must be killed by. |
| `internal/web/views/project.templ` | edit | Line 280 promises "Secure checkout via Stripe … then land back here"; both clauses are false under OpenCollective. |
| `internal/web/views/project_templ.go` | regenerate | `templ generate` output — the served HTML. Never hand-edited. |
| `internal/web/views/models.go` | edit | `ProjectVM` needs the field the new `canManage` reads (`HasBillingRelationship`), and its doc comment states why plan alone is insufficient. |
| `internal/web/handlers_test.go` | add | The gate test. |
| `internal/web/views/upgrade_card_test.go` | add | The copy test. |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCanManageRequiresARecordedSubscription` asserting a
   paid-plan workspace with no `subscriptions` row yields `CanManage == false`, and
   `TestUpgradeCardDoesNotNameAProviderItMayNotUse` asserting the rendered `UpgradeCard` contains no
   "Stripe" and makes no "land back here" promise. Confirm both are RED.
2. Add `HasBillingRelationship` to `ProjectVM`, populated from `billing.Service` by asking whether a
   subscription row exists for the team.
3. Add the lookup to `Service` as a small, nil-safe method (`HasRelationship(ctx, teamID) bool`)
   rather than exposing `*Repo` to the web layer — the consumer keeps depending on the two methods
   it uses, per the existing `PlanStore` precedent.
4. Change `canManage` to `s.billing.Enabled() && isAdmin && !onFree && !isComped && hasRelationship`.
5. Rewrite the `UpgradeCard` hint to text true under BOTH providers and BOTH activation paths:
   name no provider, promise no redirect back, promise no timing.
6. Run `templ generate`; never edit `*_templ.go`.
7. Confirm both tests are GREEN and the full package suite still passes.

## Acceptance

```bash
go test ./internal/web/... -run 'TestCanManageRequiresARecordedSubscription|TestUpgradeCardDoesNotNameAProviderItMayNotUse' -count=1 2>&1 | tee /tmp/adr042-t1-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t1-new.out && \
grep -q "^ok" /tmp/adr042-t1-new.out && \
go build ./... && go vet ./... && go test ./internal/web/... ./internal/billing/ -count=1
```

The first command runs ONLY the two new tests, so the regression suites in the last command cannot
carry the verdict by themselves. The `grep -q "^ok"` is what makes this red today: with the tests
absent, `-run` matches nothing, Go prints `ok … [no tests to run]` and exits 0, so the exit code
alone would pass on an empty tree.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCanManageRequiresARecordedSubscription` | `internal/web/handlers_test.go` | A `pro_monthly` workspace with no `subscriptions` row gets `CanManage == false`; one WITH a row gets `true` | — |
| `TestUpgradeCardDoesNotNameAProviderItMayNotUse` | `internal/web/views/upgrade_card_test.go` | Rendered `UpgradeCard` HTML contains neither "Stripe" nor a claim the user returns to the dashboard | — |

Both must fail when the mechanism is removed. For the first, delete `&& hasRelationship` and watch
it go red. For the second, restore the old hint string.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCanManageRequiresARecordedSubscription` |
| 2 — something selects it | `handlers.go:231` is itself the selection; the mutation removing `&& hasRelationship` proves the test reaches it |
| 3 — the caller can discover it | n/a: no declared interface — this removes a control rather than adding one |
| 4 — it is used | Observable as the absence of a failing flash; nothing measures this yet |

## Mutation Log

## Invariants

- The Stripe flow is unchanged: a Stripe workspace with a real subscription still sees ManageCard.
- `*_templ.go` is only ever regenerated, never hand-edited.
- The copy must remain true after T5 makes activation automatic — so it states no timing and names
  no provider. A later task may make it MORE specific; it must never make it false again.

## Risks

- Rewriting the hint to describe manual activation would go stale the moment T5 lands. Mitigated by
  the invariant above: describe the destination, not today's mechanism.
- `HasRelationship` adds a query to the project list. It is one indexed lookup per team on a page
  that already does several; if it shows up, fold it into the existing subscription read.

## Stop Condition

If the reviewer judges that the ManageCard should instead be shown with a different action for
OpenCollective (linking straight to the project page without going through `ManageURL`), stop —
that is a product decision about what "manage" means for a donations platform, not an
implementation detail, and it changes this task's shape.

## Out of Scope

- Making activation automatic — that is T2–T5.
- The `getBillingSuccess` copy, which OpenCollective never reaches today and T5 revisits.

## Verification Log
