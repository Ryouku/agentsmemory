# ADR-009: Tune retrieval against the operator's own corpus, because nobody tunes it by hand

**Status:** Proposed
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-002 (owns which normaliser and lexical weight ship), ADR-003 (owns the closet default), ADR-007 (owns whether a printed number can be trusted)
**Invalidates:** none — checked (ADR-002's shipping rule decides a GLOBAL default from two corpora; this decides a PER-INSTALL one and does not touch that rule. If ADR-002 later ships a new global default, it becomes this mechanism's starting point rather than its competitor.)

## Context

The operating reality, stated by the maintainer 2026-08-20: *"we do not experiment much with the params, we run it as is, with defaults and hope that it runs."* That is not a criticism of the operator — it is the normal case for every deployed system, and it means **the default is the product**.

Measured against that standard, the current default is the worst configuration on every corpus anyone has run. `config.Default()` ships `FUSION=linear`, `BM25_WEIGHT=auto`, page-maximum lexical normalisation — which is exactly the eval's `fusion bm25=auto` arm:

| run | the default | `bm25=0.00` (lexical leg off) | best arm in that table |
|---|---|---|---|
| n=100, pool 128 | 0.226 | 0.367 | 0.417 |
| n=100, pool 256 | 0.279 | 0.445 | 0.498 |
| four n=30 runs | 0.172 – 0.348 | 0.295 – 0.440 | 0.409 – 0.516 |

Six tables, two corpora (~240 and ~5,000 drawers), same direction every time. An operator running defaults gets roughly 40–60% less MRR than one who sets a single environment variable, and has no way to discover that.

**The obvious fix is wrong, and that is the reason for this ADR.** Shipping `BM25_WEIGHT=0` would optimise for the only query mode anybody has sampled. Every run so far used `--style paraphrase`, whose own help text reads *"no shared vocabulary"* — conceptual recall. The complementary mode, `--style literal`, *"keeps identifiers, like a real developer search"*, is implemented (`cmd/server/eval.go:577`) and **has never been run**. Lexical retrieval is expected to win precisely there. Choosing a global default from one mode would be choosing it from half the evidence, in the half that flatters the choice.

Nor can one constant be right for both corpora. The right lexical weight was already measured to be a property of the query rather than of the corpus, which is why `auto` exists at all; the anchored-normaliser work then showed the scale mismatch, not the lexical signal, is what makes naive fusion lose. Those are two different corpus-dependent quantities, and a shipped number is a guess about someone else's palace.

## Existing Primitives Audit

- **`agentsmemory eval`** — already generates cases from the operator's own corpus, scores ~30 arms, and writes a machine-readable `.cells.json`. Reuse wholesale: the measurement instrument exists and this ADR is about consuming it rather than building another.
- **`EvalReport` / `PairedDelta` / `WilsonInterval`** (`internal/palace/evalstats.go`) — paired intervals over per-case ranks. Reuse: the decision rule needs intervals, not point estimates.
- **`adaptiveBM25Weight` / `LexicalCoverage`** — per-QUERY adaptation already in the tree. Reshape conceptually: this ADR is the same idea one level up, per INSTALL, and the two compose rather than compete.
- **`config.Config` + the flag/env surface** — where a tuned value has to land to be read. Reuse; ADR-006 is separately making sure a setting that is set is actually consulted.

## Decision

Add `agentsmemory tune`: an operator-initiated command that runs the eval against the operator's own corpus across both query modes, applies a **pre-registered** decision rule, and writes a tuned configuration the server reads on start. It prints what it changed, what it measured, and what it refused to change.

Three properties make it a tuner rather than a way to overfit:

1. **Both query modes, or it refuses.** A tuning run requires a `paraphrase` case set and a `literal` one. A configuration that wins on one mode and loses on the other is not adopted; the rule optimises the worse of the two, not the mean, because the failure an operator notices is the query class that stopped working.
2. **Held-out selection.** Cases are split; the argmax is taken on one half and the margin confirmed on the other. Picking the winner from the same data that scored it is the selection bias this repository's own ADRs already reject, and a tuner is the place it would do the most damage because nobody reviews its output.
3. **It moves nothing without a margin.** A knob changes only when the held-out paired interval excludes zero. Ties keep the incumbent, and the run records the tie — an operator who runs `tune` and is told "nothing moved, here is why" learns more than one handed a new number.

**Pre-registered falsification.** If, on the maintainer's ~5,000-drawer corpus, the tuner's chosen configuration does not beat the shipped default on held-out cases in BOTH modes, this ADR is withdrawn rather than weakened: it would mean per-install tuning buys nothing that a better global constant would not, and ADR-002 should simply pick that constant.

Scope: the retrieval knobs whose effect the eval already measures — `FUSION`, `BM25_WEIGHT`, `LEX_NORM`, `RERANK_WEIGHT`, `CLOSET_BOOST`. Not the embedding model, the pool size, or anything requiring a re-index.

## Alternatives Considered

- **Ship a better global default and stop.** Rejected on the evidence above: the direction is robust but the destination is not, and one number cannot be right for a 240-drawer curated palace and a 5,000-drawer mined one. It is also the change most likely to be wrong for exactly the query mode nobody sampled. ADR-002 still owns the global default and this does not pre-empt it.
- **Tune automatically at startup.** Rejected: a server that silently changes its ranking between restarts makes every bug report unreproducible, and the run costs real inference. Tuning is a decision an operator takes, with a record.
- **Tune continuously from production traffic.** Rejected here, deferred: it is the honest long-run answer and it is blocked on telemetry volume — `search_events` held 25 rows at last count, and the `real` eval arm produced n=4 from it. Revisit when there are enough recorded queries to hold any out.
- **Publish a tuning guide.** Rejected as the primary mechanism, for the reason this ADR exists: the maintainer, who wrote the system, runs defaults. A document asking operators to do what its own author does not is not a mechanism.

## Component / Boundary Impact

A new `tune` subcommand in `cmd/server` consuming `internal/palace`'s eval; a tuned-config file read at startup beside the existing flag/env resolution. `internal/palace` gains no new ownership — it already measures. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `agentsmemory tune` subcommand | add | `cmd/server/tune.go` | operators |
| tuned-config file (path, format, precedence below explicit env/flags) | add | `tune` | server startup |
| `TuneResult` record — chosen values, margins, what was refused and why | add | `cmd/server/tune.go` | the operator, and any later audit of a change |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the literal-mode case set and its table | T1 | T2 | No — new measurement |
| `TuneResult` + the decision rule | T2 | T3 | No — new |
| tuned-config precedence | T3 | T3 | No — additive; absent file means today's behaviour |

## Implementation

`tasks/README.md` — three tasks.

## Consequences

- **Positive:** the configuration an operator actually runs is chosen by measurement on their own corpus rather than by a constant chosen on someone else's. The gap this ADR opens with — 40–60% of MRR — is recoverable without anyone reading a tuning guide.
- **Negative:** a tuning run costs real inference on the operator's own hardware or bill, and takes minutes. It is opt-in for that reason.
- **Neutral:** a second place a configuration value can come from. Explicit flags and environment variables still win, so an operator who sets a knob keeps it.

## Out of Scope

- Continuous tuning from production traffic (deferred: docs/adr/BACKLOG.md — blocked on telemetry volume, and it is the same work as the continuous-evaluation entry already there)
- Tuning the embedding model, chunk size, or pool size (permanent: each needs a re-index, which is a different cost class and a different decision; this ADR tunes what can change between restarts)
- Choosing the GLOBAL default (permanent: ADR-002 owns it under a pre-registered rule, and two decisions about one value is how they end up contradicting each other)
- Tuning per wing rather than per install (deferred: docs/adr/BACKLOG.md — corpora differ between projects too, but nobody has measured whether the optimum does)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The tuner overfits to LLM-generated questions rather than real ones | High | High | Held-out selection; both modes required; and the run records that its cases were generated, so a reader knows what the number is about. The `real` style remains the honest target and is named in the output as the thing to prefer when there is enough traffic |
| An operator tunes once and the corpus moves underneath it | Med | Med | The result records the corpus size and commit it was taken against; T3 prints staleness when the corpus has grown materially since |
| Tuning makes a bug report unreproducible | Med | Med | Never automatic; the tuned file is human-readable, version-controlled if the operator wishes, and named in `am_status` |
| The pre-registered falsification fires and the ADR is withdrawn | Med | Low | That is the designed outcome, not a failure — it would mean a global constant suffices, which is ADR-002's job |

## Rollback

Delete the tuned-config file and restart: resolution falls back to flags, environment, then `config.Default()`, exactly as today. The `tune` subcommand can be reverted independently since nothing else consumes it. No stored data changes — tuning writes configuration, never memories.

## Follow-ups
