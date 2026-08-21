# ADR-003: Retire the closet curation prior from default ranking

**Status:** Accepted
**Date:** 2026-08-19
**Owner:** Zy (with Mindaugas as upstream maintainer)
**Spec:** None — no spec stage; grounded in eval measurements and cited research.
**Cross-references:** `internal/palace/mine.go` (`closetBoosts`), `internal/palace/rank.go` (`closetRankBoosts`, `closetBoostStrength`), `internal/palace/eval.go` (`ArmHybrid` / `ArmHybridCloset`, `evalCase`, `CandidateUnion`), `internal/palace/evalstats.go` (`PairedDelta`), `cmd/server/eval.go` (the printed table and the run record), `internal/config/config.go` (`ClosetBoost`), `docs/adr/ADR-001-recall-answers-or-abstains.md` (judges the top result; what reaches the top is decided here), `docs/adr/ADR-002-anchor-the-lexical-score.md` (renormalises the fused score this prior is added to)
**Served-path change:** The closet prior's default flips to off, changing the served ranking of every deployment that has not set `CLOSET_BOOST` — T4, pending. Both landed tasks changed only the eval; `config.Default()` still ships `ClosetBoost: 1`.

## Context

Mining a source builds **closets**: short summaries that point back at the text they came from. `closetBoosts` (`internal/palace/mine.go:299`) searches the closet index with the query vector, and each of the top five closet hits lends **every drawer sharing that closet's source file** a rank boost — 0.40, 0.25, 0.15, 0.08, 0.04 by closet rank, faded linearly to zero as the closet approaches `closetDistanceCap` (0.6), then scaled by `CLOSET_BOOST`. One mined Claude session is one source, so a single closet match lifts on the order of fifty drawers at once.

What matched, in that arrangement, is not the memory. It is a paraphrase of the file the memory came from. Three independent lines say that costs more than it pays.

**1. Our own eval.** On the mined-transcript corpus (n=40 paraphrase questions against ~5,000 drawers) the closet prior costs **~0.10 MRR**, and 10 of the 40 golds are displaced by boosted neighbours — the correct memory is still retrieved and then pushed down the page by drawers that share a source with a closet the query happened to match. For scale on the same run: vector alone 0.831, hybrid 0.691.

**2. This code's own history.** `closetRankBoosts` carries the comment recording what the prior did before it was tamed: a flat +0.40 on a fused score living in [0,1] outranks every other signal combined, and with **exactly one closet filed** in the palace it dropped recall@1 from **92% to 17%**. That number is pre-fix — `closetDistanceCap` was 1.5, ported verbatim from the frozen Python, and with bge-m3 unrelated text sits at distance 0.60–0.71, so the cap admitted a cake recipe. The 0.6 cap and `closetBoostStrength`'s linear fade repaired the catastrophe. They did not remove the failure mode; the ~0.10 MRR in line 1 is what the repair left behind.

**3. The published ablation.** LongMemEval reports summary-as-key indexing costing **0.134 Recall@5** against indexing the raw value. (Quoted from a literature sweep run for this ADR; we have not re-derived it, and it is corroboration for our numbers rather than evidence of our own.)

Our variant is arguably the worse of the two. LongMemEval *replaces* the indexed key with a summary, so a summary's imprecision costs that one entry. We keep the raw key and **amplify** the summary's imprecision across every drawer sharing its source — the imprecision does not replace a signal, it multiplies over a whole session.

None of this says the prior is wrong everywhere. It says the prior is wrong on **our** corpus. A closet is a curation signal: it favours drawers belonging to a source somebody chose to mine and summarise. On a palace whose contents were curated by a human, that is very likely the right bias — the boost promotes what was kept on purpose. The corpus we measure is mined Claude transcripts, where the golds are uncurated by construction and the curated material is a small minority the prior keeps lifting over them. The shipped miner (`mine-claude`, 45× corpus growth on its first run) means the mined-transcript palace is what most installs will hold, so the default is currently tuned for the corpus almost nobody has.

`CLOSET_BOOST` (0..1, default 1) already exists as the knob, added when this was first measured; the regression was documented next to it and the full boost kept as the default. This ADR moves the default and nothing else.

## Existing Primitives Audit

- **`config.Config.ClosetBoost` + `--closet-boost` / `CLOSET_BOOST`** — the operator knob, range and meaning already correct. Reused; only its default value changes.
- **`palace.Service.WithClosetBoost`** — the setter, already clamped to [0,1]. Reused unchanged.
- **`Service.closetBoosts`** — already short-circuits at scale 0, skipping the closet vector search rather than only the arithmetic, so an off default also removes one vector round trip per recall. Reused; gains a scale-taking form for the eval.
- **`closetRankBoosts` / `closetBoostStrength` / `closetDistanceCap`** — kept verbatim, comments included. Nothing about the mechanism is deleted or retuned.
- **`ArmHybrid` / `ArmHybridCloset` in `internal/palace/eval.go`** — already the measurement that decides this. Reshaped, and an earlier draft of this ADR described them wrongly: it said both arms draw their boosts from the served scale. `ArmHybrid` passes `nil` (`eval.go:555`); it is `ArmHybridCloset` — together with eight arms that do not carry the name — that reads the shared slice. What is true is that the closet arm goes blind the moment the served default is off.
- **`palace.PairedDelta` / `BootstrapMRR` / `Interval`** — the paired-bootstrap machinery the gate needs already exists and is already printed. Reshaped: it is applied today only to "this arm versus the table's best arm", a baseline selected from the same data. The gate uses it on one preselected pair, per category.
- **`CandidateUnion`** — the real-query qrels are already pooled from several rankers and judged blind, sorted by id so the judge cannot infer which ranker proposed a candidate. The review that prompted this revision described those qrels as production's closet-on top 12, which was true when it was written and was fixed by commit 5c1b353 before this revision; the residual bias is its mirror image, and it is the one that flatters this ADR. All four pooled rankers pass `nil` boosts (`eval.go:820-831`), so nothing that only the closet prior would surface can be judged relevant. Reshaped: the closet-ON head joins the pool.
- **`caseFileMeta` + `resultsPath` + `writeResults`** — a case file already records the generator, style, wing and corpus that produced it, and a unique `--cases` path already yields a unique `.results.json`. Reshaped: `readCases` drops the provenance line (`eval.go:830`) and the results file records nothing about the code or the ranking configuration a run was taken under.
- **Closets as a curation surface** — built by `Mine`, purged and rebuilt per source, counted in the web merge/import summaries. Untouched: this ADR removes a closet's vote in ranking, not the closet.

## Decision

**`CLOSET_BOOST` defaults to 0.** The closet prior stops contributing to ranking unless an operator turns it on; the mechanism, the knob, the closets and their eval arm all stay exactly where they are, and `CLOSET_BOOST=1` restores today's ranking exactly.

The default lives in two places and both move together: `config.Default().ClosetBoost` (what the server and the eval CLI are built from, via the one `buildServices` composition root) and `palace.NewService`'s initial `closetBoostScale` (what anyone embedding the package gets). `internal/config` is deliberately dependency-free, so it cannot import the number — it duplicates it, exactly as it already duplicates `palace.DefaultRerankPool`. Duplication is fine; unchecked duplication is not, so a test in `cmd/server`, the one package that imports both, pins the copy to the original. A comment asking a reader to keep two numbers aligned is not a mechanism. That comment also inverts and must be rewritten: with the default at 0 the zero value of the field is no longer a silent footgun, it is the default.

**There are two traps in flipping this default, and both have to be closed before the measurement is taken.**

The first: the eval's arms take their boosts from `s.closetBoosts`, which returns nothing when the served scale is 0 (`internal/palace/mine.go:301`). The moment the default is off, the arm named `hybrid+closet` computes plain `hybrid` and reports it under the closet name. The table stays full and stops meaning anything — the same failure this repo has already paid for once, when a `production` arm was built, documented, and never appended to the arms list, which no table could show. It is a latent bug today as well: any operator already running `CLOSET_BOOST=0` is reading a closet column that measures no closets.

The second was found by review, and it says an earlier draft of this ADR was wrong. That draft proposed to close the first trap by handing "the named arms" their own full-strength boosts, and claimed nothing else would move. It would have moved twelve arm names that never say `closet` — seven of them when no reranker is configured, twelve when one is. `evalCase` builds ONE boosts slice (`internal/palace/eval.go:493`) and passes it to `hybrid+closet`, to `hybrid+closet+rerank` and the four `rerank blend w=<w>` sweeps (they share one fused pool, `eval.go:511`), and to `rrf`, `rrf+rerank`, `fusion bm25=auto`, `fusion bm25=auto-idf` and the four `fusion bm25=<w>` sweeps. Only `vector`, `hybrid` and `contextual chunks` pass `nil` (`eval.go:555`, `eval.go:640`). Every other arm has been carrying the curation prior all along without saying so in its name — and ADR-002's normaliser comparison is read off exactly those sweep and adaptive arms, so full-strength boosts would have pinned ADR-002's evidence to a closet-ON pipeline while production ends up closet-OFF. The contamination is not even uniform: `rankFused` adds the boost raw to a fused score living in [0,1] (`rank.go:438`), while `rankRRF` divides it by `rrfK` first (`rank.go:227`), so one slice means two different magnitudes depending on which arm reads it.

So closet use becomes an explicit **arm dimension** rather than an ambient input (T1). Every arm whose name does not say `closet` takes `nil`. The two that do say it take boosts computed at full strength whatever the served scale, so the mechanism stays measurable after its default is off. `ArmProduction` alone keeps reading the served scale, because it measures the deployment rather than the mechanism. One arm is added — `hybrid+rerank` — because after this flip production is closet-OFF plus the cross-encoder, and `hybrid+closet+rerank` would otherwise be the only reranked row in the table: an arm named after a configuration nobody runs, which is the defect T1 exists to remove.

### The gate

The flip is gated on a fresh run, and the run is read off one table. Every number the gate needs is printed by the eval and persisted beside it (T2), and no cell below leaves a judgement over.

**Sign convention, stated once.** Δ = MRR(`hybrid+closet`) − MRR(`hybrid`), computed per case over the cases of ONE category in ONE run, then paired-bootstrapped to a 95% interval [lo, hi] by `palace.PairedDelta`. Negative means the closet prior costs. The pair is the right one because the two arms differ in exactly one thing: both fuse at the fixed 0.4 lexical weight, and only one adds the prior. (Production fuses adaptively, so this pair measures the mechanism; `production (Search)` measures the deployment and decides nothing here.) **An interval with lo ≤ 0 ≤ hi — endpoints included — is a tie.** That is the entire tie rule. An earlier draft of this ADR authorised the flip when the closet arm was "at or below" `hybrid` and told the measuring task to stop when it was "at or above": equality satisfied both, so one number could pass and fail the same gate. No threshold is restated anywhere else in this ADR or its tasks.

**What is admitted, fixed before the run.** A Δ uses only the cases of its named category. `absent` cases never enter one — there is no gold to rank, and the correct behaviour there is an abstention, which a reordering prior cannot affect. Cases whose gold never entered the candidate pool are excluded too: both arms score 0, the retrieval ceiling already reports them separately (1 of 40 on the last run), and leaving them in pads the sample with zero deltas that drag every interval toward a tie. `temporal` and `crosslingual` are reported and decide nothing — the first is ADR-004's subject, the second is dominated by the lexical weight. Every exclusion is counted and printed beside its Δ, so none of them happens quietly.

**Case counts, fixed before the run.** 80 for each mined-corpus run; 40 for each curated-wing run, which holds ~103 drawers. The recorded ~0.10 MRR came off n=40 with no interval recorded at all, and doubling the count on the corpus that decides is the cheapest way to make a tie mean "no effect" rather than "not enough cases". The counts are declared here, before the run, and are not adjusted after a number is seen.

**Table 1 — the cells.** Mined and curated are separate invocations, so each row names the record it is read from.

| Cell | Run record | Category | Statistic | Role |
|---|---|---|---|---|
| D1 | `evidence/mined-paraphrase.cells.json` | `single` | Δ interval | decides |
| D2 | `evidence/mined-real.cells.json` | `real` | Δ interval | veto only |
| R1 | `evidence/curated-paraphrase.cells.json` | `single` | Δ interval | records; sets what T5 documents |
| R2 | `evidence/curated-real.cells.json` | `real` | Δ interval | records; sets what T5 documents |
| S1 | all four | every admitted case | `moved` — admitted cases the two arms ranked differently | instrument check, read first |
| M1 | `evidence/mined-paraphrase.cells.json` | `single` | Δrecall@1 and the displaced-gold count | recorded, never decides |

**Floors.** D1 is read only with at least 40 admitted cases; D2, R1 and R2 only with at least 10. Below ten admitted cases a paired bootstrap resamples fewer than ten distinct deltas, which restates the sample rather than estimating anything. A cell's floor is checked before its interval is read, and a run that clears its floor is read once: re-taking a measurement whose number has already been seen is out of scope in T3 for the obvious reason.

**Table 2 — what every state of every cell does.**

| Cell | hi < 0 (the prior costs) | tie: lo ≤ 0 ≤ hi | lo > 0 (the prior helps) | below the floor |
|---|---|---|---|---|
| D1 | the flip ships; T4 may start | the flip does not ship, and the ADR is withdrawn with the records attached | the ADR is withdrawn: the prior helps on the corpus it was accused on | the run is void — regenerate at a larger `--n` and re-take it; no Δ is read from a short run |
| D2 | agrees with D1; no separate effect | recorded; no effect | **veto** — the flip does not ship whatever D1 says, and the disagreement between replayed and generated queries goes back to the ADR | recorded as `n/a (n=<count>)`; no effect |
| R1 | T5 writes that the prior lost on the curated wing too | T5 writes that the curated wing was inconclusive at this size | T5 writes that the prior measured better on a curated palace, and names `CLOSET_BOOST=1` as the recommendation there | T5 writes that the curated wing was below the floor to measure |
| R2 | as R1, for the curated wing's replayed queries | as R1, for the curated wing's replayed queries | as R1, for the curated wing's replayed queries | as R1, for the curated wing's replayed queries |

**S1 is read before any Δ.** `moved > 0` in a run means the closet arm ordered at least one case differently from its closet-off twin, so the instrument measured the mechanism. `moved = 0` in all four runs means it measured nothing — no closets are filed in either wing, or T1 regressed — and no cell of Table 2 may be read until T1's tests pass again. `moved = 0` in some runs but not all is a fact about those corpora — a wing with no closets filed — so it is recorded and those runs' cells read `n/a`. A record that does not exist at all is not a state of any cell: T3's `TestClosetEvidenceIsComplete` fails before any of this is read.

**M1 is recorded and never decides.** Δrecall@1 and the displaced-gold count are the numbers the Context quotes, and they are a coarser statistic over exactly the same cases; gating on them as well would add a second tie surface and no information. They are recorded so the Context's ~0.10 MRR and 10-of-40 claims can be checked against the fresh run.

D1's row is the only place in this ADR where the flip ships, and both it and D2's veto are executable: `TestClosetFlipIsBackedByEvidence` (T4) reads the two cells out of the records and fails unless D1's interval lies entirely below zero and D2 — when it is at or above its floor — does not lie entirely above zero. The gate is a test, not a paragraph.

What would justify turning the prior back on is stated in the same place as the knob: a run on your own palace where the eval's closet block puts `hybrid+closet` above `hybrid` on `single` and `real`. That is one command, it is the block T1 and T2 make trustworthy, and it is the reason the mechanism is not being deleted.

## Alternatives Considered

- **Keep the default at 1 and document the knob harder** — the status quo, chosen when the regression was first measured. Rejected: the default is what nearly every palace runs, the miner we ship produces exactly the corpus the default is wrong for, and a documented regression that stays on by default is a regression with a footnote.
- **Delete `closetBoosts` and the closet rank constants outright.** Rejected: three lines of evidence say the prior is wrong on mined corpora and none of them says it is wrong on curated ones. Deleting the mechanism deletes the experiment that could tell us, and the arm costs nothing to keep.
- **A small non-zero default (say 0.25) as a compromise.** Rejected: nothing measured 0.25. It is a number picked to feel safe, which is precisely how `closetDistanceCap` arrived at 1.5 and `max_distance` at its inherited value. Any non-zero default has to come off a table.
- **Scale the prior automatically from corpus composition** (share of curated vs mined drawers, or closet coverage). Rejected as unmeasured: this is another adaptive rule of the same family as `BM25_WEIGHT=auto`, which measured *worse* than the fixed weight on paraphrase queries until IDF weighting was added. An adaptive rule invented without a table is how that happened. (That comparison was itself taken on arms silently carrying the closet prior — see the second trap below — so it dates the lesson rather than the numbers.)
- **Normalise the boost by source fan-out** — divide it by how many drawers share the source, so a fifty-part session cannot lift fifty drawers on one closet hit. This is the most direct answer to the amplification argument and is genuinely plausible. Rejected *here* because it is a new ranking formula with no run behind it, and because it should be measured against a default that is off rather than folded into the same change.
- **Concatenate the closet summary into the indexed text at mine time instead of using it as a rank-time prior.** LongMemEval reports +9.4% recall for the concatenated variant, so this has published support pointing the other way. Rejected here and deferred, not dismissed: it is a different mechanism at a different stage, it changes what gets embedded and so requires re-indexing every mined source, and it deserves its own measurement rather than riding along with a default flip.

## Component / Boundary Impact

| Component | Change | One reason to change? |
|---|---|---|
| `internal/config` | one default value and the comment explaining it | yes — it owns config defaults |
| `internal/palace` (`service.go`) | the library default for `closetBoostScale` | yes — it owns ranking |
| `internal/palace` (`mine.go`, `eval.go`, `evalstats.go`) | a scale-taking form of `closetBoosts`; closet use as an arm dimension; the preselected per-category paired statistic; the closet head in the judged pool | yes — `eval.go` and `evalstats.go` own measurement |
| `cmd/server/eval.go` | prints the closet block, records what a run was taken under, keeps the case file's provenance | yes — it owns the operator-facing view of a measurement |
| `cmd/server` (`main.go`) | applies the configured scale unconditionally, logs when the prior is on, flag help states the new default | yes — the composition root |
| `README.md`, `internal/web/ai/landing.md`, `internal/web/views` | prose describing default recall must match what ships | yes — they are the operator-facing description |

No new component, no module moves, nothing deleted. This repo has no `docs/architecture.md`; this ADR does not move a boundary, so it does not block on one.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `CLOSET_BOOST` env / `--closet-boost` flag | default 1 → 0; range, clamping and meaning unchanged | operator | `cmd/server`, `palace.Service` |
| `config.Default().ClosetBoost` | 1 → 0 | `internal/config` | `cmd/server` (server + eval CLI) |
| `palace.NewService` initial `closetBoostScale` | 1 → 0 | `internal/palace` | anything embedding the package, including tests |
| `SearchHit.ClosetBoost` / `closet_boost` in the `am_search` response | field unchanged; its value is 0 for every hit under the new default | `palace.Service.Search` | MCP clients, eval |
| eval arm semantics | `hybrid+closet` and `hybrid+closet+rerank` boost at full strength regardless of the served scale; `rrf`, `rrf+rerank`, `fusion bm25=auto`, `fusion bm25=auto-idf`, the four `fusion bm25=<w>` sweeps and the four `rerank blend w=<w>` sweeps stop carrying the prior they never named | `palace.evalCase` | operator reading the table; ADR-002, whose normaliser evidence is read off those arms |
| eval arm `hybrid+rerank` | new arm: the cross-encoder over a closet-off fused pool, which is what production becomes after this flip | `palace.evalArms` | operator reading the table |
| `palace.EvalCaseResult.PoolRank` | new field, so a case whose gold never entered the pool can be excluded from a paired statistic | `palace.EvaluateWith` | the closet statistic, the results file |
| `palace.ClosetDelta` | new: the preselected `hybrid+closet` minus `hybrid` interval for one category | `internal/palace` | `cmd/server/eval.go`, the run record |
| `palace.CandidateUnion` | pools the closet-ON fused head as well, so the real-query gold is blind to this decision | `palace.CandidateUnion` | `--style real` case generation |
| `<cases-stem>.cells.json` | new run record beside the results file: commit sha, ranking configuration, admitted/excluded counts and the closet cells, with no case text | `cmd/server/eval.go` | this ADR's evidence directory, `TestClosetFlipIsBackedByEvidence` |
| `palace.Service.ClosetBoostScale()` | new read-back, so the composition root's wiring can be tested rather than read | `palace.Service` | `cmd/server` tests |
| startup log | logs the closet prior only when it is on, naming the scale | `cmd/server` | operator |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `closetBoostsAt` — closet boosts at a caller-supplied scale | T1 | T2 | No — unexported; `closetBoosts` keeps its signature and callers |
| `armBoosts` — the arm → boosts classification every rank call goes through | T1 | T2 | No — unexported, but it changes what twelve existing arm names mean |
| `ArmHybridRerank` — the closet-off reranked arm | T1 | T2 | No — a new arm; nothing reads it yet |
| `palace.ClosetDelta` + `EvalCaseResult.PoolRank` — the preselected statistic and the field it excludes on | T2 | T3, T4 | No — additive |
| `<cases-stem>.cells.json` — the run record the truth table is read from | T2 | T3, T4 | No — a new artifact |
| The four `cells.json` records under `evidence/` | T3 | T4 | No — evidence, not code; it is the gate T4 runs behind, and `TestClosetFlipIsBackedByEvidence` reads it |
| `config.Default().ClosetBoost` = 0 and the matching `palace.DefaultClosetBoost` | T4 | T5 | Yes — every existing deployment's ranking changes unless it sets `CLOSET_BOOST=1` |

## Implementation

Five tasks — see [`ADR-003-retire-the-closet-prior/tasks/README.md`](ADR-003-retire-the-closet-prior/tasks/README.md).

They are strictly sequential and each gates the next: T1 makes every arm mean what its name says, T2 makes the run reproducible and its deciding statistic preselected, T3 takes the four runs, T4 flips the default the records justify, T5 makes the prose match what ships. T3 is the task that can end the ADR — two of the four outcome rows in Table 2 withdraw it, and nothing after T3 should be built when they fire.

## Consequences

- **Positive:** on the corpus this project's own miner produces, recall stops being reordered by a summary of a neighbouring memory's source file — by the cost D1 records. The last run put that at ~0.10 MRR and 10 of 40 displaced golds; T3 re-takes it, and D1's row decides whether the flip ships at all.
- **Positive:** one fewer vector search per recall. `closetBoosts` returns early at scale 0, so the default path drops a round trip to the closet namespace on every query.
- **Positive:** the closet arm becomes trustworthy at any served scale, which is what makes "turn it back on if it wins on your palace" a real instruction rather than an invitation to read a lying table.
- **Positive:** twelve arm names stop carrying a curation prior they never mentioned, so a lexical-weight or fusion decision read off that table is a decision about lexical weight or fusion.
- **Negative:** operators on curated palaces lose a prior that may have been helping them, silently, until they read the changelog. Mitigated by the flag help, the README and a startup log line, but a default change is a default change and some installs will not notice.
- **Negative:** `closet_boost` is 0 in every `am_search` response by default, so a client rendering that field shows a column of zeros and looks broken. The field stays because it is populated the moment the prior is on.
- **Negative:** every number those twelve arms produced before T1 was taken closet-ON, including the sweep and adaptive numbers ADR-002 reads its normaliser comparison off. Those numbers do not carry across T1 and the comparison has to be re-taken — under whichever closet setting production ends up in, which is what this ADR's D1 cell decides. T2's run record makes an old table identifiable by the commit it was taken at.
- **Negative:** the eval gets slower. A second fused pool means a second cross-encoder pass per case for the reranked arms, exactly as `rrf+rerank` already pays, and pooling a fifth ranker adds judged candidates per real query.
- **Neutral:** closets are still mined, stored, purged and rebuilt exactly as before, and are still counted in import and merge summaries. Turning the prior back on needs no re-mine.
- **Neutral:** `vector`, `hybrid` and `contextual chunks` score identically before and after — they already passed `nil`. An earlier draft of this ADR made the stronger claim that *every* non-closet arm would be unmoved, and that a moved `hybrid` or `vector` number meant a wiring error. That was wrong: twelve other arm names were reading the closet slice, and their numbers move by design in T1.

## Out of Scope

- Concatenating the closet summary into the indexed text at mine time, the LongMemEval +9.4% variant (deferred: docs/adr/BACKLOG.md)
- Normalising the closet boost by how many drawers share a source (deferred: docs/adr/BACKLOG.md)
- Choosing the prior automatically from corpus composition (deferred: docs/adr/BACKLOG.md)
- Re-taking ADR-002's normaliser comparison on arms that no longer carry the closet prior (deferred: docs/adr/ADR-002-anchor-the-lexical-score.md)
- Removing `closetBoosts`, `closetRankBoosts`, `closetBoostStrength` or the `hybrid+closet` arm (permanent: the mechanism is the experiment that could justify reversing this ADR; only its default moves.)
- Closets as a curation and browsing surface — mining, purge-on-re-mine, import/merge counts (permanent: this ADR removes a closet's vote in ranking, not the closet.)
- The lexical fusion weight, RRF, and the reranker blend (permanent: each has its own arm and its own decision; this ADR moves one prior.)
- Re-deriving the LongMemEval figures ourselves (permanent: they are cited as corroboration; the decision rests on our own runs, and T3 re-takes those.)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The measurement that would justify reversing this disappears with the default | Certain, without T1 | High | T1 keeps the closet-named arms at full strength whatever the served scale; `ArmProduction` still shows the served reality |
| ~0.10 MRR at n=40 is inside the noise | Med | Med | D1 is a paired-bootstrap interval at n=80 with a floor of 40 admitted cases, and a tie withdraws the ADR rather than shipping it |
| The gate gets read loosely — "close enough", "the direction is right" | Med | High | Table 2 gives every cell all four states, and `TestClosetFlipIsBackedByEvidence` reads D1 out of the record: the flip cannot ship on a reading, only on an exit code |
| Fixing the blinded closet arm contaminates arms that are not about closets | Certain, under the earlier draft's plan | High | T1 makes closet use an arm dimension: `nil` for every arm whose name does not say closet, full strength for the two that do |
| ADR-002's normaliser evidence was taken on closet-ON sweep and adaptive arms | Certain | Med | Recorded in Consequences and deferred in Out of Scope: the comparison is re-taken under whichever closet setting D1 leaves production in, and T2's run record dates every table by commit |
| The real-query gold favours whichever ranking produced its pool | Certain, before T2 | Med | `CandidateUnion` already pools several rankers blind; T2 adds the closet-ON head, so the pool no longer excludes what only the prior would surface |
| A curated palace regresses and nobody notices | Med | Med | `CLOSET_BOOST=1` restores exactly today's behaviour, is named in the flag help, the README and the startup log, and needs no re-mine |
| The `real` category is too small to decide anything (n=4 on the last run) | High | Med | D2 is a veto with a floor of 10 admitted cases; below it the cell reads `n/a` and decides nothing, and the count is in the record |
| The mechanism gets deleted later because "the default is off anyway" | Med | Low | Removal is tagged permanent in Out of Scope, and `rank.go`'s history comment stays where a reader of the constant finds it |
| A client rendering `closet_boost` shows zeros and files a bug | Low | Low | Documented at the field's description in the README recall section |
| ADR-002 renormalises the lexical half, moving the fused range a fixed +0.40 boost sits on | Med | Low | Off by default, the two changes do not interact at the default; an operator turning the prior back on re-measures under whichever normaliser ships — the same one command |

## Rollback

`CLOSET_BOOST=1`. That restores the previous ranking exactly — same constants, same cap, same fade — and it is a restart, not a migration. No schema changes, no re-embedding, no re-mining: closets are still built and stored the whole time the prior is off, so the signal is there the moment it is switched back on. T1's and T2's eval changes and T5's documentation are independent of the default and would normally stay.

## Follow-ups

- [ ] `hybrid+rerank` blends at the SERVED rerank weight and `rerankSweep` contains 0.5, which is also `DefaultRerankWeight` — so at the default configuration that row and `rerank blend w=0.50` are computed identically and agree exactly. Found by review. Documented at the constant rather than removed, because the sweep needs a fixed grid for runs to stay comparable and the arm needs to track what is served; decide at T3 whether the table should mark coinciding rows rather than leave a reader to notice
- [ ] Re-run the closet arm on a genuinely curated palace once one exists, and if the prior wins there, ship a per-corpus recommendation at the knob rather than a second global default.
- [ ] Re-take ADR-002's normaliser comparison on the post-T1 arms, under the closet setting D1 leaves production in — the numbers it currently quotes were measured with the prior silently on.
- [ ] Received from ADR-007: populating closets so this ADR becomes measurable. ADR-007 cannot compare arms whose populations are empty, and the closet arms are empty here for the same reason — no curated closets exist to measure. This ADR owns the corpus condition, so it owns the punt.
- [ ] Received from ADR-016: whether the closet prior should be revived at all. ADR-016 found that no drawer filed through the agent write path carries entities, so closets are empty for the same root cause hallways are — nothing extracts on `Service.Add`, only on `Mine`. If ADR-016 T2 ships, this ADR's arms become measurable on an agent-populated palace for the first time; if T2 is withdrawn, `closets: 0` is the permanent state here and this ADR's question is settled by the corpus rather than by the ranking.
