# ADR-019: A page must show the answer, not a quarter of the memory and a flag

**Status:** Proposed
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-013 (a page of memories, not chunks — this is the next question that page raises), ADR-007 (a number must carry its population — a flag that is almost always true is the same defect in a boolean)
**Served-path change:** `Service.Search` returns a different snippet for a long memory: the windows that matched, rather than one window and a flag saying there are others. Every recall an agent makes takes this path.

## Context

Measured 2026-08-21 against the live palace, replaying the same 32 real queries the first measurement used.

**The shape of the corpus makes this structural, not occasional.**

| | |
|---|---|
| memories on the palace | 394 |
| median memory length | **1,599 characters** |
| the snippet window | **400 characters** |
| memories longer than the window | 375 of 394 (**95%**) |
| memories RETURNED on the 32 pages that are longer than the window | 145 of 148 (**98%**) |

So an agent sees roughly a quarter of a typical memory, and the quarter is chosen by where the query's terms fall. The failure that the first measurement named the single largest — *the right drawer at rank 1 and the answer not in the text* — is what happens when the answer is in one of the other three quarters.

**The recovery path exists and cannot be used.** The page already tells the agent everything it would need: `content_truncated: true`, `content_length: 1600`, and `am_get_drawer` fetches the whole memory. Nothing is missing.

**But `content_truncated` is true for 98% of hits, and a flag that is almost always true carries no information.** An agent cannot act on it: fetching every truncated hit on a five-hit page is 8,000 characters, which is precisely the cost snippets exist to avoid. It has no way to tell WHICH hit is hiding the answer, so the rational move is to read the four hundred characters and conclude — which is what agents do.

This is the same defect this repository keeps finding, in a boolean: a field whose value is technically correct and whose distribution makes it useless. ADR-007 says a printed number carries the population it was computed over; a flag that never varies has no population to carry.

**Two mechanisms were being counted as one.** A blind judge scored the 32 queries and marked four failures `snippet-cut`. Re-running those four after a word-boundary fix showed two were genuine truncation — `…the PAIRED b…` became `…the PAIRED bootstrap…` — and two were unchanged, because they were never cut at the edge at all. The judge's note on one: *"its opening line says the observation was corrected and the window ends before the correction."* The window was in the wrong PLACE. Fixing edges does not touch that, and calling both `snippet-cut` hid the fact that only half the category had been addressed.

**What this ADR does not claim.** The same run scored 18 answers where the first scored 12, and that is NOT evidence of improvement: different judges, and the original pages were not saved, so the two numbers cannot be compared. The corpus also grew by 26 drawers between the runs. Only the before/after on identical inputs is attributable, and that is what is quoted above.

## Existing Primitives Audit

- **`snippetWindow`** (`internal/palace/rank.go`) — already scores every candidate window in a memory by how many query terms fall inside it, and already returns the best. Reshape: it computes the ranking of ALL windows and discards everything but the winner. The second-best window is the cheapest thing in this ADR.
- **`SnippetWithHead`** — already joins two pieces of a memory with `" … "` and already keeps the opening for identity. Reuse verbatim: the join is the rendering this needs, and it exists because a memory's first line says what it IS.
- **`ChunksMatched`** (ADR-013) — already counts how many chunks of a memory were in the pool, which is the ACROSS-chunk version of the signal this ADR needs WITHIN a chunk. Reuse as the precedent for what a discriminating signal looks like, and as the reason not to invent a second vocabulary for it.
- **`content_truncated` / `content_length`** — already on the wire. Reshape rather than remove: the fields are right and the boolean is uninformative, so what changes is what is said alongside them.
- **`am_get_drawer whole=true`** — already fetches a whole memory. Reuse: this ADR makes the decision to call it possible, and does not replace it.

## Decision

**A snippet shows every part of the memory that matched, within the caller's budget, and says what it left out.**

1. **Multiple windows, one budget.** When a memory matches in several places, the snippet is the best windows joined by the existing `" … "` — not the single best window. The caller's `snippet_chars` is unchanged and remains the ceiling: this spends the same characters on more of the answer, rather than spending more characters.
2. **A signal that discriminates.** The page reports how much of the memory the snippet covers and how many matching regions were left out. `content_truncated` stays for compatibility and stops being the thing an agent reads.

**What would make this fail, and the data exists to check it today.** The claim is that answers live in windows the current chooser discards. It is falsifiable on the 32 queries: for each hit whose snippet does not contain the answer, does a LOWER-ranked window of that same memory contain it? If the answer is usually in no window — because it is spread across the memory, or genuinely absent — then this ADR buys nothing and the failure is synthesis, which it does not address. **T1 measures that before T2 changes the served path.** Below a clear majority the ADR is withdrawn rather than shipped hopefully.

Valid for this corpus, whose median memory is 1,599 characters because `ChunkSize` is 1,600. A palace of short memories has neither the problem nor the fix.

## Alternatives Considered

- **Raise `DefaultSnippetChars`.** The obvious move and rejected as the primary fix: at 95% of memories exceeding the window, any budget short of the whole memory has the same failure mode, and the budget exists because a five-hit page of whole memories is thousands of tokens. It is a dial, not a decision. It also cannot be ruled out as a COMPONENT — T1's measurement will say what coverage is actually needed.
- **Return whole memories and let the agent skim.** Rejected on the numbers: five hits at 1,600 characters is 8,000, on every recall, for the case where 400 would have done.
- **Tell the agent to fetch when truncated.** Rejected because it is already told, in a field that is true 98% of the time. More emphasis on an uninformative signal is not a signal.
- **Make the reranker pick the window.** Attractive — a cross-encoder scores query against passage and that is exactly this question — and deferred rather than rejected: it costs an inference per candidate window, the pool is already the slowest step, and the cheap version has not been measured yet. If T1 shows term-matching picks the wrong window often, this is the next thing to try.
- **Re-chunk memories smaller so a chunk IS the answer.** Rejected here and recorded: it changes ids, invalidating every anchor, tunnel and knowledge-graph pointer, and it is a corpus migration rather than a retrieval change.

## Component / Boundary Impact

`internal/palace` owns what a snippet is; `internal/mcpserver` owns what the page says about it. Both already own exactly that. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `Snippet` / `SnippetWithHead` | change — may return several joined windows | `internal/palace/rank.go` | `internal/mcpserver/drawers.go` |
| `SearchHit.Coverage` (fraction of the memory shown) and `RegionsOmitted` | add | `internal/palace` | the search page |
| `content_coverage`, `regions_omitted` on the wire | add | `internal/mcpserver/drawers.go` | every agent |
| `content_truncated` | unchanged — kept for compatibility, no longer the field to read | `internal/mcpserver/drawers.go` | every agent |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the window-coverage measurement | T1 | T2 | No — T1 is a measurement and may withdraw T2 |
| multi-window snippets | T2 | T3 | No — the rendering is the existing `" … "` join |
| the coverage signal | T3 | T3 | No — additive fields |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** the largest measured failure mode is addressed at its mechanism rather than at its edges. An agent that still cannot answer gets a signal that discriminates, so fetching becomes a decision rather than a guess.
- **Negative:** a snippet of several windows is choppier to read than one continuous passage, and a human reading the dashboard will notice. The join marker is what makes it honest.
- **Neutral:** `content_truncated` becomes a compatibility field. Anything reading it keeps working and learns nothing new, which is its current state.

## Out of Scope

- Synthesis — answers spread across SEVERAL memories (permanent: a different capability from showing more of ONE memory, and the same judge scored it 1 of 32 on this corpus)
- Wing scoping (deferred: docs/adr/BACKLOG.md — 5 of 32 and unchanged by anything here; the empty-wing note tells an agent what to do next and does not put the fact on the page)
- Letting a cross-encoder choose the window (deferred: docs/adr/BACKLOG.md — the right idea and the expensive one; measure the cheap version first)
- Re-chunking the corpus (permanent: it changes ids and invalidates every anchor, tunnel and KG pointer — a corpus migration, not a retrieval change)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The answer is usually in NO window, so more windows buy nothing | Med | High | T1 measures it on the 32 real queries before T2 is written, and the ADR withdraws rather than ships hopefully |
| Several short windows read worse than one coherent passage, and an agent does worse with the same information | Med | Med | T3 re-runs the 32 through the SAME blind judge as this round, which is the only comparison this session has that is not confounded by a change of judge |
| The budget is spent on many tiny fragments | Med | Med | A floor on window size, and T2 pins that a memory matching in one place still returns one window |

## Rollback

Both changes are additive on the wire and internal to snippet selection. Reverting restores the single-window snippet exactly; nothing is stored, migrated or re-shaped, and `content_truncated` never changes meaning.

## Follow-ups
