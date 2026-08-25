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

That report is true and it answers the wrong question. It establishes that a span **exists** for each stage. It says nothing about whether the span **tells the truth**, and a five-lens sweep of the search path on 2026-08-25 returned thirty findings across eight families. The distinction the sweep forces is the one this ADR is built on:

**A missing signal makes a trace incomplete. A wrong signal makes it lie**, and a lie is strictly worse, because every conclusion drawn downstream inherits the error and none of them look uncertain. This repository has already paid for that once this month: a write-to-read ratio of 10.8:1 was reported from a 24-hour window dominated by a single bulk import, believed, and retracted only because it was questioned by hand.

Seven findings are lies in that exact sense, all confirmed by reading source:

- `recordSearch` is `_ = r.db.WithContext(ctx).Create(&e).Error` (`recallstats.go:140`) and returns nothing, so `service.go:1050` ends `am.search.record` with `telemetry.Ran` whatever happened. A `search_events` INSERT that failed is indistinguishable from one that succeeded — in the very table the recall statistics are computed from.
- `applyRerankWith` ends `am.search.rerank` with `Ran` and `am.pool=N` whenever the cross-encoder returned the expected count. `normalizeScores` maps an all-equal input to 1.0 at every position, so the blend then reproduces the fused order exactly. A reranker that reordered nothing reports the same span as one that reordered everything, and `reranked=true` propagates from there into the durable row.
- `semanticRerankDocuments` returns the lexical documents untouched when no candidate produced a window (`evidence.go:83`), and the caller takes the success branch: `am.search.evidence ran, am.pool=10` is emitted when zero of the ten documents were re-evidenced. `am.pool` is the shortlist size, never the number actually selected.
- The `am_search` handler resolves anchors under `if err == nil` (`drawers.go:721`). A failed lookup silently returns a page with no `stale` flags and no warning, and because `traceTool` only inspects the handler's Go error and `res.IsError`, the enclosing `am.tool` span ends `ran`.
- `tei.Client.Rerank` wraps the pool in `context.WithTimeout(c.budget)`, so the operator's own `RERANK_TIMEOUT` surfaces as an ordinary error and is recorded as `failed_open reason=error` — the same value as a reranker that is down, returning 500, or emitting undecodable JSON. There is no `ReasonTimeout`.
- `emptyWingNote` returns `"", nil` for both "the lookup failed" and "the wing has content" (`emptywing.go:32`), so a zero-hit page loses its explanation whenever `WingIsEmpty` errors.
- `searchAttrs` emits `am.has_wing` as a boolean computed from the **already-resolved** wing (`trace.go:30`). `searchWingFor` (`server.go:339-362`) substitutes the registration's default wing when the caller named none, and rewrites an explicit `"*"` to `""`. So `am.has_wing=true` is byte-identical for "the caller asked for this wing" and "the server injected it", and `am.has_wing=false` is byte-identical for three separate causes including `SEARCH_SCOPE=workspace`. Any recall statistic sliced on that attribute is sliced on a value that means three things.

Alongside the lies, two families of plain incompleteness matter enough to fix rather than receipt:

**The request the caller made is not recoverable from the trace.** `SearchPage` truncates the query to 250 runes (`service.go:970`) and clamps `limit` to `MaxSearchLimit` (`service.go:977`), and `searchAttrs` is handed the post-clamp value. A caller asking for 5000 and a caller asking for 100 emit an identical `am.limit=100`; a query cut mid-sentence before embedding leaves no evidence that the embedded text differs from what was sent.

**The filter that removed candidates is not recorded, and one of its counts is a corruption detector.** `survivorsFrom` drops on three predicates — orphan vector, wing/room mismatch, `max_distance` — and both call sites discard the count (`memory_search.go:135`, `service.go:1089`). `am.max_distance` exists on no span in the tree at all, so the trace does not even carry the threshold that was applied. The sharpest consequence is the wing/room drop: `service.go:1081-1083` documents that comparison as redundant when the index honoured the filter, kept solely so a stale index cannot surface another wing's memory. **A non-zero drop there therefore means the vector index and the durable rows have diverged** — the one number in this pipeline that is an alarm rather than a metric, and it is thrown away with `survivors, _ :=`.

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

**Three tasks, split on what the defect does to a reader, not on which file it lives in.**

**T1 — a span that reports success only for work that succeeded.** The seven lies above are fixed by selecting the outcome and reason the existing vocabulary already provides, plus one new `ReasonTimeout`. `recordSearch` starts returning its error so the caller has a value to branch on; it keeps swallowing that error for control flow, because its documented invariant — *"measurement that can break the thing it measures is worse than no measurement"* — is correct and is not what is being changed. What changes is that the span stops asserting `Ran` over a failure.

**T2 — the request, the scope, and the filter become recoverable.** The pre-clamp limit and the pre-truncation query length reach the parent span, so the served values can be compared with the asked-for ones. `am.max_distance` reaches `searchAttrs`. `survivorsFrom` returns its three drop counts and `rankRetrieved` records them at its single call site. The caller's wing intent is captured at the MCP boundary — where it still exists — rather than inferred inside the palace, where it provably cannot be.

**T3 — the completeness list becomes an identity, in both directions.** `TestSearchEmitsSemanticStageSpans` iterates `SearchStages()` and checks declared→emitted. Nothing checks emitted→declared, which is why `StageEvidence` — declared in the `Stage*` block as `am.search.evidence`, produced at exactly one site, referenced by zero tests — sits outside the list with no gate noticing. A recorded probe run on 2026-08-25 measured both directions: under the default fixture the emitted `am.search*` set equals `SearchStages()` exactly, and under a fixture with a reranker configured `am.search.evidence` appears as emitted-and-undeclared. The fix is therefore *not* "add the name": that repairs one instance and leaves the next unlisted stage to repeat it identically.

Two changes make the repeat impossible. `applyRerankWith` emits `StageEvidence` on **every** path including its three bypasses, matching the pattern every other optional stage already follows (`closet` on `scale_zero`, `recency` on `band_zero`, `rerank` on `no_reranker`) — a stage that vanishes when its feature is off is indistinguishable from a stage that was deleted, which is precisely how this one stayed invisible. And the gate asserts **set equality** between the `am.search*` names a real search emits and `SearchStages()`, replacing `TestSearchStagesIsTheWiringList`'s `len(...) < 8` against ten declared names — an assertion two deletions can survive. The hand-written `searchKids` literal at `otel_test.go:71-80`, a second declaration of "the Search stages" that references neither the first nor any test, is derived from the list instead.

**Why the tail is receipted rather than inlined.** Six sweep findings — the adaptive BM25 weight's resolved value, the whole-memory-to-400-rune degradation carried only in a prose `note`, `SearchQuery.Context` presence on the rerank span, the coerced-to-zero cosine rejection, `closetBoostsAt`'s three discard paths, and the evidence stage's window counts — are real, verified, and none of them make a span lie. They go to `BACKLOG.md` §"From ADR-029" with their finding text intact, because an ADR that fixes thirty things gives none of them a killed mutant, and a mutant per claim is what makes any of this evidence rather than assertion.

**Backend identity is deliberately out.** `VECTOR_BACKEND` and `EMBED_BACKEND` are on no span, and the second is the highest-consequence item the sweep found — the embedding model decides what every distance in every trace and every eval table *means*, and both default paths serve the same dimension count, so the one recorded attribute (`am.dim`) cannot separate them. It is excluded here only because it is `cmd/server/main.go` wiring rather than the search path, and it earns its own record. It is receipted with that reasoning, not dropped.

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
| `telemetry.ReasonTimeout` | T1 | none — T1 is its only user | No — additive to the declared reason vocabulary |
| `Repo.recordSearch` returning `error` | T1 | T1 only; the caller still refuses to act on it | No — unexported, single call site |
| `scopeDrops` from `survivorsFrom` | T2 | T2 only | No — unexported, both call sites updated in the same task |
| `StageEvidence` emitted unconditionally | T3 | T3's own set-equality gate | No — additive span on paths that previously emitted nothing |

The three tasks are contract-independent — no task consumes anything another produces — and all three touch `internal/palace/service.go`. They are ordered rather than parallel so each diff stays reviewable and each mutant stays attributable to one claim.

## Implementation

Each task is a file under `tasks/`. Each carries a red step before production code, a `## Acceptance` fence run by `adr-verify`, and at least one mutant recorded through `adr-verify --mutant` — for the T1 outcomes the mutant is unusually convincing, because reverting an outcome to `Ran` is a one-token change that must turn the fence red.

Every task also carries a **live-trace confirmation**, not only a green fence. The rule this repository learned on 2026-08-25 applies with full force here: in an instrumented system, a change that adds a signal extends the instrument in the same commit, and the only evidence that counts for an instrument is the instrument's own output against the running artifact. `scripts/redeploy.sh` already gates on a real `am.search` span appearing in the deployed container's log.

## Consequences

A trace stops being a claim and starts being evidence. Concretely: a failed `search_events` write becomes visible instead of inflating a table the recall statistics are read from; a reranker that returned identical scores stops being indistinguishable from one that reordered the page; the operator's own timeout budget stops being reported as a broken dependency; and `am.has_wing` stops meaning three things at once, which makes it usable as a slice for the first time.

The wing/room drop count becomes an alarm the pipeline can raise on its own. Today a divergence between the vector index and the durable rows is detectable only by someone reading `survivorsFrom` and noticing that its guard has to be firing.

Cost: eleven more attributes on the parent span and three on `rankRetrieved`'s enclosing span, all integers, booleans or bounded enums. No text, no unbounded cardinality, no per-hit attribute.

## Out of Scope

- **Backend identity on the span** — `VECTOR_BACKEND`, `EMBED_BACKEND`, the embedding model name (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — it is `cmd/server/main.go` wiring rather than the search path and earns its own record; trigger: the next ADR touching the embed or retrieve wiring, or the next eval table anyone intends to compare across a config change)
- **The six tail findings** — the adaptive BM25 weight's resolved value, the whole-memory-to-400-rune degradation, `SearchQuery.Context` presence, the coerced-to-zero cosine rejection, `closetBoostsAt`'s three discard paths, the evidence stage's window counts (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — all verified, none of them make a span lie)
- **An anchor/staleness stage of its own** (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — T1 makes its failure visible on the enclosing tool span; a new stage is not a list repair)
- **Any change to what `recordSearch` does on failure** (permanent: its documented invariant — measurement that can break the thing it measures is worse than no measurement — is correct; only the span stops lying about it)
- **Telling the CALLER that anchors or a wing lookup failed** (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — a response-shape change needs its own record; trigger: the first support question that turns out to be a silently unflagged stale page)
- **Acting on a non-zero out-of-scope drop count** (deferred: `docs/adr/BACKLOG.md` §"From ADR-029" — T2 makes the divergence visible; deciding what the server does about it has a blast radius this ADR does not take on)
- **Sampling, exporters, or collector configuration** (permanent: untouched; this ADR changes what spans say, never how they are transported)
- **`am_get_drawer`'s `search_id` join** (deferred: `docs/adr/BACKLOG.md` §"From ADR-028" — it remains ADR-028's deferred T3 under its own trigger, unchanged by this record)
- **Any change to ranking, scoring, or the retrieval unit** (permanent: ADR-024 owns the ranking unit; every survivor, score and order is byte-identical before and after this ADR)

## Risks

**A new attribute is a new promise.** `TestDocumentedEnvVarsAreRead` and the reachability gates in this tree exist because a documented-but-unread setting has shipped four times in one week. Every attribute this ADR adds is asserted by a test that fails when the attribute is removed, and T3's set-equality gate is what stops a future stage from being declared and unemitted.

**Set equality is a stricter gate and could cry wolf.** A stage legitimately added to the pipeline now fails the gate until it is declared — which is the intent, but it makes the gate a step in adding a stage rather than a passive check. Stated here so the failure reads as designed rather than as a bug. Measured against the current tree before shipping: both directions are empty under the default fixture today.

**`errors.Is(err, context.DeadlineExceeded)` can misclassify.** A caller-cancelled context also produces a deadline-shaped error in some paths. The classification is asserted against a fixture that expires a real budget, and the fallback remains `ReasonError`, so a misclassification degrades to today's behaviour rather than to a new wrong answer.

**Widening the `survivorsFrom` signature touches the eval path.** `rankRetrieved` has four callers: the served path at `service.go:1025` and three eval-arm sites. The drop counts are recorded via `Annotate`, which paints the current span — the arm span for an eval call, the `am.search` parent for a served call. That is the correct per-caller attribution and it is asserted, because the failure mode is arm numbers reading as served-path numbers in a table nobody re-derives.

## Rollback

Per task, and each is a clean revert: T1 restores the unconditional outcomes, T2 removes attributes and narrows the signature back, T3 restores the length assertion. Nothing persists to disk, no migration runs, no configuration is read, and no served ranking changes — so a rollback at any point returns the system to `dcc1389`'s behaviour exactly, with only the trace losing fidelity.

## Follow-ups

- Backend identity on the span, as its own ADR.
- The six receipted tail findings, to be picked up at the next `/quality-harness:adr-write` sweep via `adr-debt`.
- Once `am.has_wing` is replaced by an honest scope attribute, re-run the recall statistics that were sliced on it. The 10.8:1 ratio retraction is the precedent: a statistic computed over an ambiguous attribute is not merely imprecise, it is unfalsifiable.
