# Task ADR-017-T2: A subagent wakes knowing its wing and that memory exists

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the registered `SubagentStart` hook and the shipped agent definitions
**Consumes:** T1's compliance measurement — its result decides whether the INJECTION half of this task is built at all. The agent definitions half ships either way, because it changes what is possible rather than what is asked.
**Data dependency:** hermetic for the tests

## Goal

Every subagent dispatched on a machine with agentsmemory installed starts with its wing named and one call that answers "what has this project already decided".

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/installer.go` | edit | register `SubagentStart`, reusing `ensureHook` |
| `clients/claude-code/installer_test.go` | edit | the registration is asserted, not assumed |
| `clients/claude-code/agents/` | add | definitions naming the `am_*` tools, for agents whose `tools:` allowlist would otherwise exclude them |
| `clients/claude-code/assets.go` | edit | embed the new directory so the binary stays self-contained |
| `clients/claude-code/README.md` | edit | the "applies every session" claim says what it covers |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestInstallerRegistersSubagentStart`, `TestSubagentContextNamesTheWing`, `TestSubagentContextStaysShort`. Commit them red.
2. Register the hook through `ensureHook`, with the same supersession predicate the other three use, so a re-install replaces rather than duplicates.
3. The injected context names the WING and the one recall call. It does not restate the protocol: a subagent has one job and a budget, and the precedent on the reference machine injects roughly one paragraph.
4. Ship agent definitions whose `tools:` include the `am_*` tools. This half is unconditional: an agent whose definition restricts tools cannot call memory however it is instructed, and it is the only part of this ADR that cannot fail for compliance reasons. A `general-purpose` subagent already has all 41 and can call them — measured — so this is for the restricted definitions, of which this repository currently ships none and the machine ships three.
5. Correct `README.md`. It claims the memory-first workflow "applies every session", which was false for subagents; it must now either be true or say what it covers.
6. Falsify: drop the registration; let the context omit the wing; let it grow past the ceiling.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./clients/... -run "TestInstallerRegistersSubagentStart|TestSubagentContextNamesTheWing|TestSubagentContextStaysShort|TestShippedAgentDefinitionsNameTheMemoryTools" -count=1 -v 2>&1 | tee /tmp/a17t2.out
  grep -q -- "--- PASS: TestInstallerRegistersSubagentStart" /tmp/a17t2.out
  grep -q -- "--- PASS: TestSubagentContextNamesTheWing" /tmp/a17t2.out
  grep -q -- "--- PASS: TestSubagentContextStaysShort" /tmp/a17t2.out
  grep -q -- "--- PASS: TestShippedAgentDefinitionsNameTheMemoryTools" /tmp/a17t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a17t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestInstallerRegistersSubagentStart` | `clients/claude-code/installer_test.go` | the event is registered, and a re-install supersedes rather than duplicates | — |
| `TestSubagentContextNamesTheWing` | `clients/claude-code/installer_test.go` | the injected text names the wing, rather than telling the subagent to resolve it — Step 0c is the step an agent with one job skips | — |
| `TestSubagentContextStaysShort` | `clients/claude-code/installer_test.go` | a length ceiling, asserted rather than intended: a reminder nobody finishes reading is scenery | — |
| `TestShippedAgentDefinitionsNameTheMemoryTools` | `clients/claude-code/installer_test.go` | every shipped definition with a `tools:` allowlist includes the `am_*` tools — an instruction to call an absent tool is worse than silence | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the hook script, from T1 |
| 2 — something selects it | `TestInstallerRegistersSubagentStart` — the installer, not a hand-edited settings file |
| 3 — the caller can discover it | nothing to discover: the harness fires it, the subagent does not opt in |
| 4 — it is used | T1's measurement, re-taken after this lands |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop the `ensureHook` call for `SubagentStart` | yes | `TestInstallerRegistersSubagentStart` |
| inject a context that omits the wing | yes | `TestSubagentContextNamesTheWing` |
| inject the whole bootstrap protocol | yes | `TestSubagentContextStaysShort` |
| ship an agent definition whose `tools:` omits the `am_*` tools | yes | `TestShippedAgentDefinitionsNameTheMemoryTools` |

## Out of Scope

- codex and pi subagent models (deferred: docs/adr/BACKLOG.md)
- Running the recall in the hook and injecting the RESULTS (deferred: docs/adr/BACKLOG.md)

## Invariants

- The hook never blocks a dispatch.
- Any shipped agent definition that restricts tools includes the memory tools.

## Risks

- The injected context is read and ignored. Mitigated: T1 measures exactly that before this task is written, and the ADR names what to do instead.

## Stop Condition

Stop and ask if Claude Code does not deliver `additionalContext` from `SubagentStart` into the subagent's context — T1 should have caught that, and if it did not, the mechanism is wrong rather than the wording.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
