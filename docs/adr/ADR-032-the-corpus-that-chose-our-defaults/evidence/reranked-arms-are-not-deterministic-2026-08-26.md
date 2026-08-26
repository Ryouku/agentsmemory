# Two identical arms, two different scores — 2026-08-26

A run that was never meant to produce this result did, because it accidentally contained the
one thing every other table lacked: **a duplicate pair of RERANKED arms.**

## Provenance

```
retrieve-k: floor 30 (0 is formula-only — limit×3, raised to rerank-pool when a cross-encoder will run)
reranker: http://reranker:8080/v1 (pool 20, weight 0.50, timeout 2m0s)
ranking: fusion=rrf lex-weight=n/a lex-norm=n/a closet-boost=0.00 rerank=on(pool=20,weight=0.50,norm=sigmoid) unit=memory evidence=lexical retrieve-k=30
```

54 real cases, `--pool 100`, case set `cs-c3f71267f8c6`. The binary predates `e20890e`
(committed 10:20; this container started 09:54), so `serviceForArm` did not reset `rerankNorm`,
and `RERANK_NORM` was never set for the run. Both facts matter, and together they make the
pair below **the same configuration by construction**:

- `rrf+rerank` = `WithFusion("rrf").WithRerankWeight(weight)` — inherits the served normaliser
- `rrf+rerank norm=sigmoid` = the same, plus `.WithRerankNorm(sigmoid)`

The served normaliser resolves to `DefaultRerankNorm`, which is sigmoid. Same fusion, same
weight, same pool, same normaliser.

## The result

| arm | R@1 | R@5 | MRR |
|---|---|---|---|
| `rrf+rerank` | **59%** | 89% | **0.709** |
| `rrf+rerank norm=sigmoid` | **57%** | 89% | **0.700** |

Same configuration, same run, same pool, same 54 cases. **0.009 MRR and 2 points of R@1
apart.**

## What this establishes, and what it only suggests

**Established:** arms that invoke the cross-encoder are not reproducible. Two runs of one
configuration can differ by ~0.01 MRR on n=54.

**Suggested, not proven:** the mechanism is the reranker itself. Every non-reranked duplicate
pair this corpus has ever produced is bit-identical — `vector` and `fusion bm25=0.00` are the
same configuration by construction and score identically in all four tables to date
(0.644/0.644, 0.580/0.580, 0.587/0.587, and again here). The arms that differ are the ones
that call a threaded llama.cpp server, where float reduction order is not fixed. That is a
plausible mechanism and it is not a measurement.

## What it INVALIDATES, including in this corpus's own records

`two-corpora-2026-08-25.md` reconstructs which runs actually ran min-max, using this rule:

> identical rows mean identical configuration; **different rows mean different configuration**

The first half stands for non-reranked arms and is what the `vector`/`fusion bm25=0.00` control
supports. **The second half is now known to be false for reranked arms**, and that is exactly
where it was applied:

| run | verdict as published | status now |
|---|---|---|
| A — paraphrase n=30, 0.610 vs 0.633 | "differ → min-max ran" | **NOT established** — a 0.023 gap is within the noise just measured |
| B — real n=26, 0.708 vs 0.708 | "identical → min-max never ran" | still consistent, and corroborated by reading the source |
| real n=54, 0.668 vs 0.666 | "differ → min-max ran" | **NOT established** — 0.002 is far inside the noise |

**The B1 defect itself is unaffected.** It was found by reading `serviceForArm`, not by
inferring from tables, and it is now gated by
`TestEvalArmsAreDISTINCTCONFIGURATIONSNotJustDistinctNames`. What weakens is only the claim
about which historical runs did or did not exercise min-max.

**The ranking conclusion is unchanged and, if anything, better supported:** no corpus has shown
the normaliser moves recall. It now also turns out that the differences being argued over were
smaller than the instrument's own reproducibility.

## The consequence worth carrying forward

**A difference below roughly 0.01 MRR between two RERANKED arms on n=54 is not a result.**
Several "inconclusive" verdicts in this corpus sit in that band, and at least one sentence
(retracted separately) was written from a 0.002 gap. The paired bootstrap already refuses to
call those, which is the gate working; the error was in the prose read off the table, not in
the table.

The cheap fix for anyone re-running: register the same reranked arm twice under two names. The
delta between them is the noise floor for that run, measured rather than assumed. This run got
it by accident.
