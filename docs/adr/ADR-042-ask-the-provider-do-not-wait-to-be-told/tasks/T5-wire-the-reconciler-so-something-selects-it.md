# Task ADR-042-T5: Wire the reconciler so something actually selects it

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — composition root, config, docs)
**Owner:** unassigned
**Produces:** `TestOpenCollectiveActivationIsReachable` (the reachability gate named in ADR-042's `Enforced-by`)
**Consumes:** `Reconciler.ReconcileOnce(ctx)` (T4), `newOCOrderSource()` (T3)
**Data dependency:** hermetic

## Goal

Construct the reconciler from configuration and drive it on a schedule, so a paid contribution
actually reaches the plan flip — and add the gate that fails when that wiring is deleted.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | `billingConfig()` reads the four new env vars; the composition root constructs the reconciler and starts its loop. **This is the SELECTION line the whole ADR depends on** — every prior task is unreachable without it. |
| `internal/billing/billing.go` | edit | `Config` gains `OpenCollectivePersonalToken`, `OpenCollectiveSlug`, `OpenCollectiveAPIURL`, `ReconcileInterval`; `NewService` exposes a constructor for the reconciler. |
| `.env.example` | edit | Document all four. `TestReadEnvVarsAreDocumented` fails otherwise. |
| `.env.docker.example` | edit | Same, for the compose stack. |
| `docker-compose.prod.yml` | edit | Pass the new variables through. `TestDocumentedEnvVarsAreRead` binds this in the other direction. |
| `README.md` | edit | The hosted-billing block must stop saying activation is `set-plan` once it is not. |
| `cmd/server/main_test.go` | add | The reachability gate. |

## Ordered Steps

1. Write the failing test first (TDD red): `TestOpenCollectiveActivationIsReachable`, which parses
   `cmd/server/main.go` and fails when no path constructs the reconciler and starts it. Derive its
   universe from the source (like `TestEveryKnobIsSweptOrNamed` does) rather than hardcoding a
   symbol list, so a rename joins the check on the same commit. Confirm RED.
2. Add the `Config` fields and read them in `billingConfig()`. Every one must be BOTH assigned from
   the environment AND read by something, or `TestEveryConfigFieldIsPopulatedAndRead` fails — ADR-006
   binds here, and its stronger question applies: each must be read in the mode that is running, so
   they are read on the OpenCollective branch of the provider switch, not unconditionally.
3. Default `OpenCollectiveAPIURL` to `https://api.opencollective.com/graphql/v2` and
   `ReconcileInterval` to `15m`. One poll per interval is far under the measured 100 req/min
   authenticated limit.
4. Construct the reconciler ONLY when provider is `opencollective` AND the token and slug are set.
   Otherwise log which one is missing and leave activation manual — the existing boot-log block
   already has this shape; extend it rather than adding a second one.
5. Start the loop with the server's lifecycle context so shutdown stops it. Log every pass with the
   `ReconcileReport` counts, so "0 orders" and "the call failed" are never the same line.
6. A reconcile error logs and retries next interval. It is NEVER fatal — a payment provider being
   down must not take the server with it.
7. Update `.env.example`, `.env.docker.example`, `docker-compose.prod.yml` and the README block in
   the SAME commit; documentation is load-bearing here and gated in both directions.
8. Confirm GREEN, then run the repo's full gate.

## Acceptance

```bash
go test ./cmd/server/ -run 'TestOpenCollectiveActivationIsReachable' -count=1 2>&1 | tee /tmp/adr042-t5-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL|\[no tests to run\]" /tmp/adr042-t5-new.out && \
grep -q "^ok" /tmp/adr042-t5-new.out && \
go test ./cmd/server/ -run 'TestEveryConfigFieldIsPopulatedAndRead|TestEveryFlagIsRead|TestDocumentedEnvVarsAreRead|TestReadEnvVarsAreDocumented|TestNotOperatorFacingIsJustified' -count=1 && \
gofmt -l . | grep -v '_templ.go' | tee /tmp/adr042-t5-fmt.out && [ ! -s /tmp/adr042-t5-fmt.out ] && \
go build ./... && go vet ./... && go test ./... -count=1
```

The middle command is the repo's existing reachability and documentation family, run explicitly
because this task is exactly the kind of change they exist to catch.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOpenCollectiveActivationIsReachable` | `cmd/server/main_test.go` | Deleting the reconciler construction or its loop start turns this red | — |
| `TestReconcileLoopStopsOnContextCancel` | `cmd/server/main_test.go` | The goroutine exits on shutdown rather than leaking | — |
| `TestBillingConfigReadsOpenCollectiveReconcileVars` | `cmd/server/main_test.go` | All four env vars land on `Config` | — |

Existing gates that must stay green and are load-bearing here:
`TestEveryConfigFieldIsPopulatedAndRead`, `TestDocumentedEnvVarsAreRead`,
`TestReadEnvVarsAreDocumented`.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestBillingConfigReadsOpenCollectiveReconcileVars` |
| 2 — something selects it | `TestOpenCollectiveActivationIsReachable` — the mutant deletes the construction line and the gate must go red. This is the rung the whole ADR turns on |
| 3 — the caller can discover it | `.env.example` + README + compose, bound by `TestDocumentedEnvVarsAreRead` and `TestReadEnvVarsAreDocumented` in both directions |
| 4 — it is used | The per-pass log line carries the `ReconcileReport` counts; that is the first thing that measures whether activation ever happens |

## Mutation Log

## Invariants

- With no token configured, behaviour is exactly today's: no goroutine, no outbound call, manual
  `set-plan`.
- A reconcile failure never kills the server.
- The loop honours shutdown.
- Every new config field is both populated and read, on the branch that runs.

## Risks

- First background goroutine in this server. If it panics it must not take the process down — recover
  at the loop boundary and log.
- The README currently tells operators activation is manual. Leaving that true after this lands
  would be the same defect T1 fixes on the dashboard, one document over.

## Stop Condition

If `TestOpenCollectiveActivationIsReachable` cannot be written so that deleting the wiring makes it
fail — for example because the construction is indirect enough that a source parse cannot see it —
stop. A gate that cannot fail is worse than none, and the shape of the wiring should change rather
than the gate being weakened.

## Out of Scope

- Webhook-as-doorbell for lower latency (deferred: Follow-ups, ADR-042).
- An operator UI for unattributed orders (deferred: Follow-ups, ADR-042).

## Verification Log
