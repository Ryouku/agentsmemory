# ADR-017 Tasks

Implementation tasks for ADR-017: a subagent is a session, and must recall and persist like one.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 2 | T3 | T1 |

T1 first and alone: it measures whether an injected instruction changes what a subagent DOES, and
its result decides whether T2 and T3 instruct or act. Building them first would mean shipping a
mechanism whose central assumption was never tested. T2 and T3 are independent of each other.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-does-an-instruction-reach-a-subagent.md) | Measure whether a subagent obeys an injected instruction | the compliance measurement | none | done |
| [T2](T2-a-subagent-wakes-knowing-where-it-is.md) | A subagent wakes knowing its wing and that memory exists | `SubagentStart` registration, agent definitions | T1 | done |
| [T3](T3-a-subagent-offers-back-what-it-learned.md) | A subagent offers back what it learned | `SubagentStop` registration, the installed agent definitions | T1 | done |

T3 also repaired what T2 shipped unreachable: `agentsmemory-researcher.md` was
embedded in the binary and written to no disk, so ADR-017's mechanism 1 — the
one the Decision calls the fix that "cannot fail for compliance reasons" — had no
install path at all. See the ADR's "Mechanism 1 shipped unreachable" section.
