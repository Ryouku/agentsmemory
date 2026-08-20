# ADR-006 Tasks

Implementation tasks for ADR-006: A setting an operator changes must change something, or say why not.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T4 | none |
| 3 | T2 | T1 |
| 4 | T3 | T2 |

T4 is independent of the gate and fixes what the audit already found, so it can land first and stop
telemetry lying while the gate is still being built.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-extract-the-ranking-wiring.md) | Make the ranking wiring drivable without a server | `configureRanking` | none | done |
| [T2](T2-discover-the-inert-set.md) | Discover which knobs are inert in which mode, by running them | the discovered inert set | `configureRanking` | todo |
| [T3](T3-admit-the-condition.md) | Every discovered pair admits its condition where an operator reads it | the admission requirement | the discovered inert set | todo |
| [T4](T4-fix-what-the-audit-found.md) | Fix the three defects the audit found | honest telemetry, settable timeout, scoped CLI search | none | done |
