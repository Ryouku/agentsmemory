# Task ADR-036-T8: The protocol becomes an API

**Depends-on:** T7, T3
**Covers:** F-13, F-14, F-15, F-16, UC6-S1, UC6-S2, UC6-S3
**Estimated scope:** L
**Owner:** unassigned
**Produces:** the bootstrap surface
**Consumes:** `Service.EntryPoint` (T7), `Service.factsFor` (T3)
**Data dependency:** needs a real client transcript to measure F-16 against

## Goal

One call replaces a client-side protocol measured at ~99KB and 13 calls, and proves it costs less.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/bootstrap.go` | add | assemble entry point, eager content, on-demand pointers, swept corrections, resolved wing, truncation report |
| `internal/mcpserver/bootstrap.go` | add | the tool |
| `internal/mcpserver/server.go` | edit | register it — the line that SELECTS it |
| `internal/palace/recallanswers_spec_test.go` | edit | four red tests |

## Ordered Steps

1. Confirm the four tests are RED.
2. Assemble the response. Corrections are swept SERVER-side across all three predicates, read INCOMING — outgoing traversal structurally cannot see a correction.
3. Bound the response and REPORT what was omitted. The protocol this replaces records a prescribed tier losing 74% of itself to an unreported cap.
4. Measure output tokens against the client baseline. **Needs real data** — a client transcript — while the fence is hermetic, so record the measured comparison in the sign-off.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces' -count=1 2>&1 | tee /tmp/acc36t8.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc36t8.out && go test ./... -count=1 2>&1 | tee /tmp/acc36t8b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc36t8b.out
```

The new tests run ALONE first, so the already-green suite in the second command cannot carry the
verdict by itself. The fence ends with the whole repo because a task-scoped fence passes while a
repo-wide gate fails — measured on this corpus 2026-08-25.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOneCallBootstrapsAWing` | `internal/palace/recallanswers_spec_test.go` | one call returns all six parts; no second call, no hardcoded id | F-13 |
| `TestATruncatedBootstrapSaysWhatItDropped` | `internal/palace/recallanswers_spec_test.go` | a bounded response reports its omissions | F-14 |
| `TestCorrectionsAreSweptServerSideAcrossAllThreePredicates` | `internal/palace/recallanswers_spec_test.go` | retracts, supersedes and qualifies, read incoming | F-15 |
| `TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces` | `internal/palace/recallanswers_spec_test.go` | beats the measured client baseline | F-16 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the four tests |
| 2 — something selects it | the registration in `server.go`; mutation: unregister and the tool vanishes |
| 3 — the caller can discover it | the tool and its arguments appear in the catalogue — a bootstrap nobody can find is the protocol it replaced |
| 4 — it is used | whether any client kit drops its hardcoded root id and its traversal instructions |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- The response is always bounded and always states its omissions.
- Eager content is inline; on-demand is a pointer. Inlining everything reproduces the problem this removes.

## Risks

- A full bootstrap encodes a WORKFLOW. If the tier split or the sweep is wrong it is expensive to walk back once clients depend on it — F-16 and F-14 are what make that observable before adoption.

## Out of Scope

- Defining `must.*`/`ref.*` as server vocabulary (permanent: the server distinguishes eager from on-demand; the names are a team convention.)
- Updating the client kits to use it (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if the bootstrap cannot beat the client baseline after two attempts — that falsifies F-16 and the decision should be revisited rather than the gate loosened.
