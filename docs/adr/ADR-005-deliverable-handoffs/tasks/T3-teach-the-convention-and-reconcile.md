# Task ADR-005-T3: Put the convention where agents actually read it, and rescue the six orphans

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the corrected inbox convention in the bootstrap and both centralised skills; the reconciled wings
**Consumes:** `confirm_new_wing` (T1) — the docs must name the argument T1 adds
**Data dependency:** needs a live palace — the wing renames run against the running server, and the sign-off records the drawer counts before and after

## Goal

The inbox convention says how a target wing is named, and says it in the two centralised skills as well as the bootstrap; the six existing orphaned drawers move to wings sessions will resolve to.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/bootstrap.md` | edit | the `wing_<target>` placeholder is what two sessions mis-substituted; replace it with the naming rule and a worked example |
| `internal/mcpserver/protocoltools_test.go` | edit | **the selection**: `protocolDocs` is the list of files shipped to every install, and this is the gate that reads them. A convention absent from those files is one no session learns |
| centralised skill `writing-memories` | edit (via `am_update_skill`) | authoritative on what to file where; omits the handoff entirely |
| centralised skill `memory-orchestration` | edit (via `am_update_skill`) | authoritative on the read side; omits reading your own inbox |

## Ordered Steps

1. Write the failing test first (TDD red): extend `internal/mcpserver/protocoltools_test.go` with `TestProtocolTextTeachesTheInboxConvention`, asserting the shipped protocol names the inbox room, states that a handoff wing is named for the project, and names `confirm_new_wing`. Commit it red.
2. Rewrite the bootstrap's inbox passage: state the rule ("the wing is exactly what that project's own sessions resolve — the same rungs, the same normalisation; it is named for the project, never for the direction of travel"), give a worked example with a concrete name rather than `<target>`, and name `confirm_new_wing` and what the refusal means.
3. **Amended 2026-08-20 during execution.** The original step named `internal/mcpserver/skillset.go` as the place to mirror the rule. That was wrong: the wake-up playbook `am_skillset` returns is superadmin-authored content held in `skillset.Service`, not a Go string in that file, so there is nothing there to edit. The shipped-to-every-install surface is the `protocolDocs` list in `protocoltools_test.go` — `bootstrap.md`, `AGENTS.md`, `CLAUDE.md` and the command files. The rule goes in `bootstrap.md` (the always-on baseline) and the gate reads it there.
4. Add a "handing work to another project" section to `writing-memories` (the write side: which wing, how it is named, what a self-contained finding contains) and an "own inbox" row to `memory-orchestration`'s table (the read side: it is a lead, not a work order).
5. Reconcile the orphans: `am_merge_wing(sources: ["wing_to-<project>"], target: "wing_<project>")` for both, then `am_recompute_graph`. Record the drawer counts before and after — a merge that moves zero drawers reports success.
6. Weave a tunnel from each rescued inbox back to the wing that filed it, so the handoffs stop being anonymous.
7. Run the acceptance command, then the human sign-off for step 5.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  go vet ./...
  go test ./internal/mcpserver/ -run "TestProtocolTextTeachesTheInboxConvention" -count=1 -v 2>&1 | tee /tmp/c.out
  grep -q -- "--- PASS: TestProtocolTextTeachesTheInboxConvention" /tmp/c.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/c.out
  go test ./internal/mcpserver/ -count=1'
```

The wing reconciliation (step 5) and the skill updates (step 4) are palace state, not repository state, and no exit code in this repo can see them. Acceptance for those is human-observed: the sign-off records `am_status` wing names and drawer counts before and after, and `am_list_skills` versions before and after.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestProtocolTextTeachesTheInboxConvention` | `internal/mcpserver/protocoltools_test.go` | the shipped protocol names the inbox room, the naming rule, and `confirm_new_wing` | — |

## Out of Scope

- Acting on anything the six rescued drawers describe (permanent: they name other repositories; this session has none of their context, and the rule they were filed under says a memory is a lead, never a work order)
- A repository gate over centralised skill text (deferred: docs/adr/BACKLOG.md — the skills live in the palace, not the tree, so no exit code here can read them)
- Renaming any wing other than the two orphans (permanent: the other eight resolve correctly and renaming them would break the sessions that write to them)

## Invariants

- The bootstrap and the server-shipped preamble state the same rule; neither is the sole source.
- No drawer is deleted by the reconciliation — a merge relabels.

## Risks

- The rename target is a guess at the project's real wing. Mitigated: stripping `to-` is what the filer wrote minus the direction, and `am_merge_wing` in the opposite direction undoes it.
- Skill edits are palace state with no repo gate. Mitigated: version numbers before/after are recorded in the sign-off.

## Stop Condition

Stop and ask if either target wing already exists with unrelated content — merging into it would mix two projects' memories, which is the failure the wings exist to prevent.

## Verification Log

- 2026-08-20 · bb884ee* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-20 · human-observed · wing reconciliation verified through the server (am_list_wings), not a file copy: wing_to-<project-a> (2 drawers) -> wing_<project-a>, wing_to-<project-b> (4) -> wing_<project-b> (names redacted; they are private project names); 10 wings after, no wing_to-* remaining, 6 of 6 drawers moved, 0 deleted. Centralised skills writing-memories and memory-orchestration both v1 -> v2, each now carrying the handoff naming rule and the inbox read. No tunnel woven for the rescued items: the filing wing is not recoverable from the drawers, and inventing provenance is worse than none.
