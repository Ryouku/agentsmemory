# ADR-029: A trace that cannot lie about what it did

**Status:** Proposed
**Date:** 2026-08-25
**Owner:** Zy
**Spec:** None — no spec stage; grounded in a five-lens source sweep of the search path taken 2026-08-25 against `dcc1389`, each finding adversarially verified, plus a recorded span-set probe.
**Cross-references:** `internal/telemetry/telemetry.go` (`SearchStages`, the `Stage*` and `Reason*` vocabularies), `internal/palace/service.go` (`SearchPage`, `rankRetrieved`, `applyRerankWith`), `internal/palace/memory_search.go` (`survivorsFrom`), `internal/palace/recallstats.go` (`recordSearch`), `internal/palace/trace.go` (`searchAttrs`), `internal/mcpserver/server.go` (`searchWingFor`, `traceTool`), `docs/adr/ADR-025-executable-contract-axes.md` (spans carry knobs, never memory text), `docs/adr/ADR-028-a-recall-you-can-judge.md` (the `search_id` this ADR's spans still join on), `docs/adr/BACKLOG.md` (§"From ADR-029")
**Invalidates:** none — checked. ADR-025's privacy rule is upheld, not relaxed: every attribute this ADR adds is a count, a boolean, a bounded enum or a configured knob, and no query text, memory content or wing name reaches a span. ADR-028 is untouched — `search_id` still joins page to trace to durable row, and this ADR only makes the spans it joins honest. ADR-024 is unaffected: no score is recomputed and the ranking unit does not change.
**Served-path change:** none to ranking. Four spans stop reporting success for work that failed or did nothing (`am.search.record`, `am.search.rerank`, `am.search.evidence`, and the `am.tool` span over `am_search`), and `recordSearch` gains an error return that the caller still refuses to act on.

## Context

The OpenTelemetry work merged as `26f6531` was reported — by me, to this team, and onward to a colleague — as covering search end to end: eleven stages fire on a real recall (`search → embed → retrieve → hydrate → collapse → closet → fusion → recency → rerank → evidence → record`), each with an outcome from `ran|bypassed|failed_open|failed_closed`, a reason, a `file:line` call site, and the `search_id` that joins them; widening appears as span events and outbound HTTP as children. All fifteen declared `Reason*` constants are reachable, which is worth stating because dead vocabulary is this repository's usual failure and is genuinely not one here.

That report is true and it answers the wrong question. It establishes that a span **exists** for each stage. It says nothing about whether the span tells the truth — and the distinction the sweep forces is this: **a missing signal makes a trace incomplete, a wrong signal makes it lie.** A lie is strictly worse, because every conclusion drawn downstream inherits the error and none of them look uncertain. This repository has already paid for that once this month: a write-to-read ratio of 10.8:1 was reported from a 24-hour window dominated by a single bulk import, believed, and retracted only because a human questioned it by hand.

**The lead finding is not a span at all. It is the durable row, and it has a shipped consumer.**

`am_recall_stats` is a registered tool (`internal/mcpserver/server.go:301`, handler at `internal/mcpserver/admin.go:111-131`) that reports per-wing recall: how often each wing was asked, how often it answered, and which unanswered questions to file. It groups on `search_events.wing`, and that column is written from `q.Wing` at `internal/palace/service.go:1038` — the wing **after** `searchWingFor` (`internal/mcpserver/server.go:339-362`) has already resolved it. So:

- A search where the caller named no wing and the registration's default was **injected** is attributed to that wing, byte-identically to one where the caller asked for it.
- `"(unscoped)"` (`recallstats.go:197-199`) conflates "the caller asked for every wing" with "`SEARCH_SCOPE=workspace` widened a scoped request" with "the registration carries no default".

This is the same table the retracted 10.8:1 ratio was computed from, and the same report that prompted the reaction *"those stats get me cautious"* on 2026-08-25. The attribution cannot distinguish caller intent from server injection, so a per-wing recall number is not merely imprecise — it is unfalsifiable, because no stored value records which of the two produced it. The span-side `am.has_wing` (`internal/palace/trace.go:30`) carries the identical ambiguity, but nothing reads it yet; the durable column is the one with a consumer today.

Two further lies are confirmed, both at the MCP boundary and both silently degrading a page the caller receives:

- The `am_search` handler resolves anchors under `if err == nil` (`internal/mcpserver/drawers.go:721`). A failed lookup silently returns a page with **no `stale` flags and no warning**, and because `traceTool` only inspects the handler's Go error and `res.IsError`, the enclosing `am.tool` span ends `ran`. A page whose staleness marking silently vanished is indistinguishable — to the caller and to the trace — from one where nothing was stale.
- `emptyWingNote` returns `"", nil` for both "the lookup failed" and "the wing has content" (`internal/mcpserver/emptywing.go:32`), so a zero-hit page loses its explanation whenever `WingIsEmpty` errors.

Alongside the lies, one family of plain incompleteness earns a task rather than a receipt, because it carries an alarm the pipeline currently throws away.

**`max_distance` is the only retrieval boundary absent from the knob set, and the count it discards is a corruption detector.** Four independent confirmations converged on it: the threshold reaches no span in the tree (`internal/palace/trace.go:27`), and `survivorsFrom` drops on three predicates — orphan vector, wing/room mismatch, distance — with both call sites discarding the count (`internal/palace/memory_search.go:135`, `internal/palace/service.go:1089`). The sharpest consequence is the wing/room drop: `service.go:1081-1083` documents that comparison as redundant when the index honoured the filter, kept solely so a stale index cannot surface another wing's memory. **A non-zero drop there therefore means the vector index and the durable rows have diverged** — an alarm rather than a metric, and the one number in this pipeline that is thrown away with `survivors, _ :=`.

The 250-rune query truncation (`service.go:970`) is confirmed and rides along in the same task: nothing enforces 250 at the schema, so an over-long query is accepted and silently cut before it reaches the embedder, the lexical channel and the cross-encoder alike.


## Amended 2026-08-25 — five claims retracted after adversarial verification

**This section is kept rather than the record being rewritten.** The retracted claims below are the
most useful thing this ADR produced: a worked example of a reachability audit that REASONED about
instrumentation instead of MEASURING it, and the verification pass that caught it. Deleting them
would destroy the evidence and leave a record that looks like it was right the first time.

The sweep this ADR was built on ran in two halves: five blind lenses that FIND, and an adversarial
pass that tries to REFUTE each finding. The first half returned thirty findings. The second half
confirmed sixteen and refuted fourteen — and five of the seven "lies" this ADR's original Context
asserted are among the refuted. Each refutation below was verified against source by hand, not
accepted on the verifier's authority.

| Retracted claim | Why it was wrong | Verified at |
|---|---|---|
| `am.search.record` ending `Ran` over a failed INSERT is an unnoticed gap | It is a DOCUMENTED DECISION taken after that exact production incident (2026-08-23, goose past 00021 with `search_events` absent). The doc comment names `recordSearch`'s swallow and concludes the verdict "has to be an exit code an operator can run before trusting a deployment, not a line in a runbook." A `failed_open` here duplicates a gate that already exists. | `cmd/server/doctorschema.go:107-120` |
| A rerank cut off by `RERANK_TIMEOUT` is indistinguishable from an outage | Both produce the identical served result (fused order, `Reranked=0`) and the two are already separable by the span's own DURATION — ~budget versus ~milliseconds. The ordering property that matters is build-gated. | `cmd/server/wiring_test.go:274-287` |
| `reranked=true` when the blend reordered nothing is a lie | The repository DEFINES the field as "whether reranking HAPPENED, not whether a reranker exists", with a comment recording the fix of the opposite bug. The claim asked the field to carry a third meaning it was deliberately built not to carry — and "reordered nothing" is a continuum, not a property of all-equal scores. | `internal/palace/service.go:1039-1043` |
| `am.search.evidence` reports `ran, am.pool=10` with zero documents re-evidenced | Zero windows happens only when every document is at or under `ChunkSize`, and for exactly those the lexical document ALREADY IS the whole text — the reranker inputs cannot differ. `MEMORY_EVIDENCE_SELECTOR` also ships as `lexical`, so the semantic path is not the served default at all. | `internal/palace/evidence.go:76`, `.env.example:146` |
| `StageEvidence`'s omission from `SearchStages()` is an oversight no gate catches | `SearchStages` is documented as "the set of child stages one Search parent MUST emit". `StageRerank` opens BEFORE its bypass returns; `StageEvidence` opens AFTER — so its omission is REQUIRED by that contract, not an oversight. The lexical-versus-semantic choice is already on the parent span as `am.evidence` on every search, including the reranker-less case the claim called invisible. | `internal/telemetry/telemetry.go:160-163`, `internal/palace/trace.go:36` |

**T3 is withdrawn entirely**, and its refutation is the sharpest of the set because it was
established BY MUTATION rather than by argument. The original claim was that
`TestSearchStagesIsTheWiringList`'s `len(SearchStages()) < 8` against ten declared names lets two
names be deleted silently, and that "deleting a name from the list also deletes the only assertion
that the stage is emitted." A verifier deleted `StageCloset` from the list — leaving nine names, so
the threshold stayed satisfied — AND neutered the span emission, and the test still went red: the
hardcoded `searchKids` literal at `internal/palace/otel_test.go:71-80` is an INDEPENDENT authority
covering the other names, which the original claim had called a drift risk. The consequence as
stated is false. What remains is thin — a stage in neither list is unchecked — and it does not
justify changing production emission to satisfy a gate, which is the wrong direction of causation.

**The honest count is three lies, not seven, and sixteen confirmed gaps, not thirty.**

## Existing Primitives Audit

Checked before proposing anything new, because the characteristic defect here is building a second copy of a mechanism that already exists.

| Primitive | Where | Reused as-is? |
|---|---|---|
| `Span.End(outcome, attrs…)` with the four-outcome vocabulary | `internal/telemetry/span.go` | Yes. No new outcome is introduced; the lies are fixed by selecting the correct existing one. |
| `telemetry.AttrReason` + the fifteen `Reason*` constants | `internal/telemetry/telemetry.go:60-99` | Yes for six of seven lies. **One new constant** (`ReasonTimeout`) is required — the vocabulary genuinely cannot express a budget expiry today. |
| `telemetry.Annotate(ctx, attrs…)` | `internal/telemetry/span.go` | Yes. Added under ADR-028 and confirmed live; this is how `rankRetrieved` reports drop counts without threading a span through a pure predicate. |
| `Span.Event` — "a named decision inside a stage" | `internal/telemetry/span.go` | Yes. Already used for widening rounds; reused for the request-vs-served delta. |
| `searchAttrs` as the single parent-knob site | `internal/palace/trace.go:27` | Yes. Every new knob attribute lands here rather than at a second site. |
| `SearchStages()` as the completeness list | `internal/telemetry/telemetry.go:164` | Yes, with its authority repaired — see T3. |
| `TestSearchEmitsSemanticStageSpans` | `internal/palace/otel_test.go:18` | Yes, made bidirectional rather than replaced. |

Nothing here needs a new span type, a new exporter, or a new configuration surface.

## Decision

**Two tasks. T3 is withdrawn** — see the amendment above; its defect was refuted by mutation, and its fix would have changed production emission in order to satisfy a gate, which is the wrong direction of causation.

**T1 — the three confirmed lies.** The wing attribution written to `search_events` records what the server resolved and not what the caller asked, and `am_recall_stats` reports on it today. A `scope_source` is captured at the MCP boundary — the only place the caller's intent still exists — and stored alongside the resolved wing, so a per-wing recall number can finally say whether the caller chose that wing or the registration did. The anchor lookup's discarded error and `emptyWingNote`'s collapsed return stop being silent: both silently degrade a page the caller receives, and neither is visible in the response or the trace.

**T2 — `max_distance` and the drop counts.** The threshold reaches `searchAttrs`; `survivorsFrom` returns its three drop counts and `rankRetrieved` records them at its single call site; the pre-truncation query length reaches the parent span. The wing/room drop count is the point: it is an alarm, not a metric, and it is the one number this pipeline computes and discards.

**What changed about the ADR's own method, stated because it is the transferable part.** The original version of this record asserted seven lies from source reading alone. Five did not survive an adversarial pass, and every refutation turned on code the original had not read — a doctor exit code deliberately chosen as the detector, a build-time gate on the rerank budget, a field whose meaning is pinned by a comment, a default that keeps a whole path off the served route, and a doc comment defining a list as MUST-emit. **A reachability audit that reads only the site of the suspected defect will find defects that are not there.** The verification half of the sweep is not a formality on top of the finding half; it is what turns a reading into a fact.

## Alternatives Considered

- **Add the missing attributes and leave the outcomes alone:** rejected. It is the cheaper half and it is the half that does not matter — an incomplete trace makes a reader ask another question, a lying trace makes them stop asking. `am.search.record ran` over a failed INSERT actively defends a wrong belief about the durable table the recall statistics are computed from.
- **Teach `TestSearchEmitsSemanticStageSpans` a maximally-configured fixture and add `StageEvidence` to the list:** rejected as the whole fix, though the fixture change is real work T3 still does. It repairs the instance and leaves the mechanism: the next stage added without a list entry reproduces the defect exactly, and the gate stays a one-directional check whose subject and authority are the same list.
- **Emit `StageEvidence` only when a reranker is configured, and make the gate conditional:** rejected. A conditionally-emitted member turns `SearchStages()` from a list into a predicate, and the gate would then have to know the fixture's configuration to know what to expect — reintroducing exactly the coupling that let the stage hide. Unconditional emission also makes the stage's absence detectable in PRODUCTION, not only in a test, which the fixture route never achieves.
- **Record the drop counts inside `survivorsFrom`:** rejected on a measurement error it would have caused. The predicate is pure, takes no context, and runs twice per search — once per widening round at `memory_search.go:135` and once over the final pool at `service.go:1089`. Instrumenting inside it would multiply the counts by the number of widen rounds and attach them to whatever span happened to be current. Returning the counts and recording them at the single `rankRetrieved` site keeps the predicate pure and the numbers correct, which in an observability change is the entire deliverable.
- **Recover the caller's wing intent inside the palace:** impossible, not merely rejected. `searchAttrs` runs at `service.go:989` with `q.Wing` already resolved, so no attribute added anywhere in `internal/palace` can distinguish a caller-supplied wing from a server-injected one. The capture has to happen at the MCP boundary, which is what T2 does.
- **One ADR covering all thirty findings:** rejected. Thirty items in one record gives none of them a killed mutant, and a mutant per claim is what makes any of this evidence rather than assertion. The split is on what the defect does to a reader — lie versus omission — which is a property of the finding, not a convenience.

## Component / Boundary Impact

No module boundary moves and no new package appears. `internal/telemetry` gains one `Reason*` constant and its `SearchStages()` list gains one name. `internal/palace` gains attributes and corrected outcomes on spans it already opens, and `survivorsFrom` widens its return by three counts — an internal, unexported signature. `internal/mcpserver` annotates the tool span it already opens with the wing resolution it already performs. `internal/rerank/tei` is read, not changed; the timeout is classified at the call site by `errors.Is(err, context.DeadlineExceeded)`.

## Wiring & Contract Changes

- `telemetry.ReasonTimeout = "timeout"` — new constant, reachable from `applyRerankWith`'s error branch.
- `telemetry.SearchStages()` gains `StageEvidence`, making it eleven names.
- `Repo.recordSearch` returns `error`. The single caller ignores it for control flow and uses it for the span outcome only.
- `survivorsFrom` returns `([]SearchHit, int, scopeDrops)` where `scopeDrops` carries orphan, out-of-scope and over-distance counts. Both call sites are internal and both are updated.
- No MCP tool schema changes, no response-shape changes, no new flags or environment variables. An operator's configuration surface is identical before and after.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `scope_source` at the MCP boundary and on the `search_events` row | T1 | none in this ADR — `am_recall_stats` consumes it once the column exists | No — additive column and additive attribute; an absent value reads as unknown |
| `scopeDrops` from `survivorsFrom` | T2 | T2 only | No — unexported, both call sites updated in the same task |

The two tasks are contract-independent and both touch `internal/palace/service.go`, so they are ordered rather than parallel. T1 goes first because it is the only defect here with a live consumer: `am_recall_stats` reports per-wing numbers on the ambiguous column today.

## Implementation

Each task is a file under `tasks/`. Each carries a red step before production code, a `## Acceptance` fence run by `adr-verify`, and at least one mutant recorded through `adr-verify --mutant` — for the T1 outcomes the mutant is unusually convincing, because reverting an outcome to `Ran` is a one-token change that must turn the fence red.

Every task also carries a **live-trace confirmation**, not only a green fence. The rule this repository learned on 2026-08-25 applies with full force here: in an instrumented system, a change that adds a signal extends the instrument in the same commit, and the only evidence that counts for an instrument is the instrument's own output against the running artifact. `scripts/redeploy.sh` already gates on a real `am.search` span appearing in the deployed container's log.

## Consequences

A per-wing recall number becomes falsifiable. Today `am_recall_stats` cannot say whether `wing_X` was asked for or injected, and `"(unscoped)"` covers three different situations — so the report that prompted "those stats get me cautious" was right to prompt it. After T1 the stored row carries which of the two happened, and the question can be asked of data rather than of memory.

A page that silently lost its `stale` flags, or its empty-wing explanation, stops being indistinguishable from a healthy one.

The wing/room drop count becomes an alarm the pipeline can raise on its own. Today a divergence between the vector index and the durable rows is detectable only by someone reading `survivorsFrom` and noticing that its guard must be firing.

Cost: one additive column, one boundary attribute, and five integers on spans that already exist. No text, no unbounded cardinality, no per-hit attribute.

**A cutover, not a backfill.** Rows written before `scope_source` exists carry no value for it, and nothing can recover the caller's intent retrospectively. Every recall statistic spanning the boundary is therefore mixed, and the report has to say so rather than quietly averaging across it.

## Out of Scope

- **The five retracted claims** — the record stage's outcome, the rerank timeout reason, `reranked` versus reordered, the evidence pool count, and `StageEvidence`'s list membership (permanent: refuted by adversarial verification and confirmed against source by hand; each has a compensating mechanism or a definition that makes the change wrong, documented in the amendment above)
- **T3 — a set-equality gate over `SearchStages()`** (permanent: withdrawn. Its stated consequence was disproved by mutation, and what remains would change production span emission to satisfy a test)
- **Backend identity on the span** — `VECTOR_BACKEND` confirmed, `EMBED_BACKEND` refuted (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — it is `cmd/server/main.go` wiring rather than the search path and earns its own record)
- **The confirmed tail findings** — the adaptive BM25 weight's resolved value, the whole-memory-to-400-rune degradation, `SearchQuery.Context` presence, `closetBoostsAt`'s three discard paths, the evidence stage's window counts, `am_list_drawers`' wing narrowing (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — all confirmed, none of them make a span lie)
- **Any change to what `recordSearch` does on failure** (permanent: its documented invariant is correct, and `doctorSchema` is already the deliberate detector for the failure it hides)
- **Re-running the historical recall statistics under the corrected attribution** (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — rows written before `scope_source` exists cannot be re-attributed, so the honest outcome is a cutover date, not a backfill)
- **Acting on a non-zero out-of-scope drop count** (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — T2 makes the divergence visible; deciding what the server does about it has a blast radius this ADR does not take on)
- **Sampling, exporters, or collector configuration** (permanent: untouched; this ADR changes what is recorded, never how it is transported)
- **Any change to ranking, scoring, or the retrieval unit** (permanent: ADR-024 owns the ranking unit; every survivor, score and order is byte-identical before and after this ADR)

## Risks

**A new attribute is a new promise.** `TestDocumentedEnvVarsAreRead` and the reachability gates in this tree exist because a documented-but-unread setting has shipped four times in one week. Every attribute this ADR adds is asserted by a test that fails when the attribute is removed, and T3's set-equality gate is what stops a future stage from being declared and unemitted.

**Set equality is a stricter gate and could cry wolf.** A stage legitimately added to the pipeline now fails the gate until it is declared — which is the intent, but it makes the gate a step in adding a stage rather than a passive check. Stated here so the failure reads as designed rather than as a bug. Measured against the current tree before shipping: both directions are empty under the default fixture today.

**`errors.Is(err, context.DeadlineExceeded)` can misclassify.** A caller-cancelled context also produces a deadline-shaped error in some paths. The classification is asserted against a fixture that expires a real budget, and the fallback remains `ReasonError`, so a misclassification degrades to today's behaviour rather than to a new wrong answer.

**Widening the `survivorsFrom` signature touches the eval path.** `rankRetrieved` has four callers: the served path at `service.go:1025` and three eval-arm sites. The drop counts are recorded via `Annotate`, which paints the current span — the arm span for an eval call, the `am.search` parent for a served call. That is the correct per-caller attribution and it is asserted, because the failure mode is arm numbers reading as served-path numbers in a table nobody re-derives.

## Rollback

Per task, and each is a clean revert: T1 restores the unconditional outcomes, T2 removes attributes and narrows the signature back, T3 restores the length assertion. Nothing persists to disk, no migration runs, no configuration is read, and no served ranking changes — so a rollback at any point returns the system to `dcc1389`'s behaviour exactly, with only the trace losing fidelity.

## Follow-ups

- Backend identity on the span (`VECTOR_BACKEND` confirmed), as its own ADR.
- The six confirmed tail findings, to be picked up at the next `/quality-harness:adr-write` sweep via `adr-debt`.
- **Re-examine every statistic this repository has published from `search_events`.** The 10.8:1 ratio was already retracted for a different reason (a window dominated by a bulk import); the wing attribution is a second, independent way those numbers can be wrong, and the two were not distinguished at the time.
- **Run the adversarial half before writing the record, not after.** This ADR's own amendment is the argument: five of seven claims did not survive it, and writing first meant committing them.
