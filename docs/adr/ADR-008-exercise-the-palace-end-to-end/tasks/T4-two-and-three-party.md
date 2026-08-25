# Task ADR-008-T4: Two parties see what they should, three parties prove isolation

**Depends-on:** T1

> **Amended 2026-08-20 during execution.** The scenarios were planned for
> `internal/mcpserver/multiparty_test.go`. They live in `internal/mcptest` instead: the harness
> imports `mcpserver`, so a test inside `mcpserver` that used the harness would be an import cycle.
> Every file reference below is amended accordingly, and T2's gate reads the scenario registry, which
> can name tests in either package.
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the two-party and three-party scenarios
**Consumes:** `mcptest.Harness`'s two-registration constructor (T1)
**Data dependency:** hermetic

## Goal

What one registration writes, another finds when it should and does not when it should not; and a handoff reaches its target without touching a third party.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcptest/multiparty_test.go` | add | the two- and three-party scenarios |
| `internal/mcptest/harness.go` | edit | `Parties` (one client per wing over one database) and the wing header the client sends |

## Ordered Steps

1. Write the failing tests first (TDD red): `ScenarioSecondPartySeesASharedWing`, `ScenarioSecondPartyDoesNotSeeAnotherWing`, `ScenarioHandoffReachesBAndNotC`. Commit them red.
2. Two parties, one workspace, different default wings. Prove BOTH directions: a shared wing is visible to both, and a private wing is not visible to the other. Only the pair proves scoping — a test that only shows visibility passes with scoping removed entirely.
3. Prove the `wing: "*"` route explicitly: a cross-wing question must reach the other party's memory when asked for, since that is the mechanism the team relies on for cross-project recall and nothing currently tests it.
4. Three parties: A files into B's inbox, B reads it, C's inbox is untouched and C's search does not surface it. This week's handoff defect was invisible because nobody looked from B's side; the third party is what turns "delivered" into "delivered to the right place".
5. Assert the `confirm_new_wing` refusal from the other side too — a handoff refused for A must not have partially landed for B.
6. Falsify: remove the wing scoping from search; deliver the handoff to every wing; have C's inbox count include B's items.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcptest/ -run "TestScenario" -count=1 -v 2>&1 | tee /tmp/e4.out
  grep -q -- "--- PASS: TestScenarioSecondPartySeesASharedWing" /tmp/e4.out
  grep -q -- "--- PASS: TestScenarioSecondPartyDoesNotSeeAnotherWing" /tmp/e4.out
  grep -q -- "--- PASS: TestScenarioCrossWingRecallReachesTheOtherParty" /tmp/e4.out
  grep -q -- "--- PASS: TestScenarioHandoffReachesBAndNotC" /tmp/e4.out
  grep -q -- "--- PASS: TestScenarioHandoffIntoAnUnknownWingIsRefusedForEveryone" /tmp/e4.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e4.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestScenarioSecondPartySeesASharedWing` | `internal/mcptest/multiparty_test.go` | a shared wing is readable by both registrations | — |
| `TestScenarioSecondPartyDoesNotSeeAnotherWing` | `internal/mcptest/multiparty_test.go` | scoping holds; the pair is what proves it | — |
| `TestScenarioCrossWingRecallReachesTheOtherParty` | `internal/mcptest/multiparty_test.go` | `wing: "*"` reaches another wing when explicitly asked | — |
| `TestScenarioHandoffReachesBAndNotC` | `internal/mcptest/multiparty_test.go` | delivery and isolation observed from both the target's and a bystander's side | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop wing scoping from search (`searchWingFor(..., false)`) | yes | `TestScenarioSecondPartyDoesNotSeeAnotherWing`, `TestScenarioHandoffReachesBAndNotC` |
| `wing:"*"` resolves to the caller's wing instead of every wing | yes | `TestScenarioCrossWingRecallReachesTheOtherParty` |
| the handoff guard removed, so an undeliverable wing is accepted | yes | `TestScenarioHandoffIntoAnUnknownWingIsRefusedForEveryone` |
| the client stops sending the wing header | yes | `TestScenarioSecondPartyDoesNotSeeAnotherWing`, `TestScenarioHandoffReachesBAndNotC` |

**A defect the pair caught, in the harness itself.** The first version stored each registration's
wing and never sent it, so every client looked unscoped. `TestScenarioSecondPartySeesASharedWing`
passed — a server that shows everything to everyone satisfies it — and the negative half failed. Had
this task only asserted visibility, it would have shipped a harness that cannot observe scoping at
all, and T2's gate would then have counted those scenarios as coverage. The fix sends
`auth.WingHeader` from the client, which is how `install` writes a real registration.

**Also caught: this repo's `doclint` gate.** A scenario's doc comment opened with the scenario name
rather than the test function's, and `TestDocCommentsMatchTheirDeclaration` failed the build.

## Out of Scope

- Cross-WORKSPACE isolation (deferred: docs/adr/BACKLOG.md — tenancy is a separate trust boundary and deserves its own scenarios, not a corner of this task)
- Concurrent mutation by two parties at once (deferred: docs/adr/BACKLOG.md — three parties here act in sequence; concurrency is the continuity spec's subject)

## Invariants

- Every visibility claim is proven in both directions.
- No scenario concludes delivery from the sender's side alone.

## Risks

- Two registrations in one process share more state than two real sessions would. Mitigated: they differ only by the wing header, which is exactly how the real deployment differs.

## Stop Condition

Stop and ask if two registrations cannot be created against one in-process server — the two-party half is the point of the task, and faking it with one client and a swapped header would test the header, not the scoping.

## Verification Log

- 2026-08-20 · b2583ed* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
  ```
  testing: warning: no tests to run
  PASS
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.005s [no tests to run]
  ```
- 2026-08-20 · b2583ed* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:5f841b5bc6ffccfe8a1be41d3847b04d5288b378ae8c44e666a87fe64f1651ac
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	2.580s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.021s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.005s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.443s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.006s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.006s
  FAIL
  ```
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:639c4430042eae54e6d87b8333ffe71efaa14512f72c1639c4f9ac526e5c003f

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/mcpserver/server.go` · the two- and three-party scenarios turn on one party naming ANOTHER partys wing explicitly; inverting the test ignores the wing the caller passed and sanitizes the empty one instead, so cross-wing recall silently answers from the wrong project · acceptance-sha256:639c4430042eae54e6d87b8333ffe71efaa14512f72c1639c4f9ac526e5c003f
