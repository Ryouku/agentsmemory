# ADR-016: A memory an agent files must be navigable, or the graph must say it is empty

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-015 (defers the "a merge invalidates the derived graph" question here), ADR-003 (blocked on `mine` never having run — the same root as this)
**Invalidates:** none — checked (grepped ADR-001..015 for `hallway`, `entities`, `RecomputeGraph`, `traverse`: no accepted ADR consumes the derived graph)
**Served-path change:** `am_list_hallways`, `am_traverse` and `am_graph_stats` currently describe a graph that is empty and can never be otherwise on a palace populated through `am_add_drawer`. Either they start describing a real graph, or they say why they cannot — today they return an empty result that is indistinguishable from a working tool with nothing to report.

## Context

Measured 2026-08-21 against the live self-hosted palace, 359 drawers filed by agents over three days:

- **`hallways`: 0. `closets`: 0. Drawers carrying any entity: 0 of 359.**
- A hallway is derived by `computeHallwaysForWing` from drawers whose `entities` column holds two or more names. `extractEntities` is called in exactly one place in the tree — `internal/palace/mine.go:155`. `Service.Add`, the path every `am_add_drawer` takes, constructs its `Drawer` rows without touching `Entities` at all.
- So on any palace populated through the agent surface, `hallways` is not empty-for-now. It is structurally unreachable: `am_recompute_graph` reports success and derives nothing, however often it runs, and the merge tool's own description — "Run am_recompute_graph afterwards to rebuild hallways/tunnels" — is advice that cannot have an effect on hallways.
- `tunnels`: 6, all created explicitly by `am_create_tunnel`. The DERIVED half (entity tunnels) shares the same input and is equally unreachable.
- The knowledge graph is fine and in use — 133 entities, 75 triples — because `am_kg_add` writes facts directly rather than deriving them.

**How it survived.** `TestGraphHallwaysAndEntityTunnels` passes, and it populates its wings with `svc.Mine`. Every hallway test does. The subsystem is thoroughly tested against the path agents do not use and untested against the path they do — the repository's own recorded failure shape: *every one of them had tests, and every test exercised the component rather than the selection.*

This is the fourth surface in this repo found finished and unreachable, and the first where the unreachable thing is a whole domain concept with read, delete and recompute surfaces and no producer.

## Existing Primitives Audit

- **`extractEntities`** (`internal/palace/entity.go:166`) — frequency-and-length extraction over a chunk's text, already used by mining. Reuse verbatim; the question is where it is called, not what it does.
- **`Service.Add`** (`internal/palace/service.go`) — already chunks, embeds and writes rows. Reshape: one field set per row.
- **`RecomputeGraph`** (`internal/palace/graphquery.go`) — already derives hallways and entity tunnels from `drawers.entities`, prunes, and preserves prior dynamics. Reuse unchanged: it works, it has simply never had input.
- **`emptyWingNote`** (`internal/mcpserver/emptywing.go`) — the precedent for the second half of this decision. A zero-hit page that says WHY it is empty already exists on the search path; the graph tools need the same and have none.

## Decision

Two halves, and the second is not optional.

**1. `Service.Add` stamps entities on every drawer it writes**, using the same `extractEntities` mining uses. A memory filed by an agent then participates in the derived graph exactly as a mined one does, and `am_recompute_graph` has something to recompute.

**What would make this fail, and the data exists to check it today.** The extraction is frequency-based: a term must appear at least twice and be longer than two characters. Agent-written memories are short and deliberate where mined transcripts are long and repetitive, so the pre-registered risk is that most drawers yield fewer than two entities and hallways stay empty for a subtler reason. That is measurable before the code is written, by running `extractEntities` over the 359 drawers already filed and counting how many would carry two or more. **If fewer than 20% would, the frequency rule is wrong for this corpus and this half is withdrawn in favour of a different extractor** — not shipped and hoped over. Valid for: this palace's agent-written corpus; a transcript-mined palace already works.

**2. A graph tool that cannot answer says so.** `am_list_hallways`, `am_traverse` and `am_graph_stats` return a note when the wing holds drawers but no entities — the same shape as the empty-wing note, and for the same reason: an empty result and a broken feature are byte-identical to an agent, so it concludes the graph is empty and stops asking. This half holds even if half 1 is withdrawn; in fact it matters more then.

## Alternatives Considered

- **Extract entities at recompute time instead of at write time.** Rejected: `RecomputeGraph` would re-derive entities for every drawer on every run, and the stored `entities` column exists precisely so it does not have to. It also leaves `drawers.entities` permanently empty, so anything else reading it stays broken.
- **Use a model to extract entities on write.** Rejected for the write path: `am_add_drawer` is on the interactive path an agent waits on, and a model call per chunk buys quality nobody has yet shown is needed. `kg-extract` already exists for the model-based route and runs offline.
- **Delete hallways, `am_traverse` and `am_graph_stats`.** Genuinely considered and rejected: the derivation is written, correct, tested, and works the moment it has input. Deleting a working subsystem because its producer was never wired is the wrong repair. But if half 1's falsification fires and no extractor suits the corpus, this becomes the honest option and is recorded here so it is a decision rather than a drift.
- **Ship half 1 alone.** Rejected: if the extraction turns out thin on some corpus, the tools go back to being silently empty and nothing says so. Half 2 is what makes the failure legible.

## Component / Boundary Impact

`internal/palace` keeps ownership of what a drawer is and what the graph derives; `internal/mcpserver` keeps ownership of what an agent is told. Half 1 is internal to the write path. Half 2 adds a note to three tool handlers, reusing the shape `emptyWingNote` already established. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `drawers.entities` populated on the `Add` path | change | `internal/palace/service.go` | `RecomputeGraph`, `computeHallwaysForWing`, entity tunnels |
| an `emptyGraphNote` on the three graph tools | add | `internal/mcpserver` | every agent that asks about the graph |
| `am_add_drawer` write latency | change — one extraction pass per chunk, no model call | `internal/palace/service.go` | every writer |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the measurement that decides half 1 | T1 | T2 | No — T1 is a measurement and may withdraw T2 |
| entities on the `Add` path | T2 | T2 | No — additive; existing drawers keep their empty column until a backfill |
| `emptyGraphNote` | T3 | T3 | No — additive |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** the navigable half of the product starts existing for the people who actually use it. `am_traverse` and `am_list_hallways` stop being tools that have never once returned anything.
- **Negative:** every write does an extraction pass over its own text. It is string frequency counting, not inference, but it is not free and it is on the interactive path.
- **Neutral:** drawers filed before this change keep an empty `entities` column and remain outside the graph until something backfills them. A palace will therefore have a graph over its recent memories and not its older ones, which is worth stating in the note rather than leaving to be discovered.

## Out of Scope

- Backfilling `entities` for drawers already filed (deferred: docs/adr/BACKLOG.md)
- Model-based entity extraction on the write path (permanent: `kg-extract` owns the model-based route and runs offline; putting a model call on an interactive write is a different product decision, not a variation of this one)
- Whether the closet prior should be revived (deferred: docs/adr/ADR-003-retire-the-closet-prior.md — closets are empty for the same root cause, and ADR-003 owns that question)
- Making a merge rebuild the derived graph (deferred: docs/adr/ADR-015-the-index-must-not-outlive-the-wing-it-indexed.md — received from ADR-015; a merge cannot be said to invalidate a graph that cannot exist, so this ADR has to land first)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Agent-written memories are too short for a frequency-based extractor, so hallways stay empty for a subtler reason | High | High | T1 measures it on the 359 drawers already filed BEFORE T2 is written, and withdraws T2 below 20% |
| Extraction adds latency to every write | Med | Low | T1 measures it in the same pass; it is counting, not inference |
| The graph fills with noise entities and hallways connect nothing meaningful | Med | Med | The co-occurrence threshold already exists in `computeHallwaysForWing`; T1 reports what the derived graph WOULD look like, so the threshold is set against real data rather than guessed |

## Rollback

Half 1 is one assignment; reverting it stops new drawers carrying entities and leaves existing ones harmlessly populated — a stale `entities` column changes nothing except what the graph derives. Half 2 is additive text on three read-only tools. No schema change, no migration, nothing to undo in storage.

## Follow-ups
