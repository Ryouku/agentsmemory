# Task ADR-005-T1: Refuse a handoff into a wing nobody will resolve to

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `palace.Service.WingIsEmpty`, the `confirm_new_wing` argument, the new-wing-inbox refusal
**Consumes:** none
**Data dependency:** hermetic

## Goal

`am_add_drawer` refuses to file into a wing that holds no drawers when the room is `inbox`, naming the likely mistake and the argument that proceeds anyway.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/repo.go` | edit | `WingIsEmpty(ctx, teamID, wing)` — one COUNT, no scan |
| `internal/palace/service.go` | edit | expose it on the Service the MCP layer holds |
| `internal/mcpserver/drawers.go` | edit | **the selection**: declare `confirm_new_wing` on the tool AND branch on the check before `Add`. A refusal function nothing calls is this repo's signature defect |
| `internal/mcpserver/handoff_test.go` | edit | behavioural tests for the decision + a call-site test that the add path consults it |
| `internal/palace/repo_test.go` | edit | `WingIsEmpty` is true for an unwritten wing and false after one drawer |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestHandoffIntoAnUnresolvableWingIsRefused` and `TestHandoffRefusalCanBeOverridden` in `internal/mcpserver/handoff_test.go`. Commit them red.
2. Add `WingIsEmpty` to the repo as a `COUNT(*) … LIMIT 1`, and a passthrough on `Service`. Test it directly: an unwritten wing is empty, the same wing after one `Add` is not.
3. Extract the decision into a function the test can DRIVE — `handoffRefusal(ctx, drawers, teamID, wing, room string, confirmed bool) string`, returning the refusal or `""`. Do not inline it at the call site: a guard whose only test greps the source passes against that guard disarmed with `&& false`, which this package has already been bitten by once.
4. Declare `confirm_new_wing` on the `add_drawer` tool with a description that says what it is for, and branch on `handoffRefusal` before `drawers.Add`.
5. Write the refusal text so it carries all three things the filer needs: that the wing holds nothing, that a handoff wing is named for the PROJECT and not the direction of travel (so `wing_to-x` is almost always `wing_x`), and the list of wings that do exist. End with `confirm_new_wing: true`.
6. Falsify each half: disarm the room comparison, then the emptiness check, then bypass `handoffRefusal` at the call site. Each mutant must COMPILE and turn a test red. A mutant that does not build has not been tested.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestHandoff" -count=1 -v 2>&1 | tee /tmp/a.out
  grep -q -- "--- PASS: TestHandoffIntoAnUnresolvableWingIsRefused" /tmp/a.out
  grep -q -- "--- PASS: TestHandoffRefusalCanBeOverridden" /tmp/a.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a.out
  go test ./internal/palace/ ./internal/mcpserver/ -count=1'
```

The new tests are named and grepped for individually, so the fence cannot be satisfied by the regression suites that follow it.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHandoffIntoAnUnresolvableWingIsRefused` | `internal/mcpserver/handoff_test.go` | new wing + `room=inbox` refuses, and the text names the argument that proceeds | — |
| `TestHandoffRefusalCanBeOverridden` | `internal/mcpserver/handoff_test.go` | `confirm_new_wing: true` files it; a new wing with any other room files without asking | — |
| `TestAddPathConsultsTheHandoffCheck` | `internal/mcpserver/handoff_test.go` | the add path calls `handoffRefusal` and returns on it — the check can be right and unreached | — |
| `TestWingIsEmptyCountsDrawers` | `internal/palace/repo_test.go` | true before the first write, false after | — |

## Out of Scope

- Guessing the correct wing name and rewriting the argument (permanent: the server cannot know the target project's name; a silent rewrite is a worse failure than a refusal)
- Applying the check to any room other than `inbox` (permanent: a first write to a new wing is the documented normal case and must stay free)
- The same check on `am_diary_write` (deferred: docs/adr/BACKLOG.md — a diary entry is written to your OWN wing, so the mistake has no route in)

## Invariants

- A wing that holds drawers is never refused, whatever the room.
- A new wing with any room other than `inbox` is never refused — first writes create wings, and that is the documented normal case.
- The refusal is a tool error, not a panic or a silent drop; the drawer is not written.

## Risks

- The check runs on every `am_add_drawer`. Mitigated: one indexed COUNT with LIMIT 1, only when the room is `inbox`, so the common write path is untouched.

## Stop Condition

Stop and ask if the emptiness check cannot be made to cost less than the write it guards, or if any existing test asserts that a first write to a new wing with `room=inbox` succeeds — that would mean the convention is used somewhere this ADR did not measure.

## Verification Log

- 2026-08-20 · d38c341* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-20 · d38c341* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
