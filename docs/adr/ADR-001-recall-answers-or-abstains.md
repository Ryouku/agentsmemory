# ADR-001: Recall answers or abstains

**Status:** Proposed
**Date:** 2026-08-19
**Owner:** Zy (with Mindaugas as upstream maintainer)
**Spec:** None — no spec stage; the decision rests on measurements recorded in the eval harness rather than on elicited requirements.
**Cross-references:** `internal/palace/eval.go` (harness), `db/migrations/00021_search_events.sql` (telemetry), PR #20 comment recording the separation measurement

## Context

Recall currently returns a ranked page and says nothing about whether the page contains an answer. The agent consuming it cannot tell "here is the memory you asked for" from "here are the five least-unrelated things in the palace", so a confident-looking page of irrelevant memories is indistinguishable from a good one — and an agent that cannot tell will act on both.

`max_distance` looks like the knob for this and is not. Measured on 61 cases (40 answerable, 21 whose absence was verified against the whole corpus rather than against the note they were generated from — 4 of 25 candidates were rejected during generation because another memory answered them):

| signal | answerable | unanswerable |
|---|---|---|
| top-1 cosine distance | median 0.401 (0.251–0.496) | median 0.423 (0.364–0.519) |
| top-1 cross-encoder score | median 0.891 (−6.569–5.572) | median −3.832 (−6.327–1.500) |

The distance distributions overlap almost completely and their medians are 0.022 apart: **no threshold on cosine distance separates them at any value.** The cross-encoder's medians are ~4.7 apart, which is real signal, though the ranges still overlap at the tails.

That cross-encoder gap is an **upper bound, not the operating reality**. `evalPromptAbsent` instructs the generator "do not reuse the note's distinctive identifiers", so our unanswerable queries are systematically stripped of the lexical overlap that makes a real negative hard. A threshold fitted to these will over-answer on the negatives production actually sees — a near-miss from a neighbouring wing that shares identifiers. Hard negatives are therefore a precondition of calibration (T1), not an improvement to it.

The deeper reason this is worth building rather than left to the caller: a ranked page is a **human** interface. It exists because search engines return links for people to skim and discard. Our consumer is a model with a context budget and no ability to glance — handing it five results and no judgement makes it spend tokens re-deriving, per query, something the palace already computed and threw away.

Two properties of that score constrain any design. It is only present when a reranker is configured and actually scored the hit — which is why `SearchHit.Reranked` exists, since zero is an ordinary logit and cannot serve as a sentinel. And its **scale is backend-dependent**: `internal/rerank/tei/tei.go` sets `raw_scores: false` so TEI returns sigmoid-squashed values in (0,1), while llama.cpp's server returns bare logits — which is why the measured absent median is negative. A single hardcoded threshold would be wrong on one of the two backends we ship.

## Existing Primitives Audit

- **`palace.Service.Search`** — already computes and returns the cross-encoder score per hit (`SearchHit.RerankScore`) and, since the presence fix, `SearchHit.Reranked`. Reused as-is; the verdict is derived here, not recomputed elsewhere.
- **`palace.EvalReport.GoldRerank` / `AbsentRerank`** — already collect the two score distributions from the production arm. Reshaped: the calibration command reads them instead of a human reading two ranges off a printed line.
- **`search_events` table + `recordSearch`** — already records one row per recall with `top_score` and `reranked`. Extended with the verdict so the operating point can be audited against production traffic later, rather than only against the eval.
- **`config.Config` + `cmd/server/main.go` flag wiring** — the established pattern for an operator-set, env-overridable knob (`BM25_WEIGHT`, `CLOSET_BOOST`, `FUSION`). Reused; no new configuration mechanism.
- **`am_search` MCP tool** — the surface an agent already calls. Extended with one field; no new tool.

## Decision

`Search` gains a **confidence verdict** derived from the top hit's cross-encoder score compared against an operator-calibrated threshold, and `am_search` returns it alongside the hits.

The verdict has four values and the fourth is the point of the design: `answered` (top score at or above the threshold), `no_answer` (below it), `uncertain` (within a declared band around it), and **`unknown` — returned whenever no threshold is configured or no reranker scored the hit.** There is no default threshold. A number that is right for `bge-reranker-v2-m3` on TEI is wrong for the same model on llama.cpp, and inventing one would reproduce exactly the `max_distance` mistake this ADR exists to correct: a plausible constant, inherited, never measured, quietly deciding what the palace admits to knowing.

The threshold is produced by a new `eval --calibrate` mode rather than chosen. Given a merged answerable-plus-verified-absent case file it emits the risk–coverage curve — for each candidate threshold, the share of answerable queries still answered against the share of unanswerable ones correctly refused — and recommends the threshold meeting a declared answer-recall target (default 0.95). The operator copies that number into configuration together with the backend identity it was calibrated against; a threshold calibrated on one backend and served on another is refused at startup rather than silently applied.

Calibration scores **three** populations, not two. A case whose gold never entered the retrieved pool (`PoolRanks == 0`) is answerable-but-unreachable: abstaining on it is correct behaviour for a retrieval failure the gate cannot see, and counting it as a false abstention would tune the threshold toward over-answering. So the headline metric is precision at a declared recall on the *reachable*-answerable class, with the unreachable count reported beside it as the ceiling.

Because the distributions overlap at the tails and the calibration set is small — 21 verified-absent cases means a 10% target is really 10% give or take about the same again — the output is an operating point with a stated error rate and an explicit sample count. No distribution-free guarantee is claimed or implied; the numbers are empirical quantiles over a set we generated, and the ADR says so where an operator will read it.

One float per backend is deliberately the whole mechanism. Anything richer — a learned classifier, a multi-feature score — has to beat this baseline explicitly on the same curve before it earns the complexity, and nobody has measured the baseline yet.

## Alternatives Considered

- **Threshold on cosine distance (`max_distance`):** the knob that already exists. Rejected on measurement: the two distributions overlap with medians 0.022 apart, so every threshold either refuses answerable queries or admits unanswerable ones at a rate no operating point makes acceptable.
- **A hardcoded cross-encoder threshold with a sensible default:** simplest to ship. Rejected because the score scale differs by backend (sigmoid on TEI, logits on llama.cpp) and by model version, so a default is wrong for at least one shipped configuration while looking authoritative — the `max_distance` failure mode repeated.
- **Return the raw score and let the agent decide:** minimal, honest, and already effectively the case (`RerankScore` is in the response). Rejected because it pushes calibration onto every consumer, none of which has the absent-case data needed to do it; each agent would invent its own constant, and the palace would still not know whether it is being believed.
- **A learned abstention classifier over several features** (score, margin, distance, coverage): plausibly better than one threshold. Rejected for now as unmeasurable — we have 21 verified-absent cases, far too few to fit and hold out. Recorded as deferred, not dismissed.
- **Semantic-entropy / hidden-state probes on the consuming model:** the strongest published signal for "the model does not know". Rejected as inapplicable: we do not own the consuming model's forward pass; agents call us over MCP and receive text.

## Component / Boundary Impact

| Component | Change | One reason to change? |
|---|---|---|
| `internal/palace` | derives the verdict inside `Search`; owns the threshold comparison | yes — it already owns ranking and the score |
| `internal/mcpserver` | serialises one new field on `am_search` | yes — it owns the wire surface |
| `internal/config` + `cmd/server` | one new knob and its startup validation | yes — the composition root |
| `cmd/server/eval.go` | new `--calibrate` mode reading the existing distributions | yes — it owns measurement |
| `db/migrations` | one column on `search_events` | yes — it owns persistence |

No new component. No module moves. This repo has no `docs/architecture.md`; one should be written (`/arch-write`) before an ADR that *moves* boundaries — this one does not.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `palace.SearchHit` / new `palace.SearchResult.Confidence` | add verdict enum (`answered`/`uncertain`/`no_answer`/`unknown`) | `palace.Service.Search` | `mcpserver`, eval |
| `am_search` MCP result | add `confidence` object (`verdict`, `top_score`, `threshold`, `backend`) | `mcpserver` | every agent |
| `ABSTAIN_THRESHOLD` (env / `--abstain-threshold`) | new config key; unset = verdict `unknown` | operator | `palace.Service` |
| `ABSTAIN_BACKEND` (env / `--abstain-backend`) | new config key naming the backend the threshold was calibrated on; mismatch with `RERANK_URL`'s dialect refuses startup | operator | `cmd/server` |
| `search_events.verdict` | new nullable TEXT column | `recordSearch` | recall stats, future production calibration |
| `agentsmemory eval --calibrate` | new CLI mode | operator | operator |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `palace.Confidence` type + `Verdict` constants | T1 | T2, T3, T4 | No — new type |
| `config.AbstainThreshold` / `AbstainBackend` | T2 | T3 | No — new keys, unset is valid |
| `Search` returning a populated `Confidence` | T3 | T4, T5 | No — additive field |
| `search_events.verdict` column | T4 | T5 | No — nullable, additive |
| `eval --calibrate` risk–coverage output | T5 | operator (not a task) | No — new mode |

## Implementation

Five tasks — see [`ADR-001-recall-answers-or-abstains/tasks/README.md`](ADR-001-recall-answers-or-abstains/tasks/README.md).

T1 leads deliberately: it is the task that can invalidate the ADR. If identifier-preserving negatives collapse the measured separation, this gate does not ship on this corpus, and the remaining four tasks are never started.

## Consequences

- **Positive:** an agent can distinguish "the palace holds this" from "the palace holds nothing like this", which is the difference between recall being usable unsupervised and needing a human to sanity-check it. The `unknown` verdict makes the absence of calibration visible instead of implying confidence nobody measured.
- **Positive:** the calibration procedure is executable and repeatable, so a model or backend change is re-calibrated rather than silently invalidated — unlike `max_distance`, which nobody could re-derive.
- **Negative:** operators who configure a reranker but never calibrate get `unknown` forever and see no benefit until they run one command. This is deliberate — the alternative is a guessed constant — but it is real friction and must be documented at the knob.
- **Negative:** one more nullable column on the hottest write path in the telemetry table.
- **Neutral:** ranking is untouched. Every arm in the eval scores exactly as before; this ADR adds a judgement about the top result, not a change to which result is on top.

## Out of Scope

- Contradiction reporting — "this changed on <date>, it was X and is now Y" (deferred: docs/adr/BACKLOG.md — blocked on a populated temporal knowledge graph; ~65 triples against ~5,020 drawers)
- The write-time findability gate — testing at file time whether a new memory can be retrieved by the question it answers (deferred: docs/adr/BACKLOG.md — drafted after this ships, since it reuses the same calibration)
- Continuous evaluation with automatic promotion of the winning retrieval configuration (deferred: docs/adr/BACKLOG.md — depends on real-query telemetry volume, currently ~10 rows)
- A learned multi-feature abstention classifier (deferred: docs/adr/BACKLOG.md — revisit above ~200 verified-absent cases; 21 cannot fit and hold out)
- Changing any ranking arm, fusion weight or the reranker blend (permanent: this ADR judges the top result; what reaches the top is decided elsewhere and measured by the existing arms.)
- Abstention for the consuming model's own generation, e.g. semantic entropy (permanent: we do not own that forward pass; agents receive text over MCP.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Threshold calibrated on 21 absent cases does not generalise | High | Med | Report the risk–coverage curve, the case count and the resulting uncertainty with the recommendation; `uncertain` band absorbs the tails; re-calibration is one command as the corpus grows |
| Negatives remain artificially easy, so the threshold over-answers in production | High | High | T1 makes hard negatives (identifier-preserving, cross-wing near-miss) a precondition; calibration on the old easy set is refused by the calibrate command |
| Unreachable-answerable cases scored as false abstentions, tuning toward over-answering | Med | Med | Three-population scoring (T1); the unreachable count is reported separately as the retrieval ceiling |
| Operator serves a threshold calibrated on the other backend | Med | High | `ABSTAIN_BACKEND` must match the configured reranker dialect or the server refuses to start |
| Agents ignore the verdict and read hits regardless | High | Low | Additive field; no behaviour is removed, so ignoring it leaves today's behaviour intact |
| `no_answer` suppresses a correct answer the agent needed | Med | High | The verdict never filters hits — it annotates them; the page is returned unchanged |
| Overlapping tails make any operating point wrong for some queries | Certain | Med | Stated explicitly in the response (`top_score` and `threshold` are returned) so a consumer can apply its own bar |

## Rollback

Unset `ABSTAIN_THRESHOLD` — every verdict becomes `unknown` and the response is behaviourally identical to today, with one extra ignorable field. The `search_events.verdict` column is nullable and additive; the down migration in T4 drops it, and no read path requires it. No data is rewritten at any point, so rollback loses only the verdicts recorded while it was on.

## Follow-ups

- [ ] Re-calibrate once the verified-absent corpus exceeds 200 cases and report whether the recommended threshold moved.
