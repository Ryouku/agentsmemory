# Task ADR-021-T1: The server says what it expects

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the MCP `instructions` text
**Consumes:** none
**Data dependency:** hermetic

## Goal

Every client is told the wing rule on connection, instead of inferring one from the tool schema.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/server.go` | edit | `server.WithInstructions(...)` at construction, and the text |
| `internal/mcpserver/instructions_test.go` | add | the field is served, says the load-bearing thing, and stays short |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestHandshakeCarriesInstructions`, `TestInstructionsNameTheWingRule`, `TestInstructionsStayShort`. Commit them red.
2. Add `server.WithInstructions(serverInstructions)` to the `NewMCPServer` call.
3. Write the text. It names the wing rule — **pass no wing unless you mean to look elsewhere** — says recall before acting, and points at `am_skillset` rather than restating it. It does NOT name a wing: `WithInstructions` is construction-time and a hosted process serves many workspaces.
4. Cap the length with a test rather than an intention. This lands in every client's context on every session.
5. Falsify: drop the option; drop the wing sentence; let the text grow past the ceiling.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  if [ -n "$(gofmt -l cmd internal)" ]; then echo "gofmt"; exit 1; fi
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./internal/mcpserver/ -run "TestHandshakeCarriesInstructions|TestInstructionsNameTheWingRule|TestInstructionsStayShort" -count=1 -v 2>&1 | tee /tmp/a21t1.out
  grep -q -- "--- PASS: TestHandshakeCarriesInstructions" /tmp/a21t1.out
  grep -q -- "--- PASS: TestInstructionsNameTheWingRule" /tmp/a21t1.out
  grep -q -- "--- PASS: TestInstructionsStayShort" /tmp/a21t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a21t1.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHandshakeCarriesInstructions` | `internal/mcpserver/instructions_test.go` | a real `initialize` through the production server returns a non-empty `instructions` — driven through the transport, not read off the constant | — |
| `TestInstructionsNameTheWingRule` | `internal/mcpserver/instructions_test.go` | the text tells a client to pass NO wing by default, which is the exact rule a client got wrong unaided | — |
| `TestInstructionsStayShort` | `internal/mcpserver/instructions_test.go` | a ceiling, asserted rather than intended: this is context every client pays for on every session | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the constant compiles |
| 2 — something selects it | `TestHandshakeCarriesInstructions` drives a real initialize; the constant unused would return empty |
| 3 — the caller can discover it | it IS the discovery surface — the client is handed it without asking |
| 4 — it is used | T3, by asking a real client the question it previously got wrong |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop the `WithInstructions` option | yes | `TestHandshakeCarriesInstructions` |
| remove the wing sentence | yes | `TestInstructionsNameTheWingRule` |
| paste the whole bootstrap protocol in | yes | `TestInstructionsStayShort` |

## Out of Scope

- Per-session instructions naming the caller's actual wing (permanent: construction-time option, one process serves many workspaces; `am_status` answers that)
- Restating the bootstrap protocol here (permanent: ADR-017 measured the full protocol at 0/5; length is not what works)

## Invariants

- The text names no specific wing, because it is served to every workspace on the process.
- A client that ignores `instructions` behaves exactly as it does today.

## Risks

- Delivered and ignored. Measured in T3, which can send this task back.

## Stop Condition

Stop and ask if `instructions` cannot be served without changing the transport — the field is standard, and needing more than a construction option means the mcp-go version is not what the audit found.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
