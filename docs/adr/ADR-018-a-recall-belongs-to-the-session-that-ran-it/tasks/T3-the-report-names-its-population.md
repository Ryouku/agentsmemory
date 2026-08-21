# Task ADR-018-T3: The report names its population, and refuses the task list it cannot attribute

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** an honestly-labelled recall report
**Consumes:** none — deliberately independent of every other task in this ADR
**Data dependency:** hermetic

## Goal

The Stop hook stops presenting other sessions' work as yours, and stops handing you their unanswered questions as a to-do list.

This task depends on nothing and ships first if the others are delayed. It needs no schema change, and it removes the path that produces fabricated memories — which is the harm, while the wrong percentages are only an error.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | edit | ask for this session; label what comes back; print no task list that cannot be attributed |
| `clients/claude-code/hooks_test.go` | add | the labelling and the refusal are asserted, not intended |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestReportNamesItsPopulation`, `TestNoTaskListWithoutAttribution`. Commit them red.
2. Pass `session=` when the hook has an identity for this session.
3. Label the heading with the population the numbers actually describe — this session, or the whole palace. A statistic that names its population is useful at any scope; one that does not is the defect.
4. When the recalls cannot be attributed to this session, print the numbers under the palace-wide heading and **print no "memories to write" list at all**. The section is the most useful thing the hook emits and the most dangerous thing to misattribute: following it means writing a memory about a question you never asked, into a wing you never opened.
5. Remove the comment claiming the window is "THIS SESSION". It describes an intent the code cannot carry out, and it is why nobody looked again.
6. Falsify: print the task list without attribution; label a palace-wide report as this session's.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./clients/... -run "TestReportNamesItsPopulation|TestNoTaskListWithoutAttribution" -count=1 -v 2>&1 | tee /tmp/a18t3.out
  grep -q -- "--- PASS: TestReportNamesItsPopulation" /tmp/a18t3.out
  grep -q -- "--- PASS: TestNoTaskListWithoutAttribution" /tmp/a18t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a18t3.out
  ! grep -q "The window is THIS SESSION" clients/claude-code/hooks/agentsmemory-stop-hook.sh
  go test ./clients/... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestReportNamesItsPopulation` | `clients/claude-code/hooks_test.go` | the heading says whose recalls these are, at either scope | — |
| `TestNoTaskListWithoutAttribution` | `clients/claude-code/hooks_test.go` | unattributable recalls produce numbers and NO "memories to write" section | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | both tests |
| 2 — something selects it | the hook is already registered on `Stop`; this changes what it prints |
| 3 — the caller can discover it | the agent reads it every session |
| 4 — it is used | it fires on the first Stop of every session on an installed machine |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| print the task list regardless of attribution | n/a (shell) | `TestNoTaskListWithoutAttribution` |
| label a palace-wide report as this session's | n/a (shell) | `TestReportNamesItsPopulation` |

## Out of Scope

- Making the numbers per-session, which needs the schema change (deferred: docs/adr/ADR-018-a-recall-belongs-to-the-session-that-ran-it.md)

## Invariants

- No "memories to write" list is ever printed for recalls that cannot be attributed to this session.
- The hook still never fails a Stop.

## Risks

- An operator on a single-session machine loses the task list until T2 lands, and that list is the hook's most useful output. Accepted deliberately: a list that is right most of the time and silently wrong the rest is what produced a near-fabrication, and "most of the time" is not a property anyone can check at the moment they read it.

## Stop Condition

Stop and ask if the Stop event carries no usable per-session key at all — the hook could then never attribute, and the palace-wide-and-labelled report becomes the permanent answer rather than a fallback.

## Verification Log

- 2026-08-21 · 9f1b093* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
  ```
  === RUN   TestNoTaskListWithoutAttribution
      hooks_test.go:86: the statistics were suppressed along with the task list; they are worth keeping:
  --- FAIL: TestNoTaskListWithoutAttribution (0.00s)
  === RUN   TestReportNamesItsPopulation
      hooks_test.go:96: the report does not say whose recalls it describes, so it reads as this session's:
  --- FAIL: TestReportNamesItsPopulation (0.00s)
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/clients/claude-code	0.004s
  FAIL
  ```
