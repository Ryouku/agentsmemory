# Task ADR-016-T2: A drawer an agent files carries its entities

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `drawers.entities` populated by `Service.Add`
**Consumes:** T1's measurement — this task does not begin until it supports the change
**Data dependency:** hermetic

## Goal

A memory filed through `am_add_drawer` participates in the derived graph exactly as a mined one does.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | stamp `Entities: extractEntities(c.Content)` on each chunk `Add` builds |
| `internal/palace/graph_test.go` | edit | the hallway test must ALSO run through `Add`, not only through `Mine` |

## Ordered Steps

1. Write the failing test first (TDD red): `TestHallwaysDeriveFromDrawersAnAgentFiled` — file two drawers through `Service.Add` naming the same pair of entities, recompute, require a hallway. Commit it red. It is red today for the reason this ADR exists.
2. Set the field in `Add`'s chunk loop, using the same `extractEntities` mining uses. One expression; the extractor is not the change.
3. Keep the existing mining-based test. The point is that BOTH producers feed the graph — deleting the mining test would swap one blind spot for another.
4. Falsify: remove the assignment and watch only the new test go red, which is the proof that the old suite never covered the `Add` path.
5. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestHallwaysDeriveFromDrawersAnAgentFiled|TestGraphHallwaysAndEntityTunnels" -count=1 -v 2>&1 | tee /tmp/a16t2.out
  grep -q -- "--- PASS: TestHallwaysDeriveFromDrawersAnAgentFiled" /tmp/a16t2.out
  grep -q -- "--- PASS: TestGraphHallwaysAndEntityTunnels" /tmp/a16t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a16t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHallwaysDeriveFromDrawersAnAgentFiled` | `internal/palace/graph_test.go` | the `Add` path feeds the graph — the assertion nothing in this repo has ever made | — |
| `TestGraphHallwaysAndEntityTunnels` | `internal/palace/graph_test.go` | the mining path still does | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `extractEntities` already exists and is already tested |
| 2 — something selects it | `Service.Add` calls it — the missing line this whole ADR is about |
| 3 — the caller can discover it | nothing to discover; every write takes this path |
| 4 — it is used | `RecomputeGraph` derives hallways from what it writes, asserted end to end |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop the assignment | yes | `TestHallwaysDeriveFromDrawersAnAgentFiled` only — which is the proof the old suite was blind |
| extract from the whole input rather than the chunk | yes | `TestHallwaysDeriveFromDrawersAnAgentFiled` (a multi-chunk memory would give every chunk the same entities) |

## Out of Scope

- Backfilling drawers filed before this (deferred: docs/adr/BACKLOG.md)
- Changing what counts as an entity (deferred: T1 of this ADR owns that question and answers it with data)

## Invariants

- Entities are derived per CHUNK, matching what mining does, so a multi-chunk memory does not give every chunk the whole memory's entities.
- The extractor is unchanged; only its caller is new.

## Risks

- Write latency on the interactive path. Mitigated: measured by T1 before this task begins, and it is frequency counting rather than inference.

## Stop Condition

Stop and ask if T1's share is below the ADR's bar — this task is withdrawn, not adapted.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
