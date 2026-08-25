# ADR-029 Tasks

Implementation tasks for ADR-029: A trace that cannot lie about what it did. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` headers. This README is a derived index — when it disagrees with a task file, the task file wins and the README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | none |

The three tasks are contract-independent — no task consumes anything another produces — but all three touch `internal/palace/service.go`. They are therefore ordered rather than parallel, so each diff stays reviewable and each mutant stays attributable to one claim. T1 goes first because a span that reports success over a failure is the only defect here with a live consumer: the recall statistics are computed from the table whose failed writes it currently hides.

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | A span that reports success only for work that succeeded | `telemetry.ReasonTimeout`; `Repo.recordSearch` returning `error`; honest outcomes on four spans | none | pending | `go test ./internal/palace/ -run "TestRecordStageReportsAWriteThatFailed\|TestRerankTimeoutIsNotReportedAsAnOutage\|TestEvidenceReportsHowManyDocumentsItActuallySelected\|TestRerankSaysWhetherItReorderedAnything"` + `go test ./internal/mcpserver/ -run "TestAnchorFailureReachesTheToolSpan\|TestEmptyWingLookupFailureIsNotSilence"` |
| T2 | What was asked, what was searched, and what was dropped | `am.limit_requested`, `am.query_runes`, `am.query_truncated`, `am.max_distance`, `am.wing_source`; `scopeDrops` from `survivorsFrom` | none | pending | `go test ./internal/palace/ -run "TestRequestedLimitSurvivesTheClamp\|TestTruncatedQueryLeavesEvidence\|TestScopeDropsAreCounted\|TestScopeDropsLandOnTheArmSpanForEvalArms"` + `go test ./internal/mcpserver/ -run TestWingSourceDistinguishesCallerFromServer` |
| T3 | A stage list that is an identity, in both directions | `StageEvidence` emitted unconditionally and declared; a set-equality gate | none | pending | `go test ./internal/telemetry/ -run TestSearchStagesIsTheWiringList` + `go test ./internal/palace/ -run "TestSearchEmitsSemanticStageSpans\|TestEmittedSearchStagesAreAllDeclared"` |

## Not a task here

**Backend identity on the span.** `VECTOR_BACKEND` and `EMBED_BACKEND` reach no span, and the second is the highest-consequence finding the sweep returned: the embedding model decides what every distance in every trace and every eval table means, and both default paths serve the same dimension count, so `am.dim` cannot separate them. It is excluded because it is `cmd/server/main.go` wiring rather than the search path, and it earns its own record rather than a fourth task here. Receipted in `BACKLOG.md` §"From ADR-029".

**The six tail findings.** The adaptive BM25 weight's resolved value, the whole-memory-to-400-rune degradation carried only in a prose `note`, `SearchQuery.Context` presence on the rerank span, the coerced-to-zero cosine rejection in semantic evidence selection, `closetBoostsAt`'s three discard paths, and the evidence stage's window counts. All verified, none of them make a span lie. Receipted with their finding text intact, because an ADR that fixes thirty things gives none of them a killed mutant.

**An anchor/staleness stage.** The sweep found the anchor pass has no span at all. T1 makes its FAILURE visible on the enclosing tool span; giving it a stage of its own is a new stage rather than a list repair, and is receipted.
