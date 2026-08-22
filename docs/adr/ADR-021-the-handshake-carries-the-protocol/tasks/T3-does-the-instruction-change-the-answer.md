# Task ADR-021-T3: Does the instruction change the answer

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the measurement, and T1's verdict
**Consumes:** T1's instructions text, T2's installed registration
**Data dependency:** one live Claude Desktop session — human-observed, outside the hermetic suite

## Goal

Find out whether a client handed the instructions stops inventing the rule it invented without them.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `docs/adr/ADR-021-the-handshake-carries-the-protocol.md` | edit | record the measurement and the verdict, whichever way it goes |
| `README.md` | edit | `--agent claude-desktop`, and what Desktop does and does not get |
| `clients/claude-code/README.md` | edit | the same, plus the server-binary prerequisite |
| `internal/web/windows-guide.md` | edit | stop recommending `npx mcp-remote` for a self-hosted server that ships its own bridge |

## Ordered Steps

1. Write the failing test first (TDD red): `TestReadmeNamesEveryInstallableAgent` already exists from ADR-020 T3 and goes red the moment `claude-desktop` resolves — confirm it is red before documenting, so the gate is what drives the docs rather than the author's memory. Commit red if it is not already.
2. Ask a fresh Claude Desktop session the question that produced the wrong rule: what happens to an `am_search` that names no wing. Record the answer VERBATIM, before and after.
3. Record the verdict in the ADR. **If the answer does not change, say so and withdraw mechanism 1** — the ADR names that outcome as acceptable, and mechanism 2 ships regardless. Do not soften it into "it probably helps".
4. Document `--agent claude-desktop` in both READMEs, including the prerequisite the reference machine did not meet: a server binary on the host, which a Docker-only install does not produce.
5. Correct `windows-guide.md`: it offers the Custom-connector UI and `npx mcp-remote`, both aimed at the hosted service, and does not mention the bridge the product ships.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  if [ -n "$(gofmt -l cmd internal clients)" ]; then echo "gofmt"; exit 1; fi
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./clients/... -run "TestReadmeNamesEveryInstallableAgent" -count=1 -v 2>&1 | tee /tmp/a21t3.out
  grep -q -- "--- PASS: TestReadmeNamesEveryInstallableAgent" /tmp/a21t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a21t3.out
  grep -q "mcp-stdio" internal/web/windows-guide.md
  go test ./... -count=1'
```

**Human-observed, recorded separately:** the before/after answers from a live
Claude Desktop session. No unit test can reach this — the suite proves the text is
served, and only a client proves it is read.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestReadmeNamesEveryInstallableAgent` | `clients/claude-code/installer_test.go` | both READMEs name `--agent claude-desktop`; reused from ADR-020 T3, and it goes red on its own the moment the kit resolves | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | T1's text and T2's registration |
| 2 — something selects it | the handshake, on every connection |
| 3 — the caller can discover it | the READMEs name the agent; the guide names the bridge |
| 4 — it is used | THIS task — a live client, asked the question that failed before |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| remove `claude-desktop` from a README | n/a (markdown) | `TestReadmeNamesEveryInstallableAgent` |
| leave the guide recommending only `npx mcp-remote` | n/a (markdown) | the `grep -q mcp-stdio` line in Acceptance |

## Out of Scope

- Measuring other MCP clients' handling of `instructions` (deferred: docs/adr/BACKLOG.md — Desktop is the one that failed unaided and the one measured here)
- A control arm with the instructions switched off (permanent: the BEFORE answer is the control, it is already recorded verbatim in the ADR's Context, and it was produced before this ADR existed)

## Invariants

- The verdict is recorded whichever way it goes; a measurement that only reports success is not a measurement.

## Risks

- n = 1 session, one question. Stated as such rather than generalised — the same discipline ADR-017 T1 applied to its own 5-per-arm numbers.

## Stop Condition

Stop and ask if Claude Desktop does not surface `instructions` to its model at all — mechanism 1 would then be correct and undeliverable on this client, which is a different finding from "delivered and ignored" and deserves a different response.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
