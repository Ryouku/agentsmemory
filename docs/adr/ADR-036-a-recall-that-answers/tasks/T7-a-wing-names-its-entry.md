# Task ADR-036-T7: A wing reports its own entry point, resolved directly

**Depends-on:** T6
**Covers:** F-10, F-17, UC4-S1, UC4-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** `Service.EntryPoint`
**Consumes:** the derived-edge marker column (T6)
**Data dependency:** hermetic

## Goal

Reaching a wing's taxonomy needs no id the server did not supply, and no graph walk.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/graphquery.go` | edit | resolve the entry point's edges directly |
| `internal/mcpserver/kg.go` | edit | expose it — the line that makes it DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | two red tests |

## Ordered Steps

1. Confirm both tests are RED.
2. Resolve the entry record and its outgoing edges DIRECTLY. Do NOT use `am_traverse`: its `max_hops` is provably inert — `via` is an intersection carried forward, so hop >=2 can never add a node (verified 2026-08-26 from a hub, 25 nodes all hop <=1, and from a leaf, 10 nodes all hop 1).
3. A wing with no entry point says so, distinguishably from an error.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestAWingReportsItsOwnEntryPoint|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk' -count=1 2>&1 | tee /tmp/acc36t7.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t7.out && go test ./... -count=1 -skip 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestAFactLookupDistinguishesAbsenceFromFailure|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces' 2>&1 | tee /tmp/acc36t7b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t7b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

The `-skip` list is what makes that second command SATISFIABLE. All 17 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands — every earlier task
would be unable to record an exit-0 run, and a fence that cannot pass blocks its wave as effectively
as one that cannot fail. Verified 2026-08-26: 17 `--- FAIL` lines in `./internal/palace` before any
of this ADR is built. The list skips exactly the stubs owned by tasks this one does NOT depend on;
T7's own 2 and its ancestors' 1 still run, so the fence still
catches a regression in anything T7 was built on top of.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAWingReportsItsOwnEntryPoint` | `internal/palace/recallanswers_spec_test.go` | entry record and outgoing edges returned; a wing without one says so | F-10 |
| `TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk` | `internal/palace/recallanswers_spec_test.go` | resolution does not depend on multi-hop traversal | F-17 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | the mcpserver registration; mutation: unregister and the tool vanishes from the catalogue |
| 3 — the caller can discover it | the tool appears in the catalogue with its arguments |
| 4 — it is used | T8 consumes it; and whether any client stops hardcoding a root id |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- No graph walk. Any future reader must not "restore" traversal here without first deciding transitive-vs-confined.

## Risks

- 0 drawers are named as a triple object in this workspace (measured 2026-08-26), so the entry point may find nothing until T6 has produced edges. That is why T6 precedes it.

## Out of Scope

- Fixing `am_traverse` (deferred: docs/adr/BACKLOG.md)
- Defining the tier vocabulary (permanent: the server distinguishes eager from on-demand and does not bless particular names.)

## Stop Condition

Stop and ask if a wing turns out to need more than one entry point — that changes the shape of the result and of T8.
