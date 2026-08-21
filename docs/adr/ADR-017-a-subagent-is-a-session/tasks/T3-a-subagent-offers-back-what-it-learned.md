# Task ADR-017-T3: A subagent offers back what it learned

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the registered `SubagentStop` nudge
**Consumes:** T1's compliance measurement
**Data dependency:** hermetic for the tests

## Goal

A subagent does not finish without being asked for what it found.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/installer.go` | edit | register `SubagentStop` |
| `clients/claude-code/installer_test.go` | edit | assert the registration |
| `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | edit | serve both events; a subagent's stop is its LAST, so `once` is the wrong default there |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestInstallerRegistersSubagentStop`, `TestStopHookAsksASubagentForFindingsNotASummary`. Commit them red.
2. Register `SubagentStop` through `ensureHook`.
3. The nudge differs from the main one, and the difference is the point: a subagent is asked for FINDINGS and DECISIONS, not a session summary. A session summary per subagent is how a diary becomes unreadable, and the dispatcher writes the summary.
4. `once`-per-session is wrong for a subagent, which stops once — the loop guard the main hook uses must not suppress it.
5. Falsify: drop the registration; let the subagent nudge be identical to the session one; let the `once` guard swallow it.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./clients/... -run "TestInstallerRegistersSubagentStop|TestStopHookAsksASubagentForFindingsNotASummary|TestSubagentStopIsNotSwallowedByTheOnceGuard" -count=1 -v 2>&1 | tee /tmp/a17t3.out
  grep -q -- "--- PASS: TestInstallerRegistersSubagentStop" /tmp/a17t3.out
  grep -q -- "--- PASS: TestStopHookAsksASubagentForFindingsNotASummary" /tmp/a17t3.out
  grep -q -- "--- PASS: TestSubagentStopIsNotSwallowedByTheOnceGuard" /tmp/a17t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a17t3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestInstallerRegistersSubagentStop` | `clients/claude-code/installer_test.go` | the event is registered and supersedes rather than duplicates | — |
| `TestStopHookAsksASubagentForFindingsNotASummary` | `clients/claude-code/installer_test.go` | the two nudges differ — one diary entry per subagent is how a journal stops being read | — |
| `TestSubagentStopIsNotSwallowedByTheOnceGuard` | `clients/claude-code/installer_test.go` | a subagent stops ONCE, so a once-per-session guard would silence it entirely | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the hook already exists; this is the second event |
| 2 — something selects it | `TestInstallerRegistersSubagentStop` |
| 3 — the caller can discover it | the harness fires it |
| 4 — it is used | every subagent dispatch on an installed machine |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop the `SubagentStop` registration | yes | `TestInstallerRegistersSubagentStop` |
| use the session nudge verbatim for a subagent | yes | `TestStopHookAsksASubagentForFindingsNotASummary` |
| apply the `once` guard to subagent stops | n/a (shell) | `TestSubagentStopIsNotSwallowedByTheOnceGuard` |

## Out of Scope

- Mining past sidechains so already-finished subagent work is recoverable (deferred: docs/adr/BACKLOG.md)
- Whether a subagent's writes should be attributed to it or to its dispatcher (deferred: docs/adr/BACKLOG.md — it needs a session identity the palace does not record; see the recall-stats attribution defect filed there)

## Invariants

- The hook never blocks a subagent finishing.
- A subagent is asked for findings, not for a session summary.

## Risks

- Subagent diary entries drown the human's. Mitigated by scoping what is asked for, and re-measured after a week of real use — the number to watch is entries per session, and the ADR says so rather than assuming.

## Stop Condition

Stop and ask if `SubagentStop` does not fire on this harness — the write half would then need to live in the dispatcher, which is a different design.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
