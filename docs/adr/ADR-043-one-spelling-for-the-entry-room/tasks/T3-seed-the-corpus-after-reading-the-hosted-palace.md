# Task ADR-043-T3: Read the hosted palace, then seed this repository's entry point

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the `wing_agentmemories` `llm_init` root drawer and its `must.*` edges
**Consumes:** `Bootstrap` returning `must.*` targets outside the root room (T2)
**Data dependency:** needs a live hosted palace for step 1, and the local palace (`http://localhost:8080/mcp`) for steps 3-6. Neither is reachable from a clean checkout, which is why Acceptance is human-observed and the sign-off records what the run was taken against.

## Goal

Resolve ADR-036 T7's 25-node claim against the hosted palace first, then seed `wing_agentmemories` with the canonical root so `AGENTS.md`'s documented traversal is executable.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/BACKLOG.md` | edit | §"Four spellings of one entry point" records the answer to step 1 whichever way it falls, and the deferral this ADR files against it |
| `docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-seed-the-corpus-after-reading-the-hosted-palace.md` | edit | The sign-off line, written by `adr-verify --human` |
| `docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/README.md` | edit | The status cell the sign-off maps onto — `done` for ship, `failed` for withdraw, `blocked` for blocked |

No source file changes. What SELECTS the seeded data is `Bootstrap`'s `must.*` walk from T2; without
T2 this task writes drawers nothing reads, which is why `Depends-on` names it.

## Ordered Steps

1. **Read the hosted palace before writing anything anywhere.** `am_list_drawers(wing: "wing_agentmemories", room: "llm_init")` against the hosted workspace, then — if it returns drawers — `am_kg_query(entity: "<root drawer id>", direction: "outgoing")` and record the subject/predicate/object shapes. This is the discriminator `BACKLOG.md` names, and it is used instead of `am_entry_point`, which cannot tell "no such room" from "drawers filed before derived containment edges shipped".
2. Classify the result into exactly one of three, and record it in `BACKLOG.md` with the date and the workspace it was taken against: **canonical shape** (root-id → `must.*` → drawer-id) → the decision is confirmed and this is the local palace's catch-up; **label shape** (the local corpus's `must` → `must_load` → label) → the deployments have diverged, STOP; **nothing** → ADR-036 T7's observation was a fixture, record that, and continue.
3. On confirm-or-nothing only: file the root drawer into `wing_agentmemories` room `llm_init`, content opening `WHAT MUST I LOAD AT THE START OF A SESSION?`, following the §4.3 that T1 corrected.
4. Add `must.*` facts from that root drawer's own id to the drawer ids of the mandatory tier — the two `llm_index` drawers (`0715011203df…`, `8814ff9f0f…`), `llm_open_threads`, `llm_corrections`, `human-decisions`. Ids, not labels.
5. Supersede the label-shaped `must` → `must_load` facts with `am_kg_supersede` rather than invalidating and re-adding: the two-call sequence ends the old value at day precision and leaves both values in effect for the rest of the day, and leaves the graph with zero current values if the session dies between them.
6. Verify by calling `am_bootstrap(wing: "wing_agentmemories")` and confirming it returns the mandatory tier — not `unknown_term`, and not the root room's own drawers alone. Then run `AGENTS.md`'s documented traversal by hand and confirm step 1 of it now returns drawers, which it does not today.
7. Sign off with `adr-verify --human`, recording the workspace, the date, the counts before and after, and the decision word.

## Acceptance

Acceptance is human-observed: an operator runs the six steps above against the two named palaces and
signs off with `adr-verify <this file> --human "<one line>"`, using exactly one of the three decision
words. All three templates are given, because a template that offers only the happy word is how an
honest third outcome ends up in free text no tool reads — measured 2026-08-28 in this corpus, where
ADR-001 T3's hint offered `decision <ship|withdraw>`, the run reached a third state, and every routing
tool answered `done`.

The migration ran and the entry point answers:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-seed-the-corpus-after-reading-the-hosted-palace.md --human "hosted read <date>: <canonical|nothing>; local seeded <N> must.* edges from root <id>; am_bootstrap returns <M> tier drawers; decision ship"
```

The hosted palace holds the label shape, so the deployments have diverged and nothing was written:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-seed-the-corpus-after-reading-the-hosted-palace.md --human "hosted read <date>: label shape, deployments diverged, nothing written; decision blocked"
```

The hosted read shows the record's direction was wrong rather than merely stalled:

```bash
adr-verify docs/adr/ADR-043-one-spelling-for-the-entry-room/tasks/T3-seed-the-corpus-after-reading-the-hosted-palace.md --human "hosted read <date>: <what was found>; ADR-043's direction does not hold; decision withdraw"
```

`blocked` and `withdraw` are different outcomes and both are offered deliberately: one says the
migration cannot proceed, the other says the decision was wrong.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAHumanObservedSignOffAgreesWithTheIndex` | `internal/repohygiene/humansignoff_test.go` | The sign-off names one of the three decision words and the sibling README carries the status it maps to — existing gate, this task is a new member of its universe | — |
| `TestASignOffThatSaysStopIsCaught` | `internal/repohygiene/humansignoff_test.go` | The same comparison over fixtures that are wrong — existing gate, no change | — |

No new test is added. This task writes data, and a unit test asserting that data exists in a live
palace would be a test of the operator's run rather than of the code — which is exactly the shape
`Acceptance is human-observed` exists for.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The root drawer and its `must.*` facts, returned by `am_list_drawers` and `am_kg_query` in step 6 |
| 2 — something selects it | `Bootstrap`'s `must.*` walk from T2; step 6 calls `am_bootstrap` and reads the tier back |
| 3 — the caller can discover it | `AGENTS.md`'s traversal, unchanged and now executable; the corrected §4.3 from T1 |
| 4 — it is used | Nothing measures this yet. Whether sessions call `am_bootstrap` once it answers is unmeasured, and `am_search` ran 52 times in 8,256 tool calls in session `ee8f1fc1`, which is the number that would have to move |

## Mutation Log

## Invariants

- Nothing is written to any palace before step 1 has been read and classified.
- The two `llm_index` drawers keep their ids. They are re-filed as `must.*` targets, never re-created.
- Label-shaped facts are superseded, not deleted, so the old protocol stays readable as history.
- The hosted palace is READ in step 1 and never written by this task.

## Risks

- A derived-edge backfill is applied instead of seeding a real root, producing `matched` with only the root room's drawers. Forbidden by the Stop Condition, and T2's test is what makes the shortcut fail rather than pass.
- Steps 3-5 are hand-run MCP calls with no transaction across them, so a failure between 4 and 5 leaves both fact shapes current. Mitigated by step 5 using `am_kg_supersede`, which is one transaction, and by doing it after the new edges exist rather than before.
- The sign-off records a `ship` for a run that only touched the local palace. Mitigated by the template requiring the hosted read's date and result in the same line.

## Stop Condition

Stop and ask the owner if step 1 returns the LABEL shape: the two deployments have diverged and
migrating either one silently strands the other. Stop also if anyone proposes satisfying this task by
backfilling derived containment edges for the existing rooms — that produces false reachability, is
named as a rejected Alternative in the ADR, and would pass a check that only asked whether
`am_entry_point` resolves.

What would make this criterion impossible to fail on the data available: nothing, and that is the
point of ordering the hosted read first. If step 1 were skipped, every remaining step would succeed
against the local palace and the sign-off would read `ship` whatever the hosted palace holds.

## Out of Scope

- Backfilling corpora on deployments other than the two named (deferred: `docs/adr/BACKLOG.md` §"Four spellings of one entry point").
- Giving the entry point's data a producer in the product rather than a procedure an agent runs by hand (deferred: Follow-ups, ADR-043).

## Verification Log
