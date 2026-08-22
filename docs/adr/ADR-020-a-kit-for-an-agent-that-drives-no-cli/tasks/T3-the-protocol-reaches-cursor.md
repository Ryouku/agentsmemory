# Task ADR-020-T3: The protocol reaches Cursor

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `rules/agentsmemory.mdc`, the installed subagent definition, the documented install
**Consumes:** T2's registered server
**Data dependency:** one live `cursor-agent` check, outside the hermetic suite

## Goal

A Cursor session starts carrying the memory protocol, and a Cursor subagent can reach `am_*`.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/installer.go` | edit | write `rulesFile` from `bootstrap.md` with Cursor's front matter |
| `clients/claude-code/installer_test.go` | edit | the rule lands, carries `alwaysApply: true`, and the definition installs |
| `README.md` | edit | the install matrix says four agents and what Cursor does NOT get |
| `clients/claude-code/README.md` | edit | the same, for the kit's own readers |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCursorInstallWritesTheProtocolRule`, `TestCursorRuleIsAlwaysApplied`, `TestReadmeNamesEveryInstallableAgent`. Commit them red.
2. Write `<config>/rules/agentsmemory.mdc`: the `bootstrap.md` body under `---` front matter carrying `description:` and `alwaysApply: true`. A whole file, not a merged block — the rule is entirely ours, so there is nothing of the user's to preserve.
3. The subagent definition needs no new code: T1's kit already declares `agentsDir` and the `.md` dialect, and `writeAgentDefinitions` is kit-driven. Assert it rather than write it.
4. Document it. `TestReadmeNamesEveryInstallableAgent` reads the agent names out of `resolveAgentKits` and fails when a README omits one, in the same shape as the hook-event gate.
5. Say what Cursor does NOT get, in the README rather than only in the ADR: no Stop checkpoint and no subagent hooks, so a Cursor user recalls memory and is never prompted to write it.
6. Falsify: drop the front matter; drop the rule write; add a fifth agent name and watch the README gate fail.
7. Run the acceptance command, then the live check below.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  if [ -n "$(gofmt -l clients)" ]; then echo "gofmt"; exit 1; fi
  apk add --no-cache bash >/dev/null
  go vet ./...
  go test ./clients/... -run "TestCursorInstallWritesTheProtocolRule|TestCursorRuleIsAlwaysApplied|TestReadmeNamesEveryInstallableAgent" -count=1 -v 2>&1 | tee /tmp/a20t3.out
  grep -q -- "--- PASS: TestCursorInstallWritesTheProtocolRule" /tmp/a20t3.out
  grep -q -- "--- PASS: TestCursorRuleIsAlwaysApplied" /tmp/a20t3.out
  grep -q -- "--- PASS: TestReadmeNamesEveryInstallableAgent" /tmp/a20t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a20t3.out
  go test ./... -count=1'
```

**Live check, human-observed and recorded separately:** after a real
`aiagentmemory install --agent cursor`, `cursor-agent mcp list-tools agentsmemory`
lists the `am_*` tools. This is rung 4 and no unit test can reach it — the suite
proves the files are written, and only Cursor proves it loads them.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCursorInstallWritesTheProtocolRule` | `clients/claude-code/installer_test.go` | the rule file lands at the kit's `rulesFile` and carries the protocol body, not an empty stub | — |
| `TestCursorRuleIsAlwaysApplied` | `clients/claude-code/installer_test.go` | the front matter carries `alwaysApply: true` — without it Cursor loads the rule only on demand, which is the difference between a protocol and a document | — |
| `TestReadmeNamesEveryInstallableAgent` | `clients/claude-code/installer_test.go` | both READMEs name every agent `resolveAgentKits` accepts; a kit nobody can discover is one nobody installs | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the rule file is written |
| 2 — something selects it | `alwaysApply: true`, asserted — Cursor selects it every session |
| 3 — the caller can discover it | `TestReadmeNamesEveryInstallableAgent` — `--agent cursor` is documented where someone deciding to install it will look |
| 4 — it is used | the live `cursor-agent mcp list-tools` check, recorded in the Verification Log as human-observed |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| write the rule without front matter | yes | `TestCursorRuleIsAlwaysApplied` |
| set `alwaysApply: false` | yes | `TestCursorRuleIsAlwaysApplied` |
| skip the rule write entirely | yes | `TestCursorInstallWritesTheProtocolRule` |
| remove `cursor` from a README | n/a (markdown) | `TestReadmeNamesEveryInstallableAgent` |

## Out of Scope

- Measuring whether a Cursor session actually recalls, the way ADR-017 T1 measured Claude subagents (deferred: docs/adr/BACKLOG.md — it needs `search_events` attribution per client, and ADR-018 T2's withdrawal means there is none)
- Cursor hooks (deferred: docs/adr/BACKLOG.md — see the ADR's Out of Scope)

## Invariants

- The rule is written as a whole file owned by us; no user content is merged or lost.
- Every agent `--agent` accepts is named in both READMEs.

## Risks

- The protocol is delivered and ignored, which ADR-017 measured at 0/5 for subagents receiving it. Mitigated only in the sense of being stated: Cursor gets the read half and, without hooks, none of the write half.

## Stop Condition

Stop and ask if `alwaysApply: true` does not make the rule load — the mechanism would then be Cursor's per-project rules rather than the global ones, which is a different install target.

## Mutation Log

- 2026-08-22 · 2469a25* · mutant killed · exit 1 · `clients/claude-code/installer.go` · without alwaysApply the rule loads on demand, and on demand for an always-on operating protocol means never — the protocol ships and changes nothing

## Verification Log

- 2026-08-22 · 2469a25* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
