# Task ADR-019-T3: Say what was left out, in a field that varies

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `content_coverage`, `regions_omitted`, and a re-judged measurement
**Consumes:** T2's multi-window snippets
**Data dependency:** the 32 real queries

## Goal

An agent that still cannot answer from the page can tell WHICH hit is worth fetching.

`content_truncated` is true for 98% of hits. It is correct and it carries no information: a caller cannot fetch five whole memories, and nothing distinguishes the one hiding the answer. A signal that never varies is not a signal — the same defect ADR-007 names for numbers, in a boolean.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/palace.go` | edit | `SearchHit.Coverage` and `RegionsOmitted` |
| `internal/palace/rank.go` | edit | the window selection already knows both; it discards them |
| `internal/mcpserver/drawers.go` | edit | put them on the wire beside the fields they make readable |
| `internal/mcpserver/drawers_test.go` | edit | both reach the agent, and they VARY across a real page |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCoverageReachesTheWire` and `TestCoverageVariesAcrossAPage`. Commit them red.
2. Return coverage — the fraction of the memory the snippet shows — and how many matching regions did not fit.
3. Put both on the wire. Not `omitempty` for coverage: 0 is a real and important value, and this repository has already shipped one field whose absence meant four things.
4. Leave `content_truncated` exactly as it is. It is not wrong, it is uninformative, and removing it breaks readers for no gain.
5. Re-run the 32 real queries and re-judge them with the SAME blind judge and the SAME rubric as the previous round. It is the only comparison this work has that is not confounded by a change of judge.
6. Falsify: report coverage as a constant; drop `regions_omitted`; report coverage of the whole memory rather than of the snippet.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  apk add --no-cache bash >/dev/null
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ ./internal/palace/ -run "TestCoverageReachesTheWire|TestCoverageVariesAcrossAPage" -count=1 -v 2>&1 | tee /tmp/a19t3.out
  grep -q -- "--- PASS: TestCoverageReachesTheWire" /tmp/a19t3.out
  grep -q -- "--- PASS: TestCoverageVariesAcrossAPage" /tmp/a19t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a19t3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCoverageReachesTheWire` | `internal/mcpserver/drawers_test.go` | both fields are in the JSON an agent receives — the domain has carried signals the wire discarded twice already | — |
| `TestCoverageVariesAcrossAPage` | `internal/mcpserver/drawers_test.go` | on a page of mixed hits the values DIFFER — a field that is constant is the defect being replaced, and shipping a second one would be worse than keeping the first | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCoverageReachesTheWire` |
| 2 — something selects it | the search page emits it for every hit |
| 3 — the caller can discover it | it sits beside `content_truncated`, which is what an agent reads today |
| 4 — it is used | step 5 re-judges the 32 and reports whether the number moved |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| report coverage as a constant 1.0 | yes | `TestCoverageVariesAcrossAPage` |
| set the fields in the domain and not on the wire | yes | `TestCoverageReachesTheWire` |
| drop `regions_omitted` | yes | `TestCoverageReachesTheWire` |

## Out of Scope

- Removing `content_truncated` (permanent: it is not wrong and readers depend on it; this task makes it redundant rather than absent)
- Acting on coverage inside the server — abstaining, auto-fetching (deferred: docs/adr/BACKLOG.md — the agent decides; the page's job is to make the decision possible)

## Invariants

- Coverage is of the SNIPPET against the memory, not of the memory against anything.
- The fields vary across a real page, or they are the defect they replace.

## Risks

- A new signal nobody reads, exactly like the one it replaces. Mitigated: step 5 re-judges the same 32 through the same judge, which is the only way to find out rather than assume.

## Stop Condition

Stop and report if coverage is near-constant on the real corpus after T2 — that would mean the multi-window change did not vary what is shown, and the signal has the same defect as the one it replaces.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
