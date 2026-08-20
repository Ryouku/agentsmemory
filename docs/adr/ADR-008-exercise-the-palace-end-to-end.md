# ADR-008: Every tool the palace exposes must be exercised end to end, or the build fails

**Status:** Proposed
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-006 (settings reachability — the same principle applied to configuration), ADR-007 (measurement honesty — third in priority behind this)
**Invalidates:** none — checked (grepped ADR-001..007 for `mcpserver`, `CallToolRequest`, `catalog`: none depends on how tools are tested, because none are)

## Context

Measured 2026-08-20 on this tree:

- **41 tools are registered. 39 are named in no test file at all.**
- **Zero tests drive a tool handler.** No test in `internal/mcpserver` constructs a `mcp.CallToolRequest`; no test starts the MCP server and calls a tool. The 21 tests in that package check tool *names*, catalogue *size*, and source text.

The domain services underneath are well covered. The surface every agent actually uses is not covered at all, and the two are not the same thing — this repository's own record says so four times over: an eval arm that was built, documented and never registered; an IDF coverage function with no branch in `Search`; an embedding backend whose selector existed only in a package comment; a config field nothing consumed. Every one had tests. Every test exercised the component rather than the selection.

The existing gates are the mechanical answer to that lesson, and they stop at the boundary of this package. `TestCatalogSizeIsWhatTheReadmeClaims` proves the count is honest. `TestEveryToolNameIsUniqueAndPrefixed` proves the names do not collide. Neither proves a single tool works when called, that a write is readable afterwards, that a delete removes anything, or that one workspace's write is invisible to another. Today those properties rest on the same evidence the four shipped defects rested on: the code looks right.

Three defects found this week landed exactly in that gap, and each was found by hand rather than by a gate. `am_update_drawer` rewrote chunk 0 and left chunk 1 live with its own embedding. `Delete` orphaned every chunk but the first. A `code_anchors` list whose entries were all malformed cleared the memory's anchors and reported success. All three are round-trip properties: write, then read back and find the result is not what the write claimed.

## Existing Primitives Audit

- **`registrar` / `fullCatalog`** (`internal/mcpserver/server.go`, `catalog_test.go`) — already enumerates every registered tool at run time from the live registry rather than a list. Reuse: this is the enumeration the exhaustiveness gate needs, and it is already proven to be read from the registry.
- **`server.NewStreamableHTTPServer`** (`cmd/server/main.go:293`) — the transport agents actually use. Reuse: drive the gate through it rather than around it, or the test proves something about a path nobody runs.
- **`httptest`** — already used by 12 test files including `cmd/server/stdio_test.go`. Reuse: the precedent for standing a real server up in a test exists in this tree.
- **`newTestService`** (`internal/palace/service_test.go:51`) — migrated SQLite plus a deterministic fake embedder. Reuse for the palace behind the server, so the gate needs no network and no model.
- **`mark3labs/mcp-go` client package** — the counterpart to the server we already depend on. Reuse rather than hand-rolling JSON-RPC.

## Decision

Stand a real MCP server up in-process over HTTP, drive it with a real MCP client, and require **every registered tool to appear in an end-to-end scenario that actually invokes it**. The scenario set is checked against the live catalogue, so a tool added without one fails the build.

A scenario is not "the call returned 200". Each one asserts an **observable effect through a second call**: a write is found by a read, an update is visible in every chunk it claims to have changed, a delete makes the thing unfindable by every route that could still reach it, and a scoped read does not see another workspace's data. Where the effect cannot be observed through the tool surface, the scenario says so and names what it checked instead — the same rule ADR-007 applies to numbers.

Coverage is proven at three widths, because the failures differ:

- **One party** — round-trip per area: create, read back, update, read back, delete, confirm gone by every route.
- **Two parties** — two registrations against one workspace: what one writes the other finds when scoped to a shared wing, and does not find when scoped to its own. This is where `SEARCH_SCOPE` and `wingFor` are actually proven rather than argued.
- **Three parties** — A hands work to B's inbox while C is untouched. Delivery and isolation are one property observed from two sides, and this week's handoff defect was invisible precisely because nobody ever looked from B's side.

**Pre-registered falsification.** The gate is worthless if it can pass while a tool is broken. Each task therefore ships with the mutation that must turn it red, run and recorded in the task's Mutants table — and for this ADR specifically, the three defects already found by hand are the calibration set: re-introducing the chunk-0-only update, the orphaning delete, or the anchor-clearing parse must each fail a scenario. **A gate that does not catch all three defects it was designed after is not adopted**, and the ADR is re-planned rather than the gate weakened.

This decision is about the tool surface reachable with a SQLite store and a fake embedder. Tools whose behaviour needs a live Qdrant, TEI or OAuth issuer are named explicitly in the exhaustiveness list rather than passing by silence.

## Alternatives Considered

- **Unit-test each handler by calling it directly.** Rejected as the primary mechanism: it skips `admit()`, metering, tenant resolution and argument decoding — the layer where three of this week's defects lived. Kept as the fallback for tools whose transport-level effect is unobservable.
- **A shell-script integration suite against a docker-compose stack.** Rejected: it needs the stack, so it will not run in the loop where defects are introduced, and a gate that runs elsewhere is one people learn to work around.
- **Raise the bar only for tools that mutate state.** Rejected: `am_search` returning another workspace's memories is not a mutation and is the worst defect on the list.
- **Trust the domain-service tests and test only the wiring.** Rejected on evidence: `am_update_drawer`'s defect was in the service, and the service had tests. The round trip is what caught it.

## Component / Boundary Impact

A new test-only harness package under `internal/mcpserver` (or `internal/mcptest`) owns standing up the server and client. No production boundary moves; `mcpserver` keeps sole ownership of the tool surface.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `mcptest.Harness` — server + client + seeded palace | add | new test-only package | every scenario |
| `TestEveryToolIsExercisedEndToEnd` | add | `internal/mcpserver` | CI, and anyone adding a tool |
| the exhaustiveness list of unobservable tools | add | `internal/mcpserver` | the gate |

None of these ship in the binary.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `mcptest.Harness` | T1 | T2, T3, T4 | No — test-only, new |
| the scenario registry | T2 | T3, T4 | No — additive |

## Implementation

`tasks/README.md` — four tasks.

## Consequences

- **Positive:** the properties the whole product rests on — a write is readable, a delete removes, a scope holds — stop being arguments and become exit codes. A new tool cannot ship unexercised.
- **Negative:** the test suite gains a slower class of test that stands a server up. It stays in-process with a fake embedder and no network, but it is not a microsecond unit test.
- **Neutral:** three defects found by hand this week become regression tests, so their cost is paid once.

## Out of Scope

- Tools whose effect needs a live Qdrant, TEI or OAuth issuer (permanent: an in-process gate cannot observe them; T2 names them so the silence is declared rather than accidental)
- Real-time multi-agent collaboration — two sessions mutating concurrently and observing each other live (deferred: docs/adr/BACKLOG.md — three-party here means three parties in sequence; concurrent collaboration is a product question with its own requirements, and it is the subject of the continuity spec)
- Performance or load characteristics of the tool surface (permanent: this ADR is about correctness; a gate that also asserted latency would fail for reasons unrelated to what it exists to catch)
- The CLI `mcp` adapter's parity with the HTTP one (deferred: docs/adr/ADR-006-knobs-that-do-nothing.md — its T4 fixes the one divergence found; a general parity gate belongs with this harness once it exists)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The gate passes while a tool is broken | Med | High | The three hand-found defects are the calibration set; T3 must fail on each re-introduction or the gate is not adopted |
| Scenarios assert "no error" rather than an effect | High | High | T2 requires every scenario to make a second, observing call; a scenario with one call fails the gate itself |
| The harness is slow enough to be skipped | Med | Med | In-process, fake embedder, SQLite; T1's acceptance records wall-clock and fails above a stated budget |
| The exhaustiveness list becomes a place to park hard tools | Med | Med | Each entry needs a reason naming the external dependency, and T2 fails on an entry whose reason does not |

## Rollback

Test-only. Revert the commits and the suite loses the harness and the gate; nothing shipped changes. The three regression scenarios would be the loss worth noting, so T3 records which defect each one covers in the scenario name.

## Follow-ups
