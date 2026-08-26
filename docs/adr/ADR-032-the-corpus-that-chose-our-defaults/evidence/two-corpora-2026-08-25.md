# Two corpora, opposite answers — 2026-08-25

> **Correction, 2026-08-26 — the sentence below is wrong and the tables disprove it.**
> The two runs did NOT share a ranking configuration. See *Amendment* at the end: corpus
> B's `rrf+rerank` and `rrf+rerank norm=sigmoid` are bit-identical, which they can only be
> if both ran the same normaliser, while corpus A's are not. The normaliser is the one
> field shown to differ; the `--pool 30`, the commit and the palace are not contradicted by
> anything in these tables, which is weaker than saying they are unaffected and is all the
> tables can support.
>
> **The corpus-inversion thesis this file exists for is untouched, and structurally so.**
> Every arm carrying the inversion — `vector`, `hybrid`, `rrf`, the `fusion bm25=*` family —
> is reconstructed with `rerankWeight = 0` by the same reset block, and `applyRerankWith`
> returns at `weight <= 0` with `bypassed reason=weight_zero` before the cross-encoder is
> called at all. Those arms cannot observe `rerankNorm` under any setting. That is a
> property of the code rather than an assurance about the runs.

Both runs were taken on the same palace, the same day, on commit `3eb4b33`, with the
~~same ranking configuration (`fusion=rrf lex-weight=n/a lex-norm=n/a closet-boost=0.00
rerank=on(pool=10,weight=0.50,norm=sigmoid) unit=memory evidence=lexical`)~~ and the same
`--pool 30`. Only the QUESTIONS differ.

- **Corpus A — `--style paraphrase`, 30 cases, replayed from `eval-stack-65aaaa7.jsonl`.**
  Questions written by a local model FROM the drawers they are meant to find. They
  therefore share vocabulary and embedding neighbourhood with their own gold.
- **Corpus B — `--style real`, 26 cases, generated from `search_events`.**
  Questions agents actually typed, replayed from telemetry; gold judged by
  `qwen2.5-coder:7b` over the retrieved pool. 40 real queries were sampled and 14
  produced no relevant memory at all, leaving 26 scorable cases.

Read the `vs best` column, not the CI overlap: it is a PAIRED bootstrap on per-case
deltas. The best arm is picked from its own table, so comparisons against it flatter it,
and `inconclusive` never means equivalence.

## The inversion

| arm | A: paraphrase MRR | B: real MRR |
|---|---|---|
| `vector` | **0.644** (tied best) | **0.580 — worst**, worse by 0.10–0.38 |
| `fusion bm25=0.00` (= vector) | 0.644 | 0.580 |
| `hybrid` (linear, bm25=0.40) | **0.485 (worst)** | **0.818** |
| `fusion bm25=0.60 anchored:ceiling` | 0.508 | **0.821 — BEST** |
| `fusion bm25=auto` | 0.486 | 0.818 |
| `rrf` | 0.533 | 0.721 |
| `rrf+rerank` (min-max) | 0.610 | 0.708 |
| `rrf+rerank norm=sigmoid` | 0.633 | 0.708 |
| `rrf+rerank norm=rank` | 0.579 | 0.748 |
| `production (Search)` | 0.592 | 0.694, worse by 0.02–0.25 |

**Vector-only goes from best to worst; lexical fusion goes from worst to best.** Both
verdicts are paired intervals excluding zero.

The mechanism is visible in corpus B's query list: real developer queries are
IDENTIFIERS — `mutatesOnlyTempPaths`, `gitTreeRefreshKind`, `AIAGENTMEMORY_BIN_DIR`,
`VECTOR_BACKEND telemetry span`. BM25 matches those exactly; an embedding blurs them.
Corpus A's questions were generated from the drawers, so they carry the drawers' own
vocabulary and the dense arm wins by construction.

## Rerank weight, monotonic on real queries

| weight | A | B |
|---|---|---|
| 0.25 | 0.517 | **0.761** |
| 0.50 (shipped) | 0.542 | 0.720 |
| 0.75 | 0.587 | 0.683 |
| 1.00 | 0.601 | 0.654 |

Monotonic and in OPPOSITE directions. `config.go:352` annotates the shipped 0.50 as
"chosen by the eval's weight sweep".

## What replicated

The closet prior costs, on both corpora, and this comparison is PRESELECTED rather than
picked from the table:

| corpus | ΔMRR | 95% paired CI | Δrecall@1 | moved |
|---|---|---|---|---|
| A paraphrase | −0.048 | [−0.11, −0.00] | −0.067 | 6 |
| B real | −0.039 | [−0.10, 0.00] | −0.077 | 3 |

Same sign, same order of magnitude, two independent question sets. It is already
`closet-boost=0.00` in production, so this is confirmation, not a change.

## Retrieval ceilings

| corpus | in pool | top-1 | top-5 | top-10 |
|---|---|---|---|---|
| A | 100% | 53% | 77% | 80% |
| B | 100% | 38% | 85% | 100% |

Both saturate in-pool, which is the state ADR-001's preflight names as disqualifying for
a go/no-go. In B everything is inside the top 10, so B measures ORDERING only — which is
what makes the fusion inversion a ranking result and not a retrieval one.

## What this does NOT show

- n is 26 and 30. Neither resolves a small effect.
- Corpus B's gold is judged by a 7B local model over the retrieved pool, so a "no relevant
  memory" verdict conflates "not there" with "the judge missed it", and 14 of 40 sampled
  queries landed there.
- The winning arm in each table was selected from that table.
- ~~The sigmoid normalisation shipped earlier the same day scores IDENTICALLY to min-max on
  corpus B (0.708 both)~~ — **RETRACTED 2026-08-26, see Amendment: that is one arm measured
  twice.** `norm=rank` scores higher (0.748, inconclusive). The
  degeneracy it fixes is provable in isolation; this corpus does not confirm it moves recall.

## Corpus A — full table (paraphrase, n=30)
```
arm                                           R@1      R@5      MRR         95% CI  not found   vs best
--------------------------------------------------------------------------------------------------------------
vector                                        53%      77%    0.644    [0.51–0.79]          0   inconclusive vs best (CI spans zero)
hybrid                                        40%      50%    0.485    [0.33–0.64]          0   worse by 0.08–0.26
hybrid+closet                                 33%      50%    0.437    [0.29–0.59]          0   worse by 0.11–0.32
rrf                                           40%      67%    0.533    [0.39–0.68]          0   worse by 0.02–0.21
fusion bm25=0.00                              53%      77%    0.644    [0.51–0.79]          0   inconclusive vs best (CI spans zero)
fusion bm25=0.20                              43%      63%    0.542    [0.40–0.69]          0   worse by 0.04–0.18
fusion bm25=0.40                              40%      50%    0.485    [0.33–0.64]          0   worse by 0.08–0.26
fusion bm25=0.60                              37%      50%    0.453    [0.30–0.61]          0   worse by 0.09–0.30
fusion bm25=auto                              40%      50%    0.486    [0.34–0.64]          0   worse by 0.08–0.26
fusion bm25=auto-idf                          47%      70%    0.605    [0.46–0.74]          0   inconclusive vs best (CI spans zero)
fusion+recency band=0.02                      40%      50%    0.486    [0.34–0.64]          0   worse by 0.08–0.26
fusion+recency band=0.05                      40%      50%    0.485    [0.33–0.64]          0   worse by 0.08–0.26
fusion+recency band=0.10                      40%      50%    0.485    [0.34–0.64]          0   worse by 0.08–0.26
fusion bm25=0.20 anchored:ceiling             53%      77%    0.645    [0.50–0.79]          0   BEST over case set cs-3b9fd4c5f26e (replayed)
fusion bm25=0.40 anchored:ceiling             43%      60%    0.528    [0.38–0.68]          0   worse by 0.05–0.20
fusion bm25=0.60 anchored:ceiling             43%      50%    0.508    [0.36–0.66]          0   worse by 0.06–0.23
fusion bm25=auto anchored:ceiling             47%      60%    0.554    [0.40–0.71]          0   worse by 0.03–0.17
fusion bm25=auto-idf anchored:ceiling         47%      77%    0.621    [0.49–0.76]          0   inconclusive vs best (CI spans zero)
fusion bm25=0.20 anchored:saturating          50%      77%    0.632    [0.49–0.77]          0   inconclusive vs best (CI spans zero)
fusion bm25=0.40 anchored:saturating          47%      67%    0.575    [0.43–0.72]          0   worse by 0.02–0.14
fusion bm25=0.60 anchored:saturating          43%      53%    0.516    [0.37–0.67]          0   worse by 0.05–0.22
fusion bm25=auto anchored:saturating          47%      67%    0.582    [0.44–0.73]          0   worse by 0.01–0.13
fusion bm25=auto-idf anchored:saturating      47%      77%    0.622    [0.49–0.76]          0   inconclusive vs best (CI spans zero)
rrf+rerank norm=sigmoid                       50%      77%    0.633    [0.49–0.77]          0   inconclusive vs best (CI spans zero)
rrf+rerank norm=rank                          43%      70%    0.579    [0.44–0.72]          0   inconclusive vs best (CI spans zero)
production (Search)                           50%      77%    0.592    [0.44–0.74]          7   worse by 0.01–0.11
production (Search) limit=10                  50%      73%    0.596    [0.45–0.74]          6   worse by 0.01–0.09
production (Search) retrieve-k=50             53%      77%    0.613    [0.45–0.77]          7   inconclusive vs best (CI spans zero)
rrf+rerank                                    50%      73%    0.610    [0.46–0.75]          0   inconclusive vs best (CI spans zero)
hybrid+rerank                                 43%      67%    0.542    [0.40–0.69]          0   worse by 0.04–0.19
hybrid+closet+rerank                          43%      67%    0.539    [0.39–0.69]          0   worse by 0.04–0.19
rerank blend w=0.25                           43%      53%    0.517    [0.37–0.67]          0   worse by 0.05–0.22
rerank blend w=0.50                           43%      67%    0.542    [0.40–0.69]          0   worse by 0.04–0.19
rerank blend w=0.75                           47%      67%    0.587    [0.44–0.73]          0   worse by 0.00–0.12
rerank blend w=1.00                           50%      67%    0.601    [0.45–0.75]          0   inconclusive vs best (CI spans zero)
n=30 — CI column: single-arm bootstrap; 'vs best' verdicts: PAIRED bootstrap on per-case deltas (trust these, not CI overlap). The best arm was picked from this same table, so unadjusted comparisons against it flatter the winner; 'inconclusive' means exactly that, never equivalence
```

## Corpus B — full table (real recorded queries, n=26)
```
arm                                           R@1      R@5      MRR         95% CI  not found   vs best
--------------------------------------------------------------------------------------------------------------
vector                                        38%      85%    0.580    [0.45–0.72]          0   worse by 0.10–0.38
hybrid                                        73%      96%    0.818    [0.69–0.92]          0   inconclusive vs best (CI spans zero)
hybrid+closet                                 65%      96%    0.779    [0.65–0.89]          0   inconclusive vs best (CI spans zero)
rrf                                           54%     100%    0.721    [0.60–0.84]          0   worse by 0.00–0.21
fusion bm25=0.00                              38%      85%    0.580    [0.45–0.72]          0   worse by 0.10–0.38
fusion bm25=0.20                              62%      96%    0.770    [0.64–0.88]          0   inconclusive vs best (CI spans zero)
fusion bm25=0.40                              73%      96%    0.818    [0.69–0.92]          0   inconclusive vs best (CI spans zero)
fusion bm25=0.60                              69%      96%    0.798    [0.67–0.91]          0   inconclusive vs best (CI spans zero)
fusion bm25=auto                              73%      96%    0.818    [0.69–0.92]          0   inconclusive vs best (CI spans zero)
fusion bm25=auto-idf                          54%      96%    0.723    [0.60–0.84]          0   worse by 0.02–0.19
fusion+recency band=0.02                      73%      96%    0.817    [0.69–0.92]          0   inconclusive vs best (CI spans zero)
fusion+recency band=0.05                      69%      96%    0.798    [0.67–0.91]          0   inconclusive vs best (CI spans zero)
fusion+recency band=0.10                      69%      96%    0.798    [0.67–0.91]          0   inconclusive vs best (CI spans zero)
fusion bm25=0.20 anchored:ceiling             54%     100%    0.731    [0.61–0.84]          0   worse by 0.01–0.18
fusion bm25=0.40 anchored:ceiling             58%      96%    0.738    [0.61–0.86]          0   worse by 0.02–0.17
fusion bm25=0.60 anchored:ceiling             73%      96%    0.821    [0.69–0.93]          0   BEST over case set cs-d9ee5ccc1105 (generated)
fusion bm25=auto anchored:ceiling             58%      96%    0.738    [0.61–0.86]          0   worse by 0.02–0.17
fusion bm25=auto-idf anchored:ceiling         50%     100%    0.706    [0.59–0.82]          0   worse by 0.00–0.23
fusion bm25=0.20 anchored:saturating          50%     100%    0.702    [0.58–0.82]          0   worse by 0.01–0.23
fusion bm25=0.40 anchored:saturating          58%      96%    0.744    [0.62–0.86]          0   worse by 0.01–0.17
fusion bm25=0.60 anchored:saturating          65%      96%    0.789    [0.67–0.90]          0   inconclusive vs best (CI spans zero)
fusion bm25=auto anchored:saturating          58%      96%    0.744    [0.62–0.86]          0   worse by 0.01–0.17
fusion bm25=auto-idf anchored:saturating      42%     100%    0.663    [0.55–0.78]          0   worse by 0.03–0.28
rrf+rerank norm=sigmoid                       54%     100%    0.708    [0.58–0.83]          0   worse by 0.00–0.23
rrf+rerank norm=rank                          62%     100%    0.748    [0.62–0.87]          0   inconclusive vs best (CI spans zero)
production (Search)                           54%      96%    0.694    [0.56–0.82]          1   worse by 0.02–0.25
production (Search) limit=10                  54%     100%    0.708    [0.58–0.83]          0   worse by 0.00–0.23
production (Search) retrieve-k=50             54%      96%    0.700    [0.56–0.83]          1   worse by 0.01–0.23
rrf+rerank                                    54%     100%    0.708    [0.58–0.83]          0   worse by 0.00–0.23
hybrid+rerank                                 58%      96%    0.720    [0.58–0.85]          0   inconclusive vs best (CI spans zero)
hybrid+closet+rerank                          58%      96%    0.726    [0.59–0.85]          0   inconclusive vs best (CI spans zero)
rerank blend w=0.25                           62%      96%    0.761    [0.64–0.88]          0   inconclusive vs best (CI spans zero)
rerank blend w=0.50                           58%      96%    0.720    [0.58–0.85]          0   inconclusive vs best (CI spans zero)
rerank blend w=0.75                           54%      92%    0.683    [0.54–0.82]          0   worse by 0.02–0.27
rerank blend w=1.00                           50%      92%    0.654    [0.51–0.79]          0   worse by 0.04–0.30
n=26 — CI column: single-arm bootstrap; 'vs best' verdicts: PAIRED bootstrap on per-case deltas (trust these, not CI overlap). The best arm was picked from this same table, so unadjusted comparisons against it flatter the winner; 'inconclusive' means exactly that, never equivalence
```


---

## Amendment — 2026-08-26: corpus B's min-max control was a second sigmoid arm

A review of #57 found that `serviceForArm`'s reset block clears nine ranking knobs and
not `rerankNorm`, so a cloned arm inherits whatever normaliser the server running the eval
was set to. `rrf+rerank` sets no normaliser and `rrf+rerank norm=sigmoid` sets one, so
whenever the served value is already sigmoid the two arms are one configuration under two
names. Fixed at `e20890e`; the gate is
`TestEvalArmsAreDISTINCTCONFIGURATIONSNotJustDistinctNames`.

**What makes this provable rather than suspected: this file contains its own control.**
`vector` and `fusion bm25=0.00` are the same configuration by construction — the bm25
sweep's zero endpoint IS vector-only — and they score bit-identically in BOTH corpora
(53%/77%/0.644 in A, 38%/85%/0.580 in B). So in this eval, identical rows mean identical
configuration; they are not noise. That control is what turns corpus B's identity from a
coincidence into a proof.

Applying it to all three tables this ADR rests on:

> **Second correction, 2026-08-26 — the "differ" half of this table's rule is now known to be
> unsafe.** A later run contained a duplicate pair of RERANKED arms — the same configuration
> under two names — and they scored 0.709 against 0.700, 2 points of R@1 apart. So two identical
> reranked configurations CAN differ, and "the arms differ, therefore the configurations
> differed" does not hold for any arm that calls the cross-encoder. Rows A and n=54 below rest on
> exactly that inference and are **not established**; row B, which rests on identity rather than
> difference, still stands and is independently corroborated by reading `serviceForArm`. The B1
> defect was found in the source, not in these tables, so it is unaffected. See
> `reranked-arms-are-not-deterministic-2026-08-26.md`.

| run | `rrf+rerank` | `rrf+rerank norm=sigmoid` | so min-max… |
|---|---|---|---|
| A — paraphrase, n=30 | 0.610 (R@5 73%) | 0.633 (R@5 77%) | ~~**ran**~~ **not established** — 0.023 is inside the measured noise |
| B — real, n=26 | 0.708 (R@5 100%) | 0.708 (R@5 100%) | **never ran** — identical in every column |
| real, n=54 (`real-corpus-large.md`) | 0.668 | 0.666 | ~~**ran**~~ **not established** — 0.002 is far inside the measured noise |

So the defect voids ONE table, not the ADR. Corpus B has no min-max control and its
`rrf+rerank` row must be read as a duplicate of the sigmoid row. The other two runs
compared the normalisers for real.

**What survives.** On real queries the comparison is n=54, not "measured twice": min-max
0.668 against sigmoid 0.666, a difference of 0.002 with both inconclusive against best.
The conclusion that sigmoid is neutral on real queries is unchanged — it simply rests on
one measurement instead of two, and one of the two it was thought to rest on was an arm
compared with itself.

**Why the served normaliser differed between two runs on one commit is not recoverable —
but one candidate cause is now RULED OUT.** Neither run's startup log survives, and the
eval's `ranking:` line — which prints the resolved normaliser and would have settled it in
one grep — was not captured into this file.

An earlier version of this amendment offered "the runs straddled ADR-030 changing the
served default" as the likely cause. **That is disproven by this file's own header.** Both
runs are recorded on commit `3eb4b33`, and `3eb4b33` is a descendant of `96b505f`, the
commit that made sigmoid the default — verified with `git merge-base --is-ancestor`. A
default-configured binary at that commit runs sigmoid in BOTH runs, which would have made
corpus A's two arms identical as well, and they are not. Whatever differed was
environmental — `RERANK_NORM` set explicitly for one run, or a stale binary — not the
commit. The correction is left visible rather than swapped in, because a wrong cause that
gets quietly replaced teaches nobody why it was wrong. **Evidence files from here on should paste the
`ranking:` line**, which costs one line and would have made this whole reconstruction a
lookup.
