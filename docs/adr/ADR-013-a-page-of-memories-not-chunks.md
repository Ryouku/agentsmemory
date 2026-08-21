# ADR-013: A page of memories, not a page of chunks

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** `internal/palace/chunk.go` (the split), `internal/palace/service.go` (`Search`, `Get`), `internal/palace/repo.go:577` (`MemoryChunks`), ADR-007 (a number carries its population — this is the same disease on the retrieval side)
**Invalidates:** none — checked. Grepped ADR-001..012 for `ParentID`, `chunk`, `limit`: ADR-001 calibrates abstention from `search_events.Hits`, whose meaning this ADR changes; that is recorded in Wiring & Contract Changes and in ADR-001's follow-ups rather than left to collide.
**Served-path change:** `am_search` returns one hit per MEMORY instead of one per chunk, so a page of 10 holds 10 distinct memories rather than 8; and `am_get_drawer` can return a whole chunked memory, which today is not retrievable by any read path.

## Context

A drawer over ~1600 characters is split into chunks (`chunk.go:20 ChunkSize = 1600`) linked by
`ParentID`. `Service.Search` never references `ParentID`. It ranks and returns CHUNKS.

Chunks of one memory are similar to the same query, so they cluster: the duplicates land adjacent
and crowd each other rather than spreading. Measured on the live local palace, `wing_craft`,
`limit: 10`:

| query | slots spent on duplicates | distinct memories in 10 slots |
|-------|---------------------------|-------------------------------|
| "gate that cannot fail…" | 2 (one memory at slots 1 and 3) | 9 |
| "reachability defect…" | 4 (two memories at slots 3–4 and 5–6) | 8 |

The eval cannot see this. `eval.go:771` and `eval.go:967` both fold a hit onto
`memory = d.ParentID` before scoring — including for `ArmProduction`, the one arm that calls
`s.Search`. So the eval scores MEMORIES over a page production returns as CHUNKS. Its headline would
be unchanged if production returned ten chunks of one memory: ten chunks of the gold collapse to one
gold at rank 1, MRR 1.000. **An eval cannot report a regression it does not measure the unit of.**

The second half is a capability that exists and is unreachable. `Repo.MemoryChunks`
(`repo.go:577`) returns every chunk of a memory in `chunk_index` order and is called by exactly two
functions, `Update` and `Delete` — both write paths. `Service.Get`, behind `am_get_drawer`, returns
a single drawer. **There is no read path by which an agent can obtain a whole chunked memory today.**
That is why collapsing cannot ship alone: hiding four chunks behind one hit is a regression if the
other four cannot be fetched.

## Existing Primitives Audit

- `Drawer.ParentID` (`palace.go:42`) — already carried on every hit; nothing on the read path reads it.
- `Repo.MemoryChunks` (`repo.go:577`) — the exact query needed, already written and tested
  (`service_test.go:717`), reachable from no read path. Reused rather than rewritten.
- `hybridCandidateMultiplier = 3` (`rank.go:30`) — Search already over-fetches 3× the limit, which is
  the headroom collapsing needs. No new fetch sizing.
- The eval's own `memory = ParentID` folding — the definition of "one memory" already exists in this
  repository and is written twice; this ADR makes production use the same one.

## Decision

**A page is a page of memories.** `Search` collapses hits that belong to one memory, keeps the
highest-ranked chunk of each, and `limit` counts distinct memories.

Three consequences follow, and each is a decision rather than an accident:

1. **The best-ranked chunk survives**, not chunk 0. The surviving chunk is the one that matched, so
   its snippet is the passage the caller was looking for. Keeping chunk 0 would answer a different
   question than the one asked.
2. **The hit reports how many chunks matched** (`ChunksMatched`). A memory that matched in four
   places is stronger evidence than one that matched in one, and collapsing would otherwise destroy
   that signal silently.
3. **A short page is honest.** If the candidate pool holds fewer than `limit` distinct memories, the
   page is short. Padding it with a second chunk of a memory already shown is what this ADR removes.

**And the rest of the memory becomes reachable**: `Service.Get` gains a whole-memory read backed by
`MemoryChunks`, exposed on `am_get_drawer`, so a caller handed one chunk can ask for the rest.

## Alternatives Considered

- **Make the eval score chunks, so it matches production.** Rejected: it would make the two agree by
  making the measurement worse. The eval's unit is the right one — a person asking a question wants
  distinct answers — and production is the half that is wrong.
- **Collapse in the MCP layer rather than in `Search`.** Rejected: the CLI adapter and the eval would
  each need their own copy, which is the divergence this repository keeps paying for. One pipeline,
  one definition of a memory.
- **Merge the matching chunks' text into one hit.** Rejected for now: it changes what a snippet IS,
  the join would have to be ordered and de-overlapped (chunks overlap by construction), and it costs
  context window on every recall. `ChunksMatched` plus a whole-memory fetch gives the caller the
  choice instead of making it for them. `(deferred: docs/adr/BACKLOG.md)`
- **Collapse before ranking.** Rejected: the ranker would then score an arbitrary chunk rather than
  the best one, so the memory's rank would depend on which chunk was picked first.

## Component / Boundary Impact

No new module. `internal/palace` gains a collapse step at the end of `Search` and a whole-memory read
on `Service`. `internal/mcpserver` exposes the latter as an argument on an existing tool.

## Wiring & Contract Changes

- `SearchHit` gains `ChunksMatched int` — 1 for an unchunked memory, N when N chunks of one memory
  were in the ranked pool.
- `am_search`'s `limit` now counts memories. A caller asking for 10 may receive 10 memories where it
  previously received 10 chunks of as few as 5.
- `am_get_drawer` gains a `whole` argument: return every chunk of the memory, in order.
- `rankOf` counts distinct memory slots rather than candidate positions, so every rank, MRR and
  recall@k recorded before 2026-08-21 is on a different scale from every one after. No committed
  evidence table is invalidated in practice — ADR-002 T3 and ADR-003 T3, which produce the tables,
  are both pending — but a reader comparing an old number to a new one is comparing two quantities.
- `search_events.Hits` changes meaning from "chunks returned" to "memories returned". ADR-001
  calibrates abstention from these rows, so rows written before and after this ADR are not
  comparable; the cutover date is recorded in `docs/adr/BACKLOG.md`.

## Implementation

1. Collapse in `Search`, keyed on `ParentID` or the drawer's own id, keeping the first (best-ranked)
   chunk and counting the rest.
2. `Service.GetMemory` over `repo.MemoryChunks`; wire it to `am_get_drawer`'s `whole`.
3. Gates below.

## Consequences

An agent's context window stops carrying the same memory twice, and a `limit` finally means what a
caller thinks it means.

**Correction, 2026-08-21, after review.** An earlier version of this section claimed the eval and
production now measure the same unit. That was too strong as first written. Collapsing in `Search`
aligned the unit of an ANSWER; it did not align the arithmetic of a RANK. The eval folds onto
memories BEFORE ranking and `rankOf` then counted raw candidate positions, so two chunks of an
irrelevant memory above the gold reported "rank 3" for something the served page puts in slot 2 —
the same mismatch one level down. `rankOf` now counts distinct memory slots. Found by a
different-lineage reviewer reading the two folds against each other, not by any gate here.

That makes this the change that lets ADR-002's and ADR-003's pending measurements be worth taking —
and it means every MRR figure recorded before today is on a different scale from every one after.

The cost is that `search_events` rows straddle a meaning change, and that a caller wanting the full
text of a long memory now makes a second call.

## Out of Scope

- Merging matched chunks into one snippet (deferred: docs/adr/BACKLOG.md)
- Re-chunking existing memories or changing `ChunkSize` (permanent: this ADR changes what a page contains, never how text is split)
- Routing the eval's other ten arms through `Search` (deferred: docs/adr/BACKLOG.md — that is the larger routing question a consensus round is deciding)
- De-duplicating distinct memories with near-identical content (permanent: `am_check_duplicate` owns write-time duplication; this ADR is about one memory appearing twice, not two memories saying the same thing)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| A caller depended on receiving several chunks of one memory | Low | Medium | `ChunksMatched` says a memory matched more than once, and `am_get_drawer whole:true` returns the rest — a capability that did not exist before this ADR. |
| The pool holds fewer than `limit` distinct memories, so pages get shorter | Medium | Low | Correct behaviour, and the 3× over-fetch already in `candidateKFor` absorbs the common case. A short page is the honest answer. |
| ADR-001's calibration reads `Hits` across the meaning change | Medium | High for that ADR | ADR-001 is at 0 of 6 tasks and its calibration has never been run, so no recorded calibration is invalidated. Cutover date filed. |

## Rollback

Revert the commits: the collapse loop, `ChunksMatched`, `GetMemory` and the `whole` argument all
disappear. No migration, no persistent layout change — `ParentID` and `MemoryChunks` both predate
this ADR. `search_events` rows written in between remain, and the cutover date in the backlog is what
tells a later reader which meaning a row carries.

## Follow-ups

- [ ] Re-take the duplicate-slot measurement after this ships and record the before/after in `docs/adr/BACKLOG.md` — the two queries above are the baseline, and a change that does not move them is a change that did not work.
