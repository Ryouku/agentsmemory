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
regions the chooser scored and discarded. If they are not — if the answer is usually in no region at
all — then the failure is synthesis or absence, T2 buys nothing, and the ADR is withdrawn. The hot
path has been broken twice in one day; it is not being changed on a hypothesis.

T2 without T3 is a domain field nothing serves, which is this repository's signature defect. T2's
Reachability table says so rather than letting it look finished.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-is-the-answer-in-another-window.md) | Is the answer in a window the chooser discarded? | the measurement that accepts or withdraws T2 | none | done — 4 of 6, and the score saturates in 9 of 9: T2 proceeds |
| [T2](T2-a-hit-carries-its-regions.md) | A hit carries its matching regions | `SnippetRegions`, `MemoryIdentity` | T1 | done — functions only; nothing calls them until T3 |
| [T3](T3-put-the-choice-on-the-wire.md) | Put the choice on the wire, and re-judge | `regions`, `identity`, `content_coverage` | T2 | wired and live; the re-judge of the 32 is the remaining step |
