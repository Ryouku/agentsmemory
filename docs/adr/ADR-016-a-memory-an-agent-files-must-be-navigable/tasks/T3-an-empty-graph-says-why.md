# Task ADR-016-T3: A graph tool that cannot answer says so

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `emptyGraphNote` on the three graph tools
**Consumes:** none
**Data dependency:** hermetic

## Goal

An agent asking about the graph can tell "this wing has no connections yet" from "this palace can never have any".

This holds whether or not T2 ships. If T2 is withdrawn it matters more, not less: the tools stay empty and nothing else will ever say why.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/emptygraph.go` | add | the note, following `emptywing.go`'s fail-open shape |
| `internal/mcpserver/emptygraph_test.go` | add | silent on a populated graph, explanatory on an empty one, never fatal |
| `internal/mcpserver/graph.go` | edit | attach it to `am_list_hallways`, `am_traverse`, `am_graph_stats` |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestEmptyGraphSaysWhyItIsEmpty`, `TestGraphNoteIsSilentWhenTheGraphHasContent`, `TestGraphNoteFailsOpen`. Commit them red.
2. The note distinguishes the three real cases, because they need different actions: the wing holds no drawers at all; it holds drawers but none carry entities (nothing has been mined, and — before T2 — nothing an agent files ever will); it holds entities but no pair meets the co-occurrence threshold.
3. Fail OPEN, exactly as `emptyWingNote` does: a lookup failure must never turn a working call into an error.
4. Falsify: return the note unconditionally and watch the silent-when-populated test go red; make the lookup error propagate and watch the fail-open test go red.
5. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestEmptyGraphSaysWhyItIsEmpty|TestGraphNoteIsSilentWhenTheGraphHasContent|TestGraphNoteFailsOpen|TestEveryGraphToolCarriesTheNote" -count=1 -v 2>&1 | tee /tmp/a16t3.out
  grep -q -- "--- PASS: TestEmptyGraphSaysWhyItIsEmpty" /tmp/a16t3.out
  grep -q -- "--- PASS: TestGraphNoteIsSilentWhenTheGraphHasContent" /tmp/a16t3.out
  grep -q -- "--- PASS: TestGraphNoteFailsOpen" /tmp/a16t3.out
  grep -q -- "--- PASS: TestEveryGraphToolCarriesTheNote" /tmp/a16t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a16t3.out
  go test ./internal/mcpserver/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestEmptyGraphSaysWhyItIsEmpty` | `internal/mcpserver/emptygraph_test.go` | the three cases are distinguished, each naming what would change it | — |
| `TestGraphNoteIsSilentWhenTheGraphHasContent` | `internal/mcpserver/emptygraph_test.go` | a working graph produces no note | — |
| `TestGraphNoteFailsOpen` | `internal/mcpserver/emptygraph_test.go` | a lookup failure never turns a working call into an error | — |
| `TestEveryGraphToolCarriesTheNote` | `internal/mcpserver/emptygraph_test.go` | all THREE handlers attach it — attaching it to one and forgetting the others is this repo's recurring shape | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestEmptyGraphSaysWhyItIsEmpty` |
| 2 — something selects it | `TestEveryGraphToolCarriesTheNote` drives each registered handler, not the function |
| 3 — the caller can discover it | it is in the tool result the agent already reads |
| 4 — it is used | an empty graph is the current state of every palace populated by agents, so it fires on the first call |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| return the note unconditionally | yes | `TestGraphNoteIsSilentWhenTheGraphHasContent` |
| propagate the lookup error | yes | `TestGraphNoteFailsOpen` |
| attach the note to `am_list_hallways` only | yes | `TestEveryGraphToolCarriesTheNote` |
| collapse the three cases into one message | yes | `TestEmptyGraphSaysWhyItIsEmpty` |

## Out of Scope

- Changing what the graph tools return when they DO have content (permanent: this task adds an explanation for emptiness and nothing else)
- A note on `am_recompute_graph` reporting that it derived nothing (deferred: docs/adr/BACKLOG.md)

## Invariants

- Silent whenever the graph has content.
- Never fatal.

## Risks

- A note that fires on every call of a permanently empty palace becomes noise. Accepted deliberately: it fires precisely while the tool cannot work, and it names what would fix it.

## Stop Condition

Stop and ask if distinguishing "no entities" from "below threshold" needs a query the repo cannot answer cheaply — a diagnostic that costs a scan is one that will be disabled.

## Verification Log

- 2026-08-21 · 749b92e* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-21 · 42b0368* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
