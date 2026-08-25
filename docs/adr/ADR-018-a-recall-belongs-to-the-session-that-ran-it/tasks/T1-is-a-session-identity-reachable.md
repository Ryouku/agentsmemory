# Task ADR-018-T1: Establish whether a session identity reaches the recall

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the finding that shapes or withdraws T2
**Consumes:** none
**Data dependency:** hermetic

## Goal

Know, before a migration is written, whether anything at the point `recordSearch` runs can say which session asked.

The whole ADR rests on it. If no identity is reachable and none can be supplied, T2 is unbuildable as designed and T3 ships alone — which the ADR states is an acceptable outcome, not a failure.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/session_test.go` | add | assert what the handler can actually see about its caller |
| `docs/adr/ADR-018-a-recall-belongs-to-the-session-that-ran-it.md` | edit | the finding is pasted into Context before T2 is written |

## Ordered Steps

1. Write the failing test first (TDD red): `TestSearchHandlerCanNameItsSession` — drive a search through the real MCP handler and assert a non-empty session identity is available to it. Commit it red; red is the current state and the point of the task.
2. Find out what the transport offers. An MCP session has an identity at the protocol level; establish whether it reaches the tool handler, and name the exact mechanism rather than "it seems to".
3. If it does not, establish whether the CLIENT can supply one — an argument, a header, an initialize parameter — and what an old client that supplies nothing then produces.
4. Write the finding into the ADR as prose plus the one line of code that proves it, either way.
5. Falsify: if you conclude an identity IS available, show it differs between two concurrent sessions. An identity that is the same for everybody is worse than none, because it looks like attribution.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  # NOT `gofmt -l | grep -q . && exit 1`: when gofmt is CLEAN grep exits 1, the &&
  # list exits 1, and `set -e` aborts the whole script — a green tree failing the
  # gate for being green. Third occurrence of this shape in the corpus.
  if [ -n "$(gofmt -l cmd internal)" ]; then echo "gofmt"; exit 1; fi
  go vet ./...
  go test ./internal/mcpserver/ -run "TestSearchHandlerCanNameItsSession|TestTwoSessionsGetDifferentIdentities|TestProductionStillRunsStateless" -count=1 -v 2>&1 | tee /tmp/a18t1.out
  grep -q -- "--- PASS: TestSearchHandlerCanNameItsSession" /tmp/a18t1.out
  grep -q -- "--- PASS: TestTwoSessionsGetDifferentIdentities" /tmp/a18t1.out
  grep -q -- "--- PASS: TestProductionStillRunsStateless" /tmp/a18t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a18t1.out
  grep -qE "session identity: (available|unavailable)" docs/adr/ADR-018-a-recall-belongs-to-the-session-that-ran-it.md
  go test ./internal/mcpserver/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSearchHandlerCanNameItsSession` | `internal/mcpserver/session_test.go` | a search through the real handler can name its caller's session | — |
| `TestTwoSessionsGetDifferentIdentities` | `internal/mcpserver/session_test.go` | two concurrent sessions differ — one identity shared by everybody looks like attribution and is not | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSearchHandlerCanNameItsSession` |
| 2 — something selects it | the real handler is driven, not a helper that reads the same field |
| 3 — the caller can discover it | n/a — this task produces a finding, not a surface |
| 4 — it is used | T2 cannot begin without it |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| return a constant identity for every session | yes | `TestTwoSessionsGetDifferentIdentities` |
| read the identity from a field the handler does not receive | no — and that is the finding, recorded rather than worked around | — |

## Out of Scope

- The migration and the endpoint filter (deferred: T2 of this ADR)
- Making the hook honest, which needs none of this (deferred: T3 of this ADR — deliberately independent)

## Invariants

- The finding is written down either way. "Unavailable" is a result, and the ADR already says what happens then.

## Risks

- Concluding an identity exists because a field is present, without checking it differs between sessions. Mitigated by the second test, which is the whole reason it exists.

## Stop Condition

Stop and report if the identity exists but is stable across a client RESTART — that would attribute a week of one machine's work to one "session" and needs a different key.

## Mutation Log

- 2026-08-22 · d66e364* · mutant killed · exit 1 · `cmd/server/main.go` · a stateful transport MINTS session ids, making attribution possible and falsifying the finding — the premise test must notice
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/mcpserver/server.go` · this task is a DIAGNOSTIC: it establishes whether a session identity is reachable at all, and the answer was no because production runs the transport stateless. TestProductionStillRunsStateless is the tripwire for that premise, so flipping the flag must fail it — otherwise ADR-018 T2 stayed withdrawn on an assumption nothing checks · acceptance-sha256:8604af526cf8e0f446227e56c66b97fa96983456412498e2d2be5881248b0421

## Verification Log

- 2026-08-22 · d66e364* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
  ```
  === RUN   TestSearchHandlerCanNameItsSession
  --- PASS: TestSearchHandlerCanNameItsSession (0.01s)
  === RUN   TestTwoSessionsGetDifferentIdentities
  === RUN   TestTwoSessionsGetDifferentIdentities/two_default_clients_are_indistinguishable
  === RUN   TestTwoSessionsGetDifferentIdentities/a_client_that_supplies_its_own_id_is_distinguishable
  --- PASS: TestTwoSessionsGetDifferentIdentities (0.00s)
      --- PASS: TestTwoSessionsGetDifferentIdentities/two_default_clients_are_indistinguishable (0.00s)
      --- PASS: TestTwoSessionsGetDifferentIdentities/a_client_that_supplies_its_own_id_is_distinguishable (0.00s)
  PASS
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	0.027s
  ```
- 2026-08-22 · d66e364* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-22 · d66e364* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:8604af526cf8e0f446227e56c66b97fa96983456412498e2d2be5881248b0421
