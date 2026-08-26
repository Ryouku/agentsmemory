# Task ADR-036-T8: The protocol becomes an API, and proves it costs less for the same meaning

**Depends-on:** T7, T3, T5
**Covers:** F-13, F-14, F-15, F-16, F-19, UC6-S1, UC6-S2, UC6-S3
**Estimated scope:** L
**Owner:** unassigned
**Produces:** the bootstrap surface
**Consumes:** `Service.EntryPoint` (T7), `Service.factsFor` (T3), `kg.CorrectionsFor` (T5)
**Data dependency:** **Needs real data.** F-16 compares against `testdata/bootstrap-baseline-2026-08-26.json` — a FROZEN client transcript this task commits, carrying the call count, the byte and token totals, the tokenizer name and the model build. The fence is hermetic and runs against that file, never a live client.

## Goal

One call replaces a client-side protocol measured at ~99KB and 13 calls, and proves it costs less WITHOUT returning less.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/bootstrap.go` | add | assemble entry point, eager content, on-demand pointers, swept corrections, resolved wing, truncation report |
| `internal/palace/testdata/bootstrap-baseline-2026-08-26.json` | add | the frozen baseline transcript with its tokenizer and date |
| `internal/mcpserver/bootstrap.go` | add | the tool, its name and its schema |
| `internal/mcpserver/server.go` | edit | register it — the line that SELECTS it |
| `internal/palace/recallanswers_spec_test.go` | edit | five red tests |
| `internal/mcpserver/recallanswers_reach_test.go` | edit | the catalogue proof |

## Ordered Steps

1. Confirm all six tests are RED.
2. Pin the response CONTRACT first — tool name, request and response schema, which fields are mandatory under truncation, and the truncation ORDER. Without it F-16 is winnable by returning less.
3. Assemble the response. Corrections come from T5's `CorrectionsFor` — do not write a second sweep.
4. Bound the response and REPORT what was omitted, INCLUDING how to fetch it. The protocol this replaces lost 74% of a prescribed tier to an unreported cap; a report that says "3 omitted" without saying how to get them repeats it in a politer form.
5. Apply F-19: ONE wing rule governs the fact block, the sibling pointer, EntryPoint's edges and the bootstrap's inline content. Assert it on cross-wing correction and taxonomy fixtures — a correction target id and an outgoing edge are both ways a foreign wing can leak that a subject/predicate/object check does not see.
6. Measure F-16 against the frozen baseline: assert SEMANTIC PARITY first (the response carries the same logical payload the 13 calls did), then compare tokens under the named tokenizer. Parity is what stops a tiny useless response winning.
7. Assert the no-entry-point wing still bootstraps (UC6-S3), with its own step and its own assertion.

## Acceptance

```bash
set -o pipefail
go test ./internal/palace/ ./internal/mcpserver/ -run 'TestOneCallBootstrapsAWing|TestATruncatedBootstrapSaysWhatItDropped|TestCorrectionsAreSweptServerSideAcrossAllThreePredicates|TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces|TestOneWingRuleGovernsEveryNewResponsePath|TestBootstrapToolIsRegisteredAndDiscoverable' -count=1 2>&1 | tee /tmp/acc36t8.out; rc=$?
grep -qE "no tests to run|no test files" /tmp/acc36t8.out && exit 1
[ $rc -eq 0 ] || exit 1
go test ./... -count=1 -skip 'TestFactLookupMatchesBothEntityVocabularies|TestAnEndedFactIsNeverPresentedAsCurrent' 2>&1 | tee /tmp/acc36t8b.out; rc=$?
[ $rc -eq 0 ] || exit 1
```

`set -o pipefail` and the explicit `$rc` checks are the gate; the output grep only catches the
empty-filter case. Parsing output alone passes a test binary that fails without printing a matched
`FAIL` line. The new tests run ALONE first so the green suite cannot carry the verdict, and the
run ends repo-wide because a task-scoped fence passes while a repo-wide gate fails.

The `-skip` list is what makes the repo-wide command SATISFIABLE. All 26 ADR-036 stubs are committed
failing, so an unskipped `go test ./...` stays red until the last task lands and no earlier task
could record an exit-0 run — a fence that cannot pass blocks its wave as surely as one that cannot
fail. It skips exactly the stubs owned by tasks T8 does not depend on: T8's own 6 and its
ancestors' 18 still run, so a regression in what T8 was built on is still caught.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestOneCallBootstrapsAWing` | `internal/palace/recallanswers_spec_test.go` | one call returns all six parts; no second call, no hardcoded id; a wing with no entry point still bootstraps | F-13, UC6-S1, UC6-S3 |
| `TestATruncatedBootstrapSaysWhatItDropped` | `internal/palace/recallanswers_spec_test.go` | a bounded response reports its omissions AND how to fetch them | F-14, UC6-S2 |
| `TestCorrectionsAreSweptServerSideAcrossAllThreePredicates` | `internal/palace/recallanswers_spec_test.go` | table-driven over retracts, supersedes and qualifies, read incoming, via T5's single resolver | F-15 |
| `TestTheBootstrapCostsFewerTokensThanTheProtocolItReplaces` | `internal/palace/recallanswers_spec_test.go` | semantic parity with the frozen baseline first, then fewer tokens under the named tokenizer | F-16 |
| `TestOneWingRuleGovernsEveryNewResponsePath` | `internal/palace/recallanswers_spec_test.go` | cross-wing correction targets, taxonomy edges and inline content are all governed by one rule | F-19 |
| `TestBootstrapToolIsRegisteredAndDiscoverable` | `internal/mcpserver/recallanswers_reach_test.go` | the tool is in the catalogue with its arguments | F-13 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the five palace tests |
| 2 — something selects it | the registration in `server.go`; mutation: unregister and the catalogue test goes red while the palace tests stay green |
| 3 — the caller can discover it | the catalogue test — a bootstrap nobody can find is the protocol it replaced |
| 4 — it is used | whether any client kit drops its hardcoded root id and its traversal instructions |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- The response is always bounded, always states its omissions, and always says how to fetch them.
- Eager content is inline; on-demand is a pointer. Inlining everything reproduces the problem this removes.
- ONE wing rule, one correction sweep. Both are consumed, not reimplemented.

## Risks

- A full bootstrap encodes a WORKFLOW. If the tier split or the sweep is wrong it is expensive to walk back once clients depend on it — F-16 and F-14 make that observable before adoption.
- F-16 is falsifiable by construction: parity first, then tokens. Without parity the gate rewards omission, which is the failure it exists to prevent.

## Out of Scope

- Defining `must.*`/`ref.*` as server vocabulary (permanent: the server distinguishes eager from on-demand; the names are a team convention.)
- Updating the client kits to use it (deferred: docs/adr/BACKLOG.md)

## Stop Condition

Stop and ask if the bootstrap cannot beat the frozen baseline at parity after two attempts — that falsifies F-16, and the decision should be revisited rather than the gate loosened.
