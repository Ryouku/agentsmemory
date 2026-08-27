# Task ADR-038-T1: Give a drawer a validity window

> Re-authored 2026-08-27 from ADR-010's T1, which this record supersedes. The decision is unchanged;
> the task moved because the content-key index in T2 needs `valid_to` to exist before it is created.

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `drawers.valid_to`, `superseded_by`, `ended_reason`, `ended_at`, and the repo predicates that read them
**Consumes:** none
**Data dependency:** hermetic for the tests; the migration is additionally checked against a copy of a real database, because "every existing row reads as current with no backfill" is a claim about rows nobody wrote for this test

## Goal

A drawer can be current or ended, ending never deletes, and every existing row reads as current with
no backfill.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/000NN_drawers_validity_window.sql` | add | the four columns. **NN allocated at merge, never at authoring** (`README.md`, Development) |
| `internal/palace/palace.go` | edit | `Drawer` gains `ValidTo`, `SupersededBy`, `EndedReason`, `EndedAt` with their gorm tags |
| `internal/palace/repo.go` | edit | a `current()` scope every read predicate composes with, and `End(id, reason)` — the ONE place a row becomes historical, so a second ending path cannot diverge from the first |
| `internal/palace/validity_test.go` | add | the failing tests |

## Ordered Steps

1. Write the failing tests first — RED because the fields do not exist, so they do not compile:
   - a freshly filed drawer is current (`valid_to` empty), and `current()` returns it;
   - `End(id, reason)` sets `valid_to`, `ended_at` and `ended_reason`, leaves `content` and the row
     itself untouched, and `current()` stops returning it;
   - `End` on an already-ended drawer is refused rather than silently re-ending it with a new reason,
     because the first ending is the one that is true;
   - an ending with an empty reason is refused — the reason is the whole point of recording an end.
2. Add the migration: four columns, all `NOT NULL DEFAULT ''`. **Empty `valid_to` means current, so
   every existing row is already correct and there is no backfill** — that is the property this
   shape was chosen for, and it is what makes the rollback free.
3. Add the fields and the `current()` scope. Do not wire it into recall yet: that is T5, and doing it
   here would change what `am_search` returns in a task whose acceptance cannot see recall.
4. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestAFreshDrawerIsCurrent|TestEndSetsTheWindowAndKeepsTheRow|TestEndRefusesAnAlreadyEndedDrawer|TestEndRefusesAnEmptyReason' -count=1 2>&1 | tee /tmp/acc38t1a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t1a.out && go test ./internal/palace/ ./cmd/server/ -count=1 2>&1 | tee /tmp/acc38t1b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t1b.out
```

The four new tests run ALONE first, so the already-green palace suite in the second command cannot
carry the verdict by itself.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAFreshDrawerIsCurrent` | `internal/palace/validity_test.go` | the default state is current, with no backfill | — |
| `TestEndSetsTheWindowAndKeepsTheRow` | `internal/palace/validity_test.go` | ending never deletes — the content survives | — |
| `TestEndRefusesAnAlreadyEndedDrawer` | `internal/palace/validity_test.go` | the first ending is the true one; a second would overwrite the reason | — |
| `TestEndRefusesAnEmptyReason` | `internal/palace/validity_test.go` | an ending with no why records that something ended and destroys the only thing worth keeping about it | — |
| `TestExistingRowsReadAsCurrentAfterMigration` | `internal/palace/validity_test.go` | run against a copy of a real database, not a fixture — the no-backfill claim is about rows this test did not write | — |

**Shapes the creation path can already produce, decided rather than assumed:** a multi-chunk memory
(ending one chunk of a memory — refuse it here and let T4 decide, since a memory is the unit); a
diary entry (append-only already, and out of scope — assert it is untouched); a drawer still pending
embedding (`embedded_at IS NULL` — ending it must not resurrect it into the embed queue).

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the four unit tests |
| 2 — something selects it | nothing yet, deliberately: T2 consumes the column in its index predicate and T4 calls `End`. **A task whose new capability nothing selects is normally this repo's characteristic defect** — it is acceptable here only because two named siblings consume it, and if either is dropped this column must be dropped with it |
| 3 — the caller can discover it | n/a: no declared interface — no tool argument or response field changes in this task |
| 4 — it is used | T4 and T5. Until they land this is schema nothing reads, which is why they are not optional. |

## Mutation Log

## Invariants

- Ending never deletes a row, a vector, an anchor or an edge.
- Empty `valid_to` means current. No migration ever backfills a value into it.
- `End` is the single place a row becomes historical.

## Risks

- A column added and never read is exactly the defect this repo keeps catching. Mitigated only by T2 and T4 landing; if this ADR stops after T1, the migration should be reverted rather than left as dead schema.
- `NOT NULL DEFAULT ''` on a large table rewrites it on some SQLite versions. 2,024 rows locally; confirm against the hosted row count before merging (T2 carries the pre-flight).

## Stop Condition

Stop and ask if any existing row cannot read as current without a backfill — that would mean the
empty-means-current choice is wrong, and every downstream task rests on it.

**What would make this criterion impossible to fail?** Testing it only against fixtures this task
wrote. That is why `TestExistingRowsReadAsCurrentAfterMigration` runs against a copy of a real
database.

## Out of Scope

- Wiring `current()` into recall — T5.
- The supersede verb and the reason-carrying tools — T4.
- Applying the window to diary entries (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
