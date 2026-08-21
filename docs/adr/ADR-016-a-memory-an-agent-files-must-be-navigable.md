# ADR-016: A memory an agent files must be navigable, or the graph must say it is empty

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-015 (defers the "a merge invalidates the derived graph" question here), ADR-003 (blocked on `mine` never having run — the same root as this)
**Invalidates:** none — checked (grepped ADR-001..015 for `hallway`, `entities`, `RecomputeGraph`, `traverse`: no accepted ADR consumes the derived graph)
**Served-path change:** `am_list_hallways`, `am_traverse` and `am_graph_stats` currently describe a graph that is empty and can never be otherwise on a palace populated through `am_add_drawer`. Either they start describing a real graph, or they say why they cannot — today they return an empty result that is indistinguishable from a working tool with nothing to report.

## Context

Measured 2026-08-21 against the live self-hosted palace, 359 drawers filed by agents over three days:

- **`hallways`: 0. `closets`: 0. Drawers carrying any entity: 0 of 359.**
- A hallway is derived by `computeHallwaysForWing` from drawers whose `entities` column holds two or more names. `extractEntities` is called in exactly one place in the tree — `internal/palace/mine.go:155`. `Service.Add`, the path every `am_add_drawer` takes, constructs its `Drawer` rows without touching `Entities` at all.
- So on any palace populated through the agent surface, `hallways` is not empty-for-now. It is structurally unreachable: `am_recompute_graph` reports success and derives nothing, however often it runs, and the merge tool's own description — "Run am_recompute_graph afterwards to rebuild hallways/tunnels" — is advice that cannot have an effect on hallways.
- `tunnels`: 6, all created explicitly by `am_create_tunnel`. The DERIVED half (entity tunnels) shares the same input and is equally unreachable.
- The knowledge graph is fine and in use — 133 entities, 75 triples — because `am_kg_add` writes facts directly rather than deriving them.

**How it survived.** `TestGraphHallwaysAndEntityTunnels` passes, and it populates its wings with `svc.Mine`. Every hallway test does. The subsystem is thoroughly tested against the path agents do not use and untested against the path they do — the repository's own recorded failure shape: *every one of them had tests, and every test exercised the component rather than the selection.*

This is the fourth surface in this repo found finished and unreachable, and the first where the unreachable thing is a whole domain concept with read, delete and recompute surfaces and no producer.

**T1's measurement, taken 2026-08-21 against the live palace, 366 drawers.** Wing names and entity
names are withheld — both name real projects and real identifiers, and this file is public.

| | drawers | ≥1 entity | ≥2 entities | hallways derivable |
|---|---|---|---|---|
| total | 366 | 189 (52%) | **90 (24.6%)** | 43 |

**24.6% against a pre-registered bar of 20%: it clears. T2 is not withdrawn.** The frequency rule
survives contact with short, deliberate, agent-written memories better than the risk row feared, and
the derived graph would hold 43 hallways rather than the 0 it holds now.

**And the same run says the extractor must not be wired as it stands.** The report prints the most
frequent candidates per wing precisely so noise is visible rather than inferred, and roughly half of
them are not entities at all: they are ordinary English words an agent SHOUTED for emphasis —
conjunctions, past-tense verbs, adjectives, status words. Two causes, both checkable:

- `candidateWordRE` is `\p{Lu}[\p{L}]*`, an uppercase letter followed by letters, so an all-caps
  word matches. In prose, capitalisation marks a proper noun; in an agent's memory it marks emphasis,
  and this corpus is full of it.
- The vendored COCA list is 1,016 CONTENT words. It excludes common nouns and verbs from being
  entities, and it was never a function-word stoplist, so it filters none of them.
- `entityStoplist` — the map consulted immediately before the COCA check — is declared as an empty
  literal. It exists, it is read on every candidate, and it holds nothing. **(Wrong: see the
  correction below. An `init()` fills it with 52 words; the defect is that it matched
  case-sensitively.)**
  **Corrected by T2:** it is declared empty and then filled by an `init()` at the foot of the same
  file with 52 words, of which 20 add coverage COCA does not already have — `And`, `Assistant`, and
  the days and months. The map is not inert. What it is, is nearly redundant with COCA and matched
  CASE-SENSITIVELY, so it held `And` and let `AND` straight through — which is the same defect
  arriving by a different route, and worth recording accurately because "the list is empty" and "the
  list is case-sensitive" have different fixes.

So the pre-registration measured the right thing and was silent about the thing that matters as
much. That is a defect in the criterion, recorded rather than quietly widened: the bar decided
whether to PROCEED and it says proceed, and the ADR's own risk row already asked T1 to report what
the graph would look like "so the threshold is set against real data rather than guessed".

**Two of the three causes above are wrong, and T2 measured its way to the right one.** They are left
in place because what they got wrong changes the repair.

- **The shape rule is not the leak.** Over 163 ordinary English words, 47 survived SHOUTED and 46
  survived in Title Case — differing by exactly one word. The noise arrives through Title Case too,
  so NO rule over a token's SHAPE can separate `HTTP`/`MCP`/`ADR` from `AND`/`WAS`/`MISSING`:
  survivors run 3–11 characters and acronyms 3–12. The fix this ADR prescribed — narrowing
  `candidateWordRE` — was run as a mutant and killed all 47 acronyms.
- **`entityStoplist` is not empty.** It is declared as an empty literal and then filled by an
  `init()` at the foot of `entity.go` with 52 words, 20 of which add coverage beyond COCA. It was
  never inert. It was nearly redundant with COCA, and it matched CASE-SENSITIVELY, which is why
  `AND` survived while `And` did not. "Empty list" and "case-sensitive list" have different repairs,
  and this ADR asked for the wrong one.

The real fix is lexical: a case-folded stoplist carrying the closed-class function words, irregular
verb forms and status participles that a 1,016-word CONTENT list structurally cannot hold, plus
inflection reduction back into COCA. The two halves are separately load-bearing — 46 of the 62
must-exclude words are caught by the stoplist and 16 ONLY by inflection reduction.

**Re-measured on the live palace after T2, 2026-08-21:**

| | drawers | ≥1 entity | ≥2 entities | hallways derivable |
|---|---|---|---|---|
| before T2 | 366 | 189 (52%) | 90 (**24.6%**) | 43 |
| after T2 | 392 | 188 (48%) | 88 (**22.4%**) | 39 |

Still clears the 20% bar. **The two runs are not a clean comparison and the difference must not be
quoted as the fix's cost:** the corpus grew by 26 drawers between them, so the extractor and the
population both changed. What IS clean is the qualitative half, and it is what the change was for —
`AND`, `WAS`, `MISSING`, `FINDING` and `SIGNED` no longer appear among the most frequent candidates
in any wing, where before they dominated several.

**And the extractor is better, not clean.** `HOST`, `TAG`, `APPROVE`, `BEHAVIOUR`, `DISABLED`,
`ACCEPTED`, `ROLLBACK` and `DELTA` still get through, and the plural reduction that correctly kills
`Depends`/`Produces`/`Covers` also kills `Windows` → `window`, which is a real name lost. Recorded
here rather than rounded up.

**T2's re-measurement, 2026-08-21 — and the candidate rule was NOT the thing to fix.** Measured over
163 ordinary English words in both cases: 47 survived the extractor SHOUTED, and 46 survived in
Title Case. The two sets differ by exactly one word, `AND`. So the all-caps regex was not what let
the noise in — the noise was in Title Case too, and narrowing `candidateWordRE` would have fixed one
word out of 47 while costing every acronym, since `HTTP`, `MCP`, `ADR`, `TEI` and `RRF` are all-caps
and all entities. No rule over a token's SHAPE separates them from `AND`, `WAS` and `MISSING`: the
shouted survivors run 3–11 characters and the acronyms 3–12.

What discriminates is a LEXICON, so the repair is there. `entityStoplist` is now keyed lowercase and
consulted case-insensitively, it holds what COCA structurally cannot (closed-class function words,
irregular verb forms, status participles), and a lookup that misses falls back to COCA through the
regular English inflections — because COCA holds `ship` and `change` while an agent writes `SHIPPED`
and `CHANGED`. After:

| battery | before | after |
|---|---|---|
| ordinary words surviving, ALL CAPS | 47 / 163 | **2 / 163** (`RANKING`, `OPTIONAL`) |
| ordinary words surviving, Title Case | 46 / 163 | **2 / 163** |
| acronyms still extracted | 47 / 47 | **47 / 47** |
| ordinary-word product names still extracted (`Atlas`, `Vault`, `Delta`, `Sentry`…), both cases | 23 / 23 | **23 / 23** |

Now excluded: `AND WAS WERE BEEN ARE MISSING BROKEN STALE DEAD SHIPPED ADDED REMOVED CHANGED FAILED
WORKED RETURNED TOOK GAVE WROTE BROKE TESTING WORKING WRITING COUNTING UNLESS WHEREAS NOBODY`. Still
admitted, recorded rather than hidden: `RANKING` and `OPTIONAL` (COCA holds neither `rank` nor a
route to `optional`, and both name real things elsewhere), and the plural reduction that removes
`Depends`, `Produces`, `Scores`, `Covers` and `Services` also removes `Windows` — a real name lost to
`window`. The extractor is better, not clean.

**The corpus caveat, stated so the number cannot be misquoted.** T2 could not re-run `doctor --graph`
against the live palace: doing so needs the image rebuilt from the change, which would disturb the
running container. The batteries above are hermetic, but the corpus figures were taken over a
FIXTURE — this repository's own 99 agent-written markdown files, chunked as drawers — where the share
carrying two or more entities moves 41.8% → 40.6% and derivable hallways 251 → 231. That is a
different population from T1's 366 live drawers and is NOT a re-measurement of the 24.6%. It says
only that the repair costs a few percent of yield rather than a category of it; whether the live
share still clears the 20% bar is a `doctor --graph` run an operator still owes this ADR.

## Existing Primitives Audit

- **`extractEntities`** (`internal/palace/entity.go:166`) — frequency-and-length extraction over a chunk's text, already used by mining. Reuse, with one correction T1 measured into existence: its candidate rule admits all-caps emphasis, and its stoplist is empty. The question was going to be only where it is called; the corpus says it is also what it admits.
- **`Service.Add`** (`internal/palace/service.go`) — already chunks, embeds and writes rows. Reshape: one field set per row.
- **`RecomputeGraph`** (`internal/palace/graphquery.go`) — already derives hallways and entity tunnels from `drawers.entities`, prunes, and preserves prior dynamics. Reuse unchanged: it works, it has simply never had input.
- **`emptyWingNote`** (`internal/mcpserver/emptywing.go`) — the precedent for the second half of this decision. A zero-hit page that says WHY it is empty already exists on the search path; the graph tools need the same and have none.

## Decision

Two halves, and the second is not optional.

**1. `Service.Add` stamps entities on every drawer it writes**, using the same `extractEntities` mining uses. A memory filed by an agent then participates in the derived graph exactly as a mined one does, and `am_recompute_graph` has something to recompute.

**What would make this fail, and the data exists to check it today.** The extraction is frequency-based: a term must appear at least twice and be longer than two characters. Agent-written memories are short and deliberate where mined transcripts are long and repetitive, so the pre-registered risk is that most drawers yield fewer than two entities and hallways stay empty for a subtler reason. That is measurable before the code is written, by running `extractEntities` over the 359 drawers already filed and counting how many would carry two or more. **If fewer than 20% would, the frequency rule is wrong for this corpus and this half is withdrawn in favour of a different extractor** — not shipped and hoped over. Valid for: this palace's agent-written corpus; a transcript-mined palace already works.

**2. A graph tool that cannot answer says so.** `am_list_hallways`, `am_traverse` and `am_graph_stats` return a note when the wing holds drawers but no entities — the same shape as the empty-wing note, and for the same reason: an empty result and a broken feature are byte-identical to an agent, so it concludes the graph is empty and stops asking. This half holds even if half 1 is withdrawn; in fact it matters more then.

## Alternatives Considered

- **Extract entities at recompute time instead of at write time.** Rejected: `RecomputeGraph` would re-derive entities for every drawer on every run, and the stored `entities` column exists precisely so it does not have to. It also leaves `drawers.entities` permanently empty, so anything else reading it stays broken.
- **Use a model to extract entities on write.** Rejected for the write path: `am_add_drawer` is on the interactive path an agent waits on, and a model call per chunk buys quality nobody has yet shown is needed. `kg-extract` already exists for the model-based route and runs offline.
- **Delete hallways, `am_traverse` and `am_graph_stats`.** Genuinely considered and rejected: the derivation is written, correct, tested, and works the moment it has input. Deleting a working subsystem because its producer was never wired is the wrong repair. But if half 1's falsification fires and no extractor suits the corpus, this becomes the honest option and is recorded here so it is a decision rather than a drift.
- **Ship half 1 alone.** Rejected: if the extraction turns out thin on some corpus, the tools go back to being silently empty and nothing says so. Half 2 is what makes the failure legible.

## Component / Boundary Impact

`internal/palace` keeps ownership of what a drawer is and what the graph derives; `internal/mcpserver` keeps ownership of what an agent is told. Half 1 is internal to the write path. Half 2 adds a note to three tool handlers, reusing the shape `emptyWingNote` already established. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `drawers.entities` populated on the `Add` path | change | `internal/palace/service.go` | `RecomputeGraph`, `computeHallwaysForWing`, entity tunnels |
| an `emptyGraphNote` on the three graph tools | add | `internal/mcpserver` | every agent that asks about the graph |
| `am_add_drawer` write latency | change — one extraction pass per chunk, no model call | `internal/palace/service.go` | every writer |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the measurement that decides half 1 | T1 | T2 | No — T1 is a measurement and may withdraw T2 |
| the shared extractor and its corrected lexicon | T2 | T4 | No — T4 calls the same function from a third write path |
| entities on the `Add` path | T2 | T2 | No — additive; existing drawers keep their empty column until a backfill |
| `emptyGraphNote` | T3 | T3 | No — additive |

## Implementation

Four tasks: `tasks/README.md`. T4 was added after T2 landed and measured what it had left out.

## Consequences

- **Positive:** the navigable half of the product starts existing for the people who actually use it. `am_traverse` and `am_list_hallways` stop being tools that have never once returned anything.
- **Negative:** every write does an extraction pass over its own text. It is string frequency counting, not inference, but it is not free and it is on the interactive path.
- **Neutral:** drawers filed before this change keep an empty `entities` column and remain outside the graph until something backfills them. A palace will therefore have a graph over its recent memories and not its older ones, which is worth stating in the note rather than leaving to be discovered.

## Out of Scope

- Backfilling `entities` for drawers already filed (deferred: docs/adr/BACKLOG.md)
- Model-based entity extraction on the write path (permanent: `kg-extract` owns the model-based route and runs offline; putting a model call on an interactive write is a different product decision, not a variation of this one)
- Whether the closet prior should be revived (deferred: docs/adr/ADR-003-retire-the-closet-prior.md — closets are empty for the same root cause, and ADR-003 owns that question)
- Making a merge rebuild the derived graph (deferred: docs/adr/ADR-015-the-index-must-not-outlive-the-wing-it-indexed.md — received from ADR-015; a merge cannot be said to invalidate a graph that cannot exist, so this ADR has to land first)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Agent-written memories are too short for a frequency-based extractor, so hallways stay empty for a subtler reason | High | High | T1 measures it on the 359 drawers already filed BEFORE T2 is written, and withdraws T2 below 20% |
| Extraction adds latency to every write | Med | Low | T1 measures it in the same pass; it is counting, not inference |
| The graph fills with noise entities and hallways connect nothing meaningful | Med | Med | The co-occurrence threshold already exists in `computeHallwaysForWing`; T1 reports what the derived graph WOULD look like, so the threshold is set against real data rather than guessed |

## Rollback

Half 1 is one assignment; reverting it stops new drawers carrying entities and leaves existing ones harmlessly populated — a stale `entities` column changes nothing except what the graph derives. Half 2 is additive text on three read-only tools. No schema change, no migration, nothing to undo in storage.

## Follow-ups

- **`WriteDiary` is a second producer with the same defect.** Found while implementing T2:
  `Service.WriteDiary` (`internal/palace/service.go`) builds its own `Drawer` rows in its own chunk
  loop and never sets `Entities`, exactly as `Add` did. So every `am_diary_write` entry stays outside
  the derived graph after this ADR lands, and that is not a rounding error: on the day T2 was
  implemented the live palace held 383 drawers, 119 of them in `diary` rooms — 31% of the corpus this
  ADR exists to make navigable. Not fixed here because this ADR scopes half 1 to `Add` and a
  silent widening is how a decision stops being one. It is the repo's signature failure shape a
  second time, and it wants its own task.
- **Re-run `doctor --graph` against the live palace** once the image is rebuilt, and paste the output
  beside T1's table. T2's corpus figures are from a fixture and say nothing about whether the live
  share still clears the 20% bar.
- [ ] `Service.WriteDiary` has the identical defect T2 fixed in `Service.Add`: its own chunk loop, its own `Drawer` rows, and no `Entities`. Measured 2026-08-21 on the live palace — 119 of 383 drawers are in diary rooms, so **31% of the corpus stays outside the derived graph** after this ADR. Not folded into T2, because this ADR scopes half 1 to `Add` and widening it silently would stop it being a decision. It wants its own task.
