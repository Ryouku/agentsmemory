# Task ADR-019-T3: Put the choice on the wire, and re-judge

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `regions`, `identity`, `content_coverage` on the search page, and a re-judged measurement
**Consumes:** T2's regions and identity
**Data dependency:** the 32 real queries

## Goal

An agent reading a page can see which part of which memory to expand, and we know whether that changed anything.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | `regions`, `identity`, `content_coverage` on `searchHitView` |
| `internal/mcpserver/drawers_test.go` | edit | they reach the agent, and they VARY across a real page |
| `docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md` | edit | the re-judged result, whatever it says |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestRegionsReachTheWire`, `TestCoverageVariesAcrossAPage`, `TestContentKeepsItsMeaningOnTheWire`. Commit them red.
2. Add the fields beside `content_truncated`, where an agent already looks.
3. `content_coverage` is NOT `omitempty`: 0 is a real and important value, and this repository has already shipped one field whose absence meant four different things.
4. Leave `content` and `content_truncated` exactly as they are. Redundant is fine; changed is not.
5. Re-run the 32 real queries and re-judge with the SAME blind judge and the SAME rubric as the previous round. It is the only comparison this work has that is not confounded by a change of judge.
6. Write the result into the ADR whichever way it lands — including "no change", which is a finding about the signal and not a failure of the measurement.
7. Falsify: set the fields in the view and never populate them; report coverage as a constant; make `content` the joined regions.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  apk add --no-cache bash >/dev/null
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/mcpserver/ -run "TestRegionsReachTheWire|TestCoverageVariesAcrossAPage|TestContentKeepsItsMeaningOnTheWire" -count=1 -v 2>&1 | tee /tmp/a19t3.out
  grep -q -- "--- PASS: TestRegionsReachTheWire" /tmp/a19t3.out
  grep -q -- "--- PASS: TestCoverageVariesAcrossAPage" /tmp/a19t3.out
  grep -q -- "--- PASS: TestContentKeepsItsMeaningOnTheWire" /tmp/a19t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a19t3.out
  grep -qE "re-judged 2026|re-judged: " docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRegionsReachTheWire` | `internal/mcpserver/drawers_test.go` | regions and identity are in the JSON an agent receives — the domain has carried signals the wire discarded twice already | — |
| `TestCoverageVariesAcrossAPage` | `internal/mcpserver/drawers_test.go` | on a page of mixed hits the values DIFFER — a constant field is the defect being replaced, and shipping a second one would be worse than keeping the first | — |
| `TestContentKeepsItsMeaningOnTheWire` | `internal/mcpserver/drawers_test.go` | `content` is still the single best window — every existing reader is unaffected | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestRegionsReachTheWire` |
| 2 — something selects it | the search page emits them for every hit |
| 3 — the caller can discover it | they sit beside `content_truncated`, which is what an agent reads today |
| 4 — it is used | step 5 re-judges the 32 and reports whether the number moved |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| declare the fields and never populate them | yes | `TestRegionsReachTheWire` |
| report coverage as a constant 1.0 | yes | `TestCoverageVariesAcrossAPage` |
| set `content` to the joined regions | yes | `TestContentKeepsItsMeaningOnTheWire` |
| mark `content_coverage` omitempty so 0 disappears | yes | `TestCoverageVariesAcrossAPage` |

## Out of Scope

- Removing `content_truncated` (permanent: it is not wrong and readers depend on it; this makes it redundant rather than absent)
- Acting on coverage inside the server (deferred: docs/adr/BACKLOG.md)

## Invariants

- `content` and `content_truncated` keep their exact current meaning.
- The new fields vary across a real page, or they are the defect they replace.

## Risks

- The fields are delivered and no agent reads them, exactly like the one they replace. Mitigated: step 5 measures it against the same judge rather than assuming, and the ADR records "no change" as a real answer.

## Stop Condition

Stop and report if the re-judged score is unchanged. That is not a failed task — it is evidence that the page was never the binding constraint, and it should redirect the work rather than be smoothed over.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
