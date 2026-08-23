# Task ADR-021-T2: A kit for a client with nowhere to put anything

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `claudeDesktopKit`, `agentKit.mcpConfigFile`, the Desktop registration
**Consumes:** none
**Data dependency:** hermetic

## Goal

`aiagentmemory install --agent claude-desktop` registers the server, instead of a human pasting JSON from a guide written for another deployment.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/agentkit.go` | edit | `claudeDesktopKit`, `mcpConfigFile`, a fifth `--agent` name |
| `clients/claude-code/installer.go` | edit | the registration: a stdio entry spawning `mcp-stdio --url`, and a refusal when no server binary is found |
| `clients/claude-code/installer_test.go` | edit | the entry is asserted through `run()`, and the missing-binary case fails loudly |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestClaudeDesktopKitResolves`, `TestClaudeDesktopInstallRegistersTheBridge`, `TestClaudeDesktopRefusesWithoutAServerBinary`. Commit them red.
2. Declare `claudeDesktopKit`: `globalDir` at Claude Desktop's application-support directory, `mcpConfigFile: "claude_desktop_config.json"`, and EMPTY everything else — no commands, no memory file, no hooks, no agents dir, no config env var.
3. Generalise ADR-020's `mcpConfigFile` onto the kit so the Cursor path and this one share `ensureMCPServer` rather than each naming their own file. Two agents now need it, which is the threshold the ADR-020 decision set for generalising.
4. Write a STDIO entry, not an HTTP one: Desktop's config file speaks to local processes, and the product ships its own bridge (`mcp-stdio --url`), so no Node.js is involved. Resolve the server binary the way `--socket` installs already do.
5. Refuse when no server binary can be found. Writing a `command` that is not there produces a client that fails at spawn with a message about our binary, which reads as our bug on the user's machine.
6. `--sandbox`/`--config-dir` are already refused for a kit with no `configEnv` (ADR-020 T1) — assert it here rather than re-implement it.
7. Falsify: drop the kit from `resolveAgentKits`; write an HTTP entry; let the missing binary through.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  if [ -n "$(gofmt -l clients)" ]; then echo "gofmt"; exit 1; fi
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./clients/... -run "TestClaudeDesktopKitResolves|TestClaudeDesktopInstallRegistersTheBridge|TestClaudeDesktopRefusesWithoutAServerBinary" -count=1 -v 2>&1 | tee /tmp/a21t2.out
  grep -q -- "--- PASS: TestClaudeDesktopKitResolves" /tmp/a21t2.out
  grep -q -- "--- PASS: TestClaudeDesktopInstallRegistersTheBridge" /tmp/a21t2.out
  grep -q -- "--- PASS: TestClaudeDesktopRefusesWithoutAServerBinary" /tmp/a21t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a21t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestClaudeDesktopKitResolves` | `clients/claude-code/installer_test.go` | `--agent claude-desktop` resolves and joins `all`; `both` does not grow | — |
| `TestClaudeDesktopInstallRegistersTheBridge` | `clients/claude-code/installer_test.go` | a full `run()` writes a `command`/`args` entry spawning `mcp-stdio --url`, preserves a foreign server, and shells out to no agent CLI | — |
| `TestClaudeDesktopRefusesWithoutAServerBinary` | `clients/claude-code/installer_test.go` | with no server binary resolvable the install ERRORS naming the build command, rather than writing a `command` that does not exist | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `claudeDesktopKit` compiles |
| 2 — something selects it | `TestClaudeDesktopKitResolves` — `resolveAgentKits`, the one function `--agent` goes through |
| 3 — the caller can discover it | the README gate from ADR-020 T3 already fails when an installable agent is undocumented |
| 4 — it is used | Claude Desktop's own `mcp-server-agentsmemory.log`, read in T3 |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop `claude-desktop` from `resolveAgentKits` | yes | `TestClaudeDesktopKitResolves` |
| write an HTTP entry instead of the stdio bridge | yes | `TestClaudeDesktopInstallRegistersTheBridge` |
| warn instead of erroring on a missing server binary | yes | `TestClaudeDesktopRefusesWithoutAServerBinary` |
| replace the merge with a whole-file write | yes | `TestClaudeDesktopInstallRegistersTheBridge` |

## Out of Scope

- Claude Desktop extensions as a packaging route (deferred: docs/adr/BACKLOG.md)
- Windows and Linux Desktop config paths (deferred: docs/adr/BACKLOG.md — the kit is written against the macOS path that was measured; the others were not)
- Installing the server binary for the user (permanent: the kit installs client configuration, and building the server is the deploy step the README already covers)

## Invariants

- A server the user already had in `claude_desktop_config.json` is never lost.
- An install that cannot name a real server binary fails; it never writes a broken `command`.

## Risks

- The measured config path is macOS-only, so the kit silently targets the wrong directory elsewhere. Mitigated by refusing rather than guessing: the kit declares one path, and the Out of Scope entry says the others were never established.

## Stop Condition

Stop and ask if Claude Desktop's config turns out to accept an HTTP entry — the stdio bridge would then be an unnecessary dependency on a host binary, and the entry should be the simpler one.

## Mutation Log

- 2026-08-22 · 15bf930* · mutant killed · exit 1 · `clients/claude-code/installer.go` · a missing host binary writes a command that does not exist, and Claude Desktop then fails at spawn with an error naming our binary — which reads as our bug on the users machine

## Verification Log

- 2026-08-22 · 15bf930* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
