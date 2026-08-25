# ADR-028 Tasks

Implementation tasks for ADR-028: Return the identifier and the score a recall was decided by. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` headers. This README is a derived index — when it disagrees with a task file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |

The two tasks are independent in content and touch the same file, so they are ordered rather than parallel to keep the diff reviewable. T1 goes first because it is the half a future recording task would build on.

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | Return the recall's identifier, and accept it back | `search_id` on the `am_search` response; the optional `search_id` argument on `am_get_drawer` | none | done | `go test ./internal/mcptest/ -run "TestSearchResponseCarriesItsSearchID\|TestGetDrawerSchemaAdvertisesSearchID\|TestGetDrawerIgnoresAnUnknownSearchID"` |
| T2 | Expose the score the order was actually decided by | `blended_score` on each `am_search` hit | none | pending | `go test ./internal/mcpserver/ -run TestSearchToolDescriptionSaysBlendedIsPoolRelative` + `go test ./internal/mcptest/ -run TestHitCarriesTheScoreItWasOrderedBy` |

## Not a task here

The third half of this work — recording the fetch against the recall and reporting the ratio — is deliberately NOT a task file. It is specified in the parent ADR's Out of Scope with an explicit trigger: the first week `am_get_drawer` receives a non-empty `search_id` from a client that is not a test. Writing it as a pending task file would put a plan in the corpus for work whose precondition does not exist yet, and `adr-debt` sweeps the deferral so it resurfaces at the next `/quality-harness:adr-write` instead of being forgotten.
