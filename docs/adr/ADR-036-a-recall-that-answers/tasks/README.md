# ADR-036 Tasks

Implementation tasks for ADR-036: Put the knowledge graph on the read path. See the parent ADR for
the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers. This
README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1, T2, T6 | none |
| 2 | T3, T7 | T1, T2 (T3) · T6 (T7) |
| 3 | T4, T5, T8 | T3 (T4, T5) · T7 (T8) |

**Wave 1 is the floor, and its shape is the point.** T1 builds the instrument before anything can
claim an improvement — there is no eval arm for fact retrieval today, so the capability is
unmeasurable and therefore unimprovable. T2 makes absence distinguishable from failure, without
which T3's sibling-wing pointer cannot be trusted. T6 fixes the write path, without which T7's entry
point would index 2.9% of the corpus (57 of 1,985 drawers carry any edge, measured 2026-08-26).

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The instrument: a fact answerable-rate with a 0% baseline | pending | F-5, F-6 | `go test ./internal/palace/ -run 'TestFactAnswerableRateIsMeasured\|TestFactsOnThePageAreScoredByMRR'` + suite |
| T2 | A lookup that distinguishes absence from failure | pending | F-12 | `go test ./internal/palace/ -run 'TestAFactLookupDistinguishesAbsenceFromFailure'` + suite |
| T6 | Every drawer carries an edge, and derived ones say so | pending | F-11, UC5-S1, UC5-S2 | `go test ./internal/palace/ -run 'TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked'` + suite |
| T3 | Facts reach the page, wing-resolved, as a pointer never a crossing | pending | F-1, F-2, F-8, F-9, UC1-S1, UC2-S1, UC2-S2 | `go test ./internal/palace/ -run 'TestAWingScopedRecallNeverReturnsAnotherWingsFact\|TestARecallNamesTheWingsThatHoldTheAnswer\|TestAFactsWingComesFromItsProvenance\|TestReturningFactsDoesNotChangeDrawerRanking'` + suite |
| T7 | A wing reports its own entry point, resolved directly | pending | F-10, F-17, UC4-S1, UC4-S2 | `go test ./internal/palace/ -run 'TestAWingReportsItsOwnEntryPoint\|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk'` + suite |
| T4 | Both entity vocabularies, and an ended fact is never current | pending | F-4, F-7, UC1-S2 | `go test ./internal/palace/ -run 'TestFactLookupMatchesBothEntityVocabularies\|TestAnEndedFactIsNeverPresentedAsCurrent'` + suite |
| T5 | A corrected record arrives carrying its correction | pending | F-3, UC3-S1, UC3-S2 | `go test ./internal/palace/ -run 'TestACorrectedRecordArrivesCarryingItsCorrection'` + suite |
| T8 | The protocol becomes an API | pending | F-13, F-14, F-15, F-16, UC6-S1, UC6-S2, UC6-S3 | `go test ./internal/palace/ -run 'TestOneCallBootstrapsAWing\|TestATruncatedBootstrapSaysWhatItDropped\|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates\|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces'` + suite |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | the fact-retrieval arm and its case set | T3, T4, T8 | T1 before any task claiming an improvement |
| T2 | absence-vs-failure on a fact lookup | T3 | T2 before T3 — the pointer rests on it |
| T3 | `Service.factsFor` (wing-resolved facts) | T4, T5, T8 | T3 before T4, T5, T8 |
| T6 | the derived-edge marker column | T7, T8 | T6 before T7 — an entry point over 2.9% coverage indexes nothing |
| T7 | `Service.EntryPoint` | T8 | T7 before T8 |
