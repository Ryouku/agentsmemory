# ADR-024: Rank memories, not chunks

**Status:** Accepted
**Date:** 2026-08-24
**Owner:** Mindaugas
**Spec:** None — no spec stage. The trigger was production feedback: schema analysis found the retrieval unit, not SQLite durability, to be the binding defect.
**Cross-references:** ADR-013 (a page of memories, not chunks), ADR-019 (a hit shows matching regions), ADR-006 (a knob that does nothing must say when), ADR-014 (the shipped default is the measured one)
**Supersedes:** ADR-013's decision to rank chunks and collapse only after ranking, and its deferral of cross-chunk evidence aggregation. It does not supersede chunk-backed storage or `am_get_drawer whole=true`.
**Served-path change:** (2026-08-25) Memory is the only ranking unit. Vector retrieval fills a pool of distinct memories and BM25 and the cross-encoder score one combined evidence document per memory. `am_search` carries memory-level identity, regions, coverage and anchor staleness. `MEMORY_EVIDENCE_SELECTOR=lexical|semantic` chooses how that bounded reranker document is selected from the reassembled memory; lexical is the default/control. The chunk-ranked control and `MEMORY_LEVEL_RANKING` were deleted.

**2026-08-25 retirement:** This is a reachability wipe, not a quality overturn of the 2026-08-24 bake-offs below, and it **supersedes the Final verdict's "retain both treatments" clause**. Both comparisons found equivalent answer quality; neither found a rank difference. They disagree on cost, and both numbers belong here rather than the smaller one alone: the six-query run put the treatment ~15.5% slower at the median, and the frozen nine-query run — the one that carries the Final verdict — put it 61.8% slower at the median and 2.15 times slower at the mean.

Neither figure is a clean read on the unit change, and "Cost attribution" below says why: `Repo.MemoryChunksByRoots` examined every drawer the tenant owned, twice per recall, in BOTH arms, so it raised the latency FLOOR of every measurement in this document. That section asks for the frozen suite to be replayed after the fix before its numbers compare the arms again. **That replay has not been run**, so the retirement is decided on reachability, not on latency.

Collapsing anyway for two reasons. A flag that still selects chunk ranking is a second production `Search`, and this repository's recurring defect is a path that exists without being reachable. And `false` had already stopped being a clean control: whole-memory hydration ran unconditionally in `rankRetrieved`, so every caller received memory-shaped results with no flag set. Every consumer of `rankRetrieved` already collapsed when the flag was on; the leftover was the `if` that skipped it. `am_status.ranking` always reports `unit=memory`. ADR-014's measured default was `MEMORY_LEVEL_RANKING=false`; restoring chunk ranking is now a revert of this retirement, not a flag, and the tables below stay as history.

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
- the index returns fewer rows than requested;
- the cosine-distance boundary proves every later row would be rejected too; or
- the prefix reaches `maxCandidateWidening` times `candidateK`.

This preserves chunk embeddings — the best passage still nominates its memory — while preventing one
memory's siblings from consuming another memory's candidate slot.

That fourth stop is a safety bound rather than a tuning knob, and it is deliberately not an operator
setting. Every one of the first three assumes the index honoured the scope filter, and the loop does
not rely on that — the durable row is the authority. When the two disagree nothing in a prefix
survives filtering, the distinct count never advances, "the backend ran out" never becomes true, and
the widening walks the corpus a doubling at a time. At the ceiling the arm ranks the memories it did
find, exactly as it would when the backend runs out. Rows already resolved by a narrower prefix are
carried across rounds, so a doubling pays only for the tail it adds.

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

### Evidence selector A/B

The reranker input budget remains `ChunkSize`; “search the whole memory” means the whole reassembled
text is the passage-selection universe, not that the whole text is sent to the cross-encoder or reduced
to one pooled vector.

`MEMORY_EVIDENCE_SELECTOR=lexical` preserves the current selector: distinct raw-query terms choose at
most four coherent regions, and previously unseen terms are covered before repeated vocabulary spends
another slot. `MEMORY_EVIDENCE_SELECTOR=semantic` is the new treatment. For long memories in the
cross-encoder shortlist it:

1. creates overlapping coherent windows across the reassembled text, including original chunk
   boundaries;
2. reuses the raw query vector and embeds every candidate window in bounded batches of at most 128;
   the TEI adapter discovers `max_client_batch_size` from `/info` and splits to the server's actual
   limit, retaining the standard 32-input fallback when capability discovery is unavailable. Only a
   SUCCESSFUL probe is cached: a failure falls back for that call and is retried after a backoff, and
   the probe never runs while holding the lock that guards the cached value, so no embed ever waits
   on it;
3. selects the strongest semantic window, then non-near windows which add uncovered query terms or
   passage diversity, up to the same four-region and `ChunkSize` ceilings;
4. restores source order and cross-encodes the resulting verbatim evidence once for that memory.

Window similarity selects evidence only. It is not blended into the final memory score: summing or
maxing every window would give a long memory more chances to win merely because it is long. A single
best window is also insufficient, because it recreates the original defect when a premise, conclusion
and open item live in separate places.

The semantic selector applies only when memory-level ranking and cross-encoding both run. Short
memories already fit the model budget and are passed through without another embedding call. If the
window embedding call fails or returns unusable vectors, search fails open to the lexical evidence
documents for the entire shortlist; a failure never leaves part of a page on one selector and the
rest on the other. Passing short memories through is not that case and is not a silent mix: a memory
inside the model budget is sent whole under either selector, so there is nothing for a window
embedding to choose. Startup and
`am_status.ranking` expose `evidence=lexical|semantic`, so measurements identify the resolved arm rather
than an intended `.env` value.

This is deliberately a served-path experiment, not a storage migration or a passage score added to
fusion. Its principal cost is an additional batched embedding pass over long-memory windows. Production
comparison must record the resolved selector and end-to-end recall latency; selector failures are named
in logs. Per-window counters are a follow-up only if aggregate latency cannot explain the comparison,
and must not use raw query or memory content as metric labels.

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

Within the memory treatment, `MEMORY_EVIDENCE_SELECTOR=lexical` (the default) is the measured selector
control and `semantic` is the query-time passage-embedding treatment. The same setting is exposed as
`--memory-evidence-selector`. Selecting `semantic` while memory-level ranking or the cross-encoder is
off is observable but inert; the resolved profile still reports it so an operator does not mistake
intent for execution.

The resolved startup ranking profile and `am_status.ranking` include `unit=chunk` or `unit=memory`.
That value is the authority for which arm ran; an `.env` file is only intent and may be overridden by
Compose or process environment.

The production comparisons below selected the control, so the default remains false. Changing it
later requires new measured evidence. Shipping an unmeasured default would contradict ADR-014;
shipping an unreachable treatment would contradict ADR-006.

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
was removed and green when restored. The selector and complete-arm replays below close that live
verification thread; they did not justify changing the default.

### Evidence-selector and TEI replay

The next experiment froze nine queries and their expected 3–4 chunk memories across nine rooms on an
unchanged 1,487-drawer corpus. Both selector arms used `unit=memory`, target-room filters, `limit=10`,
`max_distance=1.5`, `snippet_chars=500`, the same context, reranker pool 128 and weight 0.75. Only
`MEMORY_EVIDENCE_SELECTOR` changed, with one sequential call per query and selector.

| Metric | `lexical` | `semantic` |
|---|---:|---:|
| Exact target hit@1 | 9/9 | 9/9 |
| Exact target MRR | 1.0 | 1.0 |
| Higher target rerank score | **6/9** | 3/9 |
| Median client-observed latency | **592 ms** | 7,714 ms |
| Mean client-observed latency | **640 ms** | 10,256 ms |

The agent-visible display regions were identical in every pair. That is expected: public regions
remain lexical snippets and do not expose the private document sent to the cross-encoder. It means
this run observed no rank or displayed-evidence gain from semantic selection, not that both selectors
necessarily sent the same cross-encoder text. Q9 had only one distinct candidate and therefore tested
evidence construction and score, not ordering.

Moving passage embeddings from Ollama to TEI reduced the two semantic latency tails without changing
their target ranks, scores or displayed regions. Adaptive client batching then cached
`/info.max_client_batch_size=128` and raised the evidence caller's request cap from 64 to 128:

| Passage embedding path | Q2 decisions | Q7 diary | Two-query mean |
|---|---:|---:|---:|
| Ollama semantic | 20,147 ms | 32,197 ms | 26,172 ms |
| TEI semantic before adaptive batching | 3,450 ms | 5,123 ms | 4,287 ms |
| TEI semantic after adaptive batching | **2,881 ms** | **4,216 ms** | **3,549 ms** |
| TEI lexical control | 1,122 ms | 1,240 ms | **1,181 ms** |

Adaptive batching improved those semantic calls by 17.2% together, but optimized semantic remained
about 3.0 times slower than lexical. This validates capability negotiation as an execution
improvement, not a selection-quality improvement. The selector verdict is therefore to keep
`MEMORY_EVIDENCE_SELECTOR=lexical` as the production default and retain `semantic` as a reachable
A/B arm.

### Final frozen complete-arm replay

The final replay compared the complete served protocols on an unchanged 1,499-drawer corpus. The
same frozen nine queries ran twice sequentially per arm with the same room filters and search/rerank
settings. `MEMORY_EVIDENCE_SELECTOR=semantic` remained configured throughout: it was active under
`MEMORY_LEVEL_RANKING=true` / `unit=memory` and intentionally inert under `false` / `unit=chunk`.
This answers whether the deployable arms return the same ranks; it is not an isolated selector test.

| Room | Target rank, true runs 1 / 2 | Target rank, false runs 1 / 2 | Median latency, true → false |
|---|---:|---:|---:|
| architecture | 1 / 1 | 1 / 1 | 1,584 → 1,337 ms |
| decisions | 1 / 1 | 1 / 1 | 3,767 → 1,513 ms |
| tooling | 1 / 1 | 1 / 1 | 1,735 → 1,153 ms |
| operations | 1 / 1 | 1 / 1 | 1,176 → 806 ms |
| technical | 1 / 1 | 1 / 1 | 699 → 489 ms |
| learnings | 1 / 1 | 1 / 1 | 1,633 → 957 ms |
| diary | 1 / 1 | 1 / 1 | 5,896 → 1,115 ms |
| human-decisions | 1 / 1 | 1 / 1 | 861 → 391 ms |
| llm_ruled_out | 1 / 1 | 1 / 1 | 304 → 438 ms |

| Aggregate metric, 18 calls per arm | true / memory + semantic | false / chunk |
|---|---:|---:|
| Exact target hit@1 | 18/18 | 18/18 |
| Exact target MRR | 1.0 | 1.0 |
| Top-memory-id disagreements | 0/18 | 0/18 |
| Target-rank disagreements | 0/18 | 0/18 |
| Median client-observed latency | 1,548 ms | **956.5 ms** |
| Mean client-observed latency | 1,961 ms | **911 ms** |

Rerank scores differed because the arms deliberately sent different cross-encoder documents; those
scores are not directly comparable across protocols. None of the score changes altered ordering.
The diary target did expose the treatment's structural effect: all three chunks matched under the
memory arm and two under the chunk arm, while the target stayed rank 1 in all four calls.

**Final verdict:** keep `MEMORY_LEVEL_RANKING=false` and
`MEMORY_EVIDENCE_SELECTOR=lexical` as the production defaults. The treatment repairs the candidate,
evidence and anchor granularity invariants in adversarial tests and can expose broader evidence, but
these frozen production replays found no ordering or answer-quality gain and a material latency cost:
memory plus semantic was 61.8% slower at the median and 2.15 times slower at the mean. Retain both
treatments for future experiments; retain adaptive TEI batching because it improved execution without
changing results.

> ⚠ **Partly superseded on 2026-08-25 — see the retirement note at the top of this ADR.** The
> chunk-ranked arm was deleted for reachability, so "keep `MEMORY_LEVEL_RANKING=false`" and "retain
> both treatments" no longer describe the code. The latency figures in this section were never
> replayed after the common-mode floor named in "Cost attribution" was fixed. The
> `MEMORY_EVIDENCE_SELECTOR=lexical` default is unchanged and still stands.

This final run is a deterministic room-filtered comparison, not a population latency estimate: it has
two sequential calls per arm, and Q9 has one distinct candidate. Before reconsidering either default,
expose selected evidence offsets and fail-open state, vector-prefix depth and cross-encoder document
cost; add a wing-wide unfiltered competition suite and a larger real-query population with multi-answer
or human-judged relevance.

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
- **Embed the entire memory once and use that vector as its fragment selector.** Rejected. One pooled
  vector averages unrelated sections and yields no source span, so it cannot identify the text the
  cross-encoder should read.
- **Use only the single best semantic fragment.** Rejected. It cannot combine separated premise,
  conclusion and remaining-work evidence, which is the structural failure this ADR exists to remove.
- **Add every window similarity to the memory score.** Rejected. A long memory receives more scoring
  opportunities and therefore a length prior. Semantic similarity chooses bounded evidence only.
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
| memory evidence selector | inert | `lexical` or query-time `semantic`, resolved in profile |
| page collapse | after ranking | unnecessary; candidates are already memories |
| anchor lookup | every sibling under `memory_id` | every sibling under `memory_id` |
| `am_search.memory_id` | added, logical root | added, logical root |
| `am_status.ranking` | includes `unit=chunk evidence=…` | includes `unit=memory evidence=lexical|semantic` |

### Cost attribution, and what it does to the measurements above

The comparisons above closed with the cost attribution incomplete: they could report ranks, evidence,
anchors and client latency, but not how much prefix widening or reranker work the treatment paid for.
A review of the implementation supplied the mechanism by source tracing and local execution. It did
not re-run any of the production workloads, so **none of the measured numbers above are revised** —
but one finding changes how they should be read.

**The largest cost was common-mode, and therefore invisible as a treatment delta.**
`Repo.MemoryChunksByRoots` was spelled `id IN (...) OR parent_id IN (...)`. No planner seeks both
sides of a disjunction in one index pass, so it examined every drawer the tenant owned; `parent_id`
had no index at all, and adding one does not change the plan while the `OR` stands. It ran twice per
recall in BOTH arms — `hydrateResultMemories` (or `collapseCandidatesToMemories` under the treatment)
and then `AnchorsForMemories`. On the hosted workspace these measurements were taken against, that is
roughly 7,361 rows examined per call, twice per search, to return ten memories. `main` did none of it.

Because the control paid it too, it never appeared as a treatment delta; it raised the FLOOR of every
latency figure in this document. The frozen nine-query suite should be replayed after these fixes
before its latency numbers are used to compare the arms again — the treatment's measured penalty was
partly this, not the unit change.

The remaining attribution, all now fixed:

- **the selector's latency was serialisation, not the cross-encoder.** A 5,000-rune memory yields 17
  windows, so a shortlist is thousands of passages, embedded one batch after another before the
  reranker started. That is the `592 ms → 7,714 ms` above. Adaptive batching cut the NUMBER of round
  trips and left them end to end, which is exactly why it bought 17.2% and no more. Batches now run
  under a bounded errgroup, and the evidence documents are byte-identical, so the selector comparison
  above remains valid.
- **adaptive TEI batching was one transient error away from never running.** Discovery used
  `sync.Once`, which fires whether the probe succeeded or failed, so a still-warming TEI or one
  disconnected caller pinned the 32-input fallback for the process lifetime, silently. The 17.2%
  credited above was not guaranteed to be present in any given process.
- **widening re-read the whole prefix on every doubling**, about twice the final prefix in database
  work, and had no ceiling but an int-overflow guard. It is incremental and bounded now.
- **`collapseCandidatesToMemories` was quadratic** in the candidate pool the treatment widens.
- **semantic windows silently skipped long unbroken tokens** — digests, image refs, URLs, which this
  corpus is full of — leaving 87% of a measured memory eligible as evidence and admitting a 98-rune
  stub into a four-slot budget.

One review finding was withdrawn rather than fixed, and is recorded so it is not re-derived: the
four-passage cap does NOT shred a memory that already fits the cross-encoder budget. Probed at 800,
1,500 and 1,599 runes, the evidence equals the content — short memories reach the whole-content
fallback intact. The shredding this ADR describes was real and was already fixed by the correction
above.

The instrumentation this document still asks for — selected evidence offsets and fail-open state,
vector-prefix depth, cross-encoder document cost — is unchanged and still wanted. A falsifiable
prediction to test it against: what remains after these fixes will be dominated by cross-encoder work
and vector search, not by SQLite row volume or embedding round trips.

## Verification

- a synthetic vector prefix made only of siblings must widen until another memory becomes rankable;
- removing that widening must make the test red;
- a cross-encoder spy must receive one document per memory and see terms supplied by separate chunks;
- a semantic-selector spy must receive batched passage embeddings rather than per-window calls, choose
  paraphrased distant regions from the whole reassembled memory, and keep them within `ChunkSize`;
- a TEI server advertising a 128-input client batch must receive 128-input requests in order, while a
  missing capability endpoint retains the safe 32-input fallback and excessive limits stay capped;
- removing the config-to-service selector assignment must make the production reachability test fail;
- semantic embedding failure must return the lexical evidence order and report fail-open rather than
  failing recall or mixing selectors within one page;
- the same fixture under the control must still receive chunk documents;
- an end-to-end MCP search whose child chunk wins must carry the stale root anchor;
- the real CLI/env binding must move `Config.MemoryLevelRanking`, and the composition-root test must
  observe different `unit=` profiles for false and true;
- the memory-chunk lookup's QUERY PLAN must show a seek on `id` and a seek on `parent_id`, asserted
  on the constrained columns rather than the index name — with migration 00024 applied the old `OR`
  spelling still names `idx_drawers_team_parent` while constraining `team_id` alone, so only the seek
  columns separate a fix from the defect;
- reverting either the union or migration 00024 must make that gate red, independently;
- anchor resolution must issue no whole-row read of `drawers`, asserted from the statements the
  anchor path ACTUALLY issues rather than from a query built in the test;
- semantic evidence batches must overlap, observed as a high-water mark of concurrent embed calls so
  the gate measures concurrency rather than machine speed;
- a failed TEI capability probe must be retried rather than cached, and a cancelled caller must not
  decide the process's batch size — the second asserted by whether `/info` was REACHED, because the
  retry alone would otherwise mask it;
- candidate widening must not re-request a row it already resolved, on a fixture proven to widen;
- widening must stop at a written-out bound, not one read from the constant under test;
- every rune of a memory containing a long unbroken token must fall inside some evidence window;
- the architecture gate and full Go suite must pass.

## Consequences

- **Positive:** candidate, score, metadata and page share one logical unit in the treatment.
- **Positive:** the experiment is reachable from production configuration and identifies itself.
- **Negative:** a clustered vector prefix may require more than one index query, bounded by
  `maxCandidateWidening` and loaded incrementally so a round pays only for the tail it added.
- **Negative:** treatment BM25 reads every chunk of each nominated memory, increasing SQLite work.
  That read is a pair of index seeks rather than a tenant scan (migration 00024 plus the union in
  `memoryChunkQuery`), and the anchor path takes ids only.
- **Negative:** semantic evidence selection adds a batched embedding pass over long-memory windows and
  must earn that latency and model load in production comparison.
- **Neutral:** stored rows, embeddings and ids remain chunk-based and require no DATA migration.
  Migration 00024 adds an index and moves nothing, so rollback stays a restart rather than a
  schema reversal.

## Rollback

Set `MEMORY_EVIDENCE_SELECTOR=lexical` to roll back only semantic passage selection, then restart.
Chunk-level ranking is no longer selectable; restoring it is a revert of the 2026-08-25 retirement,
not a flag. No data has changed.
