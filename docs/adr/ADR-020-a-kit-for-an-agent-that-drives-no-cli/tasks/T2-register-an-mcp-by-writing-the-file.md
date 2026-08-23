# Task ADR-020-T2: Register an MCP server by writing the file

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the `~/.cursor/mcp.json` writer
**Consumes:** T1's `cursorKit`
**Data dependency:** hermetic

## Goal

`--agent cursor` registers the agentsmemory MCP server without a CLI to drive, and never loses a server the user already had.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/settings.go` | edit | `ensureMCPServer` — the same read-merge-backup-write discipline as `ensureHooks`, against a different key |
| `clients/claude-code/installer.go` | edit | `registerCursorMCP`, and the case in `registerAgentsMemoryMCP`'s switch |
| `clients/claude-code/settings_test.go` | edit | the merge preserves foreign servers and refuses unparseable JSON |
| `clients/claude-code/installer_test.go` | edit | the registration is asserted through `run()`, not by calling the writer |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCursorMCPRegistrationPreservesForeignServers`, `TestCursorInstallRegistersTheMCP`, `TestCursorMCPRefusesUnparseableJSON`. Commit them red.
2. `ensureMCPServer(path, name string, entry map[string]any)` in `settings.go`, beside `ensureHooks` and sharing `childObject`: read once, merge under `mcpServers`, back up the original, write once, and write NOTHING when the entry is already identical.
3. The entry is `{"type":"http","url":<mcpURL>}`, plus `{"headers":{"Authorization":"Bearer <token>"}}` when a token is resolved — the shape Cursor's own HTTP entries use.
4. Refuse an unparseable `mcp.json` rather than replacing it. It is a file the user shares with every other MCP server they run, and this is the first registration path with no CLI between us and it.
5. Say the approval step out loud. A registered-but-unapproved server is byte-identical on disk to a working one, and Cursor loads nothing until `cursor-agent mcp enable agentsmemory` runs.
6. Falsify: replace the merge with a write; drop the backup; register under the wrong key.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  if [ -n "$(gofmt -l clients)" ]; then echo "gofmt"; exit 1; fi
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./clients/... -run "TestCursorMCPRegistrationPreservesForeignServers|TestCursorInstallRegistersTheMCP|TestCursorMCPRefusesUnparseableJSON" -count=1 -v 2>&1 | tee /tmp/a20t2.out
  grep -q -- "--- PASS: TestCursorMCPRegistrationPreservesForeignServers" /tmp/a20t2.out
  grep -q -- "--- PASS: TestCursorInstallRegistersTheMCP" /tmp/a20t2.out
  grep -q -- "--- PASS: TestCursorMCPRefusesUnparseableJSON" /tmp/a20t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a20t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCursorMCPRegistrationPreservesForeignServers` | `clients/claude-code/settings_test.go` | a pre-existing server under `mcpServers` survives; this is the highest-impact risk in the ADR | — |
| `TestCursorInstallRegistersTheMCP` | `clients/claude-code/installer_test.go` | a full `run()` writes `mcp.json` with the right key, type and url — driven through the install, not the writer | — |
| `TestCursorMCPRefusesUnparseableJSON` | `clients/claude-code/settings_test.go` | a malformed file is an error and is left byte-identical, never replaced | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `ensureMCPServer` |
| 2 — something selects it | `TestCursorInstallRegistersTheMCP` drives `run()`, so the switch case is exercised rather than the function |
| 3 — the caller can discover it | Cursor reads `mcp.json` itself; the operator is told about the approval step, which the file cannot show |
| 4 — it is used | `cursor-agent mcp list-tools agentsmemory` against a real install, in T3 |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| replace the merge with a whole-file write | yes | `TestCursorMCPRegistrationPreservesForeignServers` |
| register under `servers` instead of `mcpServers` | yes | `TestCursorInstallRegistersTheMCP` |
| overwrite an unparseable file instead of refusing | yes | `TestCursorMCPRefusesUnparseableJSON` |
| drop the case from `registerAgentsMemoryMCP`'s switch | yes | `TestCursorInstallRegistersTheMCP` |

## Out of Scope

- Running `cursor-agent mcp enable` for the user (permanent: it is an approval, and an installer that approves its own server on the user's behalf defeats the point of the prompt)
- stdio / `--socket` registration for Cursor (deferred: docs/adr/BACKLOG.md)

## Invariants

- A server the user already had is never lost.
- An unparseable `mcp.json` is refused and left untouched.
- Re-running the install writes nothing when the entry is already correct.

## Risks

- Cursor's schema drifts and we write a stale shape. Mitigated: the entry mirrors what Cursor's own entries carry today, and a wrong shape surfaces at the approval step where a human is already looking.

## Stop Condition

Stop and ask if `cursor-agent mcp list` does not show the server after a clean write — the file is the whole mechanism, and if it is not enough, the kit needs a different route rather than a louder message.

## Mutation Log

- 2026-08-22 · 2469a25* · mutant killed · exit 1 · `clients/claude-code/settings.go` · a fresh map instead of a merge silently deletes every other MCP server the user runs, and this is the first registration path with no CLI between us and their file

## Verification Log

- 2026-08-22 · 2469a25* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
