# ADR-038 Tasks

Implementation tasks for ADR-038: Dedupe on the content, refer by the id. See the parent ADR for the
decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Store what the id used to promise, on every path that mints or moves a drawer | pending | — | `go test ./internal/palace/ -run 'TestAddStampsTheContentKey\|...' -count=1 ...` |
| T2 | Dedupe on the content key, and mint an opaque id for a new row | pending | — | `go test ./internal/palace/ -run 'TestRefilingTheOriginalTextDoesNotRevertAnEdit\|...' -count=1 ...` |
| T3 | A gate that fails when an id is re-derived, or a mint path forgets its key | pending | — | `go test ./internal/palace/ -run 'TestNoPathRederivesADrawerID\|...' -count=1 ...` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `drawers.content_key` + `Drawer.ContentKey` | T2, T3 | T1 before T2 — there is nothing to dedupe on until the column exists and is written |
| T2 | `Repo.Save` upserting on `(team_id, content_key)` | T3 | T2 before T3 — T3's source gate asserts no path re-derives an id, which is only true once T2 has moved the conflict target |

## Notes

- **Allocate the migration number at merge, not at authoring.** A renumber at merge re-runs the
  migration on any database that already applied it under the old number; the crash loop and its
  repair are documented in `README.md` (Development). T1 names the file `000NN_` for this reason.
- T1 sizes its backfill against the live corpus (1,705 non-diary rows, 0 content-key collisions,
  measured 2026-08-27) but does not require it to run.
- T3's drift query is deliberately outside the acceptance fence — it needs a real corpus, and its
  result is a number to record, not a verdict.
