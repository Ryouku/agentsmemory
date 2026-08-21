# ADR-007: The eval may not print a number that means something other than what it says

**Status:** Accepted
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-002 (reads the anchored arms off these tables), ADR-003 (its truth table is one of the numbers this ADR shows cannot currently be read), ADR-004 (its supersession row is the behaviour this ADR generalises)
**Invalidates:** none — checked (grepped ADR-001..006 for `EvalReport`, `printPool`, `ClosetDelta`, `--cases`: ADR-002/003/004 CONSUME these tables and none of them depends on how a number is labelled)
**Served-path change:** None on the retrieval path — this ADR changes only what the eval PRINTS. Deliberate, and the point: the numbers an operator reads decide which production change is worth making, so an eval that misreports its population misroutes every downstream decision. 0 of 3 tasks done.

## Context

Six eval tables were taken on 2026-08-20 across two palaces (~240 drawers and ~5,000). Three of the numbers they print do not mean what they say, and one of the three has already been fixed — it is kept here as the pattern, because the other two are the same mistake in different clothes.

**Fixed already, and the precedent.** `printPoolDiagnosis` took the worst `NotFound` across arms, and `NotFound` counts a different population per arm: `ArmProduction` is `ScopePage` and is scored over the page `Search` returns, not the shared pool. So a run printed *"38 of 100 question(s) had their answer OUTSIDE the candidate pool"* four lines below a ceiling reporting **97% in pool**. The number was `cases − recall@5`, carrying nothing the recall column did not already have. Its own remedy disproved it: raising `--pool` from 128 to 256 moved it 38 → 36, while genuine pool misses went 3 → 4. A pool-miss count can only fall when the pool grows.

**Still live: a comparison whose two arms are identical prints a clean null.** Every one of the six tables printed the closet row as `admitted 96-97, ΔMRR +0.000, 95% paired CI [0.00–0.00], moved 0`. Read plainly that says the curation prior makes no difference. It says nothing of the kind: `select count(*) from closets` is **empty** on both palaces, so `hybrid` and `hybrid+closet` are the same arm and agree to three decimals because they are identical. ADR-003's truth table is read off exactly this contrast, so ADR-003 is not blocked on corpus size as recorded — it is blocked on `mine` having run. The eval already prints `moved`, which is the only reason anybody could tell; it just does not act on it.

**Still live: runs are silently incomparable.** Without `--cases`, `loadOrGenerateCases` (`cmd/server/eval.go:509`) samples `--n` drawers and generates fresh questions; the file is only read or written when the flag is set. Four n=30 runs were therefore four different question sets, and the table labelled a `BEST` arm in each with nothing saying the questions had changed. The label moved between `rerank blend w=0.75` and `w=0.50` across them, and was read — by us, in this repository — as three tables agreeing on a configuration. They were three samples.

**The behaviour to generalise is already in the tree.** The supersession section prints *"no temporal cases in this run, so nothing to measure"* and no number. That is the correct answer to an unmeasurable question, and it is what the closet row should have said six times.

## Existing Primitives Audit

- **`palace.ArmScope`** (`internal/palace/eval.go`) — classifies an arm by the population its ranks are taken over, exhaustive by construction. Reuse: this is the shape the whole ADR generalises, and the pool-diagnosis fix already consumes it.
- **`palace.ClosetDelta` / `ClosetCell`** (`internal/palace/evalstats.go`) — already counts `admitted`, `unreachable` and `moved`. Reshape: it computes the evidence that the comparison was vacuous and prints it as a footnote instead of acting on it.
- **`caseFileMeta` / `readCasesWithMeta`** (`cmd/server/eval.go`) — already carries a generator identity for replayed case files. Reshape: it is most of a case-set identity; what is missing is stamping one when no file is named.
- **`EvalReport.Warnings`** — the existing channel for "this run announced its own degradation". Reuse for the new refusals rather than inventing a second one.

## Decision

Three rules, one principle: **a printed number carries the population it was computed over, and a measurement whose mechanism had no input reports that it was not measured rather than reporting a value.**

1. **No aggregate across populations.** A statistic combined over arms includes only arms of one `ArmScope`; arms of other scopes are reported separately, each named beside the knob that moves it. (Landed; this ADR ratifies it and T1 makes it general rather than a fix at one call site.)
2. **A vacuous comparison reports `not measured`.** When a preselected contrast's two arms produced identical orderings on every admitted case — `moved == 0` — the cell prints `not measured` and says what input was missing, instead of `Δ +0.000` with an interval. **Falsification, and the failure this must avoid:** if `moved == 0` can occur when the mechanism DID have input and genuinely changed nothing, this rule converts a real null into a non-answer and is worse than what it replaces. T2 must therefore distinguish the two by checking whether the mechanism had any input at all — for the closet prior, whether the corpus holds closets — and report `no effect` in that case and `not measured` only in the other. A rule that cannot tell them apart is withdrawn.
3. **A run states its case-set identity, and a `BEST` label states what it is best over.** Every run stamps a case-set id — the hash of the case set — into the table header and the run record. A run that generated its own questions says so in the same breath as the label, because a `BEST` computed over questions nobody else will ever see is a claim about one sample and reads as a claim about the system.

This is valid for the eval's own output. It does not make runs comparable; it makes it impossible to compare them by accident.

## Alternatives Considered

- **Require `--cases` and fail without it.** Rejected: the generate-then-save flow is how a case set comes into existence in the first place, and a tool that cannot be run without a file that does not exist yet is a tool nobody starts with.
- **Suppress the `BEST` label when n is small.** Rejected: it swaps one arbitrary threshold for the problem. The defect is not that n was 30, it is that nothing said the questions had changed between runs; at n=1000 the same two runs would still be incomparable.
- **Document it in the README and the run brief.** Rejected on evidence: the run brief written for this exact corpus already said "each `--cases` path is unique so each run keeps its own questions", and four runs were taken without one anyway. Prose beside the tool loses to the tool's own output.
- **Have `ClosetDelta` return an error when the corpus holds no closets.** Rejected: it makes a reporting concern into a failure and would stop a run that is perfectly valid for every other arm in it.

## Component / Boundary Impact

`internal/palace` keeps ownership of what is measured; `cmd/server` keeps ownership of how it is printed. The case-set id is produced in `cmd/server` beside the existing `caseFileMeta` and travels into the run record. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `ClosetCell` gains a measurement status (`measured` / `no effect` / `not measured` + reason) | add | `internal/palace/evalstats.go` | `PrintClosetTable`, ADR-003's truth table |
| `EvalReport` gains `CaseSetID` and `CaseSetOrigin` (`generated` \| `replayed`) | add | `cmd/server/eval.go` | the table header, the `.cells.json` run record |
| the `BEST` column | change — names the case set it is best over | `cmd/server/eval.go` | every reader of a table |
| `palace.ClosetDelta` | change — takes the corpus's closet count so it can tell "no effect" from "no input" | `internal/palace/evalstats.go` | `cmd/server/eval.go` |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| scope-partitioned aggregation | T1 | T1 | No — generalises a landed fix |
| `ClosetCell` measurement status | T2 | T2 | Yes — ADR-003 reads this cell, and a status field changes what "Δ 0.000" means; ADR-003's Follow-ups already require a re-read |
| `CaseSetID` | T3 | T3 | No — additive |

## Implementation

`tasks/README.md` — three tasks.

## Consequences

- **Positive:** the three numbers that misled us stop being printable. A reader who does not know the eval's internals can no longer be told a retrieval failure that is a paging artifact, a null result from an experiment that never ran, or a winner over a sample nobody else has.
- **Negative:** the closet cell can no longer be read as a plain number without also reading its status, and ADR-003's truth table has to be re-derived against it.
- **Neutral:** table headers grow a case-set id. Existing `.cells.json` records lack it and are distinguishable by its absence, which is the correct reading of them.

## Out of Scope

- Making runs across different case sets statistically comparable (permanent: they are not comparable, and machinery that presented them as such would be the defect this ADR removes, in a more expensive form)
- Choosing `--n`, `--pool` or a default case set (permanent: those are the operator's experiment design; this ADR governs what the output may claim about whatever they chose)
- Extending the same rule to `am_recall_stats` (deferred: docs/adr/BACKLOG.md — the same principle applies to production telemetry, and ADR-006 T4 is already fixing one number there)
- Populating closets so ADR-003 becomes measurable (deferred: docs/adr/ADR-003-retire-the-closet-prior.md — this ADR reveals the blocker; removing it is ADR-003's own work)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `moved == 0` cannot be split into "no input" and "no effect", making rule 2 a downgrade | Med | High | T2's pre-registered falsification: it must distinguish them by a direct check on the mechanism's input, and the rule is withdrawn if it cannot |
| The case-set id changes when it should not — reordering, or regenerating identical questions | Med | Med | T3 hashes the sorted case content, not the file or the order, and pins that two orderings of one set agree |
| ADR-003's truth table becomes unreadable mid-flight | High | Low | It is unreadable today for a worse reason; T2 makes the unreadability explicit, which is what lets ADR-003 be re-planned rather than mis-decided |

## Rollback

All three changes are to output and to one struct field; none touches storage or an existing `.cells.json` on disk. Revert the commits and the table prints exactly what it printed before, including the two numbers this ADR calls wrong. Records written in between carry a `CaseSetID` that a reverted reader ignores.

## Follow-ups
