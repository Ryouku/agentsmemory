# ADR-038: Dedupe on the content, refer by the id

**Status:** Proposed
**Date:** 2026-08-27
**Owner:** M
**Spec:** None — no spec stage; grounded in a recomputation of every drawer id in the live palace against `DrawerID`'s own recipe on 2026-08-27, and in three prior records that each deferred to the primitive this ADR adds. Numbers recorded inline.
**Cross-references:** ADR-010 (a memory is ended, not overwritten — rejects event sourcing because "anchors hang off drawer identity", which is the coupling this ADR removes), ADR-013 (a page of memories, not chunks), ADR-015 (a wing merge must correct the index it invalidates — **this ADR closes its deferral**), ADR-019 (the agent sees a quarter of the memory — rejected re-chunking on the same id grounds), ADR-024 (rank memories, not chunks), ADR-027 (a maintained document is a set of records — **this ADR unblocks its rejected alternative**), ADR-036 (the knowledge graph on the read path — `kg_triples.source_drawer_id` is a consumer of drawer identity), `internal/palace/chunk.go:148` (`DrawerID`), `:164` (`diaryEntryID`), `internal/palace/service.go:660` (the mint), `:677` (`purgeSource`), `internal/palace/repo.go:85` (`OnConflict{UpdateAll: true}`), `:377` (the id-is-stable contract), `internal/palace/admin.go:306` (`MergeWing` relabels the wing in place), `internal/palace/import.go:21` (import idempotency rests on the recomputed id), `internal/store/qdrant/vector.go:29` (`pointID` = UUID5 of the drawer id), issue #39 part 2
**Governs:** `internal/palace/chunk.go`, `internal/palace/repo.go`, `internal/palace/service.go`, `internal/palace/import.go`, `internal/palace/mine.go`, `internal/palace/copywing.go`, `internal/palace/admin.go`, `db/migrations/*_drawers_content_key.sql`
**Invalidates:** none — checked. Grepped ADR-001..037 for `DrawerID`, `drawer id`, `content hash`, `idempotent`, `re-chunk` and `new ids`: no accepted ADR pins the id to its content, and the two records that touch it (ADR-015, ADR-027) both **defer** to this decision rather than depend on the current shape. It **closes** ADR-015's deferral and the id half of ADR-027's; the remainder of ADR-027's is re-pointed, not silently absorbed.
**Served-path change:** **Yes.** Re-filing a memory that has since been edited in place stops silently reverting the edit, and re-filing the edited text stops creating a duplicate row. `am_add_drawer`, `am_update_drawer` and the import path all change behaviour; no tool signature changes.

## Context

**The measurement, taken 2026-08-27 by recomputing `DrawerID(team_id, wing, room, source_file, chunk_index, content)` for every row in the live palace and comparing it to the stored primary key.** Not by reading the code and reasoning about it — by hashing 2,013 rows.

| population | rows | note |
|---|---|---|
| all drawers | 2,013 | |
| diary rows, excluded | 308 | `diaryEntryID` is a **different function** — it folds agent, topic and an unstored random seed (`service.go:2100`), so a diary id is **permanently non-derivable by construction**. Including them would report 100% drift and be a false alarm. |
| non-diary, checked | 1,705 | |
| id **matches** the hash of its own row | 1,678 | |
| id **no longer describes its row** | **27** | 1.6%, across 8 wings |

Of those 27: **5** are explained by a wing move, **1** by a room move, and **21** are unattributed — an *upper bound* on in-place content edits, not a count of them, because a merge whose source wing no longer holds any drawer is undetectable by the substitution method used.

**Three shipped paths mutate the hashed tuple while keeping the id**, and two of them are deliberate and accepted:

- `Service.Update` rewrites content, wing or room and keeps the id. `repo.go:377` says so outright: *"the id is stable — it is not recomputed from the new wing/room."*
- `MergeWing` issues `Update("wing", target)` (`admin.go:306`) and keeps the id. That is ADR-015, accepted and shipped.
- `WriteDiary` mints from a seeded function that no lookup can reproduce.

So the palace already decided, in three places, that an id **is a reference, not a description**. It just never said so, and one value is still doing both jobs.

**The cost of not saying so — two failure modes, one live, one latent.**

A source-less `am_add_drawer` skips `purgeSource` (`service.go:677`) and relies on the content-hash id colliding with the stored row under `OnConflict{UpdateAll: true}` (`repo.go:85`). For a drawer that has since been edited in place:

- re-filing the **original** text mints the id the row still carries, and the edit is **silently reverted**;
- re-filing the **edited** text mints a different id, and a **duplicate row** with identical content is created.

Measured on the same corpus: **0 of the 27 drifted rows have `source_file = ''`**, so this is a mechanism with no shipped instances today — reported as a mechanism, not an incident. The live half is the other one: all 27 carry a named source, and `purgeSource` **hard-deletes** every drawer under a `(wing, room, source_file)` triple before inserting the new set (`service.go:844` — vectors, derived edges and rows), so **each of those 27 in-place edits is destroyed by the next re-file of its source**, across 19 distinct source triples.

**How likely that re-file is cannot be measured from this corpus, and an earlier draft of this record implied otherwise.** The obvious test — does a source triple carry more than one distinct `filed_at` — returns 0 for all 27, and that number is worthless: `purgeSource` deletes its predecessor, so a re-filed source leaves no trace of having been re-filed. The check cannot produce a non-zero answer for a named source, which makes it a gate that cannot fail. What is certain is the mechanism and the 27 rows exposed to it; the rate is unknown.

**A second live loss vector, found while answering "are we losing memory?" on 2026-08-27.**
`purgeSource` calls `DeleteBySource`, and `DeleteBySource` deletes the **anchors** of every drawer
under the triple first (`repo.go:225`, *"Anchors first, while the drawers that name them are still
queryable"*). Because ids are deterministic today, an unchanged chunk comes straight back with the
**same id and no anchors**. The drawer survives; its pin to the code it explains does not, and
nothing reports it.

Measured 2026-08-27 against the live palace: **65 anchors on 41 drawers, and 39 of those drawers sit
under a named source** across 39 source triples. So 95% of every anchor in the palace is destroyed by
the next re-file of its source. As with the drift rate above, **how often that happens cannot be
measured** — a re-file leaves no trace of its predecessor — so this is an exposure, not an incident
count.

**Three records have already deferred to the primitive this ADR adds.** This is the part that makes it a decision rather than a cleanup:

- **ADR-015** — *"Making `DrawerID` independent of the wing so a merge does not invalidate anything derived from the id"* (Out of Scope, deferred; receipted at `docs/adr/BACKLOG.md:665`).
- **ADR-027** — *"Make `Update` re-chunk … it changes which ids exist, and the open question — what happens to a reference pointing at a **non-parent** chunk — is unanswered."*
- **ADR-010** — rejects event sourcing partly because *"the store already has a working row model with vectors, chunking and anchors hanging off drawer identity."*
- **ADR-019** — rejected smaller chunks because *"it changes ids, invalidating every anchor, tunnel and knowledge-graph pointer."*

Four records, one cause. None of them can move while the id that references a row is also the id that describes its bytes.

## Existing Primitives Audit

| Primitive | Where | Disposition |
|---|---|---|
| `DrawerID` | `chunk.go:148` | **Reshape.** Its recipe is kept verbatim and becomes the **content key**. It stops being the primary key's definition. |
| `diaryEntryID` | `chunk.go:164` | **Reuse unchanged.** It is already an opaque mint; this ADR names that role rather than inventing it. Diary rows carry **no** content key — a journal must not dedupe. |
| `purgeSource` | `service.go:677` | **Reuse unchanged.** Named-source wholesale replacement is orthogonal and correct. |
| `OnConflict{UpdateAll: true}` | `repo.go:85` | **Reshape.** The conflict target moves from the primary key to the content key. |
| `RelabelDrawerWing*` | `admin.go:295,313` | **Extend.** Must recompute the content key in the same statement that moves the wing. |
| `pointID` (UUID5 of drawer id) | `store/qdrant/vector.go:29` | **Untouched.** No drawer id changes, so no vector is re-keyed. This is the reason for the shape chosen below. |
| `randomID` | `recallstats.go:179` | **Reuse.** Already the house opaque-id mint, used for `search_events`. |

## Decision

**`drawers.id` becomes opaque by contract, and a new `drawers.content_key` column carries the hash that dedup matches on.**

Concretely:

1. A migration adds `content_key TEXT` to `drawers`, with a unique index on `(team_id, content_key)` **carrying the partial predicate `WHERE content_key != ''`**, backfilled for every non-diary row by computing `DrawerID` from that row's **current** fields. Diary rows get an empty key and are excluded from the index, because a journal must never dedupe.

   **That predicate is the single load-bearing clause in this decision and it gets its own test.** Without it every empty-key row shares one index entry, and once T2 points the upsert at that index, filing any keyless drawer would OVERWRITE an unrelated memory. It is the only line in this ADR whose absence destroys data rather than duplicating it, and the only one where the failure is silent.
2. Every mint path (`Add`, `AbsorbDrawers`, `Mine`, `CopyWing`) writes the content key beside the id. Every in-place mutation path (`Update`, `MergeWing`) **recomputes** it in the same statement that changes a hashed field.
3. Dedup and idempotency move to the content key: `Add` and the import path upsert on `(team_id, content_key)` and mint a fresh opaque id when there is no match. Import's contract at `import.go:21` — *"the only field recomputed is the id … so re-running an import upserts rather than duplicates"* — is preserved, now by the key rather than by the id.
4. `id` is never recomputed, never compared to a hash, and never used to infer anything about a row's content. A source check (T3) fails when `DrawerID` is called anywhere other than a content-key computation.

5. **Re-filing a named source becomes a set difference on the content key, not a delete-then-insert.**
   `purgeSource` currently deletes every row under a `(wing, room, source_file)` triple — with its
   vectors, derived edges and anchors — and `Add` then re-inserts. Under this decision it upserts the
   new set by content key and deletes only the rows under that triple whose key is **not** in it.

   **This is not a bonus; without it this ADR is a regression.** Ids are deterministic today, so a
   re-file of unchanged content re-inserts the same ids and every reference survives. Mint an opaque
   id and that stops being true: the purge deletes the row, the upsert finds no key to match, and a
   fresh id is minted — so **every re-file of a named source would re-key every drawer under it**,
   breaking exactly the resolvability this decision exists to protect. The set difference removes the
   regression, and it repairs the pre-existing anchor loss above in the same change, because a row
   that is never deleted never loses its anchors.

**Existing ids do not change.** No row is re-keyed, so no `code_anchor`, tunnel, `kg_triples.source_drawer_id`, `parent_id`, `search_events` row or Qdrant point is re-pointed, and nothing needs a transaction spanning SQLite and Qdrant. The migration is additive and the rollback is a dropped column. That is the whole reason for choosing this shape over minting new opaque ids, and it is what makes the decision cheap enough to be reversible.

**What would make this FAIL, and does data that could produce that failure exist?** The backfill's unique index is the falsifiable part: two rows sharing a `(team_id, wing, room, source_file, chunk_index, content)` tuple would collide and the migration would abort. Measured 2026-08-27 on the live corpus: **1,705 non-diary rows produce 1,705 distinct keys, 0 collisions.** Valid for this corpus at this date; T1's migration must therefore fail loudly rather than skip a colliding row, so a corpus that does collide is a stop condition and not a silent partial backfill.

**Re-chunking on update is NOT part of this decision.** This ADR removes the blocker that four records named; it does not spend it. ADR-027's remaining question — what happens to a reference pointing at a non-parent chunk that a re-chunk deletes — is still open and is re-pointed, not absorbed.

## Alternatives Considered

- **Mint new opaque ids and re-point every reference.** The clean version. Rejected on cost and risk: `pointID` is UUID5 of the drawer id, so every vector in Qdrant would be re-upserted and the old points deleted, with no transaction spanning SQLite and Qdrant — a half-done migration would leave rows whose vectors are unreachable, which is precisely the invisible state ADR-015 was written to end. The additive column buys the same property for a dropped column's worth of rollback.
- **Keep one id and make `Update` re-chunk (issue #39 part 2, ADR-027's rejected alternative).** Rejected again, and for the reason ADR-027 gave: re-chunking changes which ids exist, and those ids are what anchors, tunnels and KG facts point at. This is the option M's argument reaches for; it trades away the property that is still alive to compensate for one that is already gone.
- **Drop content-addressing entirely — random ids, no dedup.** Rejected: it is load-bearing in two places, not one. `Add` uses it for source-less idempotency, and `import.go:21` states the migration path's re-run safety rests on it. Removing it makes a re-run of an import duplicate a palace.
- **Do nothing; accept that the id no longer describes the row.** Rejected because the drift is unstatable today: with no column holding what the id used to promise, there is nothing a gate can compare, and the 27 rows were found by an ad-hoc script rather than by anything in the tree. A property nothing can check is not a property.
- **Store a `content_sha256` for reporting only, without moving dedup onto it.** Rejected: it would record the drift and fix neither failure mode. The silent revert survives, and the column becomes a number nobody acts on.

## Component / Boundary Impact

`internal/palace` keeps ownership of drawer identity and gains an explicit second key. No component moves. `internal/store` is untouched by design — the vector namespace still keys on the same drawer ids it keys on today. `internal/mcpserver` is untouched: no tool signature, argument or response field changes.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `drawers.content_key` (schema) | new `TEXT` column + unique index `(team_id, content_key)` | `db/migrations/*_drawers_content_key.sql` | `Add`, `AbsorbDrawers`, `Mine`, `CopyWing`, `Update`, `MergeWing` |
| `Repo.Save` conflict target | `id` → `(team_id, content_key)` | `internal/palace/repo.go` | `Service.Add`, `Service.AbsorbDrawers` |
| `DrawerID` role | primary-key recipe → content-key recipe (function body unchanged) | `internal/palace/chunk.go` | every mint path |
| Drawer id minting | content hash → `randomID`-style opaque mint for NEW rows | `internal/palace/chunk.go` | every mint path |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `drawers.content_key` column + `Drawer.ContentKey` field | T1 | T2, T3 | No — additive column, empty for diary rows |
| `Repo.Save` upserting on `(team_id, content_key)` | T2 | T3 | Yes — T3's source check asserts no path re-derives an id for lookup, which is only true after T2 moves the conflict target |

## Implementation

See `docs/adr/ADR-038-dedupe-on-the-content-refer-by-the-id/tasks/README.md`. Three tasks, sequential.

## Consequences

- **Positive:** Re-filing a named source stops destroying the anchors of chunks it did not change — 39 of the palace's 41 anchored drawers are exposed to that today. The two `am_add_drawer` failure modes above stop being possible. The drift becomes checkable — 27 rows were found by an ad-hoc script, and after T3 a gate finds them. Four deferred records (ADR-010, ADR-015, ADR-019, ADR-027) lose the blocker they each named. A wing merge stops invalidating anything derived from an id, which is ADR-015's deferral, closed.
- **Negative:** T2 is larger than it looks: it cannot ship the opaque mint without also converting `purgeSource`, because the two together are what keep a re-file from re-keying its source. Splitting them across commits leaves the tree in the regressed state. Two keys where there was one, and every mint path must write both — the classic shape of a field that gets forgotten on the fifth path. T3's gate exists for exactly that and must fail when a path is added without a key. The migration is a backfill over the whole `drawers` table; on the live corpus that is 1,705 rows and trivial, but it is still persistent state and needs the rollback below.
- **Neutral:** New rows get opaque ids while existing rows keep hash-shaped ones. That is heterogeneous on purpose: an id that cannot be told apart from a hash invites the next reader to re-derive it.

## Out of Scope

- Re-chunking on update (deferred: `docs/adr/BACKLOG.md`)
- Changing `ChunkSize`, `ChunkOverlap` or `MaxEmbedRunes` (permanent: this ADR changes what an id means, never how text is split)
- Re-keying existing drawers to opaque ids (permanent: rejected in Alternatives — the Qdrant re-upsert has no cross-store transaction, and the additive column delivers the same property)
- Giving diary entries a content key (permanent: a journal must not dedupe — `chunk.go:157` already states why, and this ADR names it rather than changes it)
- Validity windows, supersession or retraction on drawers (permanent: ADR-010 owns that, and it is a different question — *when* a memory is current, not *what its name means*)
- Repairing the 27 drifted rows (deferred: `docs/adr/BACKLOG.md`)
- Whether re-filing a named source should discard an in-place edit to it at all (deferred: `docs/adr/BACKLOG.md`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Backfill hits a `(team_id, content_key)` collision and aborts mid-migration | Low | High | Measured 0 collisions across 1,705 non-diary rows on 2026-08-27. T1's migration aborts loudly rather than skipping — a silent partial backfill is the failure this repo keeps catching. Rollback is the down migration. |
| A future mint path writes an id and forgets the content key | Med | High | T3's gate derives its universe from the mint sites rather than a hand-kept list, so a path added tomorrow joins the check on the same commit. |
| Someone re-derives an id for a lookup after this lands, reintroducing the coupling | Med | Med | T3's source check fails when `DrawerID` is called outside a content-key computation. Prove it by adding such a call and watching it go red. |
| The migration number is renumbered at merge and re-runs on a database that applied it | Low | High | Allocate the number at merge, never at authoring — the crash loop and its repair are already documented in `README.md` (Development). |
| Diary rows are accidentally pulled into the unique index by a later change | Low | Med | T1's test asserts two diary entries with identical text, agent and topic coexist. |
| **An opaque mint ships before `purgeSource` becomes a set difference** | Med | **High** | Every re-file of a named source would re-key every drawer under it, breaking every anchor, tunnel and KG pointer to them — the exact property this ADR protects, broken by this ADR. They are one task and one commit for that reason, and T2's first test is the one that fails if they are separated. |
| **The unique index ships without its `WHERE content_key != ''` predicate** | Low | **Data loss** | The only failure in this ADR that destroys rather than duplicates: every keyless row would share one index entry and an upsert would overwrite an unrelated memory. T1 tests the predicate directly, and the mutant is deleting it. |
| `SaveUnembedded` keeps its own `(team_id, id)` conflict target (`repo.go:109`) after `Save` moves | Med | Med | The deferred-embedding path would keep id-based dedup, so the silent-revert mechanism survives on the one path taken when the embedder is down. Named in T2's Tests table for that reason. |
| Backfill aborts partway, leaving rows with an empty key | Med | Low | Fails toward DUPLICATES, not loss: a keyless row sits outside the partial index and never matches, so a re-file inserts beside it rather than over it. Detected by the query in Rollback. |

## Rollback

**Persistent state, so this is required and it is deliberately cheap.**

`goose down` on the `drawers_content_key` migration drops the index and the column; revert the code commits. Nothing else is touched: **no drawer id changes**, so every `code_anchor`, tunnel, `kg_triples.source_drawer_id`, `parent_id` and `search_events` row still resolves, and every Qdrant point keeps the UUID5 it already has. There is no cross-store half-state to detect because the vector store is never written during this migration.

The one state to detect is a **partially backfilled** column: rows with an empty `content_key` in a non-diary room. T1 ships that as a query in the task, and the migration is written to abort rather than continue past a failure, so a partial backfill is a failed migration rather than a silent one.

## Follow-ups

- [ ] Report the first measured count of drifted rows after T3's gate lands, whichever way it falls — including "zero", which would mean the 27 were repaired by ordinary re-filing rather than by anything this ADR did.
- [ ] Decide whether ADR-027's remaining question — a reference pointing at a non-parent chunk that a re-chunk deletes — is answerable now that ids are opaque, or whether it needs its own record.
