# ADR-021 Tasks

Implementation tasks for ADR-021: the handshake carries the protocol.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 1 | T2 | none |
| 2 | T3 | T1, T2 |

T1 and T2 are independent and touch different halves of the repo: T1 is server-side and reaches
every client at once, T2 is client-side and reaches one. T3 measures the result of T1 through the
client T2 installs, so it needs both — and it is the task that can send T1 back.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-the-server-says-what-it-expects.md) | The initialize response carries the wing rule and the recall-first instruction | the `instructions` text | none | todo |
| [T2](T2-a-kit-for-a-client-with-nowhere-to-put-anything.md) | `--agent claude-desktop` registers the server instead of a human doing it | `claudeDesktopKit`, the config entry | none | todo |
| [T3](T3-does-the-instruction-change-the-answer.md) | Measure whether a client given the instructions stops inventing the rule | the measurement, and T1's verdict | T1, T2 | todo |
