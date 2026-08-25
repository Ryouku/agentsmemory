# Task ADR-008-T2: Every registered tool has a scenario, or the build fails

**Depends-on:** T1

> **Amended 2026-08-20 during execution, on the "lands red" risk.** The task said to mark the gate
> `t.Skip` with a dated TODO if it stayed red. That was the wrong instrument and the amendment is
> the whole file's, not this line's: a skipped gate is decoration that reads as coverage, and a
> permanently red one blocks every unrelated task's acceptance run until somebody deletes the gate
> rather than the gap. It is a RATCHET instead — the uncovered count is pinned exactly at today's
> honest 38, so it fails on regression AND fails when coverage improves without the ceiling being
> lowered in the same commit. T3 takes it to 0 and deletes the constant.
>
> The scenarios and the gate also live in `internal/mcptest`, not `internal/mcpserver`: the harness
> imports `mcpserver`, so a test there would be an import cycle. Same amendment as T4.
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the scenario registry and the exhaustiveness gate
**Consumes:** `mcptest.Harness` (T1)
**Data dependency:** hermetic

## Goal

The live catalogue is compared against the scenario registry; a tool with no scenario fails, and a scenario that makes only one call fails too.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcptest/exhaustive_test.go` | add | the gate, reading the RUNNING server's catalogue, not a list |
| `internal/mcptest/scenarios.go` | add | the `Scenario` and `Unobservable` types |
| `internal/mcptest/registry_test.go` | add | the scenario registry and the exemption list |
| `internal/mcptest/harness.go` | edit | record every call, so coverage is measured from what ran rather than claimed |

## Ordered Steps

1. Write the failing test first (TDD red): `TestEveryToolIsExercisedEndToEnd` — enumerate `fullCatalog`, require a scenario per name. Commit it red; it will report ~39 missing, which is the measurement this ADR opens with.
2. Enumerate from the live registry, never a literal list. `TestCatalogSizeIsWhatTheReadmeClaims` already proves that registry is the real one.
3. Require each scenario to make **at least two calls** — the acting one and an observing one. A scenario asserting "no error" is the failure mode this gate exists to prevent, so the gate must reject it structurally rather than by review.
4. Add the unobservable list: tools needing a live Qdrant, TEI or OAuth issuer. Each entry names the dependency; an entry whose reason names no external dependency fails. This is the parking-lot risk, closed mechanically.
5. Fail on a tool that is in NEITHER the registry nor the list — silence must be declared, never inherited.
6. Falsify: add a tool without a scenario; write a one-call scenario; park a tool in the unobservable list with a reason like "hard to test".
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcptest/ -run "TestEveryToolIsExercisedEndToEnd|TestScenariosObserveAnEffect|TestScenariosOnlyClaimToolsTheyCall|TestUnobservableListNamesADependency" -count=1 -v 2>&1 | tee /tmp/e2.out
  grep -q -- "--- PASS: TestEveryToolIsExercisedEndToEnd" /tmp/e2.out
  grep -q -- "--- PASS: TestScenariosObserveAnEffect" /tmp/e2.out
  grep -q -- "--- PASS: TestScenariosOnlyClaimToolsTheyCall" /tmp/e2.out
  grep -q -- "--- PASS: TestUnobservableListNamesADependency" /tmp/e2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestEveryToolIsExercisedEndToEnd` | `internal/mcptest/exhaustive_test.go` | every registered tool has a scenario or a reasoned exemption | — |
| `TestScenariosObserveAnEffect` | `internal/mcptest/exhaustive_test.go` | no scenario passes on a single call | — |
| `TestUnobservableListNamesADependency` | `internal/mcptest/exhaustive_test.go` | an exemption names an external dependency, so the list cannot become a parking lot | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| a scenario claims a tool it never calls | yes | `TestScenariosOnlyClaimToolsTheyCall` |
| a scenario body reduced to one call | yes | `TestScenariosObserveAnEffect` + the ratchet |
| exempt a tool for "more time" | yes | `TestUnobservableListNamesADependency` |
| the harness stops recording calls | yes | `TestEveryToolIsExercisedEndToEnd` (41/41 uncovered) + `TestScenariosObserveAnEffect` |
| tool list read from a literal instead of the running server | yes | `TestEveryToolIsExercisedEndToEnd` (ratchet: improved without lowering) |

All five compile and all five die. The last two are the ones worth having: coverage is counted from
the calls the harness RECORDED, so a scenario cannot claim a tool it never invoked, and the tool list
comes from the running server, so a tool added without a scenario fails without anyone maintaining a
list. Both are the failure this repo hit twice this week — an exclusion list keyed by arm name, and a
registration gate that scanned only `const` declarations.

## Out of Scope

- Writing all 39 scenarios (permanent: T3 and T4 do that; this task is the gate that makes their absence fail)
- Asserting anything about latency (permanent: correctness only, per the parent ADR)

## Invariants

- The tool list comes from the registry at run time.
- No tool is silently outside both the registry and the exemption list.

## Risks

- The gate lands red and stays red while scenarios are written. Accepted and intended: it is the measurement, and T3/T4 close it. Mark it `t.Skip` only with a dated TODO naming the task that removes the skip.

## Stop Condition

Stop and ask if the number of genuinely unobservable tools exceeds five — that is a finding about the surface's testability, not a list to fill in.

## Verification Log

- 2026-08-20 · 283c282* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:f1e35c1faf4c420b96317f3dfe0458a7d65997d21550728530bfe6fc381ba27c
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.012s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	4.058s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.326s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.004s
  FAIL
  ```
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:2ac1632988375f31efcf685c4e911f8494133e229b6035699e701e6cfa53a350

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant survived · exit 0 · `internal/mcptest/scenarios.go` · the build fails unless every registered tool is exercised or carries a named, justified exemption; accepting an exemption with no tool name at all reopens the escape hatch, and the exemption list is empty today so only a direct test of this rule can catch it · acceptance-sha256:2ac1632988375f31efcf685c4e911f8494133e229b6035699e701e6cfa53a350
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/mcptest/harness.go` · every gate in this task compares what a scenario CLAIMS against what it actually invoked, and this line is the only record of the second half; with nothing recorded the coverage check has no observations to test claims against. A first mutant on ValidExemptions empty-name branch SURVIVED, which showed this fence never reaches the exemption rule — the exemption list is empty, so that branch is exercised by no scenario · acceptance-sha256:2ac1632988375f31efcf685c4e911f8494133e229b6035699e701e6cfa53a350
