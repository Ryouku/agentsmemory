# Task ADR-036-T5: A corrected record arrives carrying its correction

**Depends-on:** T3
**Covers:** F-3, UC3-S1, UC3-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** none
**Consumes:** `Service.factsFor` (T3)
**Data dependency:** hermetic

## Goal

A record that has been retracted, superseded or qualified is returned WITH that correction, never silently or hidden.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/memory_search.go` | edit | join correction edges at collapse time |
| `internal/mcpserver/drawers.go` | edit | render the mark — the line that makes it DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | the red test |

## Ordered Steps

1. Confirm `TestACorrectedRecordArrivesCarryingItsCorrection` is RED.
2. Join `retracts`, `supersedes` and `qualifies` INCOMING for returned records.
3. Return the record in its normal rank position, carrying the edge and the replacement id. Marking, not hiding — a retraction can itself be wrong.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestACorrectedRecordArrivesCarryingItsCorrection' -count=1 2>&1 | tee /tmp/acc36t5.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t5.out && go test ./... -count=1 -skip 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces' 2>&1 | tee /tmp/acc36t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t5b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

The `-skip` list is what makes that second command SATISFIABLE. All 17 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands — every earlier task
would be unable to record an exit-0 run, and a fence that cannot pass blocks its wave as effectively
as one that cannot fail. Verified 2026-08-26: 17 `--- FAIL` lines in `./internal/palace` before any
of this ADR is built. The list skips exactly the stubs owned by tasks this one does NOT depend on;
T5's own 1 and its ancestors' 7 still run, so the fence still
catches a regression in anything T5 was built on top of.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestACorrectedRecordArrivesCarryingItsCorrection` | `internal/palace/recallanswers_spec_test.go` | a superseded record carries its correction edge and replacement id | F-3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the test above |
| 2 — something selects it | the collapse-time join; mutation: remove it and corrections stop travelling |
| 3 — the caller can discover it | the mark appears in the rendered hit |
| 4 — it is used | how many recalls return a marked record — currently unmeasurable because nothing marks |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- Nothing is hidden and nothing is demoted. Rank is unchanged, which keeps this separable from F-9.

## Risks

- A live specimen exists: `drawers.entities is_written_only_by am_mine (retired)` was contradicted by a later fact while both read `current: true`. That pair is the test fixture this task should use.

## Out of Scope

- Demoting or excluding superseded records (permanent: a retraction can itself be wrong, and a ranking input is a signal rather than a gate.)

## Stop Condition

Stop and ask if a record has more than one incoming correction with conflicting replacements — that is an ordering question this task does not decide.
