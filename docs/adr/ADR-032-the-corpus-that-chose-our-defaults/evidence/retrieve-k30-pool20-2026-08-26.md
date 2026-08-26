# Widening the fetch moves the arm `--pool` could not reach — 2026-08-26

The first run to vary `RETRIEVE_K` and `RERANK_POOL` rather than the eval's `--pool`, and the
first with a min-max control that genuinely ran.

**Run provenance**, pasted rather than described — the process fix this corpus adopted after two
runs on one commit turned out to have had different served normalisers, and no log survived to
say so:

```
retrieve-k: floor 30 (0 is formula-only — limit×3, raised to rerank-pool when a cross-encoder will run)
fusion: reciprocal-rank (bm25 weight and lex-norm do not apply)
reranker: http://reranker:8080/v1 (pool 20, weight 0.50, timeout 2m0s)
ranking: fusion=rrf lex-weight=n/a lex-norm=n/a closet-boost=0.00 rerank=on(pool=20,weight=0.50,norm=sigmoid) unit=memory evidence=lexical retrieve-k=30
```

54 real cases replayed from `search_events`, `--pool 100`, case set `cs-c3f71267f8c6`.
`RERANK_TIMEOUT=120s` deliberately, so the run measures RANKING rather than budget overruns —
324 rerank spans, every one `ran`, zero `failed_open`, zero scope drops.

## The table

| arm | R@1 | R@5 | MRR | 95% CI | not found |
|---|---|---|---|---|---|
| `rrf+rerank norm=sigmoid` | 57% | 89% | **0.700** BEST | [0.61–0.79] | 0 |
| `production (Search) retrieve-k=50` | 57% | 91% | 0.695 | [0.60–0.79] | 5 |
| `rrf+rerank` (min-max) | 57% | 91% | 0.692 | [0.59–0.79] | 0 |
| `production (Search) limit=10` | 56% | 89% | 0.682 | [0.58–0.78] | 5 |
| `rrf+rerank norm=rank` | 52% | 89% | 0.681 | [0.59–0.77] | 0 |
| `production (Search)` | 56% | 89% | 0.679 | [0.58–0.78] | 6 |

Every arm above the first is `inconclusive vs best` on the paired bootstrap. Nothing here
resolves.

## What DID move: the arm `--pool` structurally could not reach

`pool-100-paired-2026-08-26.md` recorded `production (Search)` at **0.660 with 8 misses**, and
explained why raising `--pool` never moved it: `--pool` widens the SHARED pool the other arms
score, while production re-fetches on its own path at `candidateKFor(limit, …)`. This run raised
the knobs that DO reach that path.

| configuration | production MRR | misses beyond the pool |
|---|---|---|
| `--pool 100`, default retrieve-k | 0.660 | 8 |
| `RETRIEVE_K=30`, `RERANK_POOL=20` | **0.679** | **6** |
| the same run's `retrieve-k=50` arm | **0.695** | **5** |

Two of the eight structurally-excluded golds came back at retrieve-k 30, and a third at 50.
This is the first evidence in this corpus that the fetch width, not the ranking, was holding
production down — and it is consistent with the retrieval ceiling below.

**Retrieval ceiling, same pool:** in pool 100%, top-1 46%, top-5 74%, top-10 83%, top-20 87%,
top-50 94%. The answer is ALWAYS in the pool. Every arm difference above is an ordering result;
none of them is a retrieval result. That is also why widening the fetch helps production and
helps no other arm — the others were already scoring a pool of 100.

## The min-max control, finally run

`rrf+rerank` reconstructs min-max explicitly in this build, so the comparison the n=26 table
could not make is here: **min-max 0.692, sigmoid 0.700, rank 0.681**, all inconclusive against
each other. Sigmoid is nominally ahead by 0.008 on n=54, which is not a result. Combined with
the n=54 run in `real-corpus-large.md` (min-max 0.668, sigmoid 0.666) the honest summary is
unchanged and now rests on two real comparisons that disagree in sign and agree in magnitude:
**the normaliser choice does not move recall on real queries at this sample size.** The
degeneracy sigmoid fixes remains provable in isolation, which is why it is not reverted.

## The closet prior, again

`admitted 0, unreachable 0, ΔMRR +0.000, moved 0` — no case in this corpus has a curated closet,
so the arm remains unmeasurable rather than measured-neutral. Same null as every prior run, and
for the same reason.

## What this does NOT say

- **Nothing about the shipped defaults.** This ran `RERANK_POOL=20` against a shipped 10, and
  `RETRIEVE_K=30` against a shipped 0. Neither shipped value was measured here.
- **Nothing about the served weight.** Production runs `weight=0.75`; this ran 0.50. The weight
  sweep is still owed under the shipped normaliser.
- **Nothing about latency.** `RERANK_TIMEOUT` was set to 120s precisely so budget overruns could
  not confound the ranking numbers. At the shipped 10s a majority of these calls would have
  failed open — see the compose comment and ADR-034.
