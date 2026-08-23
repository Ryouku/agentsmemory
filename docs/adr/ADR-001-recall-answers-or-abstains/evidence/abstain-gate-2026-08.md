# Abstention gate — run of 2026-08-21/22

**Decision: neither ship nor withdraw. T3's preflight disqualifies this corpus, so
the go/no-go cannot be taken here.** T4, T5 and T6 are not started.

## The preflight, which fails

T3 opens with a condition to check *before* anything else:

> If the answer is already in the pool for essentially every case, the
> answer-recall ≥ 0.95 constraint is met at a trivially low threshold and
> `--gate` exits 0 without testing anything — a pass here is not evidence, it is
> the instrument agreeing with itself.

The eval's own ceiling line, this run:

```
retrieval ceiling — where the answer sits by VECTOR DISTANCE alone:
  in pool: 100%   top-1 80%   top-5 92%   top-10 96%   top-20 96%   top-50 100%
```

**100% in pool.** That is the saturated state the preflight names, and T3 says
plainly what to do about it: run against the maintainer's ~5,020-drawer corpus,
or against this one once its ceiling is no longer saturated. Neither is available
here — this palace holds 449 memories, and the eval's candidate pool of 50 is 11%
of the entire corpus, so near-total pool recall is close to guaranteed by
arithmetic rather than earned by retrieval.

## What was measured anyway, and how much of it survives the preflight

Both populations from one corpus and one build, production arm scoring every case,
live cross-encoder.

| | |
|---|---|
| corpus | 449 memories |
| reachable-answerable | 21 (25 generated; the production arm scored 21) |
| verified-absent | 17 of 25 generated — 8 rejected because another memory answered them at depth 20 |
| unreachable | 0 |
| `answer_at` | −3.1407 (target recall 0.95, achieved 0.952) |
| `refuse_below` | −3.1407 — **the band is EMPTY**, both rules chose one threshold |
| absent below `refuse_below` | 0 of 17 |
| correct refusal at `answer_at` | **3 / 17 = 0.176** |
| 90% Wilson lower bound | **0.073** against the declared **0.30** |
| `eval --calibrate --gate` | **exit 1** |

The risk–coverage curve, and the reason a bare FAIL is not enough:

| threshold | answer recall | correct refusal |
|---|---|---|
| −5.3368 | 1.000 (25) | 0.000 (0) |
| −3.4870 | 0.960 (24) | 0.118 (2) |
| **−2.0296** | **0.920 (23)** | **0.294 (5)** |
| −1.0786 | 0.840 (21) | 0.412 (7) |
| 0.7782 | 0.760 (19) | 0.765 (13) |
| 4.2419 | 0.280 (7) | 1.000 (17) |

**No threshold clears both bars.** The best point misses recall by 0.03 and
refusal by 0.006 — close enough that the declared targets, not the mechanism, are
what the verdict turns on.

**How much of this the preflight invalidates, stated precisely rather than
waved at.** Saturation makes the answer-RECALL constraint easy to satisfy, so it
would have inflated a PASS. This run FAILED, and it failed on the REFUSAL side:
`answer_at` is pinned low by the answerable scores' low tail, not by pool
membership, and pool saturation does not move a score distribution. So the failure
is about the two distributions overlapping, which is a real property of this
corpus — but whether it replicates on a corpus ten times larger is exactly what
this run cannot say, in either direction.

## The comparison T3 asks for: hard against easy negatives

Same 25 answerable questions, both negative styles, same checker.

| negatives | answerable median distance | unanswerable median | gap |
|---|---|---|---|
| `absent-easy` (identifiers stripped) | 0.364 | 0.427 | **0.063** |
| `absent` (identifiers KEPT) | 0.364 | 0.394 | **0.030** |

The hard negatives sit **half as far** from answerable questions. That gap is the
size of the error the old generator was hiding, and it is why T1 exists. Note the
rejection rate does NOT show it — 8 of 25 hard against 6 of 25 easy, two cases
apart at n=25 — so the instrument that looked like it was rejecting the change was
measuring the wrong thing.

## Which signal the gate reads, and why it is not the shapes

Measured across 25 answerable and 17 verified-absent through the production
reranker:

| signal | kind | AUC |
|---|---|---|
| **`top_rerank`** | **absolute** | **0.81** |
| `top_gap` | contrastive | 0.71 |
| `score_spread` | contrastive | 0.70 |
| `dist_gap` | contrastive | 0.69 |
| `dist_spread` | contrastive | 0.61 |

A separate sweep of nine signals across three families (inclusion, exclusion,
null-model), plus their combinations under leave-one-out, ceilings at ~0.80: the
best single is `top_rerank` at 0.793 and adding an IDF term-coverage signal at
0.781 makes it *worse* (0.774), because the two ask the same question and err on
the same cases. **The binding constraint is not the choice of metric.**

## What would let the decision be taken

1. **A corpus whose ceiling is not saturated** — the preflight's own requirement,
   and the only one that makes a PASS meaningful.
2. **More verified-absent cases.** 17 puts a wide Wilson interval on every refusal
   rate; the ADR already names this its weakest number.
3. **Then, and only then, the targets.** 0.95/0.30 was declared before any of this
   existed and the best point misses by 0.03 and 0.006. Relaxing them after seeing
   the data is what pre-registration exists to prevent, so it is listed last and
   should be argued from what recall the system actually needs — not from what
   this sample happened to reach.

## Reproduction

```bash
agentsmemory eval --db <db> --rerank-url <tei> --rerank-pool 10 \
  --style absent --n 25 --cases absent25.jsonl
agentsmemory eval --db <db> --rerank-url <tei> --rerank-pool 10 \
  --style paraphrase --n 25 --cases answerable25.jsonl
agentsmemory eval --db <db> --rerank-url <tei> --rerank-pool 10 \
  --cases answerable25.jsonl,absent25.jsonl --calibrate --gate
```

The third command exits 1. A case set generated with `--style absent-easy` also
exits non-zero and writes no calibration file by construction — it exists only for
the comparison above.
