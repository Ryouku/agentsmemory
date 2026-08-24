# ADR-026: A history you cannot query is not a history

**Status:** Proposed
**Date:** 2026-08-25
**Owner:** unassigned
**Spec:** None — no spec stage; grounded in probes against the live graph recorded on issue #23.
**Cross-references:** ADR-004 (supersession is the graph's acceptance criterion — this ADR **amends one clause of its Out of Scope**, see the Amendment section), ADR-010 (supersede, do not overwrite — owns the drawer half and the `reason` field), ADR-007 (no number without its population), ADR-006 (a knob that does nothing must say when), ADR-014 (the shipped default is the measured one), ADR-024 (precedent for a default change that reaches every caller), `internal/palace/kg.go:418` (`KGQuery`), `:491` (`KGTimeline`), `:532` (`Current`), `:553` (`inEffectAt`), `internal/mcpserver/kg.go:95`, issue #23
**Numbering:** next free after ADR-025. Pull request #24 claims ADR-022 and ADR-023 and is still open; ADR-024 and ADR-025 are taken on `main`. This ADR takes 026 and claims nothing else.
**Invalidates:** none — checked. It **amends** one clause of ADR-004's Out of Scope rather than invalidating a decision: ADR-004's bar, pre-registered arm, interval rule, case floor and verdict branches are all untouched, and so is the write path it exists to protect. It takes over items 1–3 of issue #23; item 4 stays with ADR-010.
**Served-path change:** **Yes — a default change.** `am_kg_query` and `am_kg_timeline` return open-ended facts by default instead of every fact ever recorded, and every filtered response carries the count of what it withheld plus the parameter that restores it. `am_search`, ranking, the drawer path, the storage schema and the write tools are untouched.

## Amendment to ADR-004 (this ADR does not proceed without it)

ADR-004 is **Accepted** and its Out of Scope list says:

> - Any change to `kg_add` / `kg_invalidate` / `kg_query` behaviour (**permanent**: the write path is what is being judged, and changing it mid-measurement invalidates the measurement)

Taken literally that forbids this ADR outright, and `permanent` is not `deferred` — in ADR-004's own vocabulary a deferred item becomes reachable through a `justified` verdict while a permanent one never does. This cannot be written around. It is amended here or this ADR is abandoned.

**Proposed amendment: narrow the clause to `kg_add` / `kg_invalidate`.** Two reasons; the second is load-bearing.

1. **The clause's own rationale names only the write path** — *"the write path is what is being judged, and changing it mid-measurement invalidates the measurement."* `kg_query` is a read. The rationale does not reach it; the enumeration swept it in.

2. **The supersession measurement cannot observe `kg_query` at all**, and ADR-004 establishes this itself as the premise of its whole argument: *"nothing in the retrieval path reads the graph … `Service.Search` goes embed → vector search → fusion → closet boost → rerank and never touches a triple."* The gate scores where a superseded **drawer** lands in `am_search`. No arm calls `KGQuery`. Changing how the graph is queried cannot move the number the gate reads, in either direction.

**What stays untouched so the measurement holds:** `kg_add`, `kg_invalidate`, `tripleID`, `CurrentTripleID`, the schema, and the meaning of `valid_from` / `valid_to`. Every fact this graph holds before the change is the same fact after it. Only the questions you may ask change.

**Decided by M, 2026-08-25: amend.** M's words: *"i vote to modify kg_query"*. The rejected alternative was leaving the clause as written and closing issue #23 as blocked on ADR-004 — recorded here because a decision without its alternative reads as preference and gets reopened. T0 carries the edit into ADR-004 itself; T1–T5 are authorised by it and by nothing else.

## Context

The write side is already a proper append-only bitemporal log. This matters because it locates the gap on the read side rather than in the model:

- `tripleID(subID, pred, objID, validFrom, recordedAt)` hashes the validity start **and** the record time, so the same `subject → predicate → object` written at two moments is two rows. It appends; it never overwrites.
- `KGInvalidate` sets `valid_to` and never deletes.
- `CurrentTripleID` dedups only on `valid_to = ''`, so a fact can end and later resume, each stint its own row.
- `valid_from` is backdatable, so history can be recorded retroactively.

**You cannot ask it anything.** `am_kg_query` takes `entity`, `as_of`, `direction`; `am_kg_timeline` takes an entity. `current` appears in every response and in no request.

The default applies no temporal filter at all (`internal/palace/kg.go:553`):

```go
func inEffectAt(row kgTripleRow, asOfKey string) bool {
	if asOfKey == "" {
		return true
	}
```

So `am_kg_query(entity: "X")` — what an agent naturally writes, and what this repo's own `llm_init` bootstrap drawer instructs every session to write — returns every fact ever recorded about X, dead ones included, tagged `current:false` and left for the reader to honour. In a memory server whose argument is that accumulation is affordable **because ended records stop competing**, the cost lands exactly where the argument says it will not: the agent's context window.

### Three predicates wearing one word, and they disagree

This is why "add a `current` boolean" is the wrong patch. `current` is computed at output time as `Current: row.ValidTo == ""` (`kg.go:532`). That is **open-ended**, which is not **in effect now**, which is not **in effect at T**.

| Fact | `current` | `as_of: <today>` | Verdict |
|---|---|---|---|
| Never ended | `true` | returned | agree |
| Ended 2026-08-20 | `false` | not returned | agree |
| Ended **today**, date-only `valid_to` | `false` | **returned** | disagree — `temporalEndKey` pads a date-only `valid_to` to `T23:59:59Z`, so as-of exclusion lags `current` by up to a day |
| `valid_to` set to a **future** date | **`false`** | returned | `current` is wrong in substance: the fact is true right now and reports itself as not current |

The last row is latent rather than observed — nothing in the corpus schedules a future expiry — but `valid_to` is a free parameter on `KGAdd`, reachable by any caller, and nothing rejects it. Naming it now is cheaper than meeting it as a wrong answer later.

**So `current` means open-ended.** This ADR keeps that meaning and stops the word implying the other two, rather than silently redefining a field already on the wire.

### The audit questions that are inexpressible at any price

`KGTimeline` orders by `valid_from = '' ASC, valid_from ASC, id ASC`, and nothing anywhere sorts or filters on `valid_to`. So *"what expired this week?"* — the most natural audit question there is — cannot be asked. *"What did we learn this week?"* is only approximable by reading a timeline and filtering by eye. And the entity-free whole-graph timeline stops at `kgTimelineLimit = 100` with no paging: a trail you can only ever see the first hundred rows of is a sample, not a trail.

## Existing Primitives Audit

- **`inEffectAt`** (`kg.go:553`) — the point-in-time predicate. Reused unchanged as `as_of`. Its `asOfKey == "" → true` early return is not a bug: it is correct for "no as_of was asked". The defect is that no other filter exists to take over when it abstains.
- **`temporalStartKey` / `temporalEndKey`** — normalise a date-only bound to the start and end of its day. Reused verbatim for the new window bounds, so `started_from: 2026-08-01` includes from `T00:00:00Z` and `ended_to: 2026-08-07` includes through `T23:59:59Z`. Inclusive at both ends, which is what a human means by "between the 1st and the 7th" and is already how `as_of` behaves.
- **The two direction loops in `KGQuery`** (`kg.go:447`, `:463`) — already `if !inEffectAt(row, asOfKey) { continue }`. The new predicates are further conditions in the same place. Filtering stays in Go: rows are already fetched by entity, the set is small, and pushing down is a change of shape for no measured gain (ADR-009 is the standing rule against tuning on unmeasured belief).
- **`KGCounts`** (`kg.go:276`) — already owns the SQL form of exactly this predicate: `Where("team_id = ? AND valid_to = ''", teamID)`. "Open-ended" is therefore already expressed once in this repo; this ADR reuses that vocabulary rather than inventing a second one, which is the mistake ADR-010 warns about for validity windows generally.
- **`kgFact` / `KGFact.Current`** (`kg.go:532`) — the output-side flag. Kept, with its documentation corrected to say **open-ended**. Deliberately not renamed: it is on the wire and agents read it.
- **`kgTimelineLimit = 100`** (`kg.go:31`) — becomes the default page size rather than a ceiling. The constant stays; what changes is that it can be paged past.
- **The "no silent truncation" discipline** — `printSupersessionTable` refuses to print an all-zero block and says why; ADR-007 requires a mechanism with no input to report that it did not measure. Reused as the rule governing the default flip, not as code.

## Decision

Four filters, one default change, and a rule that the default may never withhold silently.

### 1. Endedness — `status`

`status` = `current` | `ended` | `all`.

- `current` — `valid_to == ''`, open-ended records.
- `ended` — `valid_to != ''`, closed records.
- `all` — no endedness filter; today's behaviour.

### 2. Point in time — `as_of`, unchanged

Existing semantics and implementation. It answers *"what did we believe at T"*, which no combination of the other filters expresses, because a record that has since ended still satisfies it.

### 3. Windows — `started_from` / `started_to` / `ended_from` / `ended_to`

Four explicit bounds rather than a `from`/`to` pair plus a mode selector. `ended_from`+`ended_to` is *"what expired this week"*; `started_from`+`started_to` is *"what did we learn this week"*. Each bound names the dimension it bounds, so no parameter changes another parameter's meaning.

**All filters compose by AND.** No precedence to learn, and no combination is an error: `status=ended` with `started_from` last month is *"things we learned last month that have since been retracted"* — a real and good question.

A fact with an empty `valid_from` is matched by no `started_*` bound, and one with an empty `valid_to` by no `ended_*` bound. An unbounded record is not "before all dates"; it is undated, and returning it from a dated window would be the same class of error as counting a vacuous pair in ADR-004.

### 4. The default flips to `current` — and says so, every time

`status` defaults to `current` on both tools. That is what makes the accumulation argument true rather than merely stated.

But hiding history by default collides with the reason the history exists, and ADR-010 has already written the collision down:

> A session about to redo a rejected thing does not know to ask for history — that is precisely what it does not know.

So the withholding is never silent. Every response that filtered anything carries what it removed:

```json
{ "entity": "…", "facts": [ … ], "count": 3,
  "status": "current", "withheld": { "ended": 7 },
  "hint": "7 ended fact(s) not shown — pass status:\"all\" or status:\"ended\" to see them" }
```

`withheld` is present only when a filter removed something, so the key's presence is itself information. This is ADR-007's rule applied to retrieval: **a filtered set reports what it filtered rather than presenting itself as the whole.** An agent reading `count: 3` is now reading a number that told it what it left out.

### 5. Transaction time — surface `extracted_at`, and filter on it

`kgTripleRow` carries `extracted_at`, written on every fact (`kg.go:367`, `ExtractedAt: now`). `KGFact` has no such field and no tool returns it. **The graph has recorded transaction time since it was built and has never been able to report it.**

That matters more than a missing convenience, because valid time and transaction time answer different questions and this store has only ever been able to answer one:

| Question | Dimension | Today |
|---|---|---|
| What was true on D? | valid (`valid_from`/`valid_to`) | `as_of` |
| What did we **know** on D? | transaction (`extracted_at`) | inexpressible |
| When did we learn a fact we recorded was already wrong? | both | inexpressible |

So `extracted_at` joins the wire as `recorded_at`, with `recorded_from` / `recorded_to` bounds normalised exactly like the others. **No migration**: the column exists and is populated.

Two more columns are in the same state and ship with it — `source_drawer_id` and `source_file` are stored on every fact and returned by nothing, while `source_closet` beside them is returned. `source_drawer_id` is the costly one: every fact knows which memory asserted it, and no agent can ask. This is the "a column written and never returned" class the palace already named on 2026-08-23, and checked 2026-08-25 it is not claimed by ADR-022, which was rewritten on 2026-08-24 and is about a memory carrying its own scope.

**Why this belongs in this ADR rather than a later one.** It is the same contract, changed once. Adding valid-time filters now and transaction-time filters later is two breaking passes over the same tool for one coherent capability, and the second would have no better argument than the first.

### 6. What is deliberately NOT added, and why the schema stops here

Asked directly — *what will we need in future, so this is the last modification?* — the honest answer has three tiers, and the middle one is a trap this repository has fallen into repeatedly.

**Free now (stored, unreachable):** `extracted_at`, `source_drawer_id`, `source_file`. Covered by §5. No migration, no writer, pure surfacing.

**Needs a column AND a writer, so it lands with the writer, not here:**

| Field | Purpose | Where it belongs |
|---|---|---|
| `reason` | why a fact ended — ADR-010's *"the gap that actually costs money"* | ADR-010. It also changes `am_kg_invalidate`, which the Amendment above deliberately leaves out of scope |
| `ended_by` | which agent or human retracted it | with `reason`; an audit trail wants both or neither |
| `superseded_by` | explicit link to the replacing fact, instead of today's workaround of filing supersession as triples between drawer ids (issue #34) | ADR-010, or its own once supersession is justified |

**These are not added speculatively, and the reason is this repository's own defect record.** A nullable column added ahead of its writer is a capability that is finished and unreachable — the class `AGENTS.md` is built around, and the class §5 is fixing three live instances of. Shipping `reason` empty today would mean explaining in six months why the graph has a reason column that is always blank, which is strictly worse than not having it.

**The counter-argument, stated fairly:** batching schema changes avoids repeat migrations. It is answered by what a migration actually costs here — gorm `AutoMigrate` over SQLite, where a nullable column is close to free. The expensive things are breaking the wire contract twice and carrying dead fields, and neither is helped by adding columns early. So the **contract** is designed once (§1–§5, additive-only response keys); the **schema** grows when something writes to it.

**Deliberately never:** wing-scoping the graph. KG facts are workspace-wide and `TestKnowledgeGraphIsWorkspaceWideNotWingScoped` pins it. That is a decision with a test behind it, not an omission, and reversing it is its own ADR — noted here because "facts are workspace-wide while drawers are wing-scoped" is raised as a defect often enough (issue #34 among them) that its absence from this ADR should be visibly on purpose.

### 7. Paging on the entity-free timeline

`am_kg_timeline` with no entity gains `limit` and `offset` and reports the total it paged through. Without this the filters are half-useful: *"what expired this week"* that silently stops at 100 rows is the truncation §4 exists to forbid, one layer down.

## Alternatives Considered

- **`from`/`to` plus a `window_on: started|ended|overlapped` selector.** Fewer parameters; rejected on house precedent. A mode flag that changes what two other parameters mean is one name for two facts. `internal/palace/eval.go` records the same call made the other way — `DistGap` was named separately from `TopGap` because "a gap over cross-encoder logits and a gap over cosine distances are different quantities on different scales" — and notes one-name-for-two-facts as a defect that file had already carried twice.
- **Bare `from`/`to` meaning overlap.** Simplest, and insufficient: overlap cannot express *"what expired this week"*, which is the question the issue turns on. A fact that started in 2024 and ended Tuesday overlaps every window you could name.
- **`current: true` as a boolean instead of a tri-state.** Cannot express *"only the retracted ones"*, which is the audit direction; and a boolean whose absence means "both" is a tri-state wearing a boolean's clothes.
- **Renaming the wire field `current` to `open_ended`.** More accurate, rejected: it is a live contract agents read, and this would trade it for a word. The documentation is corrected instead; a rename can ride a future breaking change.
- **Keep the default at `all` (non-breaking).** This is the status quo, which is the thing being fixed. Recorded because if T4 is rejected in review, T1–T3 and T5 still stand and the issue is most of the way closed.
- **Default to `current` and stay quiet.** Rejected on ADR-010's argument quoted in §4.
- **Push the filters into SQL.** Deferred, not rejected — see Follow-ups.

## Component / Boundary Impact

| Component | Change | Boundary crossed |
|---|---|---|
| `internal/palace` (`KGQuery`, `KGTimeline`) | Signatures take a filter value instead of a bare `asOf string`. Filtering stays in the existing direction loops | Service API, internal only |
| `internal/mcpserver` (`registerKGQuery`, `registerKGTimeline`) | New optional parameters; response gains `status`, `withheld`, `hint` | **Agent-facing MCP contract** |
| `internal/store`, migrations, schema | **None** — no column added, no row rewritten, no migration | not crossed |
| `Service.Search`, ranking, drawers | **None** — the retrieval path never reads a triple and still does not | not crossed |
| `kg_add`, `kg_invalidate` | **None** — deliberately, so ADR-004's measurement is unaffected | not crossed |

## Wiring & Contract Changes

Stated per parameter, because a parameter documented and unconsumed is the defect class this repository is named after.

| Parameter | Tool | Read by | Default | Behaviour when omitted |
|---|---|---|---|---|
| `status` | `kg_query`, `kg_timeline` | `KGQuery` / `KGTimeline` endedness predicate | `all` at T1, **`current` at T4** | T1–T3: today's behaviour. T4: open-ended only, with `withheld` |
| `as_of` | both | `inEffectAt` (unchanged) | none | no point-in-time filter |
| `started_from`, `started_to` | both | `valid_from` bound via `temporalStartKey`/`temporalEndKey` | none | no start-window filter |
| `ended_from`, `ended_to` | both | `valid_to` bound, same normalisation | none | no end-window filter |
| `recorded_from`, `recorded_to` | both | `extracted_at` bound, same normalisation | none | no transaction-time filter |
| `limit`, `offset` | `kg_timeline` (entity-free) | repo query | `kgTimelineLimit` / 0 | first 100, as today |

Response additions, all additive keys so a later field cannot break a caller: `status` (always, echoing what was applied), `withheld` (only when something was removed), `hint` (only alongside `withheld`), `total` (entity-free timeline only), and three fields that are already stored and were never returned — `recorded_at` (from `extracted_at`), `source_drawer_id`, `source_file`.

## Inter-task Contracts

- **T1 publishes the filter value** that T2, T3 and T5 extend — a single struct carrying `Status`, `AsOf` and the four bounds, passed to both service methods. T2 and T5 add fields to it; neither invents a parallel parameter list. Published as Go code before T2 starts, so the contract is checkable with `go doc` rather than agreed in prose.
- **T3 depends on T1's filter returning a count of what it dropped**, not only the surviving rows. T1 must therefore return `(facts, dropped, err)` shaped so that T3 has a number to report. A T1 that discards the count forces T3 to re-filter, and a second filter is a second place to be wrong.
- **T4 changes only a default**, never a predicate. If T4 needs to touch filter logic, T1 was wrong and the fix belongs there.
- **T5 is independent of T1–T4** and may land in any order relative to them.

## Implementation

| # | Task | Surface | Gate |
|---|---|---|---|
| T0 | **Amend ADR-004's Out of Scope clause** to `kg_add` / `kg_invalidate`, recording the reasoning in ADR-004 itself | `docs/adr/ADR-004-*.md` | Human sign-off. Nothing below starts until this lands |
| T1 | `status` on both service methods and both tools, default `all` | palace + mcpserver | `TestEndedFactIsAbsentFromCurrentQuery` — add a fact, invalidate it, assert absent under `current` and present under `all`; delete the wiring and watch it go red |
| T2 | `started_from/to`, `ended_from/to` | palace + mcpserver | `TestEndedWindowFiltersOnValidTo` — point the predicate at `valid_from` and it must fail. A window test that passes against either column tests nothing |
| T3 | `withheld` + `hint` on every filtered response | mcpserver | `TestFilteredResponseReportsWhatItWithheld` — assert the withheld **number** equals what was removed |
| T4 | **Flip the default to `current`** | mcpserver | `TestDefaultQueryIsCurrentOnly`, plus a release note per ADR-014 |
| T5 | `limit`/`offset`/`total` on the entity-free timeline | palace + mcpserver | `TestTimelinePagesPastTheDefaultLimit` — assert row 101 is reachable |
| T6 | Surface `recorded_at`, `source_drawer_id`, `source_file`; add `recorded_from/to` | palace + mcpserver | `TestEveryStoredTripleColumnIsReturned` — walk `kgTripleRow`'s fields and assert each appears on `KGFact` or is named in an explicit exclusion list. Written derived rather than hand-listed, so a column added tomorrow enters the check in the same commit that creates it |

T1 ships with the old default deliberately, so the filters can be exercised in production before the default moves. T4 is a separate, revertible commit for the same reason.

## Consequences

- **Positive:** the default query stops spending context on retracted facts, which is the claim the storage model already makes. The audit questions the log was built to answer become askable. The `current` / `as_of` disagreement becomes documented rather than latent.
- **Negative:** T4 is a breaking change to the agent-facing contract. A caller relying on the default returning ended facts gets fewer, mitigated only by `withheld` and a release note. ADR-024's default change also owes a release note that has not been written — after T4 that debt is two, and they should ship together.
- **Neutral:** the write path, schema, ranking and every stored fact are untouched; `as_of` behaves exactly as today; T1–T3 and T5 are additive and break nothing.

## Out of Scope

- **Drawer validity windows and recall returning only current drawers** (deferred: ADR-010, Proposed, 0 of 3 — this ADR is the graph half only; the two must agree in vocabulary, and ADR-010 already commits to reusing `valid_to == ''` verbatim).
- **A `reason` on invalidation** (permanent here: ADR-010's "third gap" owns it, and it changes `am_kg_invalidate`, which the Amendment deliberately leaves untouched — issue #23 item 4).
- **Semantic search over the graph** (deferred: `am_kg_query` is an exact entity lookup via `normalizeEntityID`; `am_search` is vector search over drawers with no graph access, so a fact cannot be found without already knowing its entity name. A missing capability, not a missing filter — its own issue).
- **Wiring a graph read into `Service.Search`** (permanent: ADR-004 owns it, reachable only through a `justified` verdict — issue #34).
- **Populating the graph at corpus scale with `kg-extract`** (deferred: ADR-004 gates it).
- **Entity quality** (deferred: issue #41 — the graph harvests Go identifiers, `Repo`, `Fatalf`, `Errorf` topping the degree table. A measurement problem, not a filtering one. Stated honestly: better filters over bad entities return bad facts faster).

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The Amendment is rejected and the work is wasted | Med | Low | T0 is first and blocks everything; nothing is built before the decision |
| The default flip breaks an agent relying on ended facts | Med | Med | `withheld` names what was removed and the parameter that restores it; T4 is one revertible line, separate from T1–T3 |
| `withheld` is computed and never printed | Med | Med | T3's gate asserts the number, not the field's presence. `printSupersessionGate`'s near-miss explanation was computed and thrown away for weeks — 246 characters produced, 0 printed — and only a test reading the value caught it |
| Six parameters is a surface an agent gets wrong | Med | Low | Each is independent and conjunctive; no invalid combination exists to document, and the tool description carries one example per audit question |
| Window bounds read as exclusive | Low | Med | Inclusive at both ends, matching `as_of`'s existing day-padding; stated in the parameter descriptions and asserted at the boundary instant in T2 |
| `current` keeps meaning open-ended while reading as "true now" | Low | Med | Documented in Context and in the field's description; a future `valid_to` is the case that would expose it and nothing writes one today |
| Filters ship over entities that are Go identifiers (#41) | High | Low | Out of scope and stated; this ADR makes the graph askable, not correct |

## Rollback

Per task, and cheap by construction because nothing is written differently.

- **T1–T3, T5** — additive parameters over unchanged storage. Rollback is deleting the parameters and their handlers; no data was written in a new shape, so nothing needs migrating back and no stored fact changes meaning.
- **T4** — the one that can hurt, and the one designed to be revertible: it is a single default value in the tool registration. Reverting restores `all` and every caller sees today's behaviour on the next request. This is why it is a separate commit from T1.
- **T6** — surfacing only. Rollback is removing three response keys; the columns were already written and stay written, so nothing is lost either way.
- **T0** — an ADR edit; reverting restores ADR-004's clause verbatim and the tasks stop being authorised.

No migration, no backfill, no index rebuild in either direction.

## Follow-ups

- **Push the filters into SQL** if an entity's row count ever makes the Go-side filter measurable. Not now: unmeasured, and ADR-009 is the standing rule against tuning on belief.
- **Reconcile `current` and `as_of` at the boundary instant** — the day-lag in the Context table. Documented here; a fix means deciding whether `temporalEndKey`'s end-of-day padding is right for `valid_to`, which touches the write path's semantics and therefore waits for ADR-004's measurement.
- **A `withheld` convention for `am_search`** — if reporting what a filter removed is right here, it is likely right for wing/room-scoped recall too. Worth its own look rather than generalising from one case.
- **The two owed release notes** (ADR-024's default change and this ADR's T4) should ship together, since both change what a caller receives with no flag set.
- **Revisit the entity-free timeline's shape** once #41 decides what an entity is. Paging a trail of Go identifiers is paging noise.
