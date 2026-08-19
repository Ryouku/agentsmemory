# Task ADR-001-T4: Return the verdict over MCP and record it in telemetry

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** `am_search` `confidence` field, `search_events.verdict` column
**Consumes:** `Search` returning a populated confidence (T3)

## Goal

Give the agent the verdict it can act on, and record it so the operating point can later be audited against production traffic rather than only against the eval.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | serialise `confidence` on the `am_search` result and describe it in the tool description |
| `db/migrations/00023_search_event_verdict.sql` | add | nullable `verdict` column, with a down migration that drops it |
| `internal/palace/recallstats.go` | edit | carry the verdict into `recordSearch` |
| `internal/mcpserver/search_test.go` | add | pin the field's presence and shape |
| `internal/palace/recallstats_test.go` | edit | pin the verdict is persisted and that a null verdict reads back cleanly |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSearchResultCarriesConfidence` asserting `am_search` returns the field with its four documented values, and `TestSearchEventRecordsVerdict` asserting the column round-trips including the unset case. Commit red.
2. Add migration `00023_search_event_verdict.sql` — nullable TEXT, up and down.
3. Thread the verdict into `searchEventRow` and `recordSearch`.
4. Serialise `confidence` in the `am_search` handler as an object carrying verdict, top score, threshold and backend, so a consumer that disagrees with our operating point can apply its own.
5. Extend the tool description to say what `unknown` means — an uncalibrated palace, not a low-confidence answer.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/mcpserver/ ./internal/palace/ -run "TestSearchResultCarriesConfidence|TestSearchEventRecordsVerdict|TestRecallStats" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSearchResultCarriesConfidence` | `internal/mcpserver/search_test.go` | the wire surface exposes the verdict and its inputs | — |
| `TestSearchEventRecordsVerdict` | `internal/palace/recallstats_test.go` | the column round-trips, including unset | — |

## Invariants

- The column is nullable and no read path requires it, so the down migration is safe at any time.
- Existing `am_search` consumers that ignore the new field see no change.

## Risks

- One more write on the hottest telemetry path. Mitigation: same row, one short string; `recordSearch` is already best-effort and must stay so — a telemetry failure may never fail a search.

## Stop Condition

Stop if adding the column requires rewriting existing rows; it must be nullable and additive.

## Out of Scope

- Reading the recorded verdicts back for production calibration (deferred: docs/adr/BACKLOG.md)

## Verification Log
