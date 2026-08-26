# Task ADR-036-T3: Facts reach the page, wing-resolved, as a pointer never a crossing

**Depends-on:** T1, T2
**Covers:** F-1, F-2, F-8, F-9, UC1-S1, UC2-S1, UC2-S2
**Estimated scope:** L
**Owner:** unassigned
**Produces:** `Service.factsFor` (wing-resolved facts)
**Consumes:** the fact-retrieval arm (T1), absence-vs-failure (T2)
**Data dependency:** hermetic

## Goal

A question reaches a fact in its own wing, and learns that matches exist in wings it did not search — without seeing their content.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | embed entity labels; resolve wing from provenance; add the fact block |
| `internal/palace/palace.go` | edit | the fields carrying facts and the sibling-wing pointer |
| `internal/mcpserver/drawers.go` | edit | render them — the line that makes them DISCOVERABLE |
| `internal/palace/recallanswers_spec_test.go` | edit | four red tests |

## Ordered Steps

1. Confirm the four tests are RED.
2. Embed entity labels into the existing vector store under a distinct namespace.
3. Resolve a fact's wing through `source_drawer_id`; unresolvable provenance means "elsewhere", never "here".
4. Return in-wing facts as a block BESIDE the drawer hits, and name the sibling wings holding the rest.
5. Assert drawer selection and order are byte-identical before and after.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestAWingScopedRecallNeverReturnsAnotherWingsFact|TestARecallNamesTheWingsThatHoldTheAnswer|TestAFactsWingComesFromItsProvenance|TestReturningFactsDoesNotChangeDrawerRanking' -count=1 2>&1 | tee /tmp/acc36t3.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t3.out && go test ./... -count=1 2>&1 | tee /tmp/acc36t3b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t3b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAWingScopedRecallNeverReturnsAnotherWingsFact` | `internal/palace/recallanswers_spec_test.go` | no foreign wing's subject, predicate or object appears anywhere in the response | F-1 |
| `TestARecallNamesTheWingsThatHoldTheAnswer` | `internal/palace/recallanswers_spec_test.go` | sibling wings holding matches are named and said to be queryable | F-2 |
| `TestAFactsWingComesFromItsProvenance` | `internal/palace/recallanswers_spec_test.go` | wing derives from `source_drawer_id`; unresolvable means elsewhere | F-8 |
| `TestReturningFactsDoesNotChangeDrawerRanking` | `internal/palace/recallanswers_spec_test.go` | drawer selection and order are unchanged | F-9 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the four tests |
| 2 — something selects it | the `Search` call site building the block; mutation: drop it and the fact block disappears |
| 3 — the caller can discover it | the fields appear in the rendered tool result, not only on the Go struct |
| 4 — it is used | T1's answerable-rate, whose baseline is 0% by construction |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- Ranking is untouched — F-9 is what stops this being confounded with a retrieval change.
- No foreign wing content, ever. The pointer names wings; it never carries facts.

## Risks

- Reachability caps at 46% (90 of 196 triples resolve to a drawer, measured 2026-08-26). A low answerable-rate is expected and is not evidence the retrieval failed.

## Out of Scope

- Adding a `wing` column to `kg_triples` (deferred: docs/adr/BACKLOG.md)
- Changing the reranker or fusion (permanent: ADR-030 and ADR-034 own that area.)

## Stop Condition

Stop and ask if entity-label embeddings would need a different model from the drawer embedder — mixing embedding spaces in one namespace is a different decision.
