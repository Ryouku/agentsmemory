# ADR-015 Tasks

Implementation tasks for ADR-015: a wing merge must correct the search index it invalidates.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 1 | T2 | none |
| 2 | T3 | T1, T2 |

T1 and T2 are independent and can run together. T3 needs both: T2 gives it the mechanism, T1 gives
it the acceptance. Building T3 first would mean verifying a write by the fact that it returned nil.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-see-the-drift.md) | A command reports where the index disagrees with the rows | `VectorStore.Points`, `IndexDrift`, `doctor --index` | none | todo |
| [T2](T2-a-payload-can-be-corrected.md) | A stored point's payload can be corrected without its vector | `VectorStore.SetPayload` | none | todo |
| [T3](T3-a-merge-corrects-what-it-invalidates.md) | A merge corrects the index it invalidates | a drift-free `MergeWing` | T1, T2 | todo |
