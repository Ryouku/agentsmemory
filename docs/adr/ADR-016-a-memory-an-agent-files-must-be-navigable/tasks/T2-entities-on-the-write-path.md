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
| `internal/palace/entity.go` | edit | T1 measured the extractor admitting shouted prose; the lexicon is fixed before it is wired to every write |
| `internal/palace/entity_test.go` | edit | the must-exclude / must-admit batteries that specify the lexicon |

## Ordered Steps

1. Write the failing test first (TDD red): `TestHallwaysDeriveFromDrawersAnAgentFiled` — file two drawers through `Service.Add` naming the same pair of entities, recompute, require a hallway. Commit it red. It is red today for the reason this ADR exists.
2. Fix what T1's report caught the extractor admitting, BEFORE wiring it to every write: shouted prose is not a name. Specify it with two batteries — a must-exclude set of ordinary English and a must-admit set of acronyms and product names — then write the minimum lexicon that turns them green. Measure the change and record what it excludes and what it still admits.
3. Set the field in `Add`'s chunk loop, using the same `extractEntities` mining uses. One expression.
4. Keep the existing mining-based test. The point is that BOTH producers feed the graph — deleting the mining test would swap one blind spot for another.
5. Falsify: remove the assignment and watch only the new test go red, which is the proof that the old suite never covered the `Add` path.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestHallwaysDeriveFromDrawersAnAgentFiled|TestAddExtractsEntitiesPerChunkNotPerMemory|TestEmphasisIsNotAnEntity|TestAcronymsAndNamesStayEntities|TestGraphHallwaysAndEntityTunnels" -count=1 -v 2>&1 | tee /tmp/a16t2.out
  grep -q -- "--- PASS: TestHallwaysDeriveFromDrawersAnAgentFiled" /tmp/a16t2.out
  grep -q -- "--- PASS: TestAddExtractsEntitiesPerChunkNotPerMemory" /tmp/a16t2.out
  grep -q -- "--- PASS: TestEmphasisIsNotAnEntity" /tmp/a16t2.out
  grep -q -- "--- PASS: TestAcronymsAndNamesStayEntities" /tmp/a16t2.out
  grep -q -- "--- PASS: TestGraphHallwaysAndEntityTunnels" /tmp/a16t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a16t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHallwaysDeriveFromDrawersAnAgentFiled` | `internal/palace/graph_test.go` | the `Add` path feeds the graph — the assertion nothing in this repo has ever made | — |
| `TestAddExtractsEntitiesPerChunkNotPerMemory` | `internal/palace/graph_test.go` | a multi-chunk memory does not hand every chunk the whole memory's entities | — |
| `TestEmphasisIsNotAnEntity` | `internal/palace/entity_test.go` | shouted ordinary English does not become an entity, in either case | — |
| `TestAcronymsAndNamesStayEntities` | `internal/palace/entity_test.go` | acronyms and ordinary-word product names still do | — |
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
| extract from the whole input rather than the chunk | yes | `TestAddExtractsEntitiesPerChunkNotPerMemory` (a multi-chunk memory would give every chunk the same entities) |
| match the stoplist case-sensitively again | yes | `TestEmphasisIsNotAnEntity` (`AND` returns) |
| drop the inflection reduction from `ordinary` | yes | `TestEmphasisIsNotAnEntity` (`SHIPPED`, `TESTING`, `CHANGED` return) |
| narrow `candidateWordRE` to `\p{Lu}\p{Ll}*` — the shape-based repair the ADR's Context proposed | yes | `TestAcronymsAndNamesStayEntities` (every acronym dies: `MCP`, `ADR`, `HTTP`, …). Run deliberately: it is the evidence that the fix belongs in the lexicon and not in the regex |

## Out of Scope

- Backfilling drawers filed before this (deferred: docs/adr/BACKLOG.md)
- Entities on the `WriteDiary` path, which builds its own `Drawer` rows and stamps none (deferred: ADR-016 Follow-ups)

## Invariants

- Entities are derived per CHUNK, matching what mining does, so a multi-chunk memory does not give every chunk the whole memory's entities.
- The extractor's INTERFACE is unchanged — `extractEntities(text) []string`, same call in both producers. Its lexicon is not: T1 measured it admitting shouted prose, and the ADR's Context assigns that repair here, because wiring a noisy extractor into every write is how the graph fills with hallways between conjunctions.
- A candidate is judged as a WORD, never as a shape. All-caps still qualifies, because `HTTP` and `MCP` are all-caps and are entities.

## Risks

- Write latency on the interactive path. Mitigated: measured by T1 before this task begins, and it is frequency counting rather than inference.

## Stop Condition

Stop and ask if T1's share is below the ADR's bar — this task is withdrawn, not adapted.

## Verification Log

- 2026-08-21 · 749b92e* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-21 · 749b92e* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-21 · 3d72363* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`

## Mutation Log
