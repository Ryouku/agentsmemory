# Task ADR-038-T1: Store what the id used to promise, on every path that mints or moves a drawer

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — schema + every write path)
**Owner:** unassigned
**Produces:** `drawers.content_key` column + unique index `(team_id, content_key)`; `Drawer.ContentKey` field; `DrawerID` re-documented as the content-key recipe
**Consumes:** none
**Data dependency:** hermetic — the migration and its tests run from an empty database. The backfill was SIZED against the live corpus (1,705 non-diary rows, 0 collisions, measured 2026-08-27), but nothing in this task requires that corpus to run.

## Goal

Every drawer row carries the hash of what it currently holds, in its own column, written by every
path that mints a drawer and recomputed by every path that changes a hashed field.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `db/migrations/000NN_drawers_content_key.sql` | add | the column and the unique index. **NN is allocated at merge, never at authoring** — a renumber at merge re-runs on a database that already applied it (`README.md`, Development) |
| `internal/palace/palace.go` | edit | `Drawer` gains `ContentKey string` with its gorm tag — the field the column maps to |
| `internal/palace/chunk.go` | edit | `DrawerID`'s doc comment stops calling it the identity of a drawer and calls it the content key; body unchanged |
| `internal/palace/service.go` | edit | `Add` (`:660`) and `WriteDiary` (`:2054`) — the mint sites. Diary sets an EMPTY key; that is the line that SELECTS a journal out of dedup |
| `internal/palace/import.go` | edit | `AbsorbDrawers` (`:82`) mints the key |
| `internal/palace/mine.go` | edit | `Mine` (`:155`) mints the key |
| `internal/palace/copywing.go` | edit | `CopyWing` (`:130`) mints the key for the TARGET team, not the source |
| `internal/palace/repo.go` | edit | `Update` (`:380`) recomputes the key in the same `updates` map that changes content/wing/room |
| `internal/palace/admin.go` | edit | `RelabelDrawerWingReturningIDs` (`:295`) and `RelabelDrawerWing` (`:313`,`:324`,`:342`) recompute the key in the same statement that moves the wing — this is the line whose absence would leave a merged drawer describing a wing it no longer sits in |
| `internal/palace/contentkey_test.go` | add | the failing tests |

## Ordered Steps

1. Write the failing tests first. They must be RED because `Drawer` has no `ContentKey` field, so
   they do not compile — the strongest red available:
   - a drawer filed by `Add` carries `ContentKey == DrawerID(team, wing, room, source, idx, content)`;
   - after `Update` rewrites the content, the key equals the hash of the NEW content;
   - after `MergeWing` moves a drawer, the key equals the hash computed with the TARGET wing;
   - two diary entries with identical text, agent and topic both persist, and both carry an EMPTY key.
2. Add the migration: `ALTER TABLE drawers ADD COLUMN content_key TEXT NOT NULL DEFAULT ''`, then a
   backfill `UPDATE` computing nothing (SQLite cannot SHA-256) — so the backfill is a Go step, see 3.
   Then `CREATE UNIQUE INDEX ... ON drawers(team_id, content_key) WHERE content_key != ''`. The
   partial predicate is what keeps diary rows and any un-backfilled row out of the index.
3. Add the backfill as a startup repair that runs once and **aborts on the first collision** rather
   than skipping the row. A silent partial backfill is the failure shape this repo keeps catching;
   a failed migration is recoverable, a half-done one is invisible.
4. Add `ContentKey` to `Drawer` and write it at all five mint sites and both mutation sites.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestAddStampsTheContentKey|TestUpdateRecomputesTheContentKey|TestMergeWingRecomputesTheContentKey|TestTwoIdenticalDiaryEntriesBothPersistWithNoContentKey' -count=1 2>&1 | tee /tmp/acc38a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38a.out && go test ./internal/palace/ ./internal/store/ ./cmd/server/ -count=1 2>&1 | tee /tmp/acc38b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38b.out
```

The four new tests run ALONE first, so the already-green palace suite in the second command cannot
carry the verdict by itself. `no test files` is in the guard because a `-run` filter that matches
nothing and a package with no tests both exit 0.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAddStampsTheContentKey` | `internal/palace/contentkey_test.go` | the mint path writes the key | — |
| `TestUpdateRecomputesTheContentKey` | `internal/palace/contentkey_test.go` | an in-place content edit updates the key | — |
| `TestMergeWingRecomputesTheContentKey` | `internal/palace/contentkey_test.go` | a wing move updates the key — the path that is easiest to forget | — |
| `TestTwoIdenticalDiaryEntriesBothPersistWithNoContentKey` | `internal/palace/contentkey_test.go` | a journal is not deduped, and the partial index is what allows it | — |
| `TestBackfillAbortsOnCollision` | `internal/palace/contentkey_test.go` | a colliding corpus fails the migration rather than skipping a row | — |
| `TestTheContentKeyIndexIsPartial` | `internal/palace/contentkey_test.go` | reads the real index definition via `pragma_index_list`/`sql` and fails when the `WHERE content_key != ''` predicate is absent — **the one clause in this ADR whose loss destroys data rather than duplicating it** | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the four unit tests above |
| 2 — something selects it | every mint/mutation site writes the key; mutation: delete the write in `RelabelDrawerWing` and `TestMergeWingRecomputesTheContentKey` goes red |
| 3 — the caller can discover it | n/a: no declared interface — the column is internal, no tool argument or response field changes in this task |
| 4 — it is used | T2 is the consumer. Until T2 lands the column is written and read by nothing, which is deliberate and is why T2 is not optional. |

## Mutation Log

## Invariants

- No drawer id changes. Anything that would re-key a row belongs to a different decision.
- The vector store is not written during the migration — there is no cross-store transaction to get wrong.
- Diary rows never enter the unique index, and the partial predicate — not a convention — is what keeps them out.
- Every failure mode of this task ends in a duplicate row, never in an overwritten one. The partial predicate is the whole reason that is true.

## Risks

- A mint path added between authoring and execution silently misses the key. T3's derived gate is the answer; until it lands, the Affected Files table is the list, and it was taken from `grep -n "DrawerID(" --include="*.go"` on 2026-08-27.
- `NOT NULL DEFAULT ''` on a large table rewrites it on some SQLite versions. 2,013 rows on the live corpus; trivial, but confirm on the real database before merging rather than on a fixture.

## Stop Condition

Stop and ask if the backfill finds any collision on the real corpus. Measured 0 on 2026-08-27, so a
non-zero count means something changed and the decision's cheap-rollback premise needs re-checking.

**What would make this criterion impossible to fail?** A backfill that skips colliding rows instead
of aborting. That is why step 3 says abort — a skip makes the check unfalsifiable.

## Out of Scope

- Reading the key for dedup — that is T2's job.
- Repairing the 27 drifted rows (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
