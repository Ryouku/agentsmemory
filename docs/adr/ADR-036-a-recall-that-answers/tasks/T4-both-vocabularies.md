# Task ADR-036-T4: Both entity vocabularies, and an ended fact is never current

**Depends-on:** T3
**Covers:** F-4, F-7, UC1-S2
**Estimated scope:** M
**Owner:** unassigned
**Produces:** none
**Consumes:** `Service.factsFor` (T3)
**Data dependency:** hermetic

## Goal

A fact is reachable through an extracted term as well as a KG entity, and an ended fact is never presented as current.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | match the query against `drawers.entities` as well as `kg_entities` |
| `internal/palace/recallanswers_spec_test.go` | edit | two red tests |

## Ordered Steps

1. Confirm both tests are RED.
2. Match against both vocabularies at query time, read-only. No schema change, no write-path change.
3. Filter `valid_to`. Precedent: `am_kg_query` already defaults to `status=current` via `kgQueryDefaultStatus`, so this extends an existing default rather than inventing one.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent' -count=1 2>&1 | tee /tmp/acc36t4.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t4.out && go test ./... -count=1 2>&1 | tee /tmp/acc36t4b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t4b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestFactLookupMatchesBothEntityVocabularies` | `internal/palace/recallanswers_spec_test.go` | a fact whose subject appears only in `drawers.entities` is reachable by a question naming that term | F-4 |
| `TestAnEndedFactIsNeverPresentedAsCurrent` | `internal/palace/recallanswers_spec_test.go` | a fact with non-empty `valid_to` is not in the current block | F-7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the two tests |
| 2 — something selects it | the second-vocabulary match in the lookup; mutation: remove it and the extracted-term test goes red |
| 3 — the caller can discover it | n/a: no declared interface — the behaviour is inside an existing call |
| 4 — it is used | T1's answerable-rate, split by which vocabulary matched |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- The two vocabularies are NOT merged. This is a read-time join only.
- 945 of 1,985 drawers carry entities (47.6%, measured 2026-08-26), so the second vocabulary has material to work with.

## Risks

- Frequency-extracted terms are noisier than authored entity names and may pull irrelevant facts. T1 measures whether they help or hurt.

## Out of Scope

- Unifying the vocabularies at the write path (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if matching both vocabularies measurably LOWERS the answerable-rate — that would mean the join costs more than it buys.
