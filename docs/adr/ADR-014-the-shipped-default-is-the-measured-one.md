# ADR-014: The shipped default is the measured one

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-003 (measured the closet prior's cost; this ADR executes its T4 ahead of its own T3), ADR-002 (owns `BM25_WEIGHT` and the normalisers, both inert under the new default), `cmd/server/configureranking_test.go`
**Invalidates:** none — but it PRE-EMPTS ADR-003 T4, which is checked and stated below rather than left to collide.
**Served-path change:** Every deployment that has not set `FUSION` or `CLOSET_BOOST` gets different search results: rank fusion instead of a linear blend, and no curation prior. This is the largest served-path change in the corpus so far and it changes ranking for everyone on upgrade.

## Context

The shipped defaults were `fusion=linear`, `closet-boost=1`, `lex-weight=auto`, no reranker. By this
project's own recorded measurements that is not the best combination it knows of.

**The closet prior.** ADR-003 is Accepted and records, on the mined-transcript corpus (n=40 against
~5,000 drawers): the prior costs **~0.10 MRR**, and 10 of 40 golds are displaced by boosted
neighbours. Its own history is worse — `closetRankBoosts` carries the comment recording that a flat
+0.40 on a fused score in [0,1] dropped recall@1 from **92% to 17%** with exactly one closet filed.
The distance cap and linear fade repaired the catastrophe; the ~0.10 MRR is what the repair left
behind.

**Rank fusion.** The maintainer ran the eval repeatedly at n=100 and reported `rrf+rerank` winning
every table.

**What is NOT measured, and it matters.** `rrf+rerank` is not `rrf`. The shipped default has no
reranker (`RerankURL` is empty), so the combination going out is rank fusion ALONE, and there is no
table in this repository for it. The owner made this call with that stated. It is recorded here
rather than implied, because the next person to read a regression will need to know which half of
the evidence was real.

Also on the same run: **vector alone scored 0.831 against hybrid's 0.691** — on that corpus the
lexical channel actively hurt. That is an argument for reducing the lexical channel's influence,
which rank fusion does by construction, and it is the closest thing to independent support the rrf
choice has here.

## Existing Primitives Audit

- `config.Default()` — the only place a default lives; changed there, nowhere else.
- `configureRanking` — already applies and announces every knob; only its comparison points move.
- The resolved-profile line (shipped earlier today) — already states what will act, so an operator
  reading logs after upgrading sees the new configuration named rather than inferred.
- ADR-003 T4 — designed exactly this flip for the closet half. Its steps are executed here; its
  evidence gate (T3) is not, and that is the deviation this ADR owns.

## Decision

**Ship `fusion=rrf` and `closet-boost=0` as the defaults**, both overridable, with `FUSION=linear`
and `CLOSET_BOOST=1` restoring the previous behaviour exactly.

This pre-empts ADR-003's own sequencing. T3 exists to re-measure on a second corpus before flipping,
and it has not run. The owner chose to flip on the evidence already recorded rather than wait. T3
remains worth running — it is now a check on a shipped default rather than a gate before one, which
is a weaker position and is stated as such.

## Alternatives Considered

- **Flip only the closet prior.** The recommended option: it is the change with accepted, in-repo,
  numeric evidence, and ADR-003 already decided it. Rejected by the owner in favour of both.
- **Change nothing until T3 runs.** Safest, and the reason T3 exists. Rejected: the shipped default
  is measurably poor on the one corpus anyone has measured, and waiting preserves a known cost to
  avoid an unknown one.
- **Ship a named profile an operator opts into.** Keeps every deployment stable and makes the better
  combination available. Rejected as the primary answer because a default nobody selects is the
  default everyone runs; it remains a reasonable follow-up if the flip proves contentious.
- **Make `lex-weight` deterministic (a fixed number rather than `auto`).** Not taken here. Under rrf
  the lexical weight does not apply at all, so the question is moot at the new default and returns
  the moment anyone sets `FUSION=linear`.

## Wiring & Contract Changes

- `config.Default()`: `Fusion` `linear` → `rrf`; `ClosetBoost` `1` → `0`.
- `configureRanking` applies the closet scale UNCONDITIONALLY. It previously applied it only when it
  differed from the literal `1`, and the service's own zero value is the FULL prior — so a config
  default of 0 under the old condition would have shipped the very prior it retires.
- The rrf line is announced even though rrf is now the default. Every other startup line reports a
  departure; this one deliberately does not, because an operator who sets `--bm25-weight` or
  `--lex-norm` under rrf gets no behaviour and no error, and staying quiet because "that is just the
  default" is how they would find out from a table instead of from their own logs.
- `.env.example`, `.env.docker.example` and both flag Usage strings state the new defaults.

## Consequences

**Two operator knobs are now inert by default.** `--bm25-weight` and `--lex-norm` do nothing under
rank fusion, which combines positions rather than magnitudes. `--lex-norm` shipped hours before this
ADR; it is reachable only for an operator who also sets `FUSION=linear`. That is the honest cost of
the flip and it is not hidden: both Usage strings say so, startup says so on every boot, and
`TestDiscoveredPairsAdmitTheirCondition` fails if any of those stop saying it.

**The mode-scope sweep had to stop anchoring on the default.** Its predicate needs a baseline where a
knob is live; the moment rrf became the default, `--bm25-weight` and `--lex-norm` were inert at
baseline, the two-part predicate could not be evaluated for them, and the sweep reported ZERO pairs —
including the bm25/rrf pair that plainly exists in the code. A discovery tool anchored to the default
stops discovering exactly when the default becomes interesting. It now sweeps from linear.

**The sweep also over-reported, and my first explanation of why was wrong.** With the new baseline it
observed pairs like "`--bm25-weight` is inert when `--lex-norm` is set", and this ADR originally
attributed that to fixture resolution — the corpus being too small to show the effect. A
different-lineage reviewer found the real cause: liveness was measured from `sweepBaseline()`
(linear) while every conditioned cell was still built from `config.Default()`, which after this
flip is rrf. So every conditioned cell silently carried rank fusion, both lexical knobs were inert
in all of them, and the sweep charged that to whichever knob the cell happened to vary. Measuring
liveness in one world and inertness in another is not a two-part predicate; it is two unrelated
facts. Conditioning from the same baseline collapsed fifteen observed pairs to three.

All three are now confirmed from the code and enforced as admissions, including one this ADR
originally dismissed as unconfirmable: `--lex-norm` is inert at `--bm25-weight=0`, because
`rankFused` multiplies the normalised lexical term by the weight and zero annihilates it before it
reaches the fused score. An observed pair that nobody classifies now FAILS rather than being logged
— logging it is what would let a real inert knob ship undocumented.

## Out of Scope

- Running ADR-003 T3's two-corpus measurement (deferred: docs/adr/BACKLOG.md — still worth doing, now as a check on a shipped default rather than a gate before one)
- A table for rrf WITHOUT a reranker (deferred: docs/adr/BACKLOG.md — the combination this ADR ships and the one nobody has measured)
- Making the sweep distinguish code-inertness from fixture-inertness structurally (deferred: docs/adr/BACKLOG.md)
- Changing `lex-weight=auto` to a fixed number (permanent: moot under rrf, and ADR-002 owns what the weight means)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| rrf alone is worse than linear on some corpus, and nobody measured it | Medium | High — every deployment's ranking | `FUSION=linear` restores the previous behaviour exactly, startup names the profile in force, and the gap is stated in Context rather than discovered later. |
| An operator's `BM25_WEIGHT` silently stops applying on upgrade | High | Medium | Announced on every boot and in both Usage strings; the resolved-profile line states `lex-weight` even when inert. |
| A curated palace loses recall because the prior it depended on is off | Medium | Medium | `CLOSET_BOOST=1` restores it. ADR-003 records that the prior helps on curated corpora and hurts on mined ones, and no deployment's corpus type is known here. |

## Rollback

Set `FUSION=linear` and `CLOSET_BOOST=1`, or revert this commit — the two values in `config.Default()`
are the whole change. No migration, no persistent layout change. Rows in `search_events` written
before and after rank differently, which matters to ADR-001's calibration; it has never been run.

## Follow-ups

- [ ] Run ADR-003 T3 and report whether the two-corpus evidence supports the closet flip that already shipped, including the case where it does not.
- [ ] Measure rrf WITHOUT a reranker against linear on at least one corpus — the combination this ADR ships is the one with no table behind it.
