# ADR-030: A blend that cannot tell confidence from noise

**Status:** Proposed
**Date:** 2026-08-25
**Owner:** Zy
**Spec:** None — no spec stage; grounded in a live page served by the deployed container on 2026-08-25, an isolated arithmetic probe over `BlendRerank`, and 648 reranked recalls in the deployed `search_events` table.
**Cross-references:** `internal/palace/service.go` (`BlendRerank`, `normalizeScores`), `internal/config/config.go:352` (`RerankWeight: 0.5`, "chosen by the eval's weight sweep"), `docs/adr/ADR-024-rank-memories-not-chunks.md` (the blend and the ranking unit), `docs/adr/ADR-014-the-shipped-default-is-the-measured-one.md` (a default is measured, never chosen), `docs/adr/ADR-028-a-recall-you-can-judge.md` (`blended_score` on the wire — the field that made this visible), `docs/adr/ADR-001-recall-answers-or-abstains.md` (the top-1 distance distributions)
**Invalidates:** nothing outright, but it **reopens ADR-024's weight**. ADR-024 established the blend and is not contradicted; what is contradicted is the assumption that a weight measured on large pools transfers to the pools production actually serves.
**Served-path change:** none yet. This ADR ships an eval arm and a fixture that can exhibit the defect; the ranking change is T2 and only after T1 measures which alternative wins.

## Context

`BlendRerank` combines two signals by min-max normalising each over the candidate pool and taking a weighted sum. `normalizeScores` maps the pool minimum to 0 and the maximum to 1, guarding only the case where they are exactly equal. Two consequences follow, both measured rather than reasoned.

**1. The blend is scale-free, so it cannot tell a confident cross-encoder from an indifferent one.** Measured against three real logit vectors:

```
spread=0.001  ->  norm = [1, 0, 0.5]
spread=0.232  ->  norm = [1, 0.289, 0, 0.004, 0.099]
spread=0.302  ->  norm = [1, 0.702, 0.272, 0.106, 0]
```

The first is three candidates the cross-encoder considers essentially identical; min-max stretches a one-thousandth difference across the entire range, and the weight then applies to that amplified noise exactly as it would to a decisive verdict. The middle vector is real: logits measured on this stack on 2026-08-25 without query context sat within 0.232 of each other and the ordering they implied was, by the model's own scale, close to arbitrary. The `span == 0` guard catches only exact equality, which is the one case that never occurs with floating-point logits.

**2. At the shipped weight of exactly 0.5, a small pool can cancel the cross-encoder entirely.** On a two-candidate pool both axes are forced to `{0, 1}`, so when the fused-best is the rerank-worst the two blends are `0.5·0 + 0.5·1` and `0.5·1 + 0.5·0` — identical. Probed with the most extreme disagreement expressible:

```
weight=0.50  ->  first=index0  blended=[0.5000 0.5000]   tie; fused order kept
weight=0.60  ->  first=index1  blended=[0.6000 0.4000]   flips
weight=0.90  ->  first=index1  blended=[0.9000 0.1000]   flips
```

The cross-encoder was handed −5.0 against +5.0 and its verdict was discarded. `sort.SliceStable` then preserves the fused order, which is deliberate and documented — so this is not randomness, it is the reranker being structurally cancelled. **0.5 is the single weight at which this happens**; 0.6 and 0.9 both behave.

**This is not hypothetical, and it was found by serving a real query.** A recall run against the deployed container on 2026-08-25 (`search_id=67a15fa4242b33ec190e8c7c`) returned a page whose top two hits carried `blended_score` 0.5000 and 0.5000, with the third — the closest by cosine distance, 0.402 — placed last. The trace shows `am.search.rerank ran weight=0.5 pool=3`. The arithmetic reproduces to four decimals in isolation.

**Blast radius, from the deployed table.** Of 648 reranked recalls in `search_events`, 45 (6.9%) returned zero or one hit, where no ordering exists at all, and 114 (17.6%) returned two to four — the pool sizes where min-max is most degenerate. Roughly a quarter of recalls that recorded `reranked = 1` ran on a pool where the cross-encoder either could not reorder anything or was at material risk of being cancelled. How often the tie actually fired cannot be recovered: `blended_score` reaches the wire (ADR-028 T2) but is not persisted, so the durable row cannot answer it.

**Why the eval did not catch this, which is the part worth generalising.** `RerankWeight: 0.5` is annotated in `config.go:352` as "chosen by the eval's weight sweep", and ADR-024's runs used reranker pools of 128 and 10. On a pool of 128 the fused-best being exactly the rerank-worst is rare, and a 0.001 logit spread across 128 candidates is not what a benchmark corpus produces. **The eval fixture cannot exhibit the defect** — this repository's own named failure mode, applied to its own tuning run. The weight is not wrong because 0.5 is a bad number; it is unmeasured for the pools production actually serves.

**What the instrument said while this was happening.** `am_status` reports `rerank=on(pool=10,weight=0.50)`. The span reports `am.search.rerank ran weight=0.5 pool=3`. The durable row records `reranked = 1`. Every one of those is true, and the conclusion they invite — that the cross-encoder shaped this page — is false. That is the same class of self-deception this repository keeps naming, arriving through arithmetic rather than through wiring.

## Existing Primitives Audit

| Primitive | Where | Reused? |
|---|---|---|
| The eval harness and its arm registry | `internal/palace/eval.go` | Yes. This ADR adds arms, and `TestEveryDeclaredArmIsRegistered` already fails for an arm nothing registers. |
| `blended_score` on the wire | `internal/mcpserver/drawers.go` (ADR-028 T2) | Yes — it is what made the tie visible on a served page rather than only in source. |
| `normalizeScores` | `internal/palace/service.go` | Replaced per-axis, not deleted: the fused axis is already a bounded `[0,1]` RRF score and does not need rescaling the way a raw logit does. |
| `search_events.top_score` | `internal/palace/recallstats.go` | Read-only here. Persisting `blended_score` is receipted, not taken. |
| ADR-014's rule that a default is measured | `docs/adr/ADR-014-...` | Binding. This ADR deliberately does NOT pick a replacement; T1 measures, T2 ships the winner. |

## Decision

**Measure first, then ship — two tasks, in that order, because picking a replacement blind is exactly how 0.5 got here.**

**T1 — a fixture that can exhibit the defect, and arms that can distinguish the candidates.** The eval gains small-pool cases (2, 3 and 4 candidates) and a low-spread case, because the current corpus provably cannot produce either. Three arms are registered alongside the served blend: **sigmoid on the raw logit** (bounded, monotone, and preserves the cross-encoder's absolute confidence, which is the property min-max destroys), **rank-based fusion** on the rerank axis (scale-free by construction, so indifference degrades to the fused order rather than to amplified noise), and **the served blend at a weight other than 0.5** (the cheapest possible fix, included so the expensive ones have to beat it).

**T2 — ship whichever arm wins, and make the degenerate case unable to return.** The winning normalisation becomes the default, and a test pins the property rather than the number: a cross-encoder that disagrees maximally on a two-candidate pool must change the order. That test fails today, which is what makes it worth having.

**What this ADR refuses to do.** It does not change the served ranking on the strength of an argument, however good the arithmetic looks. ADR-014 exists because that is how the previous default was set, and this record is the evidence that a measured default can still be measured on the wrong distribution.

## Alternatives Considered

- **Just change the default weight from 0.5 to 0.6:** included as an ARM rather than adopted, because it treats a knife-edge as the whole problem. It removes the exact tie and leaves finding 1 untouched — a 0.001 logit spread is still amplified to the full range at any weight. If it wins the measurement anyway, that is a real result and the cheap fix ships.
- **Guard the small-pool case explicitly — below N candidates, skip the blend and use the rerank order:** rejected as a design, kept as a diagnostic. It introduces a discontinuity in served behaviour at an arbitrary N, and the underlying defect is continuous: a pool of 30 with a 0.002 spread is degenerate too, and no pool-size threshold sees it.
- **Widen the `span == 0` guard to `span < ε`:** rejected. It replaces one arbitrary constant with another, and the honest reading of a tiny span is not "treat as equal" but "the cross-encoder is indifferent, so let the fused evidence decide" — which is what a magnitude-preserving normalisation produces on its own, without a threshold.
- **Drop the blend and hand the order to the cross-encoder:** rejected, and already settled. `config.go:241-244` records that weight 1 "measurably loses the lexical evidence a query carries when it names an identifier", and ADR-024 argues the two signals know different things. This ADR does not reopen that.
- **Persist `blended_score` so the tie rate can be measured retrospectively:** rejected for this ADR and receipted. It is a migration, it answers a question about the past, and T1's fixture answers the same question about the present without one.

## Component / Boundary Impact

No boundary moves. `internal/palace` gains eval arms and a normalisation function; `internal/config` gains at most a changed default value in T2. No MCP tool schema changes, no response-shape changes, no new operator surface. The `RerankWeight` knob keeps its meaning and its range.

## Wiring & Contract Changes

- New eval arms registered in `internal/palace/eval.go`, each covered by the existing `TestEveryDeclaredArmIsRegistered` gate.
- New eval cases with pools of 2, 3 and 4 and a low-spread logit fixture.
- T2 only: the rerank axis's normalisation function changes, and `config.Default().RerankWeight` may change. Both are single-site.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| The small-pool and low-spread eval fixture | T1 | T2 — it is the fixture T2's property test reuses | No — additive test data |
| The measured winner among three arms | T1 | T2 | No — T2 cannot start until T1 reports |

**T2 genuinely depends on T1**, unlike most pairs in this corpus: T2 has no defensible content until the measurement exists. If T1 reports that no arm beats the served blend on the small-pool cases, T2's correct content is to record that and change nothing — and that outcome is worth as much as a fix.

## Implementation

Each task is a file under `tasks/`, with a red step before production code, an `## Acceptance` fence run by `adr-verify`, and a killed mutant. T2's mutant is unusually direct: restore min-max on the rerank axis and the two-candidate property test must go red.

The live confirmation for T2 is a served page, not a test: a recall against the deployed container whose `blended_score` values are not tied where the cross-encoder disagreed. `blended_score` is on the wire because of ADR-028 T2, which is what makes this checkable from outside the process at all.

## Consequences

If an arm wins, recall quality improves precisely where the corpus is small — a tight wing, a narrow query, a filtered page — which is the case this system is for. If none wins, the eval gains a fixture that can exhibit a class of defect it previously could not, and the 0.5 default becomes measured rather than inherited.

Either way the tuning method changes: a weight swept on large pools is not a weight measured for the pools served. That generalises past this knob.

## Out of Scope

- **Persisting `blended_score` to `search_events`** (deferred: `docs/adr/BACKLOG.md` §"From ADR-030" — a migration that answers a question about the past; T1's fixture answers it about the present)
- **`max_distance`'s role as a pool shrinker** (deferred: `docs/adr/BACKLOG.md` §"From ADR-030" — it makes small pools more likely and the corpus already records it as "DEAD as a confidence signal"; whether to floor or remove the knob is its own decision)
- **Handing the whole order to the cross-encoder** (permanent: settled by ADR-024 and by the measured loss of lexical evidence recorded at `internal/config/config.go:241-244`)
- **Any change to the fused axis or to RRF** (permanent: ADR-024 owns fusion; this ADR touches only how a raw cross-encoder logit is brought onto a comparable scale)
- **Changing the served ranking before T1 reports** (permanent: ADR-014 — the shipped default is the measured one, and this ADR exists because that rule was satisfied against the wrong distribution)

## Risks

- **A new normalisation can be worse everywhere else while fixing the small-pool case.** This is why T1 measures on the existing corpus as well as the new fixture, and why the served blend is one of the arms rather than the assumed loser.
- **Sigmoid on a raw logit imports a scale assumption.** Cross-encoder logits are not calibrated probabilities, and a sigmoid's useful range depends on the model. The arm is measured, not assumed, and if it wins the ADR records that the constant is model-specific.
- **The 17.6% figure is a lower bound on exposure, not a tie rate.** It counts pages small enough for the pool to be degenerate; it does not count how often the fused-best was the rerank-worst. Stated so nobody quotes it as "17.6% of recalls were mis-ordered" — this repository has already retracted one statistic quoted past its population.

## Rollback

T1 is additive test surface and reverts cleanly. T2 changes one normalisation function and possibly one default; reverting restores min-max at weight 0.5 exactly. Nothing persists, no migration runs.

## Follow-ups

- Re-examine every default set by the weight sweep against the pool-size distribution production actually serves.
- Persist `blended_score` so a tie rate becomes measurable from the durable row rather than only from a live page.
- **A general check: for any normalisation in this pipeline, does the fixture used to tune it span the range production serves?** The answer here was no, and nothing would have said so.
