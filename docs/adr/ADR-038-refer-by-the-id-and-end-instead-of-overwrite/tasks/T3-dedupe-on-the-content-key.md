# Task ADR-038-T3: Dedupe on the content key, mint an opaque id, and end what a re-file dropped

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `Repo.Save` upserting on `(team_id, content_key)`; new drawers minted with an opaque id; `purgeSource` as a set difference on the content key that ENDS rather than deletes
**Consumes:** `drawers.content_key` + `Drawer.ContentKey` (T2); `drawers.valid_to` (T1)
**Data dependency:** hermetic

## Goal

Re-filing a memory that has since been edited in place stops reverting the edit, re-filing the
edited text stops creating a duplicate row, and re-filing a named source stops re-keying and
un-anchoring the chunks it did not change.

**The opaque mint and the `purgeSource` change are ONE commit.** Shipping the mint alone makes every
re-file of a named source re-key every drawer under it — a regression on the property this ADR
exists to protect. Step 1's first test is what fails if they are separated.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/repo.go` | edit | `Save`'s `clause.OnConflict` target moves from the primary key to `(team_id, content_key)` — this is the ONE line that selects the new behaviour, and deleting it restores both defects |
| `internal/palace/chunk.go` | edit | add the opaque mint used for NEW rows; `DrawerID` stays as the content-key recipe |
| `internal/palace/service.go` | edit | `Add` (`:660`) mints an opaque id and sets the content key rather than using the hash as the id |
| `internal/palace/import.go` | edit | `AbsorbDrawers` (`:82`) likewise — `import.go:21` documents re-run safety as resting on the recomputed id; that sentence moves to the key |
| `internal/palace/mine.go` | edit | `Mine` (`:155`) likewise |
| `internal/palace/copywing.go` | edit | `CopyWing` (`:130`) likewise |
| `internal/palace/service.go` | edit | `purgeSource` (`:844`) becomes a set difference: upsert the new set by content key, then END only the rows under the triple whose key is not in it. Today it deletes rows, vectors, derived edges AND anchors (`repo.go:225`) before `Add` re-inserts |
| `internal/palace/repo.go` | edit | a by-key variant of `IDsBySource`/`DeleteBySource` so the purge can name what to keep — the line that SELECTS survival for an unchanged chunk |
| `internal/palace/dedup_test.go` | add | the failing tests below |

## Ordered Steps

1. Write the failing tests first. All are RED against `main` today, and each was a measured failure
   mode in the ADR's Context:
   - **the re-key regression, and it must be written BEFORE the mint changes.** File a named source
     of three chunks, attach an anchor to chunk 0, re-file the source with **identical** content,
     and assert every id is unchanged and the anchor still exists. It is red today for the anchor
     (`DeleteBySource` strips it — 39 of the palace's 41 anchored drawers are exposed) and it goes
     red for the ids the moment step 3 lands without step 2b. This test is the reason the mint and
     the purge are one commit.
   - **the silent revert.** File a source-less drawer, `Update` its content, then `Add` the ORIGINAL
     text again. Assert the edited row still holds the edit, and that a SECOND row now exists
     holding the original. Today the re-add mints the id the row still carries and
     `OnConflict{UpdateAll: true}` overwrites the edit.
   - **the duplicate.** File a source-less drawer, `Update` its content, then `Add` the EDITED text.
     Assert exactly ONE row exists. Today the hash of the new content differs from the stored id, so
     a second row with identical content is inserted.
2. Move `Save`'s conflict target to `(team_id, content_key)`.
2b. Convert `purgeSource` to a set difference on the content key — upsert the new set, and set
   `valid_to` on the rows under the triple whose key is absent from it. Nothing is deleted, no
   vector is dropped, no anchor is stripped.
3. Mint opaque ids for new rows at all four mint sites. **Not before 2b**: with the delete-all purge
   still in place this step alone re-keys every drawer under every named source on every re-file.
4. Confirm import idempotency still holds — re-run `AbsorbDrawers` over the same batch twice and
   assert the row count does not grow. This is `import.go:21`'s contract and it now rests on the key.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestRefilingAnUnchangedSourceKeepsItsIdsAndAnchors|TestRefilingTheOriginalTextDoesNotRevertAnEdit|TestRefilingTheEditedTextDoesNotDuplicate|TestAbsorbDrawersStaysIdempotentOnTheContentKey' -count=1 2>&1 | tee /tmp/acc38c.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38c.out && go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1 2>&1 | tee /tmp/acc38d.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38d.out
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRefilingAnUnchangedSourceKeepsItsIdsAndAnchors` | `internal/palace/dedup_test.go` | a re-file of identical content changes no id and strips no anchor — the regression guard, and a repair of the pre-existing anchor loss | — |
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
| 2 — something selects it | `Save`'s conflict target, and the set-difference branch in `purgeSource`. Mutations: restore the conflict target to `id`, and separately restore the delete-all purge — each kills a different test, which is what proves the two are not one mechanism wearing two names |
| 3 — the caller can discover it | n/a: no declared interface — `am_add_drawer`'s schema and response are unchanged; the behaviour change is that the tool stops being wrong |
| 4 — it is used | every `am_add_drawer` call exercises it. Observable as the absence of duplicate-content rows: the query in T3 reports it. |

## Mutation Log

## Invariants

- No existing drawer id changes. New rows get opaque ids; old rows keep theirs — **including across a re-file of their source**, which is the invariant step 2b exists for.
- A chunk whose content did not change keeps its anchors through a re-file.
- A named source still RESOLVES to exactly the chunks last filed for it: what left the source is ended and leaves recall, what stayed is neither re-keyed nor un-anchored, and nothing is destroyed.
- A journal still never dedupes: diary rows carry an empty key and sit outside the partial index.

## Risks

- `SaveUnembedded` has its own `OnConflict` (`repo.go:109`) and is easy to miss — it is the deferred-embedding path, and missing it means a drawer filed while the embedder is down keeps the old behaviour. Named in the Tests section for that reason.
- An opaque id whose shape is indistinguishable from a hash invites the next reader to re-derive it. Mint it in a visibly different shape.

## Stop Condition

Stop and ask if moving the conflict target requires changing the primary key itself. It should not —
the key is a unique index, not the PK — and if it does, the additive-migration premise the ADR's
rollback rests on has broken.

## Out of Scope

- The gate that keeps future paths honest — T6.
- Whether a re-file should discard an in-place edit to that source at all — this task preserves today's answer (it does) and only stops the collateral damage to chunks the re-file did not touch (deferred: `docs/adr/BACKLOG.md`)
- Re-chunking on update (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
