# ADR-002: Anchor the lexical score so the fusion weight means what it says

**Status:** Accepted
**Date:** 2026-08-19
**Owner:** Zy (with Mindaugas as upstream maintainer)
**Spec:** None — no spec stage; grounded in eval measurements and cited research.
**Cross-references:** `internal/palace/rank.go` (`rankFused`, `bm25Scores`, `adaptiveBM25Weight`, `LexicalCoverageIDF`), `internal/palace/eval.go` (arm registry, `evalCase`), `internal/palace/evalstats.go` (paired bootstrap), `internal/palace/armreach_test.go` (arm reachability), `internal/palace/service_test.go` (`TestLexicalIDFChangesWhatSearchReturns`, the behavioural-reachability pattern), `docs/adr/ADR-001-recall-answers-or-abstains.md`, `docs/adr/ADR-003-retire-the-closet-prior.md` (it decides whether the prior this ADR must rank alongside survives at all)

## Context

`rankFused` blends a vector term and a lexical term. The two are not on the same footing:

```go
	raw := bm25Scores(query, docs)
	var maxBM25 float64
	for _, s := range raw {
		if s > maxBM25 {
			maxBM25 = s
		}
	}
	...
		norm := 0.0
		if maxBM25 > 0 {
			norm = raw[i] / maxBM25
		}
		...
		fused := vectorWeight*vecSimFromDistance(distances[i]) + bm25Weight*norm + boost
```

— `internal/palace/rank.go:419-444`.

The vector half is absolute, and the file says why at `rank.go:167-168`: "Absolute (not relative-to-max) so adding or removing a candidate cannot reshuffle the others." The lexical half is divided by the page's own maximum. So **whenever any candidate on the page scores nonzero BM25, the best one gets `norm == 1.0` exactly** and collects the full `bm25Weight` — whether its raw score is 12 or 0.3. Only the fully degenerate page is honest: with every raw score zero, `maxBM25 > 0` is false and no lexical vote is cast at all.

`bm25Weight` therefore does not name a quantity of lexical evidence. It names the weight handed to whichever candidate won a contest whose scale was discarded.

**That is not a deviation from the literature — it is the literature.** Bruch et al., *An Analysis of Fusion Functions for Hybrid Retrieval* (arXiv:2210.11934, TOIS 2023), recommend theoretical min-max for convex combination: `φ(f(q,d)) = (f(q,d) − inf f) / (M_q − inf f)`, where `inf f` is the score's theoretical infimum and `M_q` is **the maximum observed in that query's retrieved list**. The paper gives BM25 as its worked example and states its infimum is 0, so the recommendation reduces to `raw / M_q` — which is `rank.go:429-433` exactly. Our lexical half already implements the published recommendation. The fixed ceiling proposed below is **ours**, and the burden of justifying a departure is ours too.

The justification is a property the paper does not optimise for and this codebase already names for the vector half: independence from which candidate happened to win. `rankRRF`'s own doc comment (`rank.go:185-190`) names the same discomfort in passing — "BM25 is unbounded and cosine similarity is not, so combining them means normalizing two distributions that do not share a shape" — and then routes around it instead of addressing it.

This matters more than a normalisation nit, and that is the point of the ADR. Every lexical result we have was measured through this artifact:

- Lexical fusion hurts on prose and helps on identifiers. Large corpus (n=30, 3 runs): vector 0.335–0.393 MRR against hybrid at `bm25=0.4` 0.178–0.285, monotonically worse as lexical weight rises. Our corpus (n=40 paraphrase): vector 0.831, every fusion arm below it. Identifier queries (n=12): lexical fusion 1.000 against vector 0.847.
- Reading that as "the right lexical weight is per-query" produced `adaptiveBM25Weight`, `LexicalCoverage`, and then `LexicalCoverageIDF`, which beat binary coverage on four tables across two corpora: 0.377 v 0.257, 0.370 v 0.290, 0.246 v 0.183, 0.726 v 0.673.

All four of those tables were produced under page-maximum normalisation. If the adaptive weight is mostly cancelling an inflated lexical vote, it is a workaround wearing the clothes of a finding — and we would keep extending it.

How much any of this can move is bounded, and the eval prints the bound. On our mined corpus 98% of gold memories enter the candidate pool (1 of 40 is never retrieved at all), 75% are already at rank 1 and 92% inside the top 5. Every arm in this ADR is therefore competing for at most 23 points of top-1 and 6 of top-5 — which is the honest frame for reading any interval below.

**Correction to the first version of this ADR.** Two claims in the first draft were wrong and are corrected here rather than deleted, because both were load-bearing.

1. *The citation.* The first version wrote that Bruch et al. "recommend theoretical min-max — normalising against the bounds a score *can* take rather than the ones this sample happened to take. Our vector half already does that; our lexical half does not." That reads the paper as condemning our code. It does not: their normaliser takes the theoretical *minimum* and the observed *maximum*, which for BM25 is `raw/max` — today's code. The fixed ceiling `C` is our own departure, and the ADR now says so.
2. *The central identity.* The first version claimed the ceiling arm at weight `w` reproduces the ordering page-max produces at effective weight `w·a`, with `a = maxBM25/C`, and deduced from it that "the anchored normaliser **is** a per-query adaptive lexical weight", therefore the coverage machinery could be deleted. The claimed identity is false: substituting the normaliser changes the lexical coefficient but not the vector coefficient `(1−w)`. Checked over 4,000 randomly generated pages, `w' = w·a` produced a different ordering in a large fraction of them (36% in the reviewer's run, 51% in an independent replication with a different page generator — the rate is a property of the generator, the disagreement is not). The corrected identity `w' = w·a/(1 − w + w·a)` produced **no** disagreement in either run (0/4000), and is provable in one line. With a closet boost present it disagrees again (18% and 57% respectively), because no page-max weight reproduces an additive term that has been rescaled.

The experiment survives both corrections; the **deduction** does not. What was presented as an identity that made the coverage machinery redundant by algebra is now stated as an empirical hypothesis that the tables decide, and the deletion trigger has been rebuilt accordingly.

## Existing Primitives Audit

- **`rankFused`** (`rank.go:419`) — the single implementation behind `rankHybrid`, `rankHybridWeighted`, `rankHybridAdaptive`, `rankHybridAdaptiveIDF`. Reshaped: it gains the normaliser as a parameter, so every existing caller keeps today's behaviour by naming it.
- **`vecSimFromDistance`** (`rank.go:169`) — already page-independent, already documented as deliberately so. Reused unchanged; it is the property the lexical half is being brought up to.
- **`bm25Scores`** (`rank.go:98-163`) — returns raw, unbounded scores and already says "the caller normalizes". Unchanged. It takes `N`, document frequency, IDF **and average document length** over the candidate pool, which bounds how absolute any of this can get: dropping a candidate moves every raw score and moves the ceiling with it (see Risks and Out of Scope).
- **`rankRRF`** (`rank.go:194`) — the existing escape from score-scale incommensurability, and best on the large corpus with the cross-encoder on top. Kept as an arm; this ADR is the other branch of the same question.
- **`adaptiveBM25Weight` / `LexicalCoverage` / `LexicalCoverageIDF`** (`rank.go:318-405`) — the machinery under test. Reused as arms, and candidates for deletion.
- **Eval arm registry + `BootstrapMRR` / `PairedDelta`** (`evalArms` in `eval.go`, `evalstats.go`) — every arm re-orders one shared pool from one vector search, so an arm costs no extra retrieval and no extra inference. Reused as the instrument; no new statistics code, which is why the deletion trigger is built from cross-corpus transfer rather than from a selection-aware bootstrap (see Follow-ups).
- **`evalCase`'s boost handling** — as authored, one `boosts` slice was built per case and handed to the swept fusion arms and both adaptive arms alike, with only `hybrid` passing `nil`; the plan was to reuse that precedent for a paired no-closet family. **Superseded by ADR-003 T1** (`armBoosts`, `internal/palace/eval.go`): an arm now carries the prior only if its name says so, which is the same fix applied once at the source instead of per family.
- **`bm25Sweep`** (`eval.go:69`) — already `{0.0, 0.2, 0.4, 0.6}`, which contains the three fixed weights this ADR measures. Reused, and load-bearing: it is the rival hypothesis to anchoring, not just a backdrop.
- **`TestLexicalIDFChangesWhatSearchReturns`** (`service_test.go:583`) — the repo's own pattern for pinning that a flag reaches the ranking path: require two settings to produce **different scores through `Search`**. Reused verbatim in T2 and T4; its predecessor asserted only that both modes returned a result and passed while the flag was read by nothing.

## Decision

Make the lexical normaliser an explicit choice inside `rankFused`, add two **anchored** transforms beside today's page-maximum, and **ship them as eval arms first** — the shipping default does not move until the measurement says which way.

Both transforms divide by the query's achievable ceiling `C = (k1+1)·Σ_{t∈Q} idf(t)`, the `tf → ∞` limit of the BM25 term contribution, computed over the same pool IDF `bm25Scores` already builds. The sum runs over **all** query terms, including ones no candidate contains: a term nobody matches carries a large IDF, inflates `C`, and pushes the whole lexical term toward zero — which is exactly what a cross-language query needs. The two are:

- **ceiling:** `norm = raw / C` — linear, one interpretation, no parameter.
- **saturating:** `norm = raw / (raw + κ·C)`, `κ = 0.5` — non-linear, so it changes within-page *spacing* as well as scale. That is what makes the two arms non-redundant rather than one idea twice.

Under both, a candidate's lexical contribution depends on its own raw score and on the query ceiling, **not on which sibling happened to win**. It does not become independent of the candidate set: `bm25Scores` takes `N`, df, IDF and average document length over the pool, so removing a sibling moves every raw score and moves `C` with it. Winner-independence is the property being bought; candidate-set independence needs frozen corpus statistics and is out of scope.

### What the arithmetic does and does not say

For a fixed page, `maxBM25` and `C` are constants across candidates. Write `a = maxBM25/C`. Production fusion is

```
fused_i = (1 − w)·V_i + w·norm_i + B_i
```

so replacing `norm_i = raw_i/maxBM25` with `raw_i/C` moves the **lexical** coefficient only — the vector coefficient stays at `(1 − w)`. Multiplying every score by the positive constant `s = 1 − w + w·a` leaves the order unchanged, which gives, **when every `B_i` is zero**:

```
ceiling at weight w   ≡   page-max at weight   w' = w·a / (1 − w + w·a)
```

`a < 1` strictly: each matched term contributes `idf·f(k1+1) / (f + k1(1 − b + b·dl/avgdl)) < idf·(k1+1)`, and query terms no candidate contains add to `C` and nothing to `raw`. So `w' < w` always — **the ceiling normaliser can only shrink the effective lexical weight, never raise it.**

With a closet boost the equivalence fails outright. Rescaling by `s` turns `B_i` into `B_i/s`, and no page-max weight reproduces an additive, unscaled term that has been divided by something. Since `s < 1`, anchoring *amplifies* the boost relative to the fusion — the same class of scale mismatch that once cost this palace recall@1 from 92% to 17%. This is not an edge case: **Superseded — see the amendment note below.** As authored, `evalCase` handed the same `boosts` slice to every swept fusion arm and both adaptive arms, so the boosted regime was the only regime the existing arms measured. After ADR-003 T1's `armBoosts`, 27 of 29 arms receive `nil` and the unboosted regime is what they measure. The sentence is kept because it is the premise the two-regime design rested on, and a reader needs to see the premise to understand why the design changed.go:493-496`, `eval.go:657-679`), so the boosted regime is the only regime the existing arms measure.

**What the experiment must therefore decide.** The corrected algebra does not license the first version's deduction. `adaptiveBM25Weight` multiplies the base weight by `LexicalCoverage` — also always a shrink, never a raise — so anchoring and coverage are two different monotone shrink functions of two different quantities: `maxBM25/C` on one side, the share of query terms with usable document frequency on the other. Neither nests the other, and with the closet prior on, the anchored arm is not even inside the family of page-max weights the adaptive machinery searches. Whether one makes the other redundant is an **empirical question for the tables**, not an identity.

The same algebra makes the naive comparison unreadable. "Anchored at `w` versus page-max at `w`" contrasts two different effective weights, so a win there can be nothing more than "a smaller lexical weight suits this corpus" — a hypothesis `bm25Sweep` already tests for free at 0.20. The only thing anchoring adds beyond a global weight change is that `a` varies **per query**. Every comparison in this ADR is therefore **best-of-family against best-of-family**: the highest-MRR swept weight under one normaliser against the highest-MRR swept weight under the other.

### Two decisions, two standards of proof

Which `(LEX_NORM, BM25_WEIGHT)` pair ships is a **choice** under uncertainty. It is reversible with one environment variable, so it is settled by a declared deterministic rule over measured MRRs, and no confidence interval licenses it. Deleting `adaptiveBM25Weight`, `LexicalCoverage` and `LexicalCoverageIDF` is an irreversible **claim** about a mechanism. It is a git revert, not a knob, so it is settled by intervals — and by intervals that did not pick their own comparators out of the data they are computed on.

**The shipping rule (deterministic, reversible, read off the arms that match what production serves):**

> **Amended 2026-08-20, after ADR-003 T1 landed.** This rule and the trigger below were written when
> `evalCase` handed one closet-boosts slice to every fusion arm, so "the boosted arms" and "the arms
> shaped like production" were the same set. ADR-003 T1 made the closet prior something an arm opts
> into by name and put closet variants of the sweep and adaptive arms permanently out of scope, so
> **no fusion, sweep or adaptive arm carries a boost any more** — the ten anchored arms are one
> unboosted family. Read every "boosted arm" below as the unboosted fusion arms that now exist, and
> see the note after the trigger for why the second regime went with them.

1. Within each normaliser ∈ {`page-max`, `ceiling`, `saturating`}, take the highest-MRR fixed swept weight on each corpus.
2. Ship the normaliser whose best-of-family MRR is highest **on both corpora**. If the corpora disagree on the argmax, ship the incumbent `page-max` and record the disagreement.
3. `ceiling` beats `saturating` on a tie — a tie being a paired interval on their best-of-family contrast that contains zero. One fewer constant, and no `κ` to tune.
4. `page-max` beats an anchored normaliser on a tie. The incumbent wins ties because a flip moves the `score` on the `am_search` wire for no measured gain.
5. `BM25_WEIGHT` ships as the argmax over {0.20, 0.40, 0.60, `auto`, `auto-idf`} under the shipped normaliser, agreeing on both corpora; on a tie or a disagreement, the incumbent `auto`. Under outcome (i) below, `auto` and `auto-idf` no longer exist and the argmax is over the three fixed weights.

**The deletion trigger (irreversible, so pre-registered and selection-free).** For each ordered pair of corpora (A→B and B→A), over the single unboosted regime that now exists:

- on corpus **A**, pick the best anchored fixed swept weight `w*` and the stronger anchored adaptive arm `m*` (`auto` or `auto-idf`) by MRR;
- on corpus **B**, compute `PairedDelta(ranks[w*], ranks[m*])`.

Both intervals must exclude zero in favour of the fixed arm. The comparators are chosen on data the interval is not computed on, which is what the first version's "best fixed arm versus the stronger adaptive arm on the same cases" did not do: an ordinary paired interval on two arms selected from those same cases is not a 95% interval for anything.

Two honest caveats. The corpora are not an exchangeable split of one population — one is 45× larger — so this is a **transfer** test, not held-out validation; for an irreversible deletion that is the stronger bar and not the weaker one, since a mechanism that only survives on the corpus that selected it is precisely what we should not delete on. And the trigger may well never fire at n=40 and n=30, which is the designed behaviour rather than a defect: the outcome is then (iii), and nothing is deleted.

**Why there is one regime now, and why that is not a weakening.** The second regime existed to control a confound: the boosted contrast cannot separate a lexical-weighting effect from a boost-strength one, because anchoring inflates an additive boost by `1/s` with `s = 1 − w(1 − a)`, and `s` differs between a fixed arm and an adaptive arm whose effective weight moves per query. Requiring agreement in both regimes stopped a result that only appears with the prior ON from being attributed to the lexical weight.

ADR-003 T1 removed the confound at its source rather than controlling for it: no fusion, sweep or adaptive arm is boosted, so there is no boost for anchoring to inflate and no boost-strength effect to mistake for a weighting one. Four intervals become two because two of them were measuring a regime that no longer exists — not because the bar was lowered. **The bar per regime is unchanged: every interval that exists must still exclude zero in favour of the fixed arm.**

One ordering dependency follows and is deliberate. This rule now reads the arms shaped like what production serves, and ADR-003 T4 is what decides that shape. If T4 ships closet-ON after all, the arms this rule reads no longer match production and the rule must be re-derived before it is applied — which is why the Follow-ups require re-running after either ADR lands.

### The three outcomes, each naming the pair it ships

- **(i) The adaptive machinery was a workaround.** All four deletion intervals exclude zero in favour of the fixed arm, **and** the shipping rule's winner is an anchored normaliser. Ships `LEX_NORM = ceiling|saturating` (the rule's winner) and `BM25_WEIGHT =` the argmax fixed weight under it; `adaptiveBM25Weight`, `LexicalCoverage`, `LexicalCoverageIDF`, their arms and the `auto` / `auto-idf` config values are **deleted**, not extended.
- **(ii) The adaptive weighting is real.** The deletion trigger does not fire and the shipping rule's argmax weight is adaptive. Ships `LEX_NORM =` the rule's winner and `BM25_WEIGHT = auto` or `auto-idf` as the rule selects; nothing is deleted, and if the rule's winner is anchored, the coverage signal is simply carrying information the score scale does not.
- **(iii) Neither.** The deletion trigger does not fire and the rule's argmax weight is fixed. Ships `LEX_NORM =` the rule's winner and `BM25_WEIGHT =` that fixed weight; the adaptive arms and functions stay, and the committed evidence records that they did not earn the default. The case where the intervals fire but the rule ships `page-max` lands here too, with the discrepancy recorded: deleting the coverage machinery on evidence gathered under a substrate we then decline to ship would be a conclusion about a configuration nobody runs.

These are mutually exclusive and exhaustive by construction: the trigger either fires (with an anchored winner) or it does not, and the rule's argmax is either adaptive or fixed. Shipping `page-max` with a new knob and a committed negative result is a legitimate outcome, not a failure to decide.

Deletion is the outcome this ADR exists to make possible, and the corrected trigger makes it harder to reach than the first version did. That is the right direction: the first version's interval would have licensed deleting three functions on a comparison whose two arms were both chosen by the same 40 cases that scored them.

## Alternatives Considered

- **Leave it and tune `bm25-weight` per corpus:** the status quo, and the knob already exists. Rejected because the weight cannot mean the same thing on two pages, so no tuned value transfers — that is the defect, not a workaround for it.
- **Just lower the fixed weight:** the cheapest rival hypothesis, and the corrected algebra promotes it from a footnote to a real contender — since `w' < w` always, anchoring's entire effect on a page with a constant `a` *is* a smaller lexical weight, and `bm25Sweep` already contains 0.20. Not rejected: it is the null the best-of-family comparison is built to test against. If anchoring cannot beat the best global weight, this alternative is the finding and the ADR ships nothing but the knob.
- **Observed min-max (subtract the page minimum as well as dividing by the maximum):** the textbook fix, and distinct from today's code — `raw/maxBM25` is min-max with the *theoretical* minimum 0, i.e. Bruch et al.'s recommendation. Rejected: subtracting the observed minimum is strictly worse than what we already run, because it pins the page's worst candidate at exactly 0 and its best at exactly 1, guaranteeing a full-range lexical spread on a page with no lexical signal at all.
- **z-score standardisation of the lexical column:** rejected for the same reason in a different shape; a page of uniformly poor lexical matches is stretched into a confident-looking spread.
- **RRF only — drop score fusion entirely:** already an arm, already the best arm on the large corpus with the cross-encoder on top. Rejected as a *replacement* because RRF discards magnitude, which is exactly what identifier queries need (n=12: 1.000 against vector's 0.847). Keeping both is one `switch` case.
- **Global corpus IDF from a persistent term-statistics store:** the properly anchored version — df over the whole palace rather than over the retrieved pool, and the only version that would make the lexical score independent of *which candidates were retrieved* rather than only of which one won. Rejected for now: it needs term statistics maintained and invalidated on every write, and it is a bigger change than the defect warrants before the defect is shown to cost anything.
- **"This is just another adaptive weight, so it changes nothing":** the strongest objection, and the corrected algebra concedes half of it — without a boost, the ceiling arm at `w` *is* page-max at a per-query weight `w' = w·a/(1 − w + w·a)`. What it is not is the *same* per-query weight the coverage machinery computes, nor a superset of it, which is why this ADR no longer argues that anchoring subsumes the machinery by algebra. The remaining difference is that this weight is computed from the score scale with zero or one interpretable parameter and restores the winner-independence `vecSimFromDistance` documents. If that is worth nothing on these corpora, outcome (iii) says so.
- **Let the cross-encoder fix the order:** rerank is the strongest arm at scale, and `rrf+rerank` wins the large corpus. Rejected as an answer here because the reranker is optional, blended at a weight, and only ever scores the shortlist fusion hands it — every deployment without one ranks through this artifact today, and `BM25Weight` defaults to `auto` (`config.go:286`).

## Component / Boundary Impact

| Component | Change | One reason to change? |
|---|---|---|
| `internal/palace` (rank.go) | the lexical normaliser becomes a named choice; two anchored transforms added | yes — it owns ranking |
| `internal/palace` (eval.go) | anchored arms crossed with the existing weight sweep, plus a no-closet anchored family; the fusion dispatch becomes a testable seam | yes — it owns measurement |
| `internal/palace` (service.go) | the ranking switch selects the normaliser | yes — it owns the search path |
| `internal/config` + `cmd/server` | one new knob, and (outcome i only) the removal of two accepted `BM25_WEIGHT` values | yes — the composition root |

No new component, no module moves, no change to what is retrieved.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `rankFused` signature | takes the lexical normaliser; every existing caller names `page-max` and is behaviourally unchanged | `internal/palace/rank.go` | in-package callers only |
| eval arm names `fusion bm25=<w> anchored:<norm>` and `… no-closet` | additive arms; appear in the printed table and in `writeResults` JSON | `internal/palace/eval.go` | operator, `cmd/server/eval.go` |
| `Service.WithLexNorm` | new option, mirroring `WithLexicalIDF`; how the composition root reaches the normaliser | `internal/palace/service.go` | `cmd/server`, tests |
| `LEX_NORM` / `--lex-norm` | new config key: `page-max` (today's behaviour) \| `ceiling` \| `saturating` | operator | `palace.Service` |
| `HybridScore.Fused` → `am_search` `score` | same field, same shape; magnitude changes if the default normaliser flips | `internal/palace/rank.go` | `internal/mcpserver/drawers.go:300`, eval |
| `BM25_WEIGHT` accepted values | **outcome (i) only:** `auto` and `auto-idf` are removed; a fixed `0..1` becomes required | operator | `cmd/server/main.go:180` |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `lexNorm` normaliser type + `lexNormPageMax` / `lexNormCeiling` / `lexNormSaturating` | T1 | T2 | No — `page-max` reproduces today exactly |
| `evalArms` registry + anchored arm names + the no-closet anchored family + `fusionRankerFor` | T2 | T3, T4 | No — additive arms, extracted dispatch |
| `anchorEvidence` (committed results files + loader) | T3 | T4 | No — testdata |
| `LEX_NORM` config key, `Service.WithLexNorm` and the shipping default | T4 | operator (not a task) | Only under outcome (i), where `BM25_WEIGHT=auto` stops being accepted |

## Implementation

Four tasks — see [`ADR-002-anchor-the-lexical-score/tasks/README.md`](ADR-002-anchor-the-lexical-score/tasks/README.md).

T4 is written to execute whichever outcome the T3 evidence produces, and its acceptance test recomputes the shipping rule and all four deletion intervals from the committed evidence rather than trusting a hand-recorded verdict. That is what stops (i) from being quietly softened once deleting real code becomes the concrete next step — and, now, what stops the selection-free trigger from quietly reverting to the single-corpus comparison it replaced.

## Consequences

- **Positive:** `bm25-weight` starts meaning what its name says. A weight measured on one corpus becomes a candidate for another, instead of a number that silently re-scales with each page's best match.
- **Positive:** the ADR can end with less code than it started with. Under outcome (i) three functions, two arms and two config values go, and the four IDF-coverage tables get reclassified from finding to artifact.
- **Positive:** anchoring is one `switch` case and an environment variable away from being undone; the evidence is committed and replayable offline.
- **Negative:** the ADR's original argument is gone. It claimed the coverage machinery was redundant by algebra; the algebra says only that anchoring is *a* per-query shrink, not *the* one coverage computes. What remains is an experiment with a pre-registered, deliberately conservative trigger — and a real chance that the trigger never fires and nothing is deleted.
- **Negative:** the fused score's magnitude changes when the default flips, and `score` is on the `am_search` wire (`drawers.go:300`). Nothing consumes it as an absolute, but it is a visible number that will move.
- **Negative:** the closet boost is added in absolute units on top of the fused score (`rank.go:438`), so shrinking the lexical term makes a fixed `+0.40` boost relatively larger — by exactly `1/s`, `s = 1 − w(1 − a)`. This palace has already lost recall@1 from 92% to 17% to exactly that kind of scale mismatch, before `closetBoostStrength` was added.
- **Neutral:** retrieval is untouched. Every arm still re-orders one pool nominated by vector distance — BM25 reorders, it never nominates — so nothing here can change what is reachable, only what is on top.
- **Neutral:** the table gets wider again. The no-closet anchored family adds rows that cost no retrieval and no inference, and it is the only way the deletion trigger can be read without the closet prior confounding it.

## Out of Scope

- Global corpus-wide IDF from a persistent term-statistics store — the only way to make either `raw` or `C` independent of *which candidates were retrieved*, since `bm25Scores` takes `N`, df, IDF and average document length over the pool; this ADR buys winner-independence and not candidate-set independence (deferred: docs/adr/BACKLOG.md)
- A genuine lexical first-stage retriever, so BM25 can nominate candidates instead of only reordering them (deferred: docs/adr/BACKLOG.md)
- Recalibrating the closet boost and `CLOSET_BOOST` against the rescaled fused range, beyond measuring the anchored arms in both boost regimes (deferred: docs/adr/BACKLOG.md)
- The cross-encoder blend weight and every reranked arm (permanent: this ADR changes what fusion contributes to the shortlist, not what the cross-encoder does with it.)
- Abstention, confidence and the `unknown` verdict (permanent: ADR-001 owns that decision and this ADR must not re-open it.)
- RRF's smoothing constant `rrfK` (permanent: rank fusion reads positions, never magnitudes, so no normaliser can reach it.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| "Anchored at `w` beats page-max at `w`" is confounded with a plain weight change, since `w' = w·a/(1−w+w·a) < w` always | Certain | High | No comparison in this ADR is at matched nominal weight; every one is best-of-family against best-of-family, and `bm25Sweep` already contains the smaller global weights anchoring emulates |
| Anchoring inflates the additive closet boost by `1/s`, so a fixed-versus-adaptive result could be a boost-strength effect | Certain | High | The deletion trigger must fire in **both** boost regimes; the no-closet anchored family exists for exactly this, following the `hybrid` / `hybrid+closet` precedent; `CLOSET_BOOST` can scale it and ADR-003 may retire it outright |
| The selection-free deletion trigger never fires at n=40 and n=30 | High | Med | Designed behaviour, not a defect: the outcome is (iii), nothing is deleted, and the evidence is committed. A trigger that fires on comparators chosen from its own cases is the failure this replaced |
| "Anchored" is only pool-absolute — `bm25Scores` takes `N`, df, IDF and average document length over the candidate set, so both `raw` and `C` move with pool composition | Certain | Med | Stated in the Decision rather than glossed; the property claimed is independence from *which candidate won*, and T1's test is written for that narrower property; the global-IDF version is deferred, not claimed |
| Deleting three functions on one measurement | Med | High | Four intervals across two corpora and two boost regimes, with comparators selected on the other corpus; unlike the normaliser, deletion is a git revert, not an env var, and the ADR says so |
| n=40 and n=30 are small, and the retrieval ceiling leaves only ~23 points of top-1 to compete for | High | Med | Paired bootstrap on per-case deltas, which cancels question difficulty; outcome (iii) is written as "did not earn the default", never as equivalence |
| Anchoring regresses the identifier queries, where lexical fusion is the whole win (1.000 v 0.847) | Med | High | Per-category tables are part of the committed evidence, and the identifier corpus is one of the required tables — a headline gain that hides an identifier regression fails the completeness gate |
| `maxBM25/C` is small on prose, so `w'` compresses the three anchored fixed weights into a narrow band and "best fixed arm" is chosen among near-identical arms | Med | High | T3's evidence gate fails when the three anchored fixed-weight arms return identical per-case ranks; a collapsed sweep cannot support the deletion trigger and stops the chain instead of passing it |
| An anchored arm at weight 0 duplicates the page-max arm at weight 0 and reads as a finding | Certain if unhandled | Low | Anchored arms are registered only for nonzero weights; at `w=0` the lexical term vanishes regardless of normaliser, and T2 pins that |
| An anchored arm silently dispatches to the page-max path and the table reads "the normaliser makes no difference" | Med | High | T2's reachability test is behavioural — different scores out of the shared ranking seam on a fixture built to disagree — following `TestLexicalIDFChangesWhatSearchReturns`, whose predecessor passed while the flag was read by nothing |
| Operators who tuned `BM25_WEIGHT` by hand get a different effective weight after a default flip | Med | Med | `LEX_NORM=page-max` restores the old arithmetic exactly; the flip is documented at the knob |
| The sweep is truncated for the saturating arm, so best-of-family compares families over unequal ranges | Certain | Med | `raw < C` strictly, so `raw/(raw + kappa·C) < 2/3`: across `bm25Sweep` the saturating arm's lexical contribution tops out at two thirds of page-max's at the same weight (0.40 against 0.60 at w=0.6), and matching page-max at the top of the grid would need w = 0.9, outside it. Best-of-family is only fair if the grid spans each family comparably. Found by review; pinned by `TestLexNormSaturatingBoundOnAchievableInput`. T3 either widens the grid for that arm or reports the truncation beside the table — a saturating arm losing because it could not reach the weights that suit it is not evidence about the transform |

## Rollback

`LEX_NORM=page-max` restores today's ranking exactly — the same `raw[i]/maxBM25` arithmetic, byte-identical fused scores. Nothing persistent changes: no migration, no re-embedding, no reindex, no stored score. The eval arms are additive and can be left in place.

Outcome (i)'s deletions are the exception and are not env-revertable; undoing them is a git revert of T4. That asymmetry is why the deletion trigger spans both corpora, both directions and both boost regimes while the normaliser flip is a deterministic argmax with the incumbent winning ties.

## Follow-ups

- [ ] Selection-aware paired bootstrap: recompute the best-of-family argmax **inside** each bootstrap replicate, so a single-corpus contrast between selected arms gets a valid interval. This ADR uses cross-corpus transfer instead because it needs no new statistics code; the bootstrap version would let a future ADR read the trigger on one corpus.
