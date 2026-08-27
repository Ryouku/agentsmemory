# Task ADR-038-T4: Retraction carries a reason, and erasure leaves the agent surface

> Re-authored 2026-08-27 from ADR-010's T2, which this record supersedes. The decision is unchanged.
> What changed is that a supersede now mints an OPAQUE id (T3) instead of a content-derived one, so
> "the new record's id" is a name rather than a hash of the new text — which is what makes a
> supersession an ordinary edge instead of an identity problem.

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary — palace + mcpserver + tool surface)
**Owner:** unassigned
**Produces:** `am_invalidate_drawer(id, reason)`; supersede semantics on `am_update_drawer`; a required `reason` on `am_kg_invalidate`; erasure moved to the operator surface
**Consumes:** `End(id, reason)` and the validity window (T1); the opaque mint (T3)
**Data dependency:** hermetic

## Goal

An agent correcting a memory writes a new record and ends the old one with a reason; an agent cannot
erase.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `Update`'s content path becomes a supersede: mint a new row, `End` the old with the reason, link `superseded_by`. The multi-chunk refusal at `:951` is re-scoped — a supersede replaces the whole memory, which is what that refusal said to do by hand |
| `internal/mcpserver/drawers.go` | edit | `am_invalidate_drawer` declared and registered; `am_update_drawer` gains a required `reason` and returns the NEW id naming the ended one; `am_delete_drawer`, `am_delete_tunnel`, `am_delete_hallway` **removed from the agent registration** — this is the line that SELECTS the boundary, and deleting it puts erasure back in an agent's hands |
| `internal/mcpserver/server.go` | edit | the registration list — a tool removed from the agent surface must be absent from the catalogue an agent reads, not merely refused at call time |
| `cmd/server/` | edit | the operator erasure path for a single drawer, beside `wing delete`, so removal stays possible for a leaked secret |
| `internal/mcpserver/kg.go` | edit | `am_kg_invalidate` gains a required `reason`; the schema has `valid_to` and no column for one today |
| `db/migrations/000NN_kg_ended_reason.sql` | add | the column the KG reason lands in. NN allocated at merge |
| `README.md` | edit | the tool table — `TestEveryCatalogToolIsNamedInTheReadme` requires a first-cell row per catalogue tool, so adding one tool and removing three is a README change in this commit |

## Ordered Steps

1. Write the failing tests first — RED against the tree as it stands:
   - correcting a memory returns a NEW id, the old row is ended with the given reason, and
     `superseded_by` links them;
   - the old row's TEXT is still readable by id — ending is not deleting;
   - `am_update_drawer` without a reason is refused;
   - `am_invalidate_drawer(id, reason)` ends a memory that nothing replaces;
   - `am_kg_invalidate` without a reason is refused;
   - **the three destructive tools are absent from the agent catalogue** — a source or registration
     check, because a behavioural test that never calls them passes either way (rung 3).
2. Implement the supersede path in `Service`, ending through T1's single `End`.
3. Declare `am_invalidate_drawer`; add the required reasons; remove the three tools from the agent
   registration and add the operator single-drawer erasure path.
4. Update the README tool table.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestCorrectingAMemorySupersedesIt|TestTheEndedTextIsStillReadableById|TestUpdateWithoutAReasonIsRefused|TestInvalidateDrawerEndsWithNoSuccessor|TestKgInvalidateRequiresAReason|TestDestructiveToolsAreAbsentFromTheAgentCatalogue' -count=1 2>&1 | tee /tmp/acc38t4a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t4a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t4b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t4b.out
```

The whole tree runs second because this task edits `README.md`, which `TestEveryCatalogToolIsNamedInTheReadme` reads from another package.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCorrectingAMemorySupersedesIt` | `internal/palace/supersede_test.go` | a new row, the old one ended, the link written | — |
| `TestTheEndedTextIsStillReadableById` | `internal/palace/supersede_test.go` | ending is not deleting — the whole point | — |
| `TestUpdateWithoutAReasonIsRefused` | `internal/mcpserver/drawers_test.go` | the reason is required where an agent supplies it | — |
| `TestInvalidateDrawerEndsWithNoSuccessor` | `internal/mcpserver/drawers_test.go` | a retraction that replaces nothing is expressible | — |
| `TestKgInvalidateRequiresAReason` | `internal/mcpserver/kg_test.go` | the half of the store that kept history stops keeping only *that* a fact ended | — |
| `TestDestructiveToolsAreAbsentFromTheAgentCatalogue` | `internal/mcpserver/catalog_test.go` | **rung 3** — a registration check, since a behavioural test that never calls a tool passes whether or not it is offered | — |

**Shapes the creation path can already produce, decided rather than assumed:** a multi-chunk memory
(a supersede replaces the WHOLE memory — every chunk ends, one new set is written); a drawer with
anchors (`ReplaceAnchors` semantics on the new row: decide whether anchors follow the correction or
are cleared, and say which in the task rather than discovering it); a drawer already ended (refuse);
a source-less drawer (no `purgeSource`, so the supersede is the only path — assert it works).

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the supersede and invalidate unit tests |
| 2 — something selects it | `Service.Update`'s content branch; mutation: restore the in-place `Updates()` and `TestCorrectingAMemorySupersedesIt` goes red |
| 3 — the caller can discover it | `am_invalidate_drawer` in the tool schema and the README table; the three removed tools ABSENT from the agent catalogue, asserted by a registration check. **This is the rung that decides whether the erasure boundary is real** — a tool still advertised is a tool an agent will call |
| 4 — it is used | the ratio of supersedes to invalidates over a month of real writes, and the median `reason` length. Nothing measures it today; the ADR carries a Follow-up to report it. |

## Mutation Log

## Invariants

- Ending goes through T1's single `End`. This task adds callers, never a second ending path.
- The old text survives every correction.
- No agent-reachable tool destroys a drawer, a tunnel or a hallway after this task.
- Erasure remains possible for an operator — a store that cannot forget a leaked secret is not deployable.

## Risks

- Removing three tools from the agent surface is a breaking change for any client that calls them. The refusal text must name the operator path, or an agent that cannot delete will file a duplicate instead and the palace grows a class of junk this ADR did not intend.
- A required `reason` gets "obsolete". Accepted and measured, never designed around — the Follow-up reads the field and improves the prompting.
- The multi-chunk refusal at `service.go:951` was correct under the old model and becomes wrong here. Re-scope it deliberately; leaving it would make correction impossible for exactly the long documents that most need it.

## Stop Condition

Stop and ask if removing `am_delete_drawer` from the agent surface would break a live client this
repository does not own. That is a product decision about a published tool catalogue, not an
implementation detail.

## Out of Scope

- Making recall hide ended records — T5. This task ends them; nothing filters yet, so an ended record is still returned until T5 lands. Say so in the commit, because a half-landed pair looks like a bug.
- Structured reasons — a taxonomy (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
