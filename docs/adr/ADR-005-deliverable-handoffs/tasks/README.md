# ADR-005 Tasks

Implementation tasks for ADR-005: Make cross-project handoffs deliverable. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T1 |

T1 and T2 touch different files and share no contract; T3 documents the argument T1 adds, so it
cannot be written before T1 exists.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-refuse-an-undeliverable-handoff.md) | Refuse a handoff into a wing nobody will resolve to | `WingIsEmpty`, `confirm_new_wing`, the refusal | none | done |
| [T2](T2-surface-a-waiting-inbox.md) | Name a waiting inbox in the call every session already makes | `InboxCount`, the `am_status` `inbox` field | none | todo |
| [T3](T3-teach-the-convention-and-reconcile.md) | Put the convention where agents read it, and rescue the six orphans | corrected bootstrap + both centralised skills; reconciled wings | `confirm_new_wing` (T1) | todo |
