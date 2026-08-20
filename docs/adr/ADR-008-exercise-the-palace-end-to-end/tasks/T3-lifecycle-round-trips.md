# Task ADR-008-T3: Create, read, update, delete — proven by reading back, per area

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** L (cross-boundary)
**Owner:** unassigned
**Produces:** one-party scenarios for every observable tool, including three regression scenarios
**Consumes:** the scenario registry (T2)
**Data dependency:** hermetic

## Goal

Every mutable area round-trips through the tool surface, and a delete is proven gone by every route that could still reach it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/scenarios_test.go` | edit | the scenarios themselves, per area: drawers, anchors, tunnels, skills, KG, wings, diary, closets |
| `internal/mcpserver/e2e_test.go` | edit | the three regression scenarios named after the defects they cover |

## Ordered Steps

1. Write the failing tests first (TDD red): the three regression scenarios, named for the defects they cover — `ScenarioUpdateRewritesEveryChunk`, `ScenarioDeleteLeavesNoChunkBehind`, `ScenarioMalformedAnchorsDoNotClear`. Commit them red against a tree with the fixes reverted, so they are proven to fail on the real defect and not merely to pass on the fixed code.
2. **This is the calibration set and the parent ADR's adoption bar.** Re-introduce each of the three defects in turn and confirm the matching scenario fails. If any does not, the gate is not adopted — report rather than weaken it.
3. Write the per-area round trips. Each: create, read back and compare, update, read back and confirm the change is complete, delete, then confirm gone by EVERY route — direct get, list, and search — because the delete defect was invisible to a get and visible only to search.
4. For every area, assert the negative too: reading something never written returns absence, not an empty success.
5. Falsify each scenario by reverting the mechanism it covers; record every mutation in the table below with whether it compiled.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestEveryToolIsExercisedEndToEnd|Scenario" -count=1 -v 2>&1 | tee /tmp/e3.out
  grep -q -- "--- PASS: TestEveryToolIsExercisedEndToEnd" /tmp/e3.out
  grep -q -- "ScenarioUpdateRewritesEveryChunk" /tmp/e3.out
  grep -q -- "ScenarioDeleteLeavesNoChunkBehind" /tmp/e3.out
  grep -q -- "ScenarioMalformedAnchorsDoNotClear" /tmp/e3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `ScenarioUpdateRewritesEveryChunk` | `internal/mcpserver/e2e_test.go` | a multi-chunk update leaves no stale chunk competing in search | — |
| `ScenarioDeleteLeavesNoChunkBehind` | `internal/mcpserver/e2e_test.go` | delete removes every chunk, checked by get AND search | — |
| `ScenarioMalformedAnchorsDoNotClear` | `internal/mcpserver/e2e_test.go` | an all-unreadable anchor list refuses instead of clearing | — |
| per-area round trips | `internal/mcpserver/scenarios_test.go` | create/read/update/delete observable for each area | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| restore the chunk-0-only update | yes | `ScenarioUpdateRewritesEveryChunk` |
| restore the delete that orphans child chunks | yes | `ScenarioDeleteLeavesNoChunkBehind` |
| restore the anchor parse that clears on an all-malformed list | yes | `ScenarioMalformedAnchorsDoNotClear` |
| make delete remove the parent only, checked by get alone | yes | `ScenarioDeleteLeavesNoChunkBehind` |

## Out of Scope

- Multi-party visibility (permanent: T4 owns it, and mixing the two makes a failure ambiguous between a lifecycle bug and a scoping one)
- Tools on the unobservable list (permanent: T2 defines it and requires a reason)

## Invariants

- Every delete is confirmed by more than one read route.
- No scenario asserts only that a call returned without error.

## Risks

- A scenario passes because the fixture is too small to expose chunking. Mitigated: the multi-chunk scenarios seed content over the chunking threshold and assert the chunk count first.

## Stop Condition

Stop and report if any of the three regression scenarios cannot be made to fail on the reverted defect — that means the harness does not observe what it claims to, and the parent ADR says the gate is then not adopted.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
