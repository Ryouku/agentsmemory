# Task ADR-008-T1: Stand a real server up in a test and prove one round trip

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `mcptest.Harness` — a running MCP server over HTTP, a real client, a seeded palace
**Consumes:** none
**Data dependency:** hermetic — SQLite, a deterministic fake embedder, no network, no model

## Goal

A test can call any registered tool the way an agent does, and one round trip proves the harness observes real effects.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcptest/harness.go` | add | server + client + seeded palace; test-only package |
| `internal/mcptest/harness_test.go` | add | the harness itself works: a tool call reaches a handler and its effect is observable |

## Ordered Steps

1. Write the failing test first (TDD red): `TestHarnessObservesAWriteThroughARead` — call `am_add_drawer`, then `am_search`, and assert the drawer comes back. Commit it red.
2. Build the harness: `server.NewStreamableHTTPServer` over `httptest.NewServer`, an `mcp-go` client dialling it, and a palace built like `internal/palace/service_test.go:51`. Go through the transport, not around it — `admit()`, metering, tenant resolution and argument decoding are where three of this week's defects lived.
3. Give it a two-registration constructor: two clients against one workspace with different default wings, since T4 needs it and retrofitting a second party later means rewriting every scenario.
4. Assert the harness cannot silently degrade: if the client connects but the catalogue is empty, fail loudly. A harness that stands up an empty server would let every later scenario "pass".
5. Record wall-clock in the output; fail above 30s. A gate nobody waits for is a gate nobody runs.
6. Falsify: point the client at a server with no tools registered; make the seeded palace read-only; drop the search call and assert only that add returned no error — the last must fail T2's rule later, and here it must fail this test.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcptest/ -run "TestHarnessObservesAWriteThroughARead|TestHarnessFailsOnAnEmptyCatalogue" -count=1 -v 2>&1 | tee /tmp/e1.out
  grep -q -- "--- PASS: TestHarnessObservesAWriteThroughARead" /tmp/e1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e1.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHarnessObservesAWriteThroughARead` | `internal/mcptest/harness_test.go` | a tool call reaches a handler over the transport and its effect is observable by a second call | — |
| `TestHarnessFailsOnAnEmptyCatalogue` | `internal/mcptest/harness_test.go` | a server with no tools cannot silently let scenarios pass | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| no tenant on the request context (auth path bypassed) | yes | `TestHarnessObservesAWriteThroughARead` |
| catalogue guard disarmed (`len(tools) < 0`) | yes | `TestCatalogueGuardRejectsAnEmptyServer` |
| `ScopeSearchToWing: false` | yes | **SURVIVED — not covered here.** T4 owns scoping; T1's tests do not observe it, and claiming otherwise would be the coverage lie this table exists to prevent |

**What the mutants changed about the design.** The catalogue guard was originally inlined in the
constructor and disarming it left the whole package green: no test can stand a toolless server up,
so an inlined guard is unfalsifiable. Extracted as `UsableCatalogue(tools, err) error`, taking the
result rather than producing it, the rule became drivable and the mutant now dies. The first
compiling attempt at that mutant did not build (`declared and not used`), which is not a caught
mutant — it is a skipped one — so it was rewritten until it built AND failed.

## Out of Scope

- Scenarios for individual tools (permanent: T3 and T4 own those; this task is the instrument)
- A live Qdrant or TEI backend (permanent: hermetic by design, and the alternative is a gate that does not run in the loop where defects are introduced)

## Invariants

- Every call goes through the transport an agent uses.
- The harness needs no network and no model.

## Risks

- The mcp-go client API differs from the server's expectations. Mitigated: both come from the same module version already in `go.mod`.

## Stop Condition

Stop and ask if the transport cannot be driven in-process — an out-of-process harness is a different decision with different economics, and this ADR's budget assumes in-process.

## Verification Log

- 2026-08-20 · c92e2ab* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-21 · 3877f08* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:b597855edbf9a253ff7f57d9fbe60693a523726b416e40c977640f21e3aee7e2
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.011s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	3.392s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.017s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.003s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.432s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.016s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.009s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.005s
  FAIL
  ```
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:a250bd61ccbac06e0566e65487737ba2bb043b428f461fe0d5b03c363a46174c

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/mcptest/harness.go` · UsableCatalogue is the guard that stops a harness serving nothing from letting every scenario pass vacuously; a length that can never be negative accepts the empty catalogue, which is exactly TestHarnessFailsOnAnEmptyCatalogue's subject · acceptance-sha256:a250bd61ccbac06e0566e65487737ba2bb043b428f461fe0d5b03c363a46174c
