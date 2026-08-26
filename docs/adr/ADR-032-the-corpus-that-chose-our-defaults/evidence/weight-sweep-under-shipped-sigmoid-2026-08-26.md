# The weight sweep under the SHIPPED normaliser — 2026-08-26

Every previous `RERANK_WEIGHT` sweep in this corpus predates ADR-030 making sigmoid the served
default, so each measured the knob under min-max. ADR-030's amendment records that this matters:
under min-max both blend axes filled `[0,1]`, while under the shipped policy both ranges are
data-dependent, so the weight no longer divides two comparable quantities. This is the first
sweep taken under the normaliser production actually runs.

Same run and provenance as `reranked-arms-are-not-deterministic-2026-08-26.md`: 54 real cases,
`--pool 100`, `RERANK_POOL=20`, `RETRIEVE_K=30`, served `norm=sigmoid`. Because the binary
predates `e20890e`, every cloned arm inherits the served normaliser — which is wrong for the
min-max control and exactly RIGHT for a weight sweep, whose whole purpose is to vary the weight
at a fixed, production-shaped normaliser.

| weight | R@1 | R@5 | MRR | verdict |
|---|---|---|---|---|
| **0.25** | 59% | 89% | **0.729** | BEST over the case set |
| 0.50 | 57% | 93% | 0.721 | inconclusive vs best |
| **0.75 — production** | 56% | 93% | 0.701 | inconclusive vs best |
| 1.00 | 52% | 89% | 0.678 | inconclusive vs best |

**Monotonic decline with weight, same direction the pre-sigmoid sweeps found.** The served
value, 0.75, sits third of four. Nothing here resolves: every row below the top is
`inconclusive` on the paired bootstrap, and the 0.25-to-0.75 gap of 0.028 is only about three
times the reproducibility floor measured in the sibling file — so this is a direction, not a
verdict.

## What it means for the deployment question

The open question was whether production's `RERANK_WEIGHT=0.75` should move. This says:

- the direction that argued for lowering it **survives** the change of normaliser, which was the
  specific doubt ADR-030's amendment raised;
- it is still not resolved at n=54, and a single sweep whose best-vs-served gap is ~3x the noise
  floor is not grounds for changing a served default;
- the honest next step is a larger case set or a paired re-run, not a config change.

Recorded as evidence, not as a recommendation. Whoever decides the served value should read this
next to the note that no run has ever measured the shipped `RERANK_POOL` of 10.

## Also in this run

`hybrid+rerank` 0.721 and `hybrid+closet+rerank` 0.711 — the closet prior costs 0.010 here, in
the same band as the noise floor, on a corpus where no case has a curated closet. Unchanged
finding, unchanged reason.
