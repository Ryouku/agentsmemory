# ADR-031: Keep the one score that separates a recall that worked from one that did not

**Status:** Accepted
**Date:** 2026-08-25
**Owner:** Zy
**Spec:** None — no spec stage; grounded in ADR-001's measured separation table, a live page served by the deployed container on 2026-08-25, and arithmetic over `rankRRF`.
**Cross-references:** `internal/palace/rank.go` (`rankRRF`, `rrfK`), `internal/palace/service.go` (`hit.Score = r.Fused`, the record stage), `internal/palace/recallstats.go` (`RecallStats`, `WingRecall`), `internal/mcpserver/admin.go` (`am_recall_stats`), `db/migrations/00021_search_events.sql`, `docs/adr/ADR-001-recall-answers-or-abstains.md` (the separation measurement this ADR acts on), `docs/adr/ADR-030-a-blend-that-cannot-tell-confidence-from-noise.md` (the change that stopped flattening the same signal in the blend)
**Invalidates:** nothing. ADR-001's T3 verdict stays BLOCKED for the reason it gives — a saturated corpus — and this ADR does not unblock it. It removes a *second*, independent obstacle that would have defeated the gate even on a good corpus.
**Served-path change:** none to ranking. `search_events` gains `top_rerank_score`, and `am_recall_stats` reports `avg_top_rerank_score` with its own `reranked` count beside the existing `avg_top_score`.

## Context

ADR-001 measured, over 61 cases, which signal tells an answerable recall from an unanswerable one:

| signal | answerable | unanswerable |
|---|---|---|
| top-1 cosine distance | median 0.401 (0.251–0.496) | median 0.423 (0.364–0.519) |
| top-1 cross-encoder score | median 0.891 (−6.569–5.572) | median −3.832 (−6.327–1.500) |

Its own words: *"The distance distributions overlap almost completely and their medians are 0.022 apart: **no threshold on cosine distance separates them at any value.** The cross-encoder's medians are ~4.7 apart, which is real signal."*

**The durable row keeps the wrong one.** `service.go` writes `ev.TopScore = results[0].Score`, and `hit.Score = r.Fused`. Under the shipped `FUSION=rrf`, `rankRRF` sorts each retrieval arm and accumulates `1/(rrfK + rank+1)` with `rrfK = 60` — **magnitude is discarded at retrieval, on both arms.** A candidate's fused score encodes its rank position and nothing about how well it matched.

The consequence is arithmetic. A top-1 hit can only land between `1/(61) + 1/(90) = 0.0275` and `1/(61) + 1/(61) = 0.0328`. The page served on 2026-08-25 (`search_id=67a15fa4242b33ec190e8c7c`) carried `0.03252`, `0.03227`, `0.03200` — every candidate inside 1.6%. **`top_score` for a perfect answer and for garbage differ by at most 16%, and that 16% encodes rank position.**

**It has a live reader.** `RecallStats` sums it (`SUM(CASE WHEN hits > 0 THEN top_score ...)`) into `AvgTop`, which `am_recall_stats` reports as `avg_top_score`. That number is on the operator's report today and it is an average of a near-constant: a wing whose recall had collapsed would report almost exactly what a healthy one reports.

**And the signal that does separate was being flattened too, until this afternoon.** ADR-030 replaced min-max normalisation in the blend, which mapped the cross-encoder's logits onto a pool-relative range and destroyed exactly the magnitude ADR-001 measured. So as of `96b505f` the served ordering preserves it; this ADR keeps it.

## Existing Primitives Audit

| Primitive | Where | Reused? |
|---|---|---|
| `search_events` row + `reranked` flag | `db/migrations/00021_search_events.sql` | Yes. `reranked` already distinguishes "no cross-encoder ran" from "ran and scored 0" — necessary, because these are logits and 0 is mid-range, not "no match". |
| `SearchHit.RerankScore` | `internal/palace/palace.go` | Yes — already populated, already on the wire since ADR-028. Only the durable write was missing. |
| `RecallStats` aggregation | `internal/palace/recallstats.go` | Extended, not replaced. |
| `am_recall_stats` | `internal/mcpserver/admin.go` | Extended. The tool is the reader that makes this rung-3 reachable. |
| ADR-001's separation table | `docs/adr/ADR-001-...` | Binding. This ADR ships nothing new about *what* separates; it keeps the number ADR-001 already identified. |

## Decision

**Add `top_rerank_score`; do not redefine `top_score`.**

Redefining the column would make every row written before the change silently incomparable with every row after it, and nothing in the report would say so. `avg_top_score` has a live reader and a history. A new column leaves old rows honestly absent rather than quietly wrong — the same cutover-not-backfill reasoning ADR-029 reached for the wing attribution.

**Report it with its own denominator.** `avg_top_rerank_score` is averaged over answered searches *that a cross-encoder actually ordered*, and the `reranked` count is reported beside it. Folding in rows where no cross-encoder ran would divide real logits by a denominator padded with zeros that mean "not measured" — and because a logit of 0 sits mid-range, those zeros would drag a healthy wing downward while looking like evidence. This project has already retracted one statistic computed over a population that did not mean what the number claimed.

**What this does not do.** It does not add an abstention threshold. ADR-001's T3 gate stays BLOCKED on its own preflight — a corpus measuring 100% in-pool is saturated, and the go/no-go cannot be taken there in either direction. This ADR removes a *different* obstacle: even on a clean corpus, a threshold calibrated on `top_score` under `rrf` would have been calibrated on a constant. Both had to be true for T3 to be answerable; only one was known.

## Alternatives Considered

- **Redefine `top_score` to hold the blended or rerank score:** rejected. One column, two meanings, split by a date nobody records — and `avg_top_score` is already being read.
- **Store the blended score rather than the raw rerank score:** rejected. `Blended` is pool-relative by construction (ADR-028 says so on the tool description), so it is not comparable across pages and an average of it means nothing. The raw logit is the absolute quantity ADR-001 measured.
- **Report a single "quality" number and hide which signal it came from:** rejected. The two are on different scales and one of them is a rank encoding; collapsing them would produce a number no one could reason about, which is how `avg_top_score` became misleading in the first place.
- **Change `FUSION` away from `rrf` so the fused score carries magnitude again:** rejected here, and it is a real option for another record. It changes the served ordering, and the eval run of 2026-08-25 does not support a change of that size at n=30.
- **Drop `avg_top_score` from the report:** rejected for now. It is not wrong for a `FUSION=linear` deployment, and removing a field an operator may be watching deserves its own notice. Its doc comment now says what it is worth under `rrf`.

## Component / Boundary Impact

No boundary moves. One additive column, one additional aggregate in an existing query, two additional fields on an existing response. No new service, flag, or environment variable.

## Wiring & Contract Changes

- `db/migrations/00026_search_events_top_rerank_score.sql` — additive column, `NOT NULL DEFAULT 0`.
- `searchEventRow.TopRerankScore`, written at the record stage from `results[0].RerankScore`.
- `WingRecall.AvgTopRerank` and `WingRecall.Reranked`.
- `am_recall_stats` gains `avg_top_rerank_score` and `reranked` per wing. Additive; absent fields were previously absent.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `top_rerank_score` column and its aggregate | T1 | none in this ADR — a future abstention threshold consumes it | No — additive column and additive response fields |

## Implementation

One task. Its acceptance asserts both the value and the denominator, and carries a killed mutant: widening the `reranked = 1` filter so unreranked rows dilute the average must turn the fence red.

## Consequences

An operator can see whether recall is working. `avg_top_score` could not tell them: under `rrf` it is an average of a near-constant. `avg_top_rerank_score` is on the scale ADR-001 measured a 4.7-median separation on.

A future abstention threshold has something to calibrate against. That is the point of the column, and it is deliberately not spent in this ADR.

**A cutover, not a backfill.** Rows written before this migration carry 0 with `reranked = 0`, which the aggregate excludes rather than counts. Any report spanning the boundary covers fewer searches on the new metric than on the old one, and the `reranked` count is what makes that visible instead of implicit.

## Out of Scope

- **An abstention threshold or a calibrated refusal** (deferred: `docs/adr/BACKLOG.md` §"From ADR-031" — ADR-001 T3 stays blocked on corpus saturation, which this ADR does not address)
- **Backfilling `top_rerank_score` for historical rows** (permanent: the cross-encoder score was never computed for those searches, so there is nothing to recover; inventing one would be fabrication)
- **Changing `FUSION` away from `rrf` so the fused score carries magnitude** (deferred: `docs/adr/BACKLOG.md` §"From ADR-031" — a served-ordering change the 2026-08-25 eval cannot support at n=30)
- **Removing `avg_top_score` from the report** (deferred: `docs/adr/BACKLOG.md` §"From ADR-031" — it remains meaningful under `FUSION=linear`, and dropping a watched field needs its own notice)
- **Any change to ranking or the retrieval unit** (permanent: ADR-024 owns the ranking unit; ADR-030 owns the blend; this ADR only records what already happened)

## Risks

- **A new metric invites over-reading.** `avg_top_rerank_score` is a mean of logits over one wing's window, not a quality score with a natural zero. The `reranked` count is reported beside it precisely so an average over three searches is not read as a property of the wing.
- **Two score columns is a comprehension cost.** Mitigated by doc comments on both fields saying what each is worth, and by `AvgTop`'s comment now naming its own limitation rather than leaving a reader to discover it.
- **The column is only as good as the reranker being configured.** With no cross-encoder, `reranked` is 0 everywhere and the metric is honestly empty rather than misleadingly zero — which is the behaviour the separate denominator buys.

## Rollback

Revert the migration (`goose down` drops the column), the two struct fields, the aggregate, and the two response fields. Nothing else reads them, no ordering changes, and `avg_top_score` is untouched throughout.

## Follow-ups

- Re-examine every published number derived from `search_events`. `avg_top_score` is the second metric this month found to be computed over something that did not mean what it claimed.
- Once enough reranked rows exist, plot the `top_rerank_score` distribution for answered versus unanswered recalls on the real corpus and compare it with ADR-001's table.
- **Ask of every stored metric: what is its dynamic range in the shipped configuration?** `top_score`'s was 16%, and nothing said so.
