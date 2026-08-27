# Task ADR-038-T2: Dedupe on the content key, and mint an opaque id for a new row

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `Repo.Save` upserting on `(team_id, content_key)`; new drawers minted with an opaque id
**Consumes:** `drawers.content_key` + `Drawer.ContentKey` (T1)
**Data dependency:** hermetic

## Goal

Re-filing a memory that has since been edited in place stops reverting the edit, and re-filing the
edited text stops creating a duplicate row.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/repo.go` | edit | `Save`'s `clause.OnConflict` target moves from the primary key to `(team_id, content_key)` — this is the ONE line that selects the new behaviour, and deleting it restores both defects |
| `internal/palace/chunk.go` | edit | add the opaque mint used for NEW rows; `DrawerID` stays as the content-key recipe |
| `internal/palace/service.go` | edit | `Add` (`:660`) mints an opaque id and sets the content key rather than using the hash as the id |
| `internal/palace/import.go` | edit | `AbsorbDrawers` (`:82`) likewise — `import.go:21` documents re-run safety as resting on the recomputed id; that sentence moves to the key |
| `internal/palace/mine.go` | edit | `Mine` (`:155`) likewise |
| `internal/palace/copywing.go` | edit | `CopyWing` (`:130`) likewise |
| `internal/palace/dedup_test.go` | add | the two failing tests below |

## Ordered Steps

1. Write the two failing tests first. Both are RED against `main` today, and both were the measured
   failure modes in the ADR's Context:
   - **the silent revert.** File a source-less drawer, `Update` its content, then `Add` the ORIGINAL
     text again. Assert the edited row still holds the edit, and that a SECOND row now exists
     holding the original. Today the re-add mints the id the row still carries and
     `OnConflict{UpdateAll: true}` overwrites the edit.
   - **the duplicate.** File a source-less drawer, `Update` its content, then `Add` the EDITED text.
     Assert exactly ONE row exists. Today the hash of the new content differs from the stored id, so
     a second row with identical content is inserted.
2. Move `Save`'s conflict target to `(team_id, content_key)`.
3. Mint opaque ids for new rows at all four mint sites.
4. Confirm import idempotency still holds — re-run `AbsorbDrawers` over the same batch twice and
   assert the row count does not grow. This is `import.go:21`'s contract and it now rests on the key.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestRefilingTheOriginalTextDoesNotRevertAnEdit|TestRefilingTheEditedTextDoesNotDuplicate|TestAbsorbDrawersStaysIdempotentOnTheContentKey' -count=1 2>&1 | tee /tmp/acc38c.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38c.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1 2>&1 | tee /tmp/acc38d.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38d.out
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRefilingTheOriginalTextDoesNotRevertAnEdit` | `internal/palace/dedup_test.go` | the silent-revert mechanism is gone | — |
| `TestRefilingTheEditedTextDoesNotDuplicate` | `internal/palace/dedup_test.go` | the duplicate-row mechanism is gone | — |
| `TestAbsorbDrawersStaysIdempotentOnTheContentKey` | `internal/palace/dedup_test.go` | the migration path's re-run safety survives the move | — |

**Shapes the creation path can already produce, decided rather than assumed:** a multi-chunk memory
(each chunk gets its own key — assert chunk 1 and chunk 2 of one memory do not collide); a named
source, where `purgeSource` deletes before insert and the key is never consulted (assert unchanged);
a drawer filed while the embedder is down (`SaveUnembedded`, a different `OnConflict` clause at
`repo.go:109` — assert its target moves too, or state in the task why it does not).

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the three unit tests |
| 2 — something selects it | `Save`'s conflict target. Mutation: restore the target to `id` and both new tests go red |
| 3 — the caller can discover it | n/a: no declared interface — `am_add_drawer`'s schema and response are unchanged; the behaviour change is that the tool stops being wrong |
| 4 — it is used | every `am_add_drawer` call exercises it. Observable as the absence of duplicate-content rows: the query in T3 reports it. |

## Mutation Log

## Invariants

- No existing drawer id changes. New rows get opaque ids; old rows keep theirs.
- `purgeSource`'s named-source wholesale replacement is untouched.
- A journal still never dedupes: diary rows carry an empty key and sit outside the partial index.

## Risks

- `SaveUnembedded` has its own `OnConflict` (`repo.go:109`) and is easy to miss — it is the deferred-embedding path, and missing it means a drawer filed while the embedder is down keeps the old behaviour. Named in the Tests section for that reason.
- An opaque id whose shape is indistinguishable from a hash invites the next reader to re-derive it. Mint it in a visibly different shape.

## Stop Condition

Stop and ask if moving the conflict target requires changing the primary key itself. It should not —
the key is a unique index, not the PK — and if it does, the additive-migration premise the ADR's
rollback rests on has broken.

## Out of Scope

- The gate that keeps future paths honest — T3.
- Re-chunking on update (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
