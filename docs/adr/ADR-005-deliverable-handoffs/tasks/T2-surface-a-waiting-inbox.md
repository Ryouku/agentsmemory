# Task ADR-005-T2: Name a waiting inbox in the call every session already makes

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `palace.Service.InboxCount`, the `am_status` `inbox` field and its conditional hint
**Consumes:** none
**Data dependency:** hermetic

## Goal

`am_status` returns a top-level `inbox` object for the session's own wing, and its `hint` says to read it when the count is non-zero.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/repo.go` | edit | `InboxCount(ctx, teamID, wing)` — drawers in room `inbox` |
| `internal/palace/service.go` | edit | expose it on the Service |
| `internal/mcpserver/server.go` | edit | **the selection**: put the field in the marshalled response and swap the hint. A count computed and not marshalled is invisible |
| `internal/mcpserver/status_test.go` | add | the field is present, correct, and the hint changes with it |

## Ordered Steps

1. Write the failing test first (TDD red): `TestStatusNamesAWaitingInbox` in `internal/mcpserver/status_test.go`. Commit it red.
2. Add `InboxCount` to the repo and a passthrough on `Service`.
3. Build the `inbox` object from the session's `default_wing`. When the registration carries no default wing there is no "own wing" to count, so the field states that rather than reporting `0` — zero and unknown are different answers and an agent cannot tell them apart from a bare number.
4. Make the `hint` conditional: with items waiting it names the count and the wing and says to read them before acting; with none it keeps today's text. The hint is the part an agent actually reads, and prose that always says "check your inbox" is prose that is always skipped.
5. Add the field's own documentation to its description in the response: the count is taken at wake-up and does not update mid-session.
6. Falsify: make `InboxCount` return 0 unconditionally, then drop the field from the marshalled map, then make the hint unconditional. Each mutant must compile and turn a test red.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestStatusNamesAWaitingInbox" -count=1 -v 2>&1 | tee /tmp/b.out
  grep -q -- "--- PASS: TestStatusNamesAWaitingInbox" /tmp/b.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/b.out
  go test ./internal/palace/ ./internal/mcpserver/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestStatusNamesAWaitingInbox` | `internal/mcpserver/status_test.go` | the `inbox` field carries the count for the default wing, and the hint names it when non-zero | — |
| `TestStatusInboxWithoutADefaultWing` | `internal/mcpserver/status_test.go` | no registration wing reports unknown, not `0` | — |
| `TestInboxCountCountsOnlyTheInboxRoom` | `internal/palace/repo_test.go` | drawers in other rooms of the same wing are not counted | — |

## Out of Scope

- Notifying a session mid-run that an item arrived (permanent: the transport is request/response; the server cannot wake a session)
- Inbox counts for wings other than the session's own (deferred: docs/adr/BACKLOG.md — every extra count dilutes the one that matters)
- Marking an inbox item read or closed (deferred: docs/adr/BACKLOG.md — needs per-drawer state this ADR does not introduce)

## Invariants

- `am_status` never fails because of the inbox count; a counting error omits the field, as the taxonomy and workspace blocks already do.
- Every existing `am_status` field keeps its name and meaning.

## Risks

- An agent reads the count as live. Mitigated: the wake-up-only limit is stated in the response itself, not only in the ADR.

## Stop Condition

Stop and ask if `default_wing` turns out to be empty for most real registrations — the field would then be unknown almost always and the read side needs a different anchor.

## Verification Log

- 2026-08-20 · c49e0aa* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
