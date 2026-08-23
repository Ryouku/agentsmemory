# ADR-010 Tasks

Implementation tasks for ADR-010: A memory is ended, not overwritten — and retraction is not
erasure. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

Strictly sequential: the column has to exist before a correction can set it, and a correction has
to exist before recall can be asked to hide one. T3 is last and is the one that decides whether the
ADR shipped — a superseded record reachable by any default route recreates the exact defect that
motivated all of this.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-a-validity-window-on-a-drawer.md) | Give a drawer a validity window | `valid_to`, `superseded_by`, `current()` | none | todo |
| [T2](T2-correcting-a-memory-supersedes-it.md) | Correcting supersedes; erasure leaves the agent surface | supersede semantics | the columns | todo |
| [T3](T3-recall-sees-only-what-is-current.md) | Recall returns current; history when asked | current-only recall + `include_history` | supersede semantics | todo |
