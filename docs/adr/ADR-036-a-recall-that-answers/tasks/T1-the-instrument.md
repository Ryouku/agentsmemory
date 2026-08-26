# Task ADR-036-T1: The instrument: a fact answerable-rate with a 0% baseline

**Depends-on:** none
**Covers:** F-5, F-6
**Estimated scope:** M
**Owner:** unassigned
**Produces:** the fact-retrieval eval arm, and `testdata/factcases-2026-08-26.jsonl` (the frozen case set)
**Consumes:** none
**Data dependency:** **Needs real data.** The case set is drawn from the live palace's `kg_triples`, while the fence is hermetic — it runs against the FROZEN file this task commits, not the live palace. Freezing is what makes the gate able to see the requirement: an unfrozen corpus lets the fence pass while the actual dependency goes unmet.

## Goal

Fact retrieval becomes measurable against a frozen, dated corpus, so that no later task can report an improvement without an instrument — and so the instrument itself cannot drift.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | register the arm — the line that SELECTS it; an arm nothing registers appears in no table |
| `internal/palace/factcases.go` | add | load the frozen case set |
| `internal/palace/testdata/factcases-2026-08-26.jsonl` | add | the frozen corpus: questions, gold triple ids, and a header row carrying the source palace, the triple count, and the date |
| `internal/palace/recallanswers_spec_test.go` | edit | the two red tests |

## Ordered Steps

1. Confirm `TestFactAnswerableRateIsMeasured` and `TestFactsOnThePageAreScoredByMRR` are RED.
2. Build the case set from the live palace's 196 triples and **commit it frozen**, with its denominator, source and date in the file. Draw the question phrasing from real `search_events` rows, not from the triples' own words — a case set written from the text it scores is circular.
3. Register the arm in `evalArms`. Add the check that fails when that one line is deleted.
4. Report the answerable-rate as a fraction WITH its denominator. `12/30` and `0.40` are not the same claim when the corpus can change.
5. Assert the frozen file's recorded count matches the rows actually present, so a truncated corpus fails loudly instead of quietly reporting a rate over fewer cases.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ -run 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR' -count=1 2>&1 | tee /tmp/acc36t1.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t1.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestAFactLookupDistinguishesAbsenceFromFailure|TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestAnUnlocatableFactIsCountedNotDropped|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestKGQueryResultRendersResolutionState|TestSearchResultRendersFactsAndTheSiblingPointer|TestSearchResultRendersTheCorrectionMark|TestAddDrawerResultReportsItsEdge|TestEntryPointToolIsRegisteredAndDiscoverable|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t1b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T1 does not depend on: T1's own 2 and its
ancestors' 0 still run, so a regression in what T1 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestFactAnswerableRateIsMeasured` | `internal/palace/recallanswers_spec_test.go` | the arm exists, is registered, and reports a fraction with its denominator over the frozen corpus | F-5 |
| `TestFactsOnThePageAreScoredByMRR` | `internal/palace/recallanswers_spec_test.go` | the arm scores ordering by MRR on the same paired bootstrap as every other arm | F-6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | the `evalArms` registration; mutation: delete it and the arm vanishes from every table |
| 3 — the caller can discover it | `eval --arms` lists it in `--help` |
| 4 — it is used | this task IS the rung-4 instrument for the fact-retrieval work in this ADR |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- Baseline is 0% and stays stated — a non-zero result is only meaningful against it.
- The arm does not alter any existing arm's score.
- The corpus is frozen and dated. A rate quoted without its denominator is not a result.

## Risks

- A case set built from the same triples it scores is circular; question phrasing comes from real `search_events` rows to break that.
- F-6 was originally worded "once facts share the page…", which presupposed T3 — a task cannot green a fact that depends on a later task. It now asserts a property of the instrument alone.

## Out of Scope

- Improving the rate (deferred: this ADR's T3)
- Abstention (permanent: ADR-001 owns it and is Accepted with six pending tasks.)

## Stop Condition

Stop and ask if fewer than ~30 triples yield answerable questions. That floor is a judgement, not a power calculation: below it a single case moves the rate by more than 3 points, which exceeds the 0.01 MRR noise floor measured 2026-08-26 between two provably identical arms.
