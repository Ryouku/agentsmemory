# Task ADR-008-T4: Two parties see what they should, three parties prove isolation

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the two-party and three-party scenarios
**Consumes:** `mcptest.Harness`'s two-registration constructor (T1)
**Data dependency:** hermetic

## Goal

What one registration writes, another finds when it should and does not when it should not; and a handoff reaches its target without touching a third party.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/multiparty_test.go` | add | the two- and three-party scenarios |
| `internal/mcpserver/scenarios_test.go` | edit | register them so T2's gate counts them |

## Ordered Steps

1. Write the failing tests first (TDD red): `ScenarioSecondPartySeesASharedWing`, `ScenarioSecondPartyDoesNotSeeAnotherWing`, `ScenarioHandoffReachesBAndNotC`. Commit them red.
2. Two parties, one workspace, different default wings. Prove BOTH directions: a shared wing is visible to both, and a private wing is not visible to the other. Only the pair proves scoping — a test that only shows visibility passes with scoping removed entirely.
3. Prove the `wing: "*"` route explicitly: a cross-wing question must reach the other party's memory when asked for, since that is the mechanism the team relies on for cross-project recall and nothing currently tests it.
4. Three parties: A files into B's inbox, B reads it, C's inbox is untouched and C's search does not surface it. This week's handoff defect was invisible because nobody looked from B's side; the third party is what turns "delivered" into "delivered to the right place".
5. Assert the `confirm_new_wing` refusal from the other side too — a handoff refused for A must not have partially landed for B.
6. Falsify: remove the wing scoping from search; deliver the handoff to every wing; have C's inbox count include B's items.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "Scenario(SecondParty|Handoff)" -count=1 -v 2>&1 | tee /tmp/e4.out
  grep -q -- "ScenarioSecondPartySeesASharedWing" /tmp/e4.out
  grep -q -- "ScenarioSecondPartyDoesNotSeeAnotherWing" /tmp/e4.out
  grep -q -- "ScenarioHandoffReachesBAndNotC" /tmp/e4.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/e4.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `ScenarioSecondPartySeesASharedWing` | `internal/mcpserver/multiparty_test.go` | a shared wing is readable by both registrations | — |
| `ScenarioSecondPartyDoesNotSeeAnotherWing` | `internal/mcpserver/multiparty_test.go` | scoping holds; the pair is what proves it | — |
| `ScenarioCrossWingRecallReachesTheOtherParty` | `internal/mcpserver/multiparty_test.go` | `wing: "*"` reaches another wing when explicitly asked | — |
| `ScenarioHandoffReachesBAndNotC` | `internal/mcpserver/multiparty_test.go` | delivery and isolation observed from both the target's and a bystander's side | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop wing scoping from search | yes | `ScenarioSecondPartyDoesNotSeeAnotherWing` |
| make `wing: "*"` scope to the caller's wing | yes | `ScenarioCrossWingRecallReachesTheOtherParty` |
| file the handoff into every wing | yes | `ScenarioHandoffReachesBAndNotC` |
| count another wing's inbox in `am_status` | yes | `ScenarioHandoffReachesBAndNotC` |

## Out of Scope

- Cross-WORKSPACE isolation (deferred: docs/adr/BACKLOG.md — tenancy is a separate trust boundary and deserves its own scenarios, not a corner of this task)
- Concurrent mutation by two parties at once (deferred: docs/adr/BACKLOG.md — three parties here act in sequence; concurrency is the continuity spec's subject)

## Invariants

- Every visibility claim is proven in both directions.
- No scenario concludes delivery from the sender's side alone.

## Risks

- Two registrations in one process share more state than two real sessions would. Mitigated: they differ only by the wing header, which is exactly how the real deployment differs.

## Stop Condition

Stop and ask if two registrations cannot be created against one in-process server — the two-party half is the point of the task, and faking it with one client and a swapped header would test the header, not the scoping.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
