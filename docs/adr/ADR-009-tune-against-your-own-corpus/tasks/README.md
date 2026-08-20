# ADR-009 Tasks

Implementation tasks for ADR-009: Tune retrieval against the operator's own corpus. See the parent
ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

Strictly sequential, and T1 is first for a reason beyond dependency: it measures the query mode
nobody has run. If the literal mode changes the sign of the lexical result, the tuner is solving a
different problem than the one the paraphrase tables suggest — and it is better to learn that
before building the rule than after.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-measure-the-mode-nobody-sampled.md) | Run the query mode nobody has ever run | both tables on one corpus | none | todo |
| [T2](T2-the-decision-rule.md) | A decision rule that refuses more often than it moves | `TuneResult` + held-out rule | the two tables | todo |
| [T3](T3-the-tune-command.md) | `agentsmemory tune`, and a config the server reads | the subcommand + precedence | `TuneResult` | todo |
