# Task ADR-036-T7: A wing reports its own entry point, resolved directly

**Depends-on:** T6
**Covers:** F-10, F-17, UC4-S1, UC4-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `Service.EntryPoint`
**Consumes:** the derived-edge contract and marker column (T6)
**Data dependency:** hermetic

## Goal

Reaching a wing's taxonomy needs no id the server did not supply, and no graph walk.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/graphquery.go` | edit | resolve the entry point's edges directly |
| `internal/mcpserver/kg.go` | edit | register and expose it — the line that makes it DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | two red tests |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the catalogue proof |

## Ordered Steps

1. Confirm all three tests are RED.
2. Resolve the entry record and its outgoing edges DIRECTLY. Do NOT use `am_traverse`: its `max_hops` is provably inert — `via` is an intersection carried forward, so hop >=2 can never add a node. Verified 2026-08-26 from the `wing_agentmemories` `llm_init` root (25 nodes, all hop <=1) and from a leaf drawer in the same room (10 nodes, all hop 1).
3. A wing with no entry point says so, distinguishably from an error — reuse T2's state vocabulary rather than inventing a second one.
4. Register the tool and assert it appears in the CATALOGUE with its arguments. A tool the handler serves and the catalogue omits is one no agent will ever call.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestAWingReportsItsOwnEntryPoint|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestEntryPointToolIsRegisteredAndDiscoverable' -count=1 2>&1 | tee /tmp/acc36t7.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t7.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestAFactLookupDistinguishesAbsenceFromFailure|TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestAnUnlocatableFactIsCountedNotDropped|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestKGQueryResultRendersResolutionState|TestSearchResultRendersFactsAndTheSiblingPointer|TestSearchResultRendersTheCorrectionMark|TestBootstrapToolIsRegisteredAndDiscoverable' 2>&1 | tee /tmp/acc36t7b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T7 does not depend on: T7's own 3 and its
ancestors' 2 still run, so a regression in what T7 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAWingReportsItsOwnEntryPoint` | `internal/palace/recallanswers_spec_test.go` | entry record and outgoing edges returned; a wing without one says so distinguishably | F-10, UC4-S1, UC4-S2 |
| `TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk` | `internal/palace/recallanswers_spec_test.go` | resolution does not depend on multi-hop traversal | F-17 |
| `TestEntryPointToolIsRegisteredAndDiscoverable` | `internal/mcpserver/recallanswers_reach_test.go` | the tool is in the catalogue with its arguments | F-10 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two palace tests |
| 2 — something selects it | the mcpserver registration; mutation: unregister it — the palace tests stay green and the catalogue test goes red |
| 3 — the caller can discover it | the catalogue test IS this rung |
| 4 — it is used | whether any client kit stops hardcoding a root id — measured after T8, not here |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- No graph walk. A future reader must not "restore" traversal here without first deciding transitive-vs-confined.
- The absence vocabulary is T2's, not a second one.

## Risks

- T6 fixes the write path only, so on today's corpus the entry point still reaches almost nothing (97.1% orphans, measured 2026-08-26). T6 precedes this task so the edge CONTRACT is settled first; the coverage claim needs the deferred backfill and is not made here.

## Out of Scope

- Fixing `am_traverse` (deferred: docs/adr/BACKLOG.md)
- Defining the tier vocabulary (permanent: the server distinguishes eager from on-demand and does not bless particular names.)

## Stop Condition

Stop and ask if a wing turns out to need more than one entry point — that changes the shape of the result and of T8.
