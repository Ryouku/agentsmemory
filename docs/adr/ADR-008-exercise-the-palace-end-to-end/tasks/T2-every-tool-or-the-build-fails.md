# Task ADR-008-T2: Every registered tool has a scenario, or the build fails

**Depends-on:** T1
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
| `internal/mcpserver/e2e_test.go` | add | the gate, reading `fullCatalog` — the live registry, not a list |
| `internal/mcpserver/scenarios_test.go` | add | the registry, one entry per tool |

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
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestEveryToolIsExercisedEndToEnd|TestScenariosObserveAnEffect|TestUnobservableListNamesADependency" -count=1 -v 2>&1 | tee /tmp/e2.out
  grep -q -- "--- PASS: TestEveryToolIsExercisedEndToEnd" /tmp/e2.out
  grep -q -- "--- PASS: TestScenariosObserveAnEffect" /tmp/e2.out
  grep -q -- "--- PASS: TestUnobservableListNamesADependency" /tmp/e2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestEveryToolIsExercisedEndToEnd` | `internal/mcpserver/e2e_test.go` | every registered tool has a scenario or a reasoned exemption | — |
| `TestScenariosObserveAnEffect` | `internal/mcpserver/e2e_test.go` | no scenario passes on a single call | — |
| `TestUnobservableListNamesADependency` | `internal/mcpserver/e2e_test.go` | an exemption names an external dependency, so the list cannot become a parking lot | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| register a new tool with no scenario | yes | `TestEveryToolIsExercisedEndToEnd` |
| replace a scenario body with a single call | yes | `TestScenariosObserveAnEffect` |
| exempt a tool with the reason "hard to test" | yes | `TestUnobservableListNamesADependency` |
| enumerate scenarios from a literal list instead of `fullCatalog` | yes | `TestEveryToolIsExercisedEndToEnd` |

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

<Tool-written by adr-verify. Do not hand-edit.>
