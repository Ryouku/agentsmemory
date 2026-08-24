# ADR-024: Rank memories, not chunks

**Status:** Accepted
**Date:** 2026-08-24
**Owner:** Mindaugas
**Spec:** Production feedback: schema analysis found the retrieval unit, not SQLite durability, to be the binding defect.
**Cross-references:** ADR-013 (a page of memories, not chunks), ADR-019 (a hit shows matching regions), ADR-006 (a knob that does nothing must say when), ADR-014 (the shipped default is the measured one)
**Supersedes:** ADR-013's decision to rank chunks and collapse only after ranking, and its deferral of cross-chunk evidence aggregation. It does not supersede chunk-backed storage or `am_get_drawer whole=true`.
**Served-path change:** behind `MEMORY_LEVEL_RANKING=true`, vector retrieval fills a pool of distinct memories, BM25 and the cross-encoder score one combined evidence document per memory, and `am_search` carries memory-level identity, regions and anchor staleness. The unset/false control keeps the existing chunk-ranked path for production A/B comparison.

## Context

SQLite is the durable source of truth and stores one row per chunk. That is not the binding problem.
The served search path uses that storage row as its ranking unit too:

1. vector search returns chunks;
2. BM25 scores chunks;
3. the cross-encoder scores chunks;
4. only after every rank has been decided does `Search` collapse siblings onto `ParentID`.

That ordering creates three structural failures which tuning cannot remove.

**A long memory can spend the candidate pool on itself.** `candidateK` counts chunks. If six sibling
chunks occupy a six-candidate prefix, the next memory is unreachable to BM25 and the cross-encoder,
however relevant it would have been. Raising the pool changes how often this happens and never changes
the unit that causes it.

**Evidence cannot combine across chunks.** A memory whose premise is in chunk 0 and conclusion in
chunk 1 is presented to every ranker as two unrelated documents. `ChunksMatched` reports the loss
afterward; no score consumes it.

**Staleness can disappear.** `am_add_drawer` attaches supplied code anchors to chunk 0. ADR-013 keeps
the best-ranked chunk, deliberately, so a child can win. The MCP adapter then asks for anchors by the
winning child id only. A stale root anchor therefore vanishes from precisely the search result whose
memory it is meant to qualify.

ADR-013 fixed the page unit and explicitly deferred merging evidence until usage justified the cost.
This feedback is the missing evidence: the defect is before the page, at candidate and score
granularity. ADR-019 improved what the agent can inspect after retrieval; it cannot recover a memory
which sibling chunks kept outside the pool.

## Decision

**Chunk remains the storage and embedding unit. Memory becomes the optional retrieval and ranking
unit.** No migration rewrites ids, vectors, tunnels or knowledge-graph pointers.

### Candidate pool

The treatment interprets `candidateK` as a target number of distinct memory roots. It asks the vector
index for a prefix, resolves each hit to `ParentID` (or its own id when unchunked), and widens the
prefix until either:

- the target number of distinct in-scope memories is present;
- the index returns fewer rows than requested; or
- the cosine-distance boundary proves every later row would be rejected too.

This preserves chunk embeddings — the best passage still nominates its memory — while preventing one
memory's siblings from consuming another memory's candidate slot. The widening has no new tuning
constant: it doubles only while the declared memory target has not been met.

### Memory evidence and scores

Every nominated root is hydrated with all of its chunks in `chunk_index` order. Overlapping add-drawer
chunks are de-overlapped; diary chunks, which never overlap, are concatenated exactly.

- vector distance is the best (smallest) distance among the memory's retrieved chunks;
- BM25 sees the reassembled memory once, so terms in separate chunks contribute to one score;
- the cross-encoder sees one bounded evidence document per memory, assembled from at most four
  coherent 400-rune matching passages within the existing `ChunkSize` budget; selection covers
  previously unseen query terms before spending another passage on repeated vocabulary;
- only the raw user query selects those passages; optional `SearchQuery.Context` is appended to the
  cross-encoder query but cannot change which source text the model receives;
- closet boost is applied once per memory;
- `ChunksMatched` remains the count of that memory's chunks present in the vector prefix.

The representative drawer id remains on the wire for compatibility. A new always-present
`memory_id` states the logical identity explicitly. Snippets, identity, regions, content length and
coverage are computed against the reassembled memory rather than the representative chunk. With
`snippet_chars=0`, `content` is the whole reassembled memory, matching the tool's existing promise.

### Anchors

Anchors are reported at memory granularity. Given any representative chunk, the read path resolves
its memory root and returns anchors attached to any sibling. Existing root anchors therefore require
no migration, and a child hit cannot lose a root's stale verdict.

### Production A/B

`MEMORY_LEVEL_RANKING=false` (the default) is the legacy control.
`MEMORY_LEVEL_RANKING=true` is the treatment. The same setting is exposed as
`--memory-level-ranking` so CLI and environment go through one binding.

The resolved startup ranking profile and `am_status.ranking` include `unit=chunk` or `unit=memory`.
That value is the authority for which arm ran; an `.env` file is only intent and may be overridden by
Compose or process environment.

The default stays false until production comparison selects a winner. Shipping an unmeasured default
would contradict ADR-014; shipping an unreachable treatment would contradict ADR-006.

### Measured production comparison (2026-08-24)

The first bounded production comparison does **not** justify changing the default. The same PR #25
image was restarted once per arm. `am_status.ranking` proved that every ranking knob was unchanged
(`rrf`, auto lexical weight, page-max normalisation, closet boost 0, reranker pool 128 and weight
0.75); only `unit=memory` changed to `unit=chunk`. Six fixed queries with preselected memory ids were
run three times per arm against `wing_agentmemories`, with the same limits, context and distance
thresholds.

| Query | Treatment rank | Control rank | Treatment median | Control median |
|---|---:|---:|---:|---:|
| live open threads | 1 | 1 | 1,479 ms | 1,257 ms |
| chunk-crowding design | 1 | 1 | 1,318 ms | 1,267 ms |
| authenticated templ preview technique | 1 | 1 | 1,464 ms | 1,172 ms |
| PR image protocol | 1 | 1 | 1,469 ms | 1,341 ms |
| current deploy truth | 1 | 2 | 1,467 ms | 1,230 ms |
| rejected alternatives / child passage | 2 | 2 | 1,423 ms | 1,309 ms |

Using one expected id per query gives treatment MRR 0.917 and control MRR 0.833, with hit@1 of 5/6
and 4/6 respectively. That apparent quality gain is not strong evidence: for the deploy query the
control ranked an older memory containing the same correct answer first, and the child-passage query
also had another relevant implementation diary at rank 1 in both arms. By answer correctness rather
than one-id identity, both arms answered all six probes. The treatment did select child chunk 1 for
the child-passage probe while control selected root chunk 0; both returned the full memory and both
delivered the two verified sibling/root anchors. Anchor delivery is therefore verified, but it does
not distinguish the arms because the protocol fix intentionally applies to both.

Across the 18 graded calls, client-observed median elapsed time was 1,466 ms for treatment and
1,269 ms for control: treatment was about 197 ms or 15.5% slower. This is a small sequential sample,
not a server-side latency benchmark. The treatment-baseline write also added two chunks before the
control window (1,460 to 1,462 drawers), so the comparison is near-matched rather than an identical
corpus. Neither change altered the selected answers except for the equivalent deploy ordering above.

The required cost attribution is still incomplete. Search telemetry records candidates, hits and
whether reranking ran, but neither `am_search` nor `am_recall_stats` exposes candidate/vector depth or
reranker document cost. The comparison can therefore assess returned ranks, evidence, anchors and
client latency, but cannot say how much prefix widening or reranker work the treatment paid for.

**Verdict:** retain `MEMORY_LEVEL_RANKING=false`. The treatment fixes the adversarial structural
failures pinned by tests, but this small live workload showed equivalent answer quality and a latency
cost. A later decision to change the default needs a larger real-query population with multi-answer
relevance (`ExpectAny` or human judgement), an unchanged corpus or crossover replay, and exposed
candidate/reranker cost. The treatment remains available for that experiment; the control is not
removed.

### Unchanged-corpus long-memory comparison

A second comparison held the corpus fixed at 1,462 drawers and selected one 3–5 chunk target from
each of the nine rooms containing such memories. The query set was frozen under `unit=chunk` and
replayed unchanged under `unit=memory`: nine queries requiring separated evidence, three sequential
runs per arm, `limit=10`, `max_distance=1.5`, `snippet_chars=500`, and identical rerank context.

| Room | Chunks | Target rank false → true | Median latency false → true |
|---|---:|---:|---:|
| architecture | 5 | 1 → 1 | 1,316 → 1,568 ms |
| decisions | 3 | 2 → 2 | 1,354 → 1,556 ms |
| tooling | 4 | 1 → 1 | 1,276 → 1,525 ms |
| operations | 4 | **1 → 2** | 1,355 → 1,441 ms |
| technical | 4 | 1 → 1 | 1,304 → 1,668 ms |
| learnings | 3 | 1 → 1 | 1,299 → 1,572 ms |
| diary | 3 | 1 → 1 | 1,290 → 1,460 ms |
| human-decisions | 3 | **1 → 2** | 1,348 → 1,533 ms |
| llm_ruled_out | 3 | 1 → 1 | 1,327 → 1,376 ms |

| Aggregate metric | false / chunk | true / memory |
|---|---:|---:|
| Exact target hit@1 | **8/9** | 6/9 |
| Exact target hit@10 | 9/9 | 9/9 |
| Exact target MRR | **0.944** | 0.833 |
| Fully correct semantic top answer | **9/9** | 8/9 |
| Median client-observed latency, 27 calls | **1,316 ms** | 1,525 ms |
| Latency delta | — | **+209 ms / +15.9%** |

The treatment was active rather than silently unwired: it aggregated 4/4 chunks for the operations
target where the control carried 3/4. Nevertheless, that complete memory fell to rank 2 behind a
shorter diary which omitted why recomputation cannot restore old entities. This was the one semantic
top-answer regression; the decisions and human-decisions rank changes had fully correct alternate
memories.

### Algorithm correction after the long-memory comparison

Source tracing found two coupled evidence-document defects in the first treatment implementation.
`Search` passed `query + Context` both as the cross-encoder question and as the selector for source
regions. That violated the existing `SearchQuery.Context` contract: neutral experiment context could
replace or dilute passages selected by the user's question. Separately, `SnippetRegions` divided a
1,600-rune reranker budget into as many as sixteen 100-rune fragments. This maximized match coverage
but often removed the explanation following each matched term, giving a short partial note more
usable prose than a complete long memory.

The treatment now separates the two inputs. The raw query alone selects evidence, while Context still
reaches the cross-encoder as intended. Reranker evidence is capped at four passages, retaining about
400 contiguous runes per selected place; the public agent-visible `SnippetRegions` behavior is
unchanged. A served-path regression fixture reproduces the production shape: the control still ranks
a two-reason short diary first, while the corrected memory arm exposes all three separated reasons
and ranks the complete memory first. A second test proves adding Context changes the model query but
not its evidence documents.

### First corrected-image replay

The frozen nine-query workload was replayed against PR #25 image
`sha256:a66a2c72572305efc4ac638f969c8275c9eb9a6e221cb24466136a36b09501f3`.
The profile was unchanged except for the corrected code and still reported `unit=memory`. The corpus
was 1,463 drawers rather than 1,462: immediately before deployment, the ADR-024 source-backed memory
was idempotently replaced with a two-chunk implementation record, a net increase of one. That new
memory appeared at ranks 6, 4 and 8 for three queries and was absent from the other six; it was not
the new rank-1 diary competitor described below. The comparison is therefore near-matched, not an
unchanged-corpus verdict.

| Room | false rank | first true rank | corrected true rank | Corrected median |
|---|---:|---:|---:|---:|
| architecture | 1 | 1 | 1 | 1,468 ms |
| decisions | 2 | 2 | 2 | 1,446 ms |
| tooling | 1 | 1 | 1 | 1,376 ms |
| operations | 1 | 2 | **1** | 1,406 ms |
| technical | 1 | 1 | 1 | 1,406 ms |
| learnings | 1 | 1 | 1 | 1,465 ms |
| diary | 1 | 1 | **2** | 1,417 ms |
| human-decisions | 1 | 2 | **1** | 1,386 ms |
| llm_ruled_out | 1 | 1 | 1 | 1,436 ms |

| Aggregate metric | false / chunk | first true / memory | corrected true / memory |
|---|---:|---:|---:|
| Exact target hit@1 | **8/9** | 6/9 | 7/9 |
| Exact target MRR | **0.944** | 0.833 | 0.889 |
| Fully correct semantic top answer | **9/9** | 8/9 | 8/9 |
| Median client-observed latency | **1,316 ms** | 1,525 ms | 1,406 ms |

The intended operations regression was fixed: its complete four-chunk memory moved from rank 2 to
rank 1, and human-decisions also moved from 2 to 1. Latency fell by 119 ms relative to the first
treatment, although it remained 90 ms or 6.8% above control.

The correction did not meet the semantic gate; it moved the failure. The diary target fell from rank
1 to rank 2 behind a decision memory which answered why the subject is derived but omitted both the
stale-GitHub verification lesson and the remaining open items. Source tracing showed the remaining
mechanism: the four evidence passages were still selected by absolute term count. Several distant
passages repeating the dense first clause could consume all four slots before a lower-density clause
such as “what remained open” received one.

The next correction makes private reranker selection coverage-aware. Query terms are deduplicated,
and each next passage is chosen by how many previously uncovered terms it adds; after all reachable
terms are covered, ordinary score order resumes so repeated occurrences can still combine premise
and conclusion. Agent-visible `SnippetRegions` remains score-first. A focused fixture with four
repetitions of the dense clause and one low-density `remained open` passage was red when this wiring
was removed and green when restored. This second correction still requires the same live replay;
the shipped default remains false.

## Alternatives Considered

- **Change the durable schema to one row and one vector per memory.** Rejected. It removes passage-level
  vector recall, rewrites ids and invalidates references. SQLite durability is not the defect.
- **Only move collapse before BM25/rerank.** Rejected. A long memory has already consumed the vector
  prefix, so memories outside it remain unreachable.
- **Raise the candidate or rerank pool.** Rejected as the fix. It reduces frequency without changing
  granularity, and makes latency the price of an invariant.
- **Score every chunk and add the scores.** Rejected. Long memories receive more chances and therefore
  a length prior; a memory with ten weak chunks can beat one strong short memory because it is long.
- **Send the entire memory to the cross-encoder.** Rejected. The current chunk size is already chosen
  around the model's useful passage budget; a long concatenation is truncated by the model and silently
  recreates the child-chunk problem at its input boundary.
- **Turn the treatment on by default immediately.** Rejected for this PR. The user explicitly asked for
  production A/B, and ADR-014 requires the shipped default to be the measured one.

## Component / Boundary Impact

`internal/palace` owns candidate aggregation, memory reassembly and memory-level ranking.
`internal/mcpserver` owns the additive wire field and presentation against whole-memory text.
`cmd/server` owns the A/B selection and its observable resolved profile. Storage interfaces and
backends do not change.

`docs/architecture.md` records both served paths while the experiment exists. Removing the control
after a verdict is a follow-up change, not part of the experiment.

## Wiring & Contract Changes

| Surface | Control (`false`) | Treatment (`true`) |
|---|---|---|
| vector candidate target | chunks | distinct memories |
| BM25 document | one chunk | one reassembled memory |
| reranker document | one chunk | bounded cross-chunk regions from one memory |
| page collapse | after ranking | unnecessary; candidates are already memories |
| anchor lookup | every sibling under `memory_id` | every sibling under `memory_id` |
| `am_search.memory_id` | added, logical root | added, logical root |
| `am_status.ranking` | includes `unit=chunk` | includes `unit=memory` |

## Verification

- a synthetic vector prefix made only of siblings must widen until another memory becomes rankable;
- removing that widening must make the test red;
- a cross-encoder spy must receive one document per memory and see terms supplied by separate chunks;
- the same fixture under the control must still receive chunk documents;
- an end-to-end MCP search whose child chunk wins must carry the stale root anchor;
- the real CLI/env binding must move `Config.MemoryLevelRanking`, and the composition-root test must
  observe different `unit=` profiles for false and true;
- the architecture gate and full Go suite must pass.

## Consequences

- **Positive:** candidate, score, metadata and page share one logical unit in the treatment.
- **Positive:** the experiment is reachable from production configuration and identifies itself.
- **Negative:** a clustered vector prefix may require more than one index query.
- **Negative:** treatment BM25 reads every chunk of each nominated memory, increasing SQLite work.
- **Neutral:** stored rows, embeddings and ids remain chunk-based and require no migration.

## Rollback

Set `MEMORY_LEVEL_RANKING=false` and restart. The control path is retained intact; no data has changed.
Reverting the implementation removes the treatment, flag and additive `memory_id` field without a
schema rollback.
