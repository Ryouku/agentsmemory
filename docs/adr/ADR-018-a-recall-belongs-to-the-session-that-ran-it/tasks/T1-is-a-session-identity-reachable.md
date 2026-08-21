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
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestSearchHandlerCanNameItsSession|TestTwoSessionsGetDifferentIdentities" -count=1 -v 2>&1 | tee /tmp/a18t1.out
  grep -q -- "--- PASS: TestSearchHandlerCanNameItsSession" /tmp/a18t1.out
  grep -q -- "--- PASS: TestTwoSessionsGetDifferentIdentities" /tmp/a18t1.out
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

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
