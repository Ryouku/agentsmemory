# Task ADR-036-T1: The instrument: a fact answerable-rate with a 0% baseline

**Depends-on:** none
**Covers:** F-5, F-6
**Estimated scope:** M
**Owner:** unassigned
**Produces:** the fact-retrieval eval arm and its case set
**Consumes:** none
**Data dependency:** needs a case set built from the live palace's own kg_triples

## Goal

Fact retrieval becomes measurable, so that no later task can report an improvement without an instrument.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/eval.go` | edit | register the arm — this is the line that SELECTS it; an arm nothing registers appears in no table |
| `internal/palace/factcases.go` | add | build a case set whose gold answers are kg_triples |
| `internal/palace/recallanswers_spec_test.go` | edit | the two red tests |

## Ordered Steps

1. Confirm `TestFactAnswerableRateIsMeasured` and `TestFactsOnThePageAreScoredByMRR` are RED (they are — committed as failing stubs with the spec).
2. Build the case set: questions whose gold answer is a specific `kg_triple`. **Needs real data** — the live palace's 196 triples — while the fence below is hermetic, so record the corpus size and date in the sign-off.
3. Register the arm in `evalArms`, and add a check that fails if the registration line is deleted.
4. Report binary answerable-rate. Baseline is 0% by construction: search returns no facts today.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestFactAnswerableRateIsMeasured|TestFactsOnThePageAreScoredByMRR' -count=1 2>&1 | tee /tmp/acc36t1.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t1.out && go test ./... -count=1 2>&1 | tee /tmp/acc36t1b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t1b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestFactAnswerableRateIsMeasured` | `internal/palace/recallanswers_spec_test.go` | the arm exists, is registered, and reports a fraction | F-5 |
| `TestFactsOnThePageAreScoredByMRR` | `internal/palace/recallanswers_spec_test.go` | ordering is scored on the same paired bootstrap as every other arm | F-6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests above |
| 2 — something selects it | the `evalArms` registration; mutation: delete it and the arm vanishes from every table |
| 3 — the caller can discover it | `eval --arms` lists it in `--help` output |
| 4 — it is used | this task IS the rung-4 instrument for every other task in this ADR |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- Baseline is 0% and stays stated — a non-zero result is only meaningful against it.
- The arm does not alter any existing arm's score.

## Risks

- A case set built from the same triples it scores is circular. Mitigate by drawing questions from real `search_events` phrasing, not from the triples' own words.

## Out of Scope

- Improving the rate (deferred: this ADR's T3)
- Abstention (permanent: ADR-001 owns it and is Accepted with six pending tasks.)

## Stop Condition

Stop and ask if fewer than ~30 triples can be turned into answerable questions — below that the instrument cannot separate a real gain from noise.
