# Task ADR-016-T1: Measure what the extractor would produce, before wiring it

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the measurement that accepts or withdraws T2
**Consumes:** none
**Data dependency:** hermetic for the test; the report is run against a real palace by an operator

## Goal

Know, before changing the write path, whether a frequency-based extractor produces a usable graph on agent-written memories.

The pre-registered risk is explicit: mining feeds `extractEntities` long repetitive transcripts, and agents file short deliberate notes. A term must appear at least twice to be an entity, and a hallway needs two entities in one drawer. If that rule is wrong for this corpus, T2 ships a write-path cost that buys nothing and the graph stays empty for a subtler reason than it is empty now.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/doctor.go` | edit | a `--graph` report: what the derived graph WOULD hold if every drawer were extracted now |
| `cmd/server/doctor_graph_test.go` | add | the report counts what it claims to count, on a fixture whose answer is known by hand |

## Ordered Steps

1. Write the failing test first (TDD red): `TestGraphReportCountsWhatItClaims` over a fixture of five drawers whose entity counts are worked out by hand. Commit it red.
2. Report, per wing: drawers, drawers that would carry >= 1 entity, drawers that would carry >= 2, the hallways that would be derived at the current threshold, and the ten most frequent candidate entities so noise is visible rather than inferred.
3. Report the extraction cost: total wall time to extract every drawer, and the per-drawer mean.
4. Print the decision line in the report itself — the share with >= 2 entities against ADR-016's 20% bar — so the number and the criterion are read together and neither can be quoted alone.
5. Run it against the live palace and paste the output into the ADR's Context before T2 is written.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestGraphReportCountsWhatItClaims|TestGraphReportStatesTheBarBesideTheNumber" -count=1 -v 2>&1 | tee /tmp/a16t1.out
  grep -q -- "--- PASS: TestGraphReportCountsWhatItClaims" /tmp/a16t1.out
  grep -q -- "--- PASS: TestGraphReportStatesTheBarBesideTheNumber" /tmp/a16t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a16t1.out
  go test ./cmd/server/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestGraphReportCountsWhatItClaims` | `cmd/server/doctor_graph_test.go` | the counts match a fixture whose answer is known by hand | — |
| `TestGraphReportStatesTheBarBesideTheNumber` | `cmd/server/doctor_graph_test.go` | the 20% criterion is printed with the measurement, so the number cannot be quoted without it | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestGraphReportCountsWhatItClaims` |
| 2 — something selects it | it is a flag on a registered subcommand, covered by ADR-015 T1's `TestDoctorIsRegistered` |
| 3 — the caller can discover it | `doctor --help` names it |
| 4 — it is used | T2 may not begin until this has been run against a real palace and its output pasted into the ADR |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| count drawers with >= 1 entity where >= 2 is claimed | yes | `TestGraphReportCountsWhatItClaims` |
| print the share without the bar | yes | `TestGraphReportStatesTheBarBesideTheNumber` |

## Out of Scope

- Changing the extractor (deferred: T2 of this ADR, and only if this measurement supports it)
- Backfilling entities (deferred: docs/adr/BACKLOG.md)

## Invariants

- The report writes nothing. It is a measurement of what a change would do, taken before the change.
- The bar and the number are printed together.

## Risks

- A measurement that is run once and quoted forever. Mitigated: it is a subcommand, so it can be re-run, and it prints the corpus it measured.

## Stop Condition

Stop and report if the share is between 15% and 25% — that is close enough to the bar that the bar itself needs a human decision rather than an automatic one.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
