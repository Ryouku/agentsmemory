# ADR-018 Tasks

Implementation tasks for ADR-018: a recall belongs to the session that ran it.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T3 | none |
| 1 | T1 | none |
| 2 | T2 | T1 |

T3 is listed first deliberately and depends on nothing. It removes the path that produces fabricated
memories, needs no schema change, and ships even if the measurement finds no session identity and the
migration is withdrawn. The wrong percentages are an error; the misattributed task list is a harm.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-is-a-session-identity-reachable.md) | Establish whether a session identity reaches the recall | the finding that shapes or withdraws T2 | none | todo |
| [T2](T2-a-recall-records-who-ran-it.md) | A recall records which session ran it | `session_id`, `/stats?session=` | T1 | todo |
| [T3](T3-the-report-names-its-population.md) | The report names its population and refuses what it cannot attribute | an honest report | none | done |
