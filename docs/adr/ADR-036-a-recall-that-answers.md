# ADR-036: Put the knowledge graph on the read path

**Status:** Proposed
**Date:** 2026-08-26
**Owner:** Zy
**Spec:** `docs/specs/2026-08-26-a-recall-that-answers.md`
**Cross-references:** `docs/adr/ADR-001-recall-answers-or-abstains.md`, `docs/adr/ADR-004-supersession-not-recall.md`, `docs/adr/ADR-016-a-memory-an-agent-files-must-be-navigable.md`, `docs/adr/ADR-031-the-column-abstention-would-calibrate-on.md`, `docs/architecture.md`
**Invalidates:** none — checked. ADR-001 (abstention) is Accepted with all six tasks pending and is a NON-GOAL here, deliberately not re-decided. ADR-016 is Accepted and executed; F-4 depends on it rather than changing it. ADR-031's calibration aggregate reads `reranked`, which this ADR does not touch. ADR-034 (open PR #61) adds `rerank_skip_reason`; this ADR takes migration `00028` to avoid its `00027`.
**Served-path change:** `am_search` gains a fact block, a sibling-wing pointer naming wings it did not search, and correction marks on hits; a new one-call bootstrap surface appears. An agent's recall visibly changes.

## Context

The palace holds a temporal knowledge graph — measured 2026-08-26 on the live palace: 342 entities,
196 triples, 182 current, 14 ended, validity windows, provenance — and a recall never opens it.
`kg_triples` and `kg_entities` appear **zero times** in `internal/palace/service.go`,
`memory_search.go` and `rank.go`. The only indexes are B-trees on subject/object/predicate, so a
fact is reachable only by already knowing its entity string.

**This ADR delivers a deferral that was recorded and then lost.** `ADR-004` T5 (Accepted, `done`)
carries `- Wiring the graph into Service.Search (deferred: docs/adr/BACKLOG.md)`, and `BACKLOG.md`
has no entry for it under any wording. `adr-debt` reports 0 unreceipted because the pointer resolves
to a real file — the exact failure the deferral rule exists to catch, surviving inside the sweep
built to catch it.

**A related backlog item's premise is now false, and its expectation was falsified.** BACKLOG item 2
("Decide the entity graph: feed it or retire it") argues from *"`Service.Add` does not [call
extractEntities], 82 of 82 today"*. ADR-016 shipped since: `Service.Add` stamps entities on every
chunk, and **945 of 1,985 drawers (47.6%) carry them, measured 2026-08-26**. So the item's own
recommended option — *feed it* — was taken. Hallways are **still 0**. Feeding the extractor was
necessary and not sufficient, and nobody has separated the two remaining causes (recompute not run
since, or no entity pair clearing the co-occurrence threshold). That is why this ADR routes around
the derived graph rather than through it.

Two measurements bound what is achievable and are stated up front so a modest result reads as the
instrument working rather than the feature failing:

- **Fact reachability caps at 46%.** 196 triples, 106 carry `source_drawer_id`, **90 resolve** to an
  existing drawer. 16 pointers dangle.
- **97.1% of drawers are orphans** (57 of 1,985 carry any edge), and **0 drawers are named as a
  triple object** — so the pointer pattern an entry point indexes has no adoption in this workspace
  at all.

## Existing Primitives Audit

- **`kg_triples` / `kg_entities` + `am_kg_query`** — the store and its exact-match reader. **Reused,
  not reshaped.** The read path gains a semantic entry; the write path and schema of triples are
  untouched except for the additive column in T6.
- **`vectors` + the embedding worker** — already indexes drawer chunks per team. **Reused** by
  embedding entity labels into the same store under a distinct namespace; no new backend.
- **`extractEntities` / `drawers.entities`** (ADR-016, executed) — **reused read-only** by F-4. Not
  merged with `kg_entities`, deliberately: merging an unmeasured mechanism into a working one adds
  risk with no way to detect it.
- **`Service.Search` / `rankRetrieved` / `collapseCandidatesToMemories`** — **extended additively.**
  F-9 pins that drawer selection and order are unchanged, so the fact block cannot be confounded
  with a ranking change.
- **`am_traverse`** — **NOT reused.** Its `max_hops` is provably inert (F-17): `via` is an
  intersection carried forward, so hop ≥2 can never add a node. The bootstrap resolves edges
  directly.
- **`eval` arm registry + case sets** — **reused** for the instrument in T1, following the arm
  pattern ADR-003 established.

## Decision

The knowledge graph joins the read path, in four movements, each measurable before the next claims
anything.

**Facts become reachable by a question.** Entity labels are embedded into the existing vector store;
a recall matches the query against them, expands to current triples, and returns them in a block
BESIDE the drawer hits — never merged into them, so ranking is unaffected.

**The wing boundary is resolved by a pointer, not a crossing.** `kg_triples` has no wing column and
the graph is workspace-wide while search is wing-scoped. A wing-scoped recall therefore never
returns another wing's fact CONTENT, and MUST report that matches exist elsewhere and name those
wings. Silence is indistinguishable from "nothing is filed", which is the failure this replaces.

**Corrections apply at read time, marking rather than hiding.** A returned record that is the object
of `retracts`, `supersedes` or `qualifies` carries that edge and its replacement's id. Hiding is
refused because a retraction can itself be wrong.

**The protocol becomes an API.** One bootstrap call returns a wing's entry point, its eager tier's
content, its on-demand tier as pointers, corrections already swept, the resolved wing, and what it
omitted — replacing a client-side protocol measured at ~99KB (~25k tokens) plus a hardcoded root id
plus 13 calls.

**What would make this fail, and the data to produce that failure exists today.** T1 builds the
instrument first and its baseline is **0% by construction** — search returns no facts at all now.
Any non-zero answerable-rate is therefore real, which is what exempts it from the MRR noise floor
(two arms of provably identical configuration scored 0.709 against 0.700 on 2026-08-26). The
ceiling is 46% by F-8, and if the measured rate sits far below that with provenance resolving, the
retrieval premise is falsified rather than quietly unmet. F-16 is the bootstrap's own falsifier: it
must beat 13 calls / ~2.8k output tokens, measured, or it has reproduced the problem inside one
call. Every threshold here is valid for THIS corpus and this embedder, never in the abstract.

## Alternatives Considered

- **Personalized PageRank over the graph, seeded from query entities** (the HippoRAG shape,
  arXiv 2405.14831). Rejected for now, not on merit: it presumes a connected graph, and ours derives
  **zero hallways** against 945 entity-carrying drawers. Seeding a walk over an edgeless graph
  returns the seeds. Revisit once T6 has produced edges and T1 can measure the difference.
- **Unify the two entity vocabularies at the write path** (the HippoRAG 2 shape, arXiv 2502.14802,
  which reports +7% on associative memory from putting phrase and passage nodes in one graph).
  Rejected as the first move for the same reason: the extraction-side vocabulary is itself
  unmeasured. F-4 takes the read-only join instead, which needs no schema or write-path change.
- **Add a `wing` column to `kg_triples` and backfill.** Rejected in favour of deriving wing from
  provenance (F-8), which needs no migration on live data. The cost is a 46% ceiling, recorded in
  the spec's Risks. Revisit if provenance proves too sparse to be useful.
- **Fix `am_traverse` and build routing on it.** Rejected: whether traversal should be transitive or
  confined is an unmade product decision, and they are different products. F-17 resolves edges
  directly instead.
- **Repurpose `reranked` into an enum rather than adding a column.** Not applicable here — that
  alternative belongs to ADR-034 and is recorded there.

## Component / Boundary Impact

Inherited from `docs/architecture.md` §Module Map; delta: `internal/palace` gains a read-path
dependency on its own KG tables, which it did not have. No module moves and no ownership changes.
The MCP layer gains one surface. `internal/store` is unchanged — entity vectors use the existing
`VectorStore` under a separate namespace rather than a new backend.

## Wiring & Contract Changes

Inherited from `docs/specs/2026-08-26-a-recall-that-answers.md` §Contracts Touched; delta:

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `db/migrations/00028_kg_triples_derived.sql` | new nullable column marking a server-derived edge | T6 | T7, T8 |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the fact-retrieval arm and its case set (T1) | T1 | T3, T4, T8 | No — additive eval surface |
| `palace.ValidSearchID`-style resolution of absence vs failure (T2) | T2 | T3 | No — internal |
| `Service.factsFor` returning wing-resolved facts (T3) | T3 | T4, T5, T8 | No — new internal method |
| `searchEventRow`-style derived-edge marking (T6) | T6 | T7, T8 | No — additive nullable column |
| `Service.EntryPoint` (T7) | T7 | T8 | No — new internal method |

## Implementation

See `tasks/README.md`. Eight tasks in three waves.

## Consequences

- **Positive:** a question can reach a fact, and a recall stops being silent about answers it did
  not search. The client-side protocol shrinks by roughly the half that is traversal instructions.
- **Positive:** every claim here is measurable before it is believed — T1 exists precisely so that
  nothing after it can report an improvement without an instrument.
- **Negative:** the bootstrap encodes a WORKFLOW, not just data. A wrong tier split or sweep is
  expensive to walk back once clients depend on it. F-14 and F-16 are what make that observable
  early.
- **Negative:** F-8's 46% ceiling means over half of today's facts stay unreachable from a
  wing-scoped recall until provenance improves. That is a write-path problem this ADR does not
  solve.
- **Neutral:** ranking is untouched. F-9 pins it, so this cannot be confused with a retrieval change.

## Out of Scope

Inherited from `docs/specs/2026-08-26-a-recall-that-answers.md` §Non-Goals; delta:

- Fixing `am_traverse`'s inert `max_hops` (deferred: docs/adr/BACKLOG.md §"From ADR-036")
- Separating why hallways derive nothing — recompute never run, or the co-occurrence threshold never met (deferred: docs/adr/BACKLOG.md §"From ADR-036")
- Repairing the 16 dangling `source_drawer_id` pointers (deferred: docs/adr/BACKLOG.md §"From ADR-036")
- Personalized PageRank over the graph — it presumes a connected graph, and ours derives zero hallways against 945 entity-carrying drawers, measured 2026-08-26; revisit once T6 has produced edges and T1 can judge it (deferred: docs/adr/BACKLOG.md)

## Risks

Inherited from `docs/specs/2026-08-26-a-recall-that-answers.md` §Risks; delta:

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Migration `00028` collides with another open branch | Low | High | Checked 2026-08-26 across every remote branch: `00027` is the highest anywhere, held by ADR-034 on PR #61 |
| ADR number 036 collides | Low | High | Checked across every remote branch: 033 (#58), 034 (#61), 035 (#60) are claimed; 036 is free. `TestADRNumbersAreUnique` guards it thereafter |

## Rollback

Persistent state and a public contract, so rollback is real and ordered. `00028` carries a
`-- +goose Down` dropping its column; the column is nullable, so a previous binary against the
migrated schema writes NULL and reads nothing. The `am_search` additions are additive fields — a
client ignoring them sees today's response. The bootstrap surface is a new tool: removing it breaks
only callers that adopted it, which is why F-16 gates adoption on a measured win rather than a
promise. Revert order: binary, then tool registration, then migration.

## Follow-ups

- [ ] Report the first measured fact answerable-rate in `BACKLOG.md` whichever way it falls, including "0% — provenance too sparse", which would falsify F-8's derivation rather than extend it.
- [ ] Report whether the bootstrap actually beat 13 calls / ~2.8k output tokens, with the measurement, before any client is told to depend on it.
