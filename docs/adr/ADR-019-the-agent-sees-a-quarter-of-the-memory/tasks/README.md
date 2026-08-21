# ADR-019 Tasks

Implementation tasks for ADR-019: a page must show the answer, not a quarter of the memory and a
flag. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

Strictly sequential, and T1 is not a formality. It asks whether the answers agents miss are in
windows the chooser discarded. If they are not — if the answer is usually in no window at all — then
the failure is synthesis or absence, T2 buys nothing, and the ADR is withdrawn. The hot path has
been broken twice in one day; it is not being changed on a hypothesis.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-is-the-answer-in-another-window.md) | Is the answer in a window the chooser discarded? | the measurement that accepts or withdraws T2 | none | todo |
| [T2](T2-a-snippet-shows-every-place-that-matched.md) | A snippet shows every place that matched | multi-window snippets | T1 | todo |
| [T3](T3-say-what-was-left-out.md) | Say what was left out, in a field that varies | `content_coverage`, `regions_omitted` | T2 | todo |
