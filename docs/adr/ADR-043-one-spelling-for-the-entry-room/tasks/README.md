# ADR-043 Tasks

Implementation tasks for ADR-043: One spelling for the entry room, and a tier the entry point
actually reaches. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T1, T2 |

T1 and T2 are independent — one corrects the artifacts, the other corrects the mechanism — and either
may go first. T3 is last because it writes data that only T2 can read back, and because its first
step is the read that can stop the whole record.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | The served onboarding document teaches the room the code resolves | pending | — | `go test ./internal/repohygiene/ -run 'TestTheServedDocumentTeachesTheRoomTheCodeResolves' -count=1 …` |
| T2 | An entry point that resolves reaches the mandatory tier | pending | — | `go test ./internal/palace/ -run 'TestBootstrapReachesTheMandatoryTier' -count=1 …` |
| T3 | Read the hosted palace, then seed this repository's entry point | pending | — | human-observed sign-off via `adr-verify --human` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T2 | `Bootstrap` returning `must.*` targets outside the root room | T3 | T2 before T3 — without it T3 writes drawers nothing reads |
| T1 | `entryRoomDisagreements()` | none | internal to T1; listed because the gate and its falsifiability subtest must drive the same function |
| T3 | the `wing_agentmemories` `llm_init` root drawer and its `must.*` edges | none | last |

## Notes

- **T3 needs two live palaces and writes to one of them.** Its first ordered step reads the hosted
  workspace and can stop the record; nothing is written anywhere before that read is classified.
- T3's sign-off must name one of `ship`, `withdraw` or `blocked`, and this README's status cell for
  T3 must carry the status it maps to (`done` / `failed` / `blocked`) — checked by
  `TestAHumanObservedSignOffAgreesWithTheIndex`.
