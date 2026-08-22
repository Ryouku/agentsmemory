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
| `clients/claude-code/hooks_test.go` | edit | the captured payload fixture and the three behaviour tests, beside the harness that runs the shipped script |
| `scripts/redeploy.sh` | edit | the client-kit freshness gate never listed T2's `agentsmemory-subagent-start-hook.sh`, so a stale copy of it was the one installed artifact the gate could not see |
| `clients/claude-code/assets.go` | edit | `agentAssets` — the list of subagent definitions an install writes; T2 embedded the file and wrote it nowhere |
| `clients/claude-code/installer.go` | edit | `writeAgentDefinitions` — ADR-017's mechanism 1 had no rung 2 |

## Ordered Steps

1. Write the failing tests first (TDD red), against a REAL captured `SubagentStop` payload rather than a hand-authored one — register a hook that only tees its stdin, dispatch one trivial subagent, read it. Three facts the branch depends on are not decidable by reading, and a fixture written from imagination proves the branch works for the JSON its author imagined. Commit them red.
2. Register `SubagentStop` through `ensureHook`, pointing at the SAME script as `Stop`: the two nudges differ in text, not machinery.
3. The nudge differs from the main one, and the difference is the point: a subagent is asked for FINDINGS and DECISIONS, not a session summary. A session summary per subagent is how a diary becomes unreadable, and the dispatcher writes the summary. It is also told to pass NO wing — on the read side a guessed wing costs a bad recall, on the write side it costs another project's palace.
4. `once`-per-session is wrong for a subagent, which stops once — and the marker is keyed on a `session_id` that SubagentStop shares with its parent, so a subagent stop must neither read nor write it.
5. Repair what T2 left unreachable, found while checking this task's own deploy gate: `agentsmemory-researcher.md` was embedded in the binary and installed nowhere, and the test covering it globbed the repository rather than the install.
6. Falsify: drop each registration; let the subagent nudge be identical to the session one; let the `once` guard swallow it; exit 0 instead of 2.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  # NOT `gofmt -l clients | grep -q . && exit 1`: when gofmt is CLEAN grep exits 1,
  # the && list exits 1, and `set -e` aborts — a green tree failing its gate for
  # being green. FIFTH occurrence of this shape in this corpus.
  if [ -n "$(gofmt -l clients)" ]; then echo "gofmt"; exit 1; fi
  # The hook tests execute the SHIPPED bash scripts and FAIL LOUDLY without bash
  # rather than skipping, so the base image needs it before anything runs.
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./clients/... -run "TestInstallerRegistersSubagentStop|TestStopHookAsksASubagentForFindingsNotASummary|TestSubagentStopIsNotSwallowedByTheOnceGuard|TestUnknownStopEventKeepsTheSessionBehaviour|TestSubagentStopHookCanBeDisabledOnItsOwn|TestInstallerInstallsAgentDefinitions|TestEveryShippedAgentDefinitionIsInstalled|TestRedeployKitCheckCoversEveryInstalledArtifact" -count=1 -v 2>&1 | tee /tmp/a17t3.out
  grep -q -- "--- PASS: TestInstallerRegistersSubagentStop" /tmp/a17t3.out
  grep -q -- "--- PASS: TestStopHookAsksASubagentForFindingsNotASummary" /tmp/a17t3.out
  grep -q -- "--- PASS: TestSubagentStopIsNotSwallowedByTheOnceGuard" /tmp/a17t3.out
  grep -q -- "--- PASS: TestUnknownStopEventKeepsTheSessionBehaviour" /tmp/a17t3.out
  grep -q -- "--- PASS: TestSubagentStopHookCanBeDisabledOnItsOwn" /tmp/a17t3.out
  grep -q -- "--- PASS: TestInstallerInstallsAgentDefinitions" /tmp/a17t3.out
  grep -q -- "--- PASS: TestEveryShippedAgentDefinitionIsInstalled" /tmp/a17t3.out
  grep -q -- "--- PASS: TestRedeployKitCheckCoversEveryInstalledArtifact" /tmp/a17t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a17t3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestInstallerRegistersSubagentStop` | `clients/claude-code/installer_test.go` | the event is registered and supersedes rather than duplicates | — |
| `TestStopHookAsksASubagentForFindingsNotASummary` | `clients/claude-code/hooks_test.go` | the two nudges differ, the subagent one asks for findings not a summary, tells it to pass no wing, and exits 2 so the text actually reaches the agent | — |
| `TestSubagentStopIsNotSwallowedByTheOnceGuard` | `clients/claude-code/hooks_test.go` | both directions of the shared-marker collision: a fired session marker must not silence subagents, and a subagent must not claim the marker and silence the human | — |
| `TestUnknownStopEventKeepsTheSessionBehaviour` | `clients/claude-code/hooks_test.go` | the branch degrades to today's behaviour on an unrecognised event, rather than failing closed and taking the human's checkpoint with it | — |
| `TestSubagentStopHookCanBeDisabledOnItsOwn` | `clients/claude-code/hooks_test.go` | `AGENTSMEMORY_SUBAGENT_STOP_HOOK=off` silences the subagent half alone and leaves the session checkpoint | — |
| `TestInstallerInstallsAgentDefinitions` | `clients/claude-code/installer_test.go` | the INSTALLED agent definition exists — T2 shipped it into the binary and onto no disk, and its own test globbed the repository | — |
| `TestEveryShippedAgentDefinitionIsInstalled` | `clients/claude-code/installer_test.go` | a definition added to the repository but not to `agentAssets` is embedded and installed nowhere, in silence | — |
| `TestRedeployKitCheckCoversEveryInstalledArtifact` | `clients/claude-code/installer_test.go` | the deploy staleness gate's hand-maintained list covers every artifact an install writes | — |

The four hook-behaviour tests live beside `runStopHookWithInput` in
`hooks_test.go` rather than in `installer_test.go` as first written: the harness
that executes the shipped shell script is there, and a task file naming the wrong
file is the drift this pipeline exists to catch.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the hook already exists; this is the second event |
| 2 — something selects it | `TestInstallerRegistersSubagentStop` |
| 3 — the caller can discover it | the harness fires it |
| 4 — it is used | MEASURED, 2026-08-22, after installing the kit. Four live dispatches; counts below |

### The rung-4 measurement, and what it does not show

Four dispatches on the installed kit. The first is reported separately because it
was **not a valid trial**: its `am_search` was permission-denied in the headless
session, so it had no working write path either and "nothing worth persisting"
was the only answer available to it, not a judgment. It is evidence the nudge is
DELIVERED and read, and nothing more.

The other three had the `am_*` tools allowed and reached the server (the server's
own call counter moved on each):

| | count |
|---|---|
| received the nudge and acted on it | **3 / 3** |
| filed a drawer | **1 / 3** |
| took the nudge's documented out, explicitly | **2 / 3** |

The one that filed wrote a good memory: correct, in the right room, and **code-anchored**
to `internal/palace/import.go` — an anchor the server later verified. Not a
retelling of its task, which is what the nudge asks for.

**The two abstentions may be correct rather than a compliance failure, and this
design cannot tell.** All three tasks were "read this file and state X",
answerable from about twenty lines of source — exactly what the protocol says not
to file ("don't save what the repo already records"). So the honest reading is:
the mechanism reaches every subagent and every subagent acts on it, and the write
RATE on small re-derivable tasks is 1 in 3. Whether that is discrimination or
avoidance needs trials whose finding is genuinely not recoverable from the code,
which these were not.

**The number to watch**, as the ADR's risk row already says: drawers filed per
dispatch, over a week of real fan-outs. If it stays near zero on tasks that DO
produce unrecoverable findings, the write half needs the same correction T1
forced on the read half — and the fallback is the same one the ADR already names
for the read side: do it for the agent rather than ask.

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop the `SubagentStop` registration | yes | `TestInstallerRegistersSubagentStop` |
| use the session nudge verbatim for a subagent | yes | `TestStopHookAsksASubagentForFindingsNotASummary` |
| apply the `once` guard to subagent stops | n/a (shell) | `TestSubagentStopIsNotSwallowedByTheOnceGuard` |
| let a subagent stop WRITE the once marker | n/a (shell) | `TestSubagentStopIsNotSwallowedByTheOnceGuard` (second subtest) |
| exit 0 instead of 2 on the subagent branch | n/a (shell) | `TestStopHookAsksASubagentForFindingsNotASummary` |
| treat every unrecognised event as a subagent | n/a (shell) | `TestUnknownStopEventKeepsTheSessionBehaviour` |
| drop the `writeAgentDefinitions` call | yes | `TestInstallerInstallsAgentDefinitions` |
| remove a definition from `agentAssets` | yes | `TestEveryShippedAgentDefinitionIsInstalled` |
| drop an artifact from redeploy.sh's freshness list | n/a (shell) | `TestRedeployKitCheckCoversEveryInstalledArtifact` |

## Out of Scope

- Mining past sidechains so already-finished subagent work is recoverable (deferred: docs/adr/BACKLOG.md)
- Whether a subagent's writes should be attributed to it or to its dispatcher (deferred: docs/adr/BACKLOG.md — it needs a session identity the palace does not record; see the recall-stats attribution defect filed there)

## Invariants

- The hook never PREVENTS a subagent finishing. It costs at most one extra turn,
  once per subagent, and this wording is the amended one: the task was written
  saying "never blocks", which exit 2 makes false. Exit 2 is the mechanism — a
  `SubagentStop` hook that exits 0 emits text nobody reads, which is registered,
  fired, and inert. The bound is the loop guard: `stop_hook_active` is sent on
  `SubagentStop` (observed; the published payload reference does not list it), so
  the second stop passes straight through.
- A subagent is asked for findings, not for a session summary.
- An unrecognised stop event keeps today's behaviour. The branch may not be able
  to take the human's checkpoint away by failing to recognise its own event.

## What the captured payload settled

Three facts, none of them decidable by reading, all captured by registering a
hook that did nothing but tee its stdin and dispatching one trivial subagent:

- `hook_event_name` is exactly `"SubagentStop"`. The branch turns on this string.
- `stop_hook_active` is present. The payload reference omits it; without it an
  exit-2 nudge would re-fire forever, and this task would have shipped a loop.
- `session_id` is IDENTICAL to the parent session's. That is what makes the
  `once` marker a collision rather than a theory — and it is the reason a subagent
  stop must neither read nor write it, in both directions.

## Risks

- Subagent diary entries drown the human's. Mitigated by scoping what is asked for, and re-measured after a week of real use — the number to watch is entries per session, and the ADR says so rather than assuming.
- A wide fan-out pays one extra turn per branch. Mitigated by `AGENTSMEMORY_SUBAGENT_STOP_HOOK=off`, which silences the subagent half alone rather than forcing a choice between subagent writes and the human's checkpoint.
- A subagent whose definition carries a `tools:` allowlist without the `am_*` tools is blocked and CANNOT comply. Bounded, not removed: the loop guard means it costs one turn, in which the subagent says it has no such tool and stops. The nudge's own last line gives it the out.

## Stop Condition

Stop and ask if `SubagentStop` does not fire on this harness — the write half would then need to live in the dispatcher, which is a different design.

## Mutation Log

- 2026-08-22 · 6c9347f* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the registration points at an event no harness fires, so every subagent finishes with its findings in a transcript mineclaude drops by design
- 2026-08-22 · 6c9347f* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · the once-per-session marker is shared with the parent session, so a session that already stopped silences every subagent under it and a subagent that stops first silences the human
- 2026-08-22 · 6c9347f* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · exit 0 means the nudge never reaches the agent: registered, fired, and inert, which is indistinguishable from never having been registered
- 2026-08-22 · 6c9347f* · mutant killed · exit 1 · `clients/claude-code/hooks/agentsmemory-stop-hook.sh` · failing closed on an unrecognised event takes the humans own checkpoint away too, on a rename nobody announced
- 2026-08-22 · 6c9347f* · mutant killed · exit 1 · `clients/claude-code/installer.go` · the agent definition stays embedded in the binary and reaches no disk, which is exactly the defect T2 shipped and this task found
- 2026-08-22 · 6c9347f* · mutant killed · exit 1 · `scripts/redeploy.sh` · an installed artifact missing from the freshness list is the one the staleness gate cannot see, which is how a stale SubagentStart hook reported as deployed and verified

## Verification Log

- 2026-08-22 · 6c9347f* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
