# T1 — the larger real corpus, n=54 — 2026-08-26

70 recorded searches sampled from `search_events` across all wings, judged by
`qwen2.5-coder:7b`, 54 scorable (23% attrition). Commit `733f233`, `--pool 30`,
same ranking configuration as the two runs in `two-corpora-2026-08-25.md`.

## Verdict: T2's precondition FAILS. Nothing flips.

`rrf` is **inconclusive** against the linear arms at n=54. The contrast this run was
bought to resolve did not resolve, so `FUSION` and `RERANK_WEIGHT` stay as they are.
That outcome was pre-registered in T2's Precondition before this run started.

## What the larger sample did to the n=26 claims

| contrast | n=26 | n=54 | survived? |
|---|---|---|---|
| `vector` worst | 0.580, worse by 0.10–0.38 | 0.587, worse by 0.01–0.20 | **YES** — still excludes zero |
| `hybrid` | 0.818 | 0.647 | magnitude collapsed |
| `rrf` | 0.721, worse by 0.00–0.21 | 0.636, **inconclusive** | **NO** |
| `production (Search)` | 0.694, worse by 0.02–0.25 | 0.660, **inconclusive** | **NO** |
| best arm | `anchored:ceiling` 0.821 | `rerank blend w=0.25` 0.694 | different arm |

**The ~0.12 MRR fusion gap was substantially noise.** At n=26 the spread across
`vector`/`rrf`/`hybrid` was 0.580→0.721→0.818; at n=54 it is 0.587→0.636→0.647. The
ordering is the same and the magnitude is four times smaller. ADR-032's Context is
amended accordingly: the corpus inversion is real, the size of the effect it implied
for `FUSION` was not.

## What replicated, and is now stronger

**The closet prior costs — third independent measurement, and the first to resolve.**
This comparison is PRESELECTED, not chosen from the table it appears in.

| corpus | n | ΔMRR | 95% paired CI | Δrecall@1 | moved |
|---|---|---|---|---|---|
| paraphrase | 30 | −0.048 | [−0.11, −0.00] | −0.067 | 6 |
| real | 26 | −0.039 | [−0.10, 0.00] | −0.077 | 3 |
| **real (this run)** | **51 admitted** | **−0.027** | **[−0.06, −0.00]** | **−0.039** | **7** |

Same sign three times, shrinking magnitude, interval now excluding zero. It is already
`closet-boost=0.00` in production, so this is confirmation rather than a change.

**`vector`-only is genuinely the worst arm on real queries.** Resolved at both n=26 and
n=54. This is the part of the corpus inversion that survives.

**ADR-030's sigmoid is neutral on real queries, measured ONCE.** 0.666 against min-max's
0.668 here. ~~0.708 against 0.708 at n=26~~ — **retracted 2026-08-26**: at n=26 the served
normaliser was already sigmoid and `serviceForArm` did not reset it, so `rrf+rerank` was a
second sigmoid arm rather than the min-max control. Identical rows there are one arm
measured twice, not a replication. See the Amendment in `two-corpora-2026-08-25.md`; fixed
at `e20890e`.

The n=54 comparison above is unaffected — its two rerank arms differ (0.668 vs 0.666), and
`vector`/`fusion bm25=0.00`, which are the same configuration by construction, are
bit-identical at 0.587, so in this run identity means same-config and difference means the
control genuinely ran. The degeneracy sigmoid fixes is provable in isolation and no corpus
has shown it moves recall. It is not reverted — reverting on an inconclusive result is the
same error as adopting on one — but the evidence for neutrality is one run, not two.

**Lower rerank weight remains suggestive and unresolved.** `w=0.25` is the top arm in both
real runs (0.761 at n=26, 0.694 here) but it is the arm the table selected, so the
comparison flatters it, and `w=0.50` is inconclusive against it.

## The instrument got better, and this is the first corpus that can fail

| corpus | in pool | top-1 | top-5 | top-10 |
|---|---|---|---|---|
| paraphrase n=30 | 100% | 53% | 77% | 80% |
| real n=26 | 100% | 38% | 85% | 100% |
| **real n=54** | **94%** | 46% | 76% | 83% |

Both earlier corpora measured 100% in-pool — the saturated state ADR-001's preflight names
as disqualifying, because it makes the ceiling arithmetic rather than retrieval. **This one
does not.** Three of 54 answers were never retrieved by any arm, which the run reports as a
retrieval failure distinct from a ranking one, and `production` lost a further five below
its page cut.

That distinction is the most useful thing this run produced: 3 cases no ranking change can
reach, and 5 that a wider page or a larger `RERANK_POOL` could.

## What this run still cannot settle

- Gold is judged by a 7B model over the retrieved pool, so absolute figures are
  judge-limited. Arm-vs-arm comparisons are safe: every arm faces the same gold.
- The best arm was selected from this table, so `w=0.25` at 0.694 is flattered.
- 54 cases still leaves most contrasts inconclusive. That is the finding, not a defect in
  the run — and enlarging until a preferred contrast resolves is p-hacking with extra steps.

## Full table
```
arm                                           R@1      R@5      MRR         95% CI  not found   vs best
--------------------------------------------------------------------------------------------------------------
vector                                        46%      76%    0.587    [0.48–0.70]          3   worse by 0.01–0.20
hybrid                                        52%      85%    0.647    [0.54–0.75]          3   inconclusive vs best (CI spans zero)
hybrid+closet                                 48%      85%    0.622    [0.52–0.72]          3   worse by 0.02–0.13
rrf                                           54%      83%    0.636    [0.53–0.75]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.00                              46%      76%    0.587    [0.48–0.70]          3   worse by 0.01–0.20
fusion bm25=0.20                              48%      85%    0.631    [0.53–0.73]          3   worse by 0.01–0.12
fusion bm25=0.40                              52%      85%    0.647    [0.54–0.75]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.60                              50%      83%    0.640    [0.54–0.74]          3   worse by 0.00–0.11
fusion bm25=auto                              52%      85%    0.647    [0.54–0.75]          3   inconclusive vs best (CI spans zero)
fusion bm25=auto-idf                          50%      83%    0.640    [0.54–0.74]          3   inconclusive vs best (CI spans zero)
fusion+recency band=0.02                      52%      85%    0.645    [0.54–0.75]          3   inconclusive vs best (CI spans zero)
fusion+recency band=0.05                      50%      85%    0.632    [0.53–0.74]          3   worse by 0.01–0.12
fusion+recency band=0.10                      50%      85%    0.629    [0.52–0.74]          3   worse by 0.01–0.12
fusion bm25=0.20 anchored:ceiling             50%      83%    0.646    [0.55–0.75]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.40 anchored:ceiling             50%      85%    0.642    [0.54–0.74]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.60 anchored:ceiling             52%      85%    0.650    [0.55–0.75]          3   inconclusive vs best (CI spans zero)
fusion bm25=auto anchored:ceiling             50%      85%    0.642    [0.54–0.74]          3   inconclusive vs best (CI spans zero)
fusion bm25=auto-idf anchored:ceiling         52%      83%    0.647    [0.55–0.75]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.20 anchored:saturating          56%      83%    0.672    [0.57–0.78]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.40 anchored:saturating          46%      85%    0.627    [0.53–0.73]          3   inconclusive vs best (CI spans zero)
fusion bm25=0.60 anchored:saturating          50%      83%    0.642    [0.54–0.74]          3   worse by 0.00–0.10
fusion bm25=auto anchored:saturating          46%      85%    0.627    [0.53–0.73]          3   inconclusive vs best (CI spans zero)
fusion bm25=auto-idf anchored:saturating      54%      80%    0.654    [0.55–0.76]          3   inconclusive vs best (CI spans zero)
rrf+rerank norm=sigmoid                       52%      89%    0.666    [0.57–0.77]          3   inconclusive vs best (CI spans zero)
rrf+rerank norm=rank                          52%      89%    0.655    [0.56–0.76]          3   inconclusive vs best (CI spans zero)
production (Search)                           54%      85%    0.660    [0.55–0.76]          8   inconclusive vs best (CI spans zero)
production (Search) limit=10                  52%      89%    0.665    [0.57–0.77]          5   inconclusive vs best (CI spans zero)
production (Search) retrieve-k=50             54%      89%    0.667    [0.57–0.77]          6   inconclusive vs best (CI spans zero)
rrf+rerank                                    52%      89%    0.668    [0.57–0.77]          3   inconclusive vs best (CI spans zero)
hybrid+rerank                                 54%      85%    0.672    [0.57–0.77]          3   inconclusive vs best (CI spans zero)
hybrid+closet+rerank                          54%      85%    0.672    [0.57–0.77]          3   inconclusive vs best (CI spans zero)
rerank blend w=0.25                           57%      85%    0.694    [0.59–0.79]          3   BEST over case set cs-c3f71267f8c6 (generated)
rerank blend w=0.50                           54%      85%    0.672    [0.57–0.77]          3   inconclusive vs best (CI spans zero)
rerank blend w=0.75                           50%      87%    0.657    [0.56–0.75]          3   inconclusive vs best (CI spans zero)
rerank blend w=1.00                           52%      87%    0.655    [0.56–0.75]          3   inconclusive vs best (CI spans zero)
n=54 — CI column: single-arm bootstrap; 'vs best' verdicts: PAIRED bootstrap on per-case deltas (trust these, not CI overlap). The best arm was picked from this same table, so unadjusted comparisons against it flatter the winner; 'inconclusive' means exactly that, never equivalence
```

## Retrieval ceiling and the unreachable cases
```
retrieval ceiling — where the answer sits by VECTOR DISTANCE alone, before any arm re-orders:
  in pool: 94%   top-1 46%   top-5 76%   top-10 83%   top-20 89%   top-50 94%
  3 of 54 answer(s) were never retrieved at all — no ranking change can reach those; they need a wider pool, a different embedding, or a lexical channel that can NOMINATE candidates rather than only reorder them
  every arm above re-orders this same pool, so arm-vs-arm differences are ordering results, never retrieval ones

3 of 54 question(s) had their answer OUTSIDE the candidate pool — a retrieval failure, not a ranking one.
  No reranker can recover those. Raise --pool and re-run: if they come back, the ranking is fine and the pool was too small;
  if they stay missing, the embedding is not placing those memories near their question.

production missed 5 question(s) beyond the pool's misses — it is scored over the PAGE Search returns, not the pool, so those golds were retrieved and ranked below the page cut. Raising --pool does not move them: the knobs are the search limit and RERANK_POOL (which widens the fetch when a reranker is configured).

3 question(s) no arm retrieved (check whether the question is about the note at all):
  - HTTP MCP stdio proxy in-process CLI three transports one server
  - EVIDENCE-LIMITED completion gate escape last_assistant_message lifecycle.mjs
  - findings handed to this repository for evaluation

```
