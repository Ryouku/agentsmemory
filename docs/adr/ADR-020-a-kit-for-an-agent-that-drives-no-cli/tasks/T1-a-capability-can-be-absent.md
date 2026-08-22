# Task ADR-020-T1: An absent capability is legal kit data

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `cursorKit`, the empty-capability guards, the sandbox refusal
**Consumes:** none
**Data dependency:** hermetic

## Goal

A kit can declare that an agent has no commands directory, no memory file and no relocatable config dir, and every install step honours that instead of writing files nowhere.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/agentkit.go` | edit | `cursorKit`, `rulesFile`, and `resolveAgentKits` learning a fourth name |
| `clients/claude-code/installer.go` | edit | guard `writeCommands` and `registerMemoryBootstrap` on empty; refuse `--sandbox`/`--config-dir` for an agent with no `configEnv` |
| `clients/claude-code/installer_test.go` | edit | the guards and the refusal are asserted, not assumed |
| `clients/claude-code/agentkit_test.go` | edit | `--agent cursor` resolves, and `all` includes it |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCursorKitResolves`, `TestAgentWithoutACommandsDirWritesNoCommands`, `TestSandboxIsRefusedForAnAgentThatCannotRelocate`. Commit them red.
2. Declare `cursorKit`: `globalDir: ".cursor"`, `bin: "cursor-agent"`, `agentsDir: "agents"`, `agentAssetExt: ".md"`, `rulesFile: "rules/agentsmemory.mdc"`, and EMPTY `configEnv`, `commandsDir`, `memoryFile`, `hooksFile`.
3. Guard `writeCommands` on `commandsDir == ""` and `registerMemoryBootstrap` on `memoryFile == ""`. Empty must mean "this agent has none", the way `hooksFile == ""` already does for pi — a name comparison would be a fourth place to update for the fifth agent.
4. Refuse `--sandbox` and `--config-dir` when the kit has no `configEnv`: the files would be written where no Cursor looks, and the install would report success. An install that cannot be honoured fails loudly.
5. `resolveAgentKits` accepts `cursor` and includes it in `all`. `both` stays claude+codex.
6. Falsify: drop each guard and watch commands land in the config root; drop the refusal and watch a sandbox install report success.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  if [ -n "$(gofmt -l clients)" ]; then echo "gofmt"; exit 1; fi
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./clients/... -run "TestCursorKitResolves|TestAgentWithoutACommandsDirWritesNoCommands|TestSandboxIsRefusedForAnAgentThatCannotRelocate" -count=1 -v 2>&1 | tee /tmp/a20t1.out
  grep -q -- "--- PASS: TestCursorKitResolves" /tmp/a20t1.out
  grep -q -- "--- PASS: TestAgentWithoutACommandsDirWritesNoCommands" /tmp/a20t1.out
  grep -q -- "--- PASS: TestSandboxIsRefusedForAnAgentThatCannotRelocate" /tmp/a20t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a20t1.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCursorKitResolves` | `clients/claude-code/agentkit_test.go` | `--agent cursor` resolves to one kit and `all` contains it, while `both` does not | — |
| `TestAgentWithoutACommandsDirWritesNoCommands` | `clients/claude-code/installer_test.go` | an empty `commandsDir` writes NOTHING rather than writing into the config root — the failure an unguarded join produces | — |
| `TestSandboxIsRefusedForAnAgentThatCannotRelocate` | `clients/claude-code/installer_test.go` | `--sandbox`/`--config-dir` with a kit that has no `configEnv` is an error, not a silent success | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `cursorKit` compiles |
| 2 — something selects it | `TestCursorKitResolves` — `resolveAgentKits`, the one function `--agent` goes through |
| 3 — the caller can discover it | `--agent`'s help string names cursor; asserted in T3 with the README |
| 4 — it is used | an install run against a real `~/.cursor`, in T3 |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop `cursor` from `resolveAgentKits` | yes | `TestCursorKitResolves` |
| drop the `commandsDir == ""` guard | yes | `TestAgentWithoutACommandsDirWritesNoCommands` |
| make the sandbox refusal a warning | yes | `TestSandboxIsRefusedForAnAgentThatCannotRelocate` |
| leave `cursor` out of `all` | yes | `TestCursorKitResolves` |

## Out of Scope

- Any writing for Cursor — this task only makes the shape expressible (permanent: T2 and T3 own the writes, and splitting them is what keeps this one falsifiable)
- Project-scoped `.cursor` installs (deferred: docs/adr/BACKLOG.md)

## Invariants

- An empty capability field means the agent has none, everywhere. No install step compares `kit.name` to decide whether a capability exists.
- An install that cannot be honoured errors; it never reports success.

## Risks

- A guard added in one place and missed in another, so one asset still lands in the config root. Mitigated: the test asserts the config dir contains no unexpected files rather than asserting one absence.

## Stop Condition

Stop and ask if Cursor turns out to read a commands or memory file after all — then the kit sets those fields and the guards are still correct, but T3's protocol delivery changes shape.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
