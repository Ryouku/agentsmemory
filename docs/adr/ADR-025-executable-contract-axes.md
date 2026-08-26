# ADR-025: A production promise is a contract axis, not a passing component test

**Status:** Proposed
**Date:** 2026-08-24
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-006 (operator knobs must have an effect), ADR-008 (the MCP surface must be exercised end to end), ADR-012 (write authority), ADR-014 (the shipped default is the measured one), ADR-015 (stored rows and the index must agree), ADR-017 (installed hooks must actually fire), `docs/architecture.md` (concept ownership and composition roots), `docs/adr/BACKLOG.md` “The product is a runtime quality control plane” (profile identity, stage outcomes and search identity)
**Invalidates:** none — this generalises the existing reachability gates and preserves their decisions

**Numbering:** next free after ADR-021. Pull request #25 claims ADR-024 for
ranking (`ADR-024-rank-memories-not-chunks.md`). Ranking itself must consume
`Service.Search` / `rankRetrieved`, not add a selector only one path reads.

**Served-path change:** None in T1. Later tasks change production only when an executable axis exposes a concrete residual; every such fix lands with the observation and mutant that proved the gap.

## Context

The repository has many good tests and a recurring failure they do not prevent: a component works when called directly, while no production selector calls it, an adapter drops the setting, a mirror omits it, or the outer surface reports success without the promised effect.

The current tree demonstrates the ambiguity mechanically. `TestEveryToolIsExercisedEndToEnd` derives its universe from the running MCP server, but it is a ratchet with `uncoveredCeiling = 5`. On current `main` it exits successfully while reporting these five tools with no scenario:

- `am_delete_hallway`
- `am_delete_tunnel`
- `am_delete_wing`
- `am_list_drawers`
- `am_merge_wing`

The test is honest in its source and misleading at the build boundary: a green suite can be read as complete end-to-end coverage when the gate itself says coverage is partial. `docs/architecture.md` names two more classes with no divergence check at all: the read/write classification copied into three adapters, and the fake embedder used by every MCP scenario.

“Fix every bug” is not a finite or falsifiable target. The finite target is:

> Every item reachable from a production registry or composition root has named cases, and every case is selected and observed at the outer surface in both the positive and negative direction — or it carries a typed, owned, expiring exception. Every axis is protected by a compiling selector mutant. The complete residual set is printed every run.

This is the **contract axis**. A field, tool, route, backend, migration or installable asset is an item in a universe. Its production selector is a separate fact. Its externally visible effect is a third. A component test proves none of the latter two by implication.

## Decision

### 1. One small runner, thin adapters

Add a repository-local `internal/contractaxis` test library. The core is stack-neutral: it compares item and case identifiers and records binding/positive/negative calls in a runner-owned observation. An adapter cannot return a precomputed passing evidence struct. The core knows nothing about MCP, urfave/cli, Goose, Chi, embed.FS or Go ASTs. Those details live in thin adapters beside the production surface they inspect.

An axis supplies:

1. **Universe** — derived from the authoritative production structure at runtime, never an item list copied into a manifest.
2. **Cases** — a non-nil, non-empty set of unique, stable names for every item. A case is one selectable contract path such as a role, transport, success/refusal path or mode; it is not an optional label on a shared probe.
3. **Binding** — the real selector or adapter that makes each named case reachable.
4. **Positive observation** — per-case evidence at the outer surface that selecting the case changes the promised state or output.
5. **Negative observation** — per-case evidence that the forbidden effect is absent: another workspace cannot read it, a refused role cannot write it, a deleted object is gone by every read route, an unknown selector fails loudly.
6. **Mutant** — a disposable wire cut or default flip that compiles and makes a named assertion fail.
7. **Maturity** — `enforced`, `ratchet`, or `advisory`.
8. **Exception** — typed ownership of a residual that cannot yet be closed.

Axis IDs are unique within a run. The structural sentinels `*`, `<empty>` and `<registry>` are reserved and cannot be production identifiers. Nil or empty case sets, duplicate case IDs, and declared cases the adapter never observes are structural failures, not empty coverage. The probe receives `(item, case)` while the runner supplies the current axis identity; binding/positive/negative calls are recorded against that exact `(axis, item, case)` tuple, so evidence from one case cannot satisfy another.

The core emits every residual, sorted lexicographically by `(axis, item, case, contract)`. Its stable report and ratchet identity is `escape(axis)/escape(item)/escape(case)/escape(contract)`, where `escape` uses URL path escaping (including `%` and `/` as `%25` and `%2F`); identifiers containing delimiters therefore cannot collide. It never stops at the first failure and never turns an unknown into an all-clear.

The trust boundary is explicit: the core proves which adapter calls it recorded and whether the declared assertion is mutation-sensitive. It cannot prove that a dishonest adapter actually crossed the production boundary. Adapters therefore remain thin and reviewable, and every production axis still needs a wire-cut mutant that would survive if its probe observed a fake or copied list.

### 2. The axis list is finite because the production roots are finite

The runner does not claim to discover arbitrary business rules from source. It covers the selection surfaces this repository declares authoritative:

| Axis | Universe authority | Production selector / binding | Outer observation | Current residual or risk | Target |
|------|--------------------|-------------------------------|-------------------|--------------------------|--------|
| MCP lifecycle | runtime `tools/list` catalogue | registered handler plus scenario invocation | real MCP HTTP call followed by an independent read | 5/41 tools have no scenario | enforced |
| MCP policy and scope | live `CatalogEntry` records and declared read arguments | `registrar.add` / `addWrite`, wing resolution, admission | member/admin/unauthenticated and two-workspace calls | tenancy is examples, not a class gate; read/write is mirrored three times | enforced |
| CLI and configuration | runtime urfave command/flag tree plus reflected `config.Config` fields | `configFromCmd`, env resolution, `buildServices`, command dispatch | parsed real CLI, startup report and behavioural probe | source scans miss aliases/helper reads; CLI/HTTP parity is unbound | enforced |
| Eval and served ranking | registered eval arms/sweeps plus the served ranking shape | `evalArms`, `configureRanking`, production `Search` | emitted rows, candidate depth and served ordering | a “mentioned” arm can still be inert; mode coverage is partial | enforced |
| Persistence and schema | migration files, actual SQLite schema, registered store backends | Goose application and backend factory | schema introspection plus backend conformance round trip | prior duplicate/recorded-without-effect migrations prove the class | enforced |
| Installer and hooks | embedded asset directories plus declared agent-kit capabilities | install/update path and hook registration | files in a temporary agent home plus captured real event payload | Codex subagent hook execution contract remains unmeasured | ratchet until the event contract is captured |
| HTTP surface | runtime Chi route walk | registered handler/middleware chain | request, response and promised state transition | no class-level route reachability inventory exists | ratchet, then enforced |
| Export and redaction | actual database columns plus explicit sensitivity policy | export query and redaction classification | inspect the produced archive | credential regressions are covered by examples, not every future sensitive column | enforced |
| Runtime execution telemetry | enabled semantic stages and feature identifiers derived from the same production registries as their owning axes | instrumentation at the real branch decision | OpenTelemetry trace plus unsampled feature counters correlated by `search_id` and `profile_id` | `search_events` is one page-level row and cannot explain bypasses, fail-open paths or unused enabled features | ratchet, then enforced |

Adding a new kind of production registry or composition root requires adding an axis or an explicit architecture decision that it belongs to an existing one. The runner cannot prove that humans named every concept in the universe; `docs/architecture.md` remains the ownership map, and CI proves every row in that map names an executable axis.

### 3. Maturity is visible and cannot impersonate completion

- **Enforced:** any unexplained residual fails.
- **Ratchet:** typed obligations pin the exact residual identifiers, owner, reason, reference and future expiry — not only a count. A new residual, stale/improved list, ownerless obligation or expired obligation fails. Output and the GitHub step summary say `PARTIAL` and print the obligations even when the ratchet itself passes.
- **Advisory:** diagnostic only. It cannot satisfy an ADR task, release gate or “covered” claim.

The end state of this ADR is zero ratchet axes for in-process production surfaces. Ratchets are migration states, not permanent green substitutes.

### 4. Exceptions are typed obligations

An exception carries:

- axis, item and case identifier;
- kind: `external_dependency`, `policy_undecided`, `unsupported_platform`, or `non_production`;
- owner;
- concrete reason and the issue/ADR that can close it;
- expiry or explicit permanent rationale;
- the observation that remains missing.

Free-text substring admission such as “mentions qdrant” is insufficient: it proves vocabulary, not dependency. An expired, ownerless, unknown-kind or no-longer-present exception fails.

Exceptions and ratchets may own only real per-case contract residuals. Structural instrument failures — including universe or probe failure, invalid/duplicate axis or case definitions, stale ratchet declarations, and invalid or surviving mutant evidence — cannot be excepted or ratcheted into green. Mutation execution may be excepted only when it returns the typed `ErrMutationUnsupported`, and that obligation must use `unsupported_platform`; compile failure, an unapplied patch, an un-killed assertion or missing provenance is a failed instrument, not an external dependency.

`policy_undecided` is not permission to invent behaviour. The current examples are concurrent update semantics, a possible admin-only tier, and mid-session realtime delivery. They stay visible as decision points until their owning ADR settles them.

### 5. Mutants execute away from the working tree

The mutation runner creates a disposable Git worktree, applies one patch, and requires this sequence:

1. the mutant applies cleanly;
2. the directly declared compile command succeeds — the runner never infers compilation from the assertion command;
3. the named assertion fails and emits exactly once a marker constructed from the runner's unpredictable one-run challenge, supplied through a dedicated environment variable;
4. the clean source passes the same assertion;
5. no generated or derived artifact differs after cleanup.

A mutant that does not compile is skipped evidence, not killed evidence. The runner generates an unpredictable nonce for each execution and injects the challenge into the assertion environment; the clean assertion must not emit the resulting marker and the mutated assertion must emit it exactly once. A hard-coded marker or non-zero exit alone is never kill evidence.

Successful evidence is bound to the axis ID, the resolved repository root, the exact `HEAD`, the SHA-256 digest of the patch and the normalized paths changed by that patch. Restoration re-checks both the primary and disposable repository `HEAD`; a content-equivalent empty commit is still a failure. Changed paths include ignored and untracked files, preserve legal whitespace, are sorted, and are serialized as a JSON array so delimiters remain unambiguous. The report prints that provenance together with structured identities for the directly declared compile and assertion commands. Each identity preserves argv boundaries and working directory and carries only environment key names plus a digest of declared values, so secrets are not printed and distinct declarations do not collapse. Evidence therefore cannot be silently replayed for another axis, checkout, patch or declared fence.

The primary worktree must remain unchanged, and a fence that invokes a generator must either regenerate after restoration or prove every generated output in the primary repository and disposable worktree is unchanged. Command directories are resolved through symlinks before execution and special files are rejected without opening them. This is not a general filesystem sandbox: T1 detects changes in the primary repository and disposable worktree, but cannot attest arbitrary ambient filesystem state. Adapters that can write elsewhere must redirect those writes into a declared temporary root and assert it separately.

T1 mutation execution is supported on POSIX platforms where the runner can contain and kill the command's process group. Intentionally detached descendants are outside that guarantee. Windows returns a typed `ErrMutationUnsupported` until a Job Object implementation and native process-tree test exist; killing only the immediate process would make timeout and cleanup claims false.

Every axis has at least one **axis mutant** that breaks the selection rather than the component. High-risk items also keep item-specific mutants.

### 6. Calibrate before generalising

The runner is adopted only if it classifies and kills the repository’s known defect corpus:

- declared eval arm omitted from the registry;
- IDF transform present but unreachable from production `Search`;
- embedding backend implemented but unselectable;
- documented/configured knob never read;
- flag populated from the default but unsettable by an operator;
- CLI adapter dropping the registration’s scope;
- MCP role resolved and reported but not enforced;
- installable agent definition embedded but written nowhere;
- migration version recorded while its schema effect is absent;
- drawer update/delete reporting success while stale chunks remain.

If a historical defect maps to no axis cell, the inventory is incomplete. If its wire-cut mutant survives, the observation is not authoritative enough. The response is to repair the axis, not to weaken the adoption set.

### 7. Runtime traces explain use; they do not define reachability

The executable axes prove that a production choice can be selected and that cutting its wire makes a named outer assertion fail. They do not say which choices real traffic took, where time went, or whether a valid fallback silently became the common path. Runtime telemetry supplies that second kind of evidence.

Instrumentation is semantic rather than line-by-line branch coverage. One search trace carries child spans for embedding, candidate retrieval, row hydration/filtering, closet lookup, fusion, cross-encoder reranking, memory collapse and event recording. Every enabled feature or stage reports a stable identifier and one closed outcome vocabulary: `ran`, `bypassed`, `failed_open` or `failed_closed`. A `profile_id` identifies the resolved ranking/index configuration, and the returned `search_id` correlates the trace with durable relevance feedback. Raw queries, memory content and tenant identifiers are not metric labels; trace-only identifiers follow the deployment's privacy and retention policy.

“Unused” is not inferred from the absence of a span. For every declared feature, unsampled counters record `eligible`, `selected`, `effect` and `fallback`. A feature is unused only when a defined observation window contains eligible traffic but no selection or effect. The contract-axis adapter derives the feature universe from the production registry and fails when an enabled feature has no telemetry binding; a nightly report compares that universe with the counters and prints every never-selected or always-fallback residual. Sampled traces explain individual paths, while counters make population claims. Error and fail-open traces are retained by collector policy; representative successes may be sampled.

SQLite `search_events` remains the durable product-feedback record: search identity, chosen results and later use can support relevance measurement. OpenTelemetry is the operational execution record: stage paths, outcomes and latency. Neither is a reason to split drawer storage before measurement. The trace first shows whether loss occurred at retrieval, fusion, reranking or chunk-to-memory collapse; memory-level schema changes are justified only when held-out cases show that the logical chunk boundary is the limiting stage.

Telemetry is best-effort and must not change the served decision. Export failure drops observability, not the search; the missing telemetry is itself exposed as an instrument-health signal rather than reported as an all-clear.

### 8. Land in five reviewable PRs

1. **ADR + runner:** types, reporting, exception validation, mutation sandbox, and self-tests. No production behaviour change.
2. **MCP closure:** five missing scenarios, zero ceiling, class-level tenancy, one read/write classification, CLI-vs-HTTP parity.
3. **CLI/config/eval:** behavioural binding for commands, flags, environment, config fields, mode scopes and served/eval parity.
4. **Persistence/installer/web:** migrations, backends, assets/hooks, routes, exports and the remaining typed exceptions.
5. **Runtime execution telemetry:** profile/search identity, semantic stage outcomes, OpenTelemetry spans and feature-usage counters, then an axis that compares the production feature universe with its instrumentation bindings.

Do not stack all production fixes into one review. Each confirmed residual is a logical commit with its observation and mutant evidence. A PR may reduce a ratchet; only the PR that reaches zero changes that axis to `enforced`.

## Alternatives Considered

*Added 2026-08-25. The section was absent, not empty. The entries below are EXTRACTED from arguments
this ADR already makes in its own Decision and Consequences — they are not reconstructed after the
fact. An alternative nobody recorded weighing cannot be recovered, and inventing one would be the
fabrication the Mutation Log exists to prevent. If options were weighed that are not listed here,
they belong here and only the author can add them.*

- **The status quo — coverage claims scattered across test logs, ADR tables and reviewer memory:**
  rejected because a claim held in three places at once is a claim nobody can check. This ADR's own
  Consequences state the aim as "one report shows residual coverage instead of scattering it across
  test logs, ADR tables and reviewer memory".
- **Ordinary line or branch coverage as the instrument:** rejected because the states this ADR cares
  about are semantic and a coverage percentage collapses them. Its Consequences name five that must
  stay distinguishable — "implemented", "selectable", "selected", "effective" and "failed open" —
  and its Out of Scope draws the same boundary: only declared semantic decisions are instrumented,
  never every source-level branch.

## Runner self-contract

Before an adapter may protect production, the runner’s own tests must prove:

- axis IDs are unique and duplicate IDs fail structurally;
- an empty universe fails instead of passing vacuously;
- every item declares a non-nil, non-empty case set, duplicate case IDs fail, and a declared-but-unobserved case remains a residual;
- an item added to the universe with no binding appears as a residual;
- a case binding with no positive observation remains a residual;
- a positive-only case does not satisfy the negative contract, and observations from another case cannot satisfy it;
- a copied/claimed coverage list cannot outrank calls the harness actually recorded;
- an unknown, stale, ownerless or expired exception fails;
- structural instrument failures cannot be excepted or acknowledged by a ratchet, and mutation unsupported is admitted only as typed `unsupported_platform`;
- a ratchet reports exact residual identifiers and fails on both regression and unacknowledged improvement;
- a mutant is bound to its axis, repository, `HEAD`, patch digest and changed paths; must pass its explicit compile fence; must fail the named assertion with a runner-generated nonce marker; and must leave the clean repository and disposable worktree unchanged;
- POSIX cleanup proves process-group containment without claiming containment of intentionally detached descendants or writes to arbitrary external paths.

## Consequences

- **Positive:** “available”, “covered” and “wired” become comparable, machine-checked claims across stacks.
- **Positive:** one report shows residual coverage instead of scattering it across test logs, ADR tables and reviewer memory.
- **Negative:** outer-surface probes are slower than unit tests, and mutation evidence costs additional builds.
- **Negative:** a generic core does not remove per-stack adapter work; it makes that work explicit and reusable.
- **Positive:** runtime traces distinguish “implemented”, “selectable”, “selected”, “effective” and “failed open” instead of collapsing them into one success bit.
- **Negative:** telemetry introduces cardinality, privacy and sampling constraints; population claims therefore come from bounded counters, not sampled traces.
- **Neutral:** component tests remain useful. They prove local behaviour; contract axes prove selection and externally visible effect.

## Wiring & Contract Changes

None — implementation-internal only. `internal/contractaxis` is imported by no
production package (checked 2026-08-25); it is a harness the test suite drives.

## Out of Scope

- Proving the absence of every possible bug (permanent: no finite gate can, and claiming otherwise is the failure this ADR is about)
- Choosing product policy for concurrent updates, realtime delivery or privilege tiers (permanent: product policy, not a contract axis)
- Treating live Qdrant, TEI, OAuth or model quality as hermetic. Those require a separate integration cohort with typed dependencies (deferred: docs/adr/BACKLOG.md — needs its own integration cohort)
- Publishing a universal external tool before this implementation has passed at least three different-stack pilots. The data contract is stack-neutral now; extraction is earned by use. (deferred: T6 owns the pilots that would justify it)
- Recording every source-level branch. Only declared semantic decisions are instrumented; ordinary code coverage and mutants remain the executable evidence for source paths. (permanent: only declared semantic decisions are instrumented; ordinary coverage is a different instrument)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The inventory is another hand-maintained list | High | High | Item universes come from production registries; architecture rows must name executable axes |
| A probe observes its own fake instead of production | Med | High | Every adapter names the substituted boundary and carries parity or a typed exception |
| Mutation evidence damages generated files | Med | High | disposable worktree, clean-diff assertion, generator-aware restoration |
| Ratchets become permanent green debt | High | High | exact residual IDs, owner/expiry, visible `PARTIAL`, final acceptance requires enforced |
| One giant PR becomes unreviewable | High | Med | five PR sequence and one logical fix per commit |
| Sampled traces make an unused feature look absent | High | High | unsampled eligibility/selection/effect/fallback counters; traces explain examples but never establish population coverage |
| Telemetry leaks query or tenant data through labels | Med | High | no raw query/content/tenant metric labels; bounded identifiers and deployment retention policy for traces |

## Acceptance

ADR-025 is complete only when:

1. every axis above is `enforced` or has an unexpired typed exception for an external/policy boundary;
2. the live MCP catalogue reports 0/41 tools without an observable scenario;
3. the historical calibration corpus maps to an axis and its representative mutant is killed;
4. CI publishes the complete residual report on every run;
5. `gofmt`, `go vet ./...`, `go test ./... -count=1`, and `go build ./...` pass on the clean tree;
6. an independent fresh-context review finds no claim of coverage that the executable report contradicts.
7. every enabled semantic feature has an executable telemetry binding, and the runtime report distinguishes no eligible traffic from eligible-but-never-selected.

## Implementation

*Amended 2026-08-25: the `tasks/` directory held a README and no task files — a plan, never task
files. It is inlined here so the record is self-contained, and the directory removed rather than
back-filled with six task files written to match code that already shipped.*

> **Amended 2026-08-25.** The rows below were a plan, not task files: none was ever
> written, while T1's design shipped anyway as `internal/contractaxis` (a runner with a
> 15-test mutation suite, imported by no production package). They are kept here as prose
> so the intended scope survives, and deliberately NOT re-written as task files after the
> fact — a plan invented to match code that already shipped is the same fabrication the
> Mutation Log exists to prevent. Re-scoping this record is a decision for its owner.

- **T1** — Add `internal/contractaxis`, named per-item cases, exact residual reports, typed exceptions and disposable mutation execution
- **T2** — Make every live MCP tool observable and derive adapter policy from one catalogue
- **T3** — Bind commands, flags, environment, config fields and eval arms to real behavioural effects
- **T4** — Bind schema, stores, assets/hooks, routes and exports to outer observations
- **T5** — Trace semantic runtime decisions without making sampled traces the source of population claims
- **T6** — Run the same data contract in two other stacks and decide whether to extract a standalone tool

**Planned sequence** (prose for the same reason as above — these are not task files):

1. T1 — runner and inventory — no dependencies (track A)
2. T2 — MCP closure and adapter parity — after T1 (track B)
3. T3 — CLI/config/eval axes — after T1 (track C)
4. T4 — persistence/installer/web axes — after T1 (track D)
5. T5 — runtime execution telemetry axis — after T1, T3 (track E)
6. T6 — three-stack extraction decision — after T2, T3, T4, T5 (track later decision)

## Rollback

T1 is test tooling and documentation only. Later tasks keep production fixes separate from the generic runner, so the runner can be reverted without reverting corrected production behaviour. Removing an axis after it found a real defect requires preserving the focused regression test that now protects that defect.
