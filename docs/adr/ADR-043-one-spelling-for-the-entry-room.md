# ADR-043: One spelling for the entry room, and a tier the entry point actually reaches

**Status:** Proposed
**Date:** 2026-08-28
**Owner:** unassigned
**Spec:** None — no spec stage. `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` names this decision in its Non-Goals ("Deciding which entry-point layer is canonical … an ADR-level decision") and proceeds independently of it; this record is that decision, not an implementation of that spec.
**Cross-references:** `docs/adr/BACKLOG.md` (§"Four spellings of one entry point, and the served document teaches a fifth"), `docs/adr/ADR-027-a-maintained-document-is-a-set-of-records.md`, `docs/adr/ADR-036-a-recall-that-answers.md` (T7, T8), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md`, `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md`, `internal/palace/graphquery.go`, `internal/palace/bootstrap.go`, `internal/web/bootstrap-memory.md`, `AGENTS.md`, `README.md`, `model/draf1.md`
**Governs:**
- type: path
  pattern: "internal/web/bootstrap-memory.md"
- type: path
  pattern: "internal/repohygiene/entryroom_test.go"

<!-- Class: every artifact that TEACHES or RESOLVES the room a wing's entry point lives in — as
opposed to one that merely records what was once decided about it. Enumerated 2026-08-28 with
`grep -rln "llm_init\|llm_index\|EntryRoom" --include="*.go" --include="*.md" . | grep -v '^./.git' | grep -v _test.go`
→ 11 files. Five teach or resolve: `AGENTS.md`, `README.md`, `internal/web/bootstrap-memory.md`,
`model/draf1.md`, `internal/palace/graphquery.go`. Six are historical records and are deliberately
NOT members — `CHANGELOG.md`, `docs/adr/BACKLOG.md`, and the four ADR files
(ADR-026, ADR-027, ADR-036/T7, ADR-038): rewriting a record to agree with a later decision is the
evidence-chain edit this corpus exists to prevent. Of the five members, only
`internal/web/bootstrap-memory.md` and `README.md:167` are CHANGED here; `AGENTS.md`,
`model/draf1.md` and `graphquery.go` already say what this decision decides, which is the largest
single argument for the direction taken. Five test files also name the room
(`internal/mcpserver/kgquery_test.go`, `internal/mcptest/registry_test.go`,
`internal/palace/currentonly_test.go`, `internal/palace/recallanswers_spec_test.go`,
`internal/palace/targetauth_test.go`); they consume `palace.EntryRoom` or a fixture and need no edit
because the constant does not change. -->

**Enforced-by:** None — no gate exists at authoring time, and naming one that does not resolve is the rot this header exists to prevent. T1 produces `internal/repohygiene/entryroom_test.go::TestTheServedDocumentTeachesTheRoomTheCodeResolves`, whose universe is the two real artifacts (the constant parsed from `internal/palace/graphquery.go`, the room names read from the served document) rather than a list kept beside them; this header is updated to name it when T1 lands.
**Invalidates:** **ADR-036 T8's scoping, narrowly and deliberately.** T8 put the `must.*` / `ref.*` vocabulary explicitly out of scope for `Bootstrap`, which was correct for T8's goal (replace a 13-call client protocol with one call) and is not correct once the entry room is populated by backfill: a containment edge alone makes `am_entry_point` answer `matched` while returning only the root room's own drawers. T2 amends that scoping and nothing else in ADR-036; ADR-036 remains authoritative over the entry-point API, and this record does not re-decide T7's derived-edge design. Otherwise: ADR-027 is Accepted and this record USES its model (an `llm_init` root spine, an `llm_index` routing drawer) rather than changing it. ADR-038 is untouched — no id is recomputed here. Checked by grepping every record in `docs/adr` for `llm_init`, `llm_index` and `EntryRoom` (4 records, all listed above).
**Served-path change:** An agent that calls `am_bootstrap` or `am_entry_point` on `wing_agentmemories` gets its mandatory tier instead of `unknown_term`, and a new agent reading `/bootstrap-memory` is taught the room the server actually resolves instead of one it does not.

## Context

`BACKLOG.md` §"Four spellings of one entry point" records four layers that all claim to be the
entry point and leaves the choice open, correctly, as a product decision. This record makes it.

Measured 2026-08-28 against the palace this repository's sessions actually use
(`http://localhost:8080/mcp`, `mode: local`, workspace slug `local`, 2,153 drawers):

- `am_list_drawers(wing: "*", room: "llm_init", include_history: true)` → **0**. Not one drawer in
  any wing, and not one ended drawer either: this palace has never held the room the code resolves.
  That is the second independent read of the same fact — `BACKLOG.md` records
  `am_kg_query(entity: "room:wing_agentmemories/llm_init", status: "all")` returning `unknown_term`
  on 2026-08-28, and the two derivations agree.
- `am_list_drawers(wing_agentmemories, room: "llm_index")` → **2 drawers**, whose `source_file`
  values cite `setup.md §4.3` and `setup.md §6` — the served onboarding document, which was
  `setup.md` until commit `bd611a3` and is now `internal/web/bootstrap-memory.md`.
- `am_kg_query(entity: "must", direction: "outgoing")` → **8 facts, `resolution: "matched"`**, whose
  objects are LABELS (`llm_index`, `llm_index_keys`, `llm_open_threads`, `llm_corrections`,
  `human_decisions`, `effective-go`, `memory-orchestration`, `human-decisions`) and not drawer ids.

Counts over the artifacts, same day: `internal/web/bootstrap-memory.md` says `llm_index` 15 times
and `llm_init` 0; `AGENTS.md` says `llm_init` 3 and `llm_index` 0; `model/draf1.md` says `llm_init`
8 and `llm_index` 5; `internal/palace/graphquery.go:471` declares `const EntryRoom = "llm_init"`.

**Two consequences follow that nothing currently reports.**

1. **`AGENTS.md`'s documented traversal is unexecutable against this palace today.** It instructs a
   session to run `am_list_drawers(wing:"wing_agentmemories", room:"llm_init")` with the comment
   `# several drawers; see below`, and then teaches that zero edges from the wrong drawer must be
   read as a failed query. Against this palace the first call returns zero drawers, so the protocol
   ends before the step that would tell you it had.
2. **`README.md:167` teaches a false diagnosis.** It explains `am_bootstrap`'s `unknown_term` as
   happening "on a wing whose `llm_init` drawers were filed before the derived room edges shipped".
   There are no such drawers here to be un-backfilled. The stated cause cannot be this palace's
   cause, so an operator who reads it goes looking for a backfill that would not help.

**One conflict is named rather than resolved here.**
`docs/adr/ADR-036-a-recall-that-answers/tasks/T7-a-wing-names-its-entry.md:27` records a
verification taken 2026-08-26 "from the `wing_agentmemories` `llm_init` root (25 nodes, all hop
<=1)". That cannot be this palace, which has never held the room. It was the hosted deployment or a
fixture. Which one decides whether adopting `llm_init` strands an existing corpus or none at all, so
T3 verifies it against the hosted palace **before** migrating anything, and records the answer
whichever way it falls. This record is written for the direction the evidence supports and names the
observation that could refute the cheap version of it.

## Existing Primitives Audit

- **`palace.EntryRoom` + `EntryPoint` (`internal/palace/graphquery.go:471`, `:509`)** — reuse
  unchanged. The constant already names the room this record makes canonical; nothing about it is
  wrong, which is why no code constant moves.
- **`Bootstrap` (`internal/palace/bootstrap.go`)** — reshape. It takes outgoing edges from the
  derived containment node and never examines `must.*` or `ref.*` (ADR-036 T8, deliberate). T2
  extends it to follow the mandatory tier, because a containment edge alone is the false-reachability
  trap.
- **The `must.*` protocol** — reshape, not replace. It exists in prose only: `must.*` appears in no
  Go source, and nothing in the tree produces or consumes it. T2 gives it a consumer; T3 gives this
  corpus a producer's output in the canonical shape (drawer ids, not labels).
- **`internal/repohygiene`** — reuse. This is where the tree's artifact-agreement gates already live
  (`TestEveryCitedADRResolves`, `TestAgentsMdNamesGatesThatExist`, `TestAHumanObservedSignOffAgreesWithTheIndex`),
  and T1's gate is the same shape: a universe derived from two real artifacts rather than a list.
- **`am_merge_wing` / `am_update_drawer` relocation** — reuse for T3. A memory created at or under
  1,600 runes stays one row and can be relocated for life; both `llm_index` drawers are under that
  ceiling, so no re-chunk is needed.

## Decision

**`llm_init` is the canonical entry room.** The served onboarding document
`internal/web/bootstrap-memory.md` is the outlier and is corrected: §4.3 seeds an `llm_init` root
drawer whose content opens `WHAT MUST I LOAD AT THE START OF A SESSION?`, plus `must.*` knowledge-graph
edges from that root's own drawer id to the drawer ids of the mandatory tier. `llm_index` keeps
exactly the job ADR-027 already gives it — a routing drawer, "which room answers which question" —
and is reached as one of the root's `must.*` targets rather than instead of the root.

**And a resolving entry point must reach the mandatory tier, not merely the root room.** A backfill
that writes derived containment edges alone makes `am_entry_point` answer `matched` while returning
only the root room's own drawers; that is a worse state than `unknown_term`, because the caller has
no way to tell a complete answer from a truncated one. T2 makes `Bootstrap` follow `must.*` targets
into other rooms, and T1's gate covers the artifact half.

**What would make this decision fail, and whether data that could produce that failure exists
today.** The direction rests on `llm_init` being empty everywhere, so that adopting it strands no
corpus. That is measured on the local palace (0 drawers, 0 ended, all wings, 2026-08-28) and is
**not** measured on the hosted one, where ADR-036 T7 recorded a 25-node root on 2026-08-26. T3's
first ordered step is to read the hosted palace. **If the hosted palace holds an `llm_init` corpus in
the canonical root-id → `must.*` → drawer-id shape, this decision is confirmed and T3's migration is
only the local palace's catch-up. If it holds one in the LABEL shape this corpus uses, the two
deployments have diverged and T3 stops for the owner rather than migrating either.** The criterion is
falsifiable because the data that would falsify it is one call away and has not been made; T3 is
`Data dependency: needs a live hosted palace` for exactly this reason.

This decision is valid for the two deployments named — the local self-hosted server and the hosted
SaaS workspace. It says nothing about a third-party palace built from an older served document; those
are covered by the served document's correction going forward, not retroactively.

## Alternatives Considered

- **Adopt `llm_index` as the entry room** (change `EntryRoom`, `AGENTS.md`, `model/draf1.md`, and
  ADR-027's model to match the served document and this corpus). Rejected because it is the larger
  edit for the smaller gain: it changes four artifacts to preserve two drawers, and it does not
  actually make the entry point resolve — `llm_index` has no root drawer and no `must.*` edges from a
  drawer id either, so it would need the same T2/T3 work under a different name. The two drawers it
  would preserve cost one relocation to move.
- **Backfill derived containment edges for the existing rooms and change nothing else.** Rejected
  explicitly, and named here so it is not re-proposed: it is the cheapest-looking fix and it produces
  FALSE reachability. `am_entry_point` would answer `matched` while returning only the root room's own
  drawers, never the mandatory tier the manual protocol traverses — this repository's characteristic
  defect, delivered by the fix for it.
- **Declare the served document canonical for onboarding and the code canonical for the API, and
  document the split.** Rejected because it is the current state, written down. A new agent would keep
  building corpora the server cannot resolve, and the split is invisible from either side.
- **Withdraw the entry-point surface entirely and rely on `am_search("what should I load next")`**,
  which is what the served document's §4.3 actually teaches (a search hop to a routing drawer at rank
  1). Rejected on measurement rather than taste: `am_search` ran 52 times in 8,256 tool calls in
  session `ee8f1fc1`, and a hop that depends on an agent thinking to ask a particular question is the
  failure mode `am_entry_point` exists to remove.

## Component / Boundary Impact

| Component | Ownership after change | One reason to change? |
|-----------|------------------------|-----------------------|
| `internal/web` (served onboarding document) | Unchanged — web owns the document; this record is authoritative over the room it teaches | Yes — it changes when the onboarding protocol changes |
| `internal/palace` (entry point + bootstrap) | Unchanged — ADR-036 remains authoritative over the API; T2 amends only its `must.*` scoping | Yes |
| `internal/repohygiene` (gates) | Gains one gate, same shape as its siblings | Yes |
| The palace corpus (data, not code) | Owned by the operator; T3 is a migration, not a code change | n/a — not a code component, which is why T3 is human-observed |

No module is added, moved or renamed, so `docs/architecture.md`'s Module Map is unchanged.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `internal/web/bootstrap-memory.md` §4.3 (the seeding procedure a new agent follows) | modify — seeds an `llm_init` root drawer and `must.*` edges to drawer ids; `llm_index` becomes one of its targets | T1 | every agent that reads `/bootstrap-memory`; every corpus built from it |
| `README.md:167` (`am_bootstrap`'s documented `unknown_term` cause) | modify — the cause is a wing with no root drawer, not un-backfilled edges | T1 | operators diagnosing a bootstrap that returns nothing |
| `Bootstrap`'s returned tier (`internal/palace/bootstrap.go`) | modify — follows `must.*` targets into other rooms; additive to the existing response shape | T2 | `am_bootstrap` callers; `AGENTS.md`'s one-call path |
| `palace.EntryRoom` | retain — unchanged at `"llm_init"`, and the gate in T1 reads it rather than restating it | none | T1's gate; five existing test files |
| The `wing_agentmemories` corpus (`llm_init` root drawer + `must.*` edges) | add | T3 | every session in this repository |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `entryRoomDisagreements()` — the detection function T1's gate and its falsifiability subtest both drive | T1 | none | No — internal to T1; it is listed because a falsifiability half that shares nothing with the gate pins nothing |
| `Bootstrap` returning `must.*` targets outside the root room | T2 | T3 | No — additive; a wing with no `must.*` edges is unchanged |
| The migrated `llm_init` root drawer id and its `must.*` edges | T3 | none | No — T3 is last |

## Implementation

See `tasks/README.md`. Three tasks: T1 the gate and the document correction it makes green, T2 the mandatory-tier
reachability, T3 the corpus migration with its hosted-palace check.

## Consequences

- **Positive:** `am_bootstrap` and `am_entry_point` answer on this repository's own wing, which is
  the surface ADR-036 T8 built and which has never once worked here. `AGENTS.md`'s traversal becomes
  executable without editing `AGENTS.md`. The four spellings become one, and the three artifacts that
  already agreed (`AGENTS.md`, `model/draf1.md`, `graphquery.go`) are not touched.
- **Positive, and measured in the currency an agent actually spends:** the manual traversal makes a session EMIT drawer ids, and a 64-character hex id BPEs at roughly two characters per token — about 30 output tokens each, so a five-item `must.*` tier costs ~150 output tokens in ids alone before a single one is fetched. Worse, the traversal asks the session to CHOOSE which edges to follow, and deliberating that costs 500-1,500 output tokens against ~225 to fetch five outright. `Bootstrap` returning the tier inline removes both: no ids are emitted, and a decision becomes a lookup. Measured 2026-08-28. This is a second and independent argument for the same code — the record was written on reachability alone, and the cost argument arrives at the same line. ⚠ Shortening the id is NOT the alternative: ADR-038 made it opaque deliberately and `TestNoPathRederivesADrawerID` guards that, because a content-derived key put two identical journal entries in one row and reported two.
- **Negative:** T2 amends a scoping ADR-036 T8 chose deliberately, which is real scope in the palace
  package rather than a documentation edit. And the decision is taken on a measurement of one
  deployment; T3 can stop it, but only after T1–T2 have landed.
- **Neutral:** the two existing `llm_index` drawers keep their content and their ids — they are
  relocated under the root, not rewritten, so nothing in ADR-038's identity model is disturbed.
- **Neutral:** the label-shaped `must` facts in this corpus (`must → must_load → "llm_index"`) are
  superseded by drawer-id-shaped ones rather than deleted, so the label protocol stays readable as
  history.

## Out of Scope

- Reconciling `model/draf1.md`'s mixed usage — `llm_init` 8, `llm_index` 5 — into one vocabulary (permanent: after this record the two words name two different things, an entry room and a routing drawer, so a document that describes both correctly uses both)
- Rewriting `CHANGELOG.md`, `docs/adr/BACKLOG.md`, ADR-026, ADR-027, ADR-036/T7 or ADR-038 to agree with this decision (permanent: they are historical records, and editing one to match a later decision is the evidence-chain edit this corpus exists to prevent — BACKLOG.md gains a new dated entry instead)
- Backfilling derived containment edges for corpora on deployments other than the local and hosted ones T3 names (deferred: `docs/adr/BACKLOG.md` §"Four spellings of one entry point" — entry written in the same commit as this record, naming ADR-043)
- Any change to how `am_search` ranks the routing drawer, or to read cost generally (permanent: `docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` owns read cost and names this decision in its own Non-Goals, so the two proceed independently by mutual agreement of both documents)
- Deciding whether `ref.*` edges join the tier `Bootstrap` follows (deferred: Follow-ups, ADR-043 — T2 covers `must.*` only, because `ref.*` is on-demand by the manual protocol's own design and making it eager would reintroduce the response-size problem ADR-036 T8 measured)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The hosted palace holds an `llm_init` corpus in the label shape, so the two deployments have diverged | Med | High | T3's first step reads it and STOPS for the owner rather than migrating; the decision's falsifier is written into the task, not left to judgement |
| T2's change to `Bootstrap` inflates the response past the budget ADR-036 T8 measured | Med | Med | `must.*` only, `ref.*` stays on demand; T2's Acceptance asserts the truncation report is populated rather than the tier being silently cut |
| A backfill is applied instead of T3's seeding, producing false reachability | Med | High | Named as a rejected Alternative and forbidden in T3's Stop Condition; T2's test fails when the tier is unreachable, so the cheap fix cannot pass the gate |
| T1's gate is written to match the document rather than the constant, so correcting the document is not what turns it green | Med | Med | The gate's universe is `palace.EntryRoom` parsed from source; T1's mutation is changing the constant and watching the gate follow it |
| ADR-036 T7's 25-node claim is simply wrong, and there is no hosted corpus either | Low | Low | T3 records the answer whichever way it falls; a refutation makes the migration smaller, not larger |

## Rollback

Persistent state changes in T3 only. `am_update_drawer` relocation keeps ids, so moving the two
`llm_index` drawers back is one call each; the new `llm_init` root drawer and its `must.*` facts are
retracted with `am_invalidate_drawer` and `am_kg_invalidate`, which end them without erasing them —
the label-shaped facts they supersede stay readable and become current again only by an explicit
re-add, which is the correct behaviour rather than an automatic reversal. T1–T2 are code and
documentation, reverted by reverting their commits; nothing they change is read by a migration that
has already run.

## Follow-ups

- [ ] Report ADR-036 T7's 25-node observation as CONFIRMED (hosted), REFUTED, or FIXTURE, in
      `BACKLOG.md`, whichever way it falls — including "fixture", which would mean this repository has
      never had a working entry point on any deployment and the four spellings were four descriptions
      of nothing.
- [ ] Decide whether `ref.*` joins the tier `Bootstrap` follows, once T2 has a measured response size
      for `must.*` alone.
- [ ] Decide whether the served document should be able to SEED a corpus mechanically rather than by
      instructing an agent to run `am_add_drawer` by hand — the entry point's data has no producer in
      the product, which is the deeper cause of all four spellings and is not fixed here.
