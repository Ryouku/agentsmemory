# ADR-029 Tasks

Implementation tasks for ADR-029: A trace that cannot lie about what it did. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` headers. This README is a derived index — when it disagrees with a task file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |

The two tasks are contract-independent — no task consumes anything another produces — but both touch `internal/palace/service.go`. They are therefore ordered rather than parallel, so each diff stays reviewable and each mutant stays attributable to one claim. T1 goes first because a span that reports success over a failure is the only defect here with a live consumer: the recall statistics are computed from the table whose failed writes it currently hides.

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | A span that reports success only for work that succeeded | `telemetry.ReasonTimeout`; `Repo.recordSearch` returning `error`; honest outcomes on four spans | none | pending | `go test ./internal/palace/ -run "TestRecordStageReportsAWriteThatFailed\|TestRerankTimeoutIsNotReportedAsAnOutage\|TestEvidenceReportsHowManyDocumentsItActuallySelected\|TestRerankSaysWhetherItReorderedAnything"` + `go test ./internal/mcpserver/ -run "TestAnchorFailureReachesTheToolSpan\|TestEmptyWingLookupFailureIsNotSilence"` |
| T2 | What was asked, what was searched, and what was dropped | `am.limit_requested`, `am.query_runes`, `am.query_truncated`, `am.max_distance`, `am.wing_source`; `scopeDrops` from `survivorsFrom` | none | pending | `go test ./internal/palace/ -run "TestRequestedLimitSurvivesTheClamp\|TestTruncatedQueryLeavesEvidence\|TestScopeDropsAreCounted\|TestScopeDropsLandOnTheArmSpanForEvalArms"` + `go test ./internal/mcpserver/ -run TestWingSourceDistinguishesCallerFromServer` |

## Withdrawn

**T3 — a set-equality gate over `SearchStages()`.** Withdrawn after adversarial verification, and kept here as a named withdrawal rather than a quiet deletion. Its stated defect was disproved BY MUTATION: a verifier deleted `StageCloset` from the list (nine names, so the `len < 8` threshold stayed satisfied) and neutered the emission, and the test still went red — the `searchKids` literal at `otel_test.go:71-80` is an independent authority, not the drift risk the task claimed.

A second review then found the task's premise wrong in another way: `searchKids` is a **direct-child topology** assertion, not a duplicate of the stage list. `StageHydrate` is deliberately checked under `StageRetrieve` and `StageEvidence` is a child of rerank, so a flat `SearchStages()` cannot derive it. The task's step 5 — "derive `searchKids` from the list" — would have destroyed a hierarchy assertion to remove a duplication that was not one.

What remains of the idea is thin and honest: a stage in neither list is unchecked, a stage started with a string literal is in neither set, and a stage on an error path no fixture reaches evades both directions. None of that justifies changing production span emission to satisfy a gate.

## Not a task here

**Backend identity on the span.** `VECTOR_BACKEND` and `EMBED_BACKEND` reach no span, and the second is the highest-consequence finding the sweep returned: the embedding model decides what every distance in every trace and every eval table means, and both default paths serve the same dimension count, so `am.dim` cannot separate them. It is excluded because it is `cmd/server/main.go` wiring rather than the search path, and it earns its own record rather than a fourth task here. Receipted in `BACKLOG.md` §"From ADR-029".

**The six tail findings.** The adaptive BM25 weight's resolved value, the whole-memory-to-400-rune degradation carried only in a prose `note`, `SearchQuery.Context` presence on the rerank span, the coerced-to-zero cosine rejection in semantic evidence selection, `closetBoostsAt`'s three discard paths, and the evidence stage's window counts. All verified, none of them make a span lie. Receipted with their finding text intact, because an ADR that fixes thirty things gives none of them a killed mutant.

**An anchor/staleness stage.** The sweep found the anchor pass has no span at all. T1 makes its FAILURE visible on the enclosing tool span; giving it a stage of its own is a new stage rather than a list repair, and is receipted.
