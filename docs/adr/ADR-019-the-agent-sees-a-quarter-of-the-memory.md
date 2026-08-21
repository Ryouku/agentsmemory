# ADR-019: A hit shows its matching regions and lets the agent choose

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-013 (a page of memories, not chunks — this is the next question that page raises), ADR-007 (a number must carry its population — a flag that is almost always true is the same defect in a boolean)
**Served-path change:** A search hit gains a `regions` array — every part of the memory that matched, verbatim, with the score that ranked it — and the memory's own identity line. `content` is unchanged, so nothing that reads it today breaks. Every recall an agent makes takes this path.

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

**T1's mechanical half, measured 2026-08-21 across the nine real queries the blind judge scored as not-answered and not wing-scoped.** No judgement is involved in any of it.

| | |
|---|---|
| chosen windows starting within 130 runes of the memory's beginning | **7 of 9** |
| chosen windows TIED on score by a later window the agent never sees | **5 of 9** |
| cases where any later window scores HIGHER than the chosen one | **0 of 9** |

The third row explains the first two, and it names a mechanism rather than a tendency. **The score saturates.** A window is ranked by how many distinct query terms fall inside it, so once a window contains the terms, every other window containing them scores identically — and the chooser resolves ties by position, because it keeps a candidate only on `score > bestScore` and the earliest maximum therefore wins.

In this corpus that is not a coin toss. A memory opens with a header line — a date, the project, the subject — which is exactly where a query's terms live, so the opening ties or wins by construction and the body is never shown. The chooser is not picking the best part of the memory; it is picking the first part that mentions the subject.

That is enough on its own to say the ranking cannot discriminate at 400 runes against 1,600-rune memories: in nine real cases it never once found a window it preferred to the opening. Whether the ANSWERS are in those tied windows is the semantic half, judged separately below — a saturating score is a reason the chooser cannot help, not evidence that showing more would.

**T1's semantic half, judged blind.** A second judge, given no knowledge of what this measurement is for, read each whole memory against the exact slice the agent was shown and answered one question: is the answer inside the shown slice, elsewhere in that memory, or nowhere in it?

| where the answer is | count |
|---|---|
| **`in_rest`** — elsewhere in the memory, in the part the agent never saw | **4** |
| `in_shown` — inside the slice the agent received | 3 |
| `nowhere` — not stated anywhere in that memory | 2 |

In the acceptance command's own vocabulary: answer in the chosen window: 3. answer in a different window: 4. answer in no window: 2.

**Applying the criterion, with the denominator stated plainly because this is where a measurement gets fudged.** The pre-registration asks: *"for each hit whose snippet does not contain the answer, does a LOWER-ranked region of that same memory contain it?"* The population is therefore the six cases where the shown slice did NOT contain the answer — `in_rest` plus `nowhere`. Of those six, four have the answer elsewhere in the memory.

- **4 of 6 (67%)** by the criterion as written. A clear majority. **T2 proceeds.**
- 4 of 9 (44%) if all nine cases are counted, including the three the criterion excludes by its own wording.

Both are recorded so nobody has to take the first on trust. The three `in_shown` cases are excluded because their snippet DID contain the answer — they are not instances of the question being asked.

**Two honest caveats, neither of which changes the decision.**

The two judges applied different bars, and the disagreement is informative rather than a fault. The first asked *"could an agent act on this and stop searching?"*; the second asked *"is the answer stated in this slice?"*. A slice saying a thing was later corrected, without the correction, fails the first and passes the second. That is why three cases the first judge called partial come back `in_shown`.

And **n = 6**. The original pre-registration said of n=32 that it was "enough to rank failure modes, not to put an interval on any of them"; six is smaller again. This supports a direction, not a magnitude, and nothing downstream should be read as if it were precise.

**Taken with the mechanical half, the two independent measurements agree.** The chooser cannot discriminate — the score saturates and ties go to the opening, in 9 of 9 cases. And in two thirds of the cases where the agent was given the wrong part, the right part was there to be given. The first says the current mechanism has no way to do better; the second says there is something better to reach.

## Existing Primitives Audit

- **`snippetWindow`** (`internal/palace/rank.go`) — already scores every candidate window in a memory by how many query terms fall inside it, and already returns the best. Reshape: it computes the ranking of ALL windows and discards everything but the winner. The second-best window is the cheapest thing in this ADR.
- **`SnippetWithHead`** — already joins two pieces of a memory with `" … "` and already keeps the opening for identity. Reuse verbatim: the join is the rendering this needs, and it exists because a memory's first line says what it IS.
- **`ChunksMatched`** (ADR-013) — already counts how many chunks of a memory were in the pool, which is the ACROSS-chunk version of the signal this ADR needs WITHIN a chunk. Reuse as the precedent for what a discriminating signal looks like, and as the reason not to invent a second vocabulary for it.
- **`content_truncated` / `content_length`** — already on the wire. Reshape rather than remove: the fields are right and the boolean is uninformative, so what changes is what is said alongside them.
- **`am_get_drawer whole=true`** — already fetches a whole memory. Reuse: this ADR makes the decision to call it possible, and does not replace it.

## Decision

**A hit stops deciding for the agent which quarter of the memory it may see, and shows it the choice.**

Today the server picks one window by term density and hands over 400 characters. The agent has the
question; the server has the corpus; and the one decision that needs both — *which part of this
memory answers what I asked* — is taken by the half that cannot see the question's intent, silently,
with no way to appeal.

1. **A hit carries its matching REGIONS.** Every part of the memory that scored, verbatim, each with
   the score that ranked it and its position. `snippetWindow` already computes this ranking and
   discards everything but the winner; the discard is the change.
2. **A hit carries the memory's IDENTITY LINE** — its own first line, which by this repository's
   convention says what the memory IS (the date, the project, the subject; `SnippetHeadChars` exists
   because of it). It is the summary, and it is not written by a model.
3. **`content` is unchanged.** It stays the single best window, so every existing reader keeps
   working and nothing has to be migrated. The new fields sit beside it.
4. **Nothing on this path is generated.** Every string an agent receives is text from the memory. See
   the alternative below — this is a deliberate refusal, not an omission.

The cost is bytes, not seconds: the regions are already computed, and the caller's `snippet_chars`
governs how many are returned.

**What would make this fail, and the data exists to check it today.** The claim is that answers live
in regions the current chooser discards. It is falsifiable on the 32 real queries: for each hit whose
snippet does not contain the answer, does a LOWER-ranked region of that same memory contain it? If
the answer is usually in no region — spread across the memory, or genuinely absent — then this buys
nothing and the failure is synthesis, which this ADR does not address. **T1 measures that before T2
changes the served path.** Below a clear majority the ADR is withdrawn rather than shipped hopefully.

Valid for this corpus, whose median memory is 1,599 characters because `ChunkSize` is 1,600. A palace
of short memories has neither the problem nor the fix.

## Alternatives Considered

- **Raise `DefaultSnippetChars`.** The obvious move and rejected as the primary fix: at 95% of memories exceeding the window, any budget short of the whole memory has the same failure mode, and the budget exists because a five-hit page of whole memories is thousands of tokens. It is a dial, not a decision. It also cannot be ruled out as a COMPONENT — T1's measurement will say what coverage is actually needed.
- **Return whole memories and let the agent skim.** Rejected on the numbers: five hits at 1,600 characters is 8,000, on every recall, for the case where 400 would have done.
- **Tell the agent to fetch when truncated.** Rejected because it is already told, in a field that is true 98% of the time. More emphasis on an uninformative signal is not a signal.
- **Have the cross-encoder pick the regions.** The strongest version and genuinely close to being chosen: the reranker already scores query against passage, which is exactly this question, and it is already loaded and warm. Measured at roughly 0.4s per passage (pool 50 took 21.8s; pool 10 takes 5.1s), so scoring three regions across the top three hits is about four seconds on top of every search. Deferred rather than rejected because the cheap version has not been measured: if T1 shows term matching picks the wrong region often, this is the next thing to try and not a refinement.
- **Generate a summary of each hit against the query.** The most pleasant thing to read, offered, considered, and REFUSED — and the reason is specific to this product rather than to cost. `am_add_drawer`'s own contract reads "The verbatim text to remember — stored exactly, never summarised." A generated summary on the read path puts prose no human wrote in front of an agent that will act on it, in a system whose entire promise is that a memory comes back as it was filed. The identity line gives the same affordance — a short thing to choose by — and it is text the author wrote. If this is ever revisited, the verbatim regions must ship alongside and the generated text must be labelled as generated, so an agent never mistakes one for the other.
- **Re-chunk memories smaller so a chunk IS the answer.** Rejected here and recorded: it changes ids, invalidating every anchor, tunnel and knowledge-graph pointer, and it is a corpus migration rather than a retrieval change.

## Component / Boundary Impact

`internal/palace` owns what a snippet is; `internal/mcpserver` owns what the page says about it. Both already own exactly that. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `snippetWindow` | change — returns its ranked candidates rather than only the winner | `internal/palace/rank.go` | `Snippet`, `SearchHit` |
| `SearchHit.Regions` — verbatim text, score, position | add | `internal/palace` | the search page |
| `SearchHit.Identity` — the memory's own first line | add | `internal/palace` | the search page |
| `regions`, `identity`, `content_coverage` on the wire | add | `internal/mcpserver/drawers.go` | every agent |
| `content` | UNCHANGED — still the single best window | `internal/mcpserver/drawers.go` | every existing reader |
| `content_truncated` | unchanged — kept for compatibility, no longer the field to read | `internal/mcpserver/drawers.go` | every agent |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the window-coverage measurement | T1 | T2 | No — T1 is a measurement and may withdraw T2 |
| ranked regions from the chooser | T2 | T3 | No — `Snippet`'s return is unchanged; T2 exposes what it already discards |
| the wire fields | T3 | T3 | No — additive, and `content` keeps its meaning |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** the decision that needs the question moves to the half that has it. An agent that still cannot answer from `content` can see the other regions and their scores, so expanding becomes a choice rather than a guess — and every string it reads is text somebody wrote.
- **Negative:** the page grows. Several regions plus an identity line per hit is more bytes than one window, on every recall, and the budget that governs it has to be defended rather than assumed.
- **Neutral:** `content_truncated` becomes a compatibility field — anything reading it keeps working and learns nothing new, which is its current state.

## Out of Scope

- Synthesis — answers spread across SEVERAL memories (permanent: a different capability from showing more of ONE memory, and the same judge scored it 1 of 32 on this corpus)
- Wing scoping (deferred: docs/adr/BACKLOG.md — 5 of 32 and unchanged by anything here; the empty-wing note tells an agent what to do next and does not put the fact on the page)
- Letting a cross-encoder choose the regions (deferred: docs/adr/BACKLOG.md — the right idea and the expensive one; measure the cheap version first)
- Generating summaries of hits (permanent: `am_add_drawer` promises text is "stored exactly, never summarised", and generated prose on the read path is that promise broken at the other end. The identity line is the same affordance written by the author.)
- Re-chunking the corpus (permanent: it changes ids and invalidates every anchor, tunnel and KG pointer — a corpus migration, not a retrieval change)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The answer is usually in NO window, so more windows buy nothing | Med | High | T1 measures it on the 32 real queries before T2 is written, and the ADR withdraws rather than ships hopefully |
| The regions are delivered and no agent reads them — the same fate as `content_truncated` | **High** | High | T3 re-runs the 32 through the SAME blind judge as this round, which is the only comparison this session has that is not confounded by a change of judge. And the identity line is placed where an agent already looks rather than in a new field it must learn |
| The page grows enough to cost more context than the answers are worth | Med | Med | The caller's `snippet_chars` governs the regions too, and T2 pins that a memory matching in one place returns exactly what it returns today |
| A region is a fragment too small to mean anything | Med | Med | A floor on region size; below it, one larger region |

## Rollback

Both changes are additive on the wire and internal to snippet selection. Reverting restores the single-window snippet exactly; nothing is stored, migrated or re-shaped, and `content_truncated` never changes meaning.

## Follow-ups
