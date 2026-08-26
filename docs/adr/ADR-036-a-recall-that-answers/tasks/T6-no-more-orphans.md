# Task ADR-036-T6: Every drawer carries an edge, and derived ones say so

**Depends-on:** none
**Covers:** F-11, UC5-S1, UC5-S2
**Estimated scope:** L
**Owner:** unassigned
**Produces:** the derived-edge marker column
**Consumes:** none
**Data dependency:** hermetic

## Goal

A filed drawer is reachable by traversal, and a server-derived edge is distinguishable from one a writer authored.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/00028_kg_triples_derived.sql` | add | nullable marker; `00027` is taken by ADR-034 on PR #61 — checked across every branch 2026-08-26 |
| `internal/palace/kg.go` | edit | carry the marker |
| `internal/palace/service.go` | edit | attach an edge at write time — the line that SELECTS it |
| `internal/palace/recallanswers_spec_test.go` | edit | the red test |

## Ordered Steps

1. Confirm `TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked` is RED.
2. Add migration `00028` with a `-- +goose Down`.
3. Attach a server-derived edge on the write path, MARKED as derived.
4. An authored edge always wins; a derived edge never overwrites one.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked' -count=1 2>&1 | tee /tmp/acc36t6.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t6.out && go test ./... -count=1 -skip 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestAFactLookupDistinguishesAbsenceFromFailure|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestAWingReportsItsOwnEntryPoint|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces' 2>&1 | tee /tmp/acc36t6b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t6b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

The `-skip` list is what makes that second command SATISFIABLE. All 17 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands — every earlier task
would be unable to record an exit-0 run, and a fence that cannot pass blocks its wave as effectively
as one that cannot fail. Verified 2026-08-26: 17 `--- FAIL` lines in `./internal/palace` before any
of this ADR is built. The list skips exactly the stubs owned by tasks this one does NOT depend on;
T6's own 1 and its ancestors' 0 still run, so the fence still
catches a regression in anything T6 was built on top of.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked` | `internal/palace/recallanswers_spec_test.go` | a drawer filed with no edge gets a derived one, marked; an authored edge is not overwritten | F-11 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the test above |
| 2 — something selects it | the `Service.Add` call site; mutation: remove it and drawers file as orphans again |
| 3 — the caller can discover it | `am_add_drawer`'s result reports whether the drawer has an edge and whether it was derived |
| 4 — it is used | orphan rate per wing, reportable and expected to fall from the 97.1% measured 2026-08-26 |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- A derived edge is always distinguishable from an authored one — otherwise the noise it may introduce is unmeasurable and unremovable.
- ADR-016's entity stamping is untouched.

## Risks

- Derived edges invent taxonomy the writer did not choose, and the extraction side derives zero hallways today. The marker is what keeps that measurable and reversible.

## Out of Scope

- Backfilling edges for the 1,928 existing orphans (deferred: docs/adr/BACKLOG.md)
- Repairing the 16 dangling `source_drawer_id` pointers (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if the derived edge would need a predicate vocabulary the server has to invent — that is a product decision, not an implementation one.
