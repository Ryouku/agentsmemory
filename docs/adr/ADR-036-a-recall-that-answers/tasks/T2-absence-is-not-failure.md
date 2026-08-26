# Task ADR-036-T2: A lookup that distinguishes absence from failure

**Depends-on:** none
**Covers:** F-12
**Estimated scope:** S
**Owner:** unassigned
**Produces:** absence-vs-failure on a fact lookup
**Consumes:** none
**Data dependency:** hermetic

## Goal

A fact lookup that resolved nothing is distinguishable from one that did not resolve at all.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/kg.go` | edit | return a resolved/unresolved signal beside the count |
| `internal/mcpserver/kg.go` | edit | surface it — the line that makes it DISCOVERABLE to a caller |
| `internal/palace/recallanswers_spec_test.go` | edit | the red test |

## Ordered Steps

1. Confirm `TestAFactLookupDistinguishesAbsenceFromFailure` is RED.
2. Observed 2026-08-26: `am_kg_query` returned `count: 0` with no error for a nonexistent entity AND a nonexistent predicate. Reproduce both in the test.
3. Return whether the entity/predicate resolved, distinct from how many facts matched.
4. Surface it in the tool result, not only in the Go struct.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestAFactLookupDistinguishesAbsenceFromFailure' -count=1 2>&1 | tee /tmp/acc36t2.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t2.out && go test ./... -count=1 -skip 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR|TestAQuestionReachesTheFactThatAnswersIt|TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking|TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent|TestACorrectedRecordArrivesCarryingItsCorrection|TestEveryDrawerCarriesAnEdgeAndDerivedOnesAreMarked|TestAWingReportsItsOwnEntryPoint|TestTheBootstrapResolvesEdgesDirectlyNotByGraphWalk|TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces' 2>&1 | tee /tmp/acc36t2b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t2b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

The `-skip` list is what makes that second command SATISFIABLE. All 18 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands — every earlier task
would be unable to record an exit-0 run, and a fence that cannot pass blocks its wave as effectively
as one that cannot fail. Verified 2026-08-26: 18 `--- FAIL` lines in `./internal/palace` before any
of this ADR is built. The list skips exactly the stubs owned by tasks this one does NOT depend on;
T2's own 1 and its ancestors' 0 still run, so the fence still
catches a regression in anything T2 was built on top of.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAFactLookupDistinguishesAbsenceFromFailure` | `internal/palace/recallanswers_spec_test.go` | a nonexistent entity and a nonexistent predicate are each distinguishable from a real empty result | F-12 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the test above |
| 2 — something selects it | the mcpserver render site; mutation: drop the field and the test goes red |
| 3 — the caller can discover it | the field appears in the tool RESULT — a struct field the handler never renders is invisible to every agent |
| 4 — it is used | T3 depends on it; a pointer built on a fail-open lookup cannot be trusted |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- A real empty result still reports zero — this adds a signal, it does not change counts.

## Risks

- Callers may treat "unresolved" as an error and stop. It is not an error; the field is advisory and the call still succeeds.

## Out of Scope

- Validating entity spelling on write (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if `am_kg_query` turns out to have callers beyond the MCP layer that would break on a wider result shape.
