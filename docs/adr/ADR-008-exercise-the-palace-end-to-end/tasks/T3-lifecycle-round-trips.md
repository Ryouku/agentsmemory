# Task ADR-008-T3: Create, read, update, delete — proven by reading back, per area

**Depends-on:** T2

> **Amended 2026-08-20 during execution.** Scenarios live in `internal/mcptest` (import cycle —
> same amendment as T2 and T4). The three regression scenarios are named as registry entries rather
> than `Scenario*` Go functions, so the acceptance fence greps the gate's own test names instead.
> One scenario also had to be rewritten against the real contract: the chunk-0 defect was fixed by
> REFUSING a multi-chunk content edit, not by rewriting every chunk, so the regression asserts the
> refusal and that nothing half-landed.
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
| `internal/mcptest/registry_test.go` | edit | the scenarios themselves, per area: drawers, anchors, tunnels, skills, KG, wings, diary, closets |
| `internal/mcptest/registry_test.go` | edit | the three regression scenarios named after the defects they cover |

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
  go test ./internal/mcptest/ -run "TestEveryToolIsExercisedEndToEnd|TestScenarios" -count=1 -v 2>&1 | tee /tmp/e3.out
  grep -q -- "--- PASS: TestEveryToolIsExercisedEndToEnd" /tmp/e3.out
  grep -q -- "--- PASS: TestScenariosObserveAnEffect" /tmp/e3.out
  grep -q -- "--- PASS: TestScenariosOnlyClaimToolsTheyCall" /tmp/e3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| regression: no stale half after update | `internal/mcptest/registry_test.go` | a multi-chunk update leaves no stale chunk competing in search | — |
| regression: delete leaves no chunk | `internal/mcptest/registry_test.go` | delete removes every chunk, checked by get AND search | — |
| regression: malformed anchors do not clear | `internal/mcptest/registry_test.go` | an all-unreadable anchor list refuses instead of clearing | — |
| per-area round trips | `internal/mcptest/registry_test.go` | create/read/update/delete observable for each area | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| multi-chunk update guard disarmed (`len(chunks) > 1 && false`) | yes | regression: no stale half after update |
| delete truncated to the parent row (`ids = ids[:1]`) | yes | regression: delete leaves no chunk |
| all-unreadable anchor refusal disarmed | yes | regression: malformed anchors do not clear |

**The adoption bar was not met on the first attempt, and the check is why.** The delete mutation
SURVIVED: the scenario put its marker at the START of the content, so it landed in chunk 0 — which
the buggy delete removes anyway — while the orphaned child held only filler. The scenario could not
see the defect it existed for. Moving the marker into the LAST chunk fixed it, and the mutation now
dies. This is the "fixture too small to expose the defect" risk in this task's own Risks section,
and it would have shipped as coverage.

**A review also found the harness unfaithful in a way that mattered.** `Parties` built a SERVER PER
PARTY and shared only the database. Production runs one process serving everybody, so per-process
state that leaks between clients — a cached search key omitting the wing, a latched "current wing"
field — was invisible: each party latched its own correct wing and isolation looked perfect. It is
now one server, N clients.

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

- 2026-08-20 · 62d7c38* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
