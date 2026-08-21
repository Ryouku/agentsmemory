# ADR-016 Tasks

Implementation tasks for ADR-016: a memory an agent files must be navigable, or the graph must say
it is empty. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 1 | T3 | none |
| 2 | T2 | T1 |

T1 and T3 are independent. T2 is gated on T1's measurement and may be WITHDRAWN by it — that is the
pre-registration, not a formality. T3 stands either way, and matters more if T2 is withdrawn.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-measure-before-deciding.md) | Measure what the extractor would produce, before wiring it | the measurement that accepts or withdraws T2 | none | done — 24.6% vs a 20% bar: T2 proceeds, carrying a candidate-rule fix the same run exposed |
| [T2](T2-entities-on-the-write-path.md) | A drawer an agent files carries its entities | populated `drawers.entities` | T1 | todo |
| [T3](T3-an-empty-graph-says-why.md) | A graph tool that cannot answer says so | `emptyGraphNote` | none | todo |
