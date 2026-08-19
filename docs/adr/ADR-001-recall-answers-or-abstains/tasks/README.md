# ADR-001 Tasks

Implementation tasks for ADR-001: Recall answers or abstains. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | none |
| 3 | T3 | T2 |
| 4 | T4 | T3 |
| 5 | T5 | T1 |

T1 and T2 are independent and may run in either order; T1 is listed first because it is the one
that can invalidate the ADR. If hard negatives collapse the measured separation, the gate does not
ship and T2–T5 are wasted work — so it goes first deliberately.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Generate hard negatives and label the three calibration populations | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestPopulation\|TestAbsentPrompt\|TestEvaluate"` |
| T2 | Add the abstention threshold as operator configuration, with backend validation | pending | — | `go test ./cmd/server/ ./internal/palace/ ./internal/config/ -run "TestAbstain"` |
| T3 | Derive the confidence verdict inside Search | pending | — | `go test ./internal/palace/ -run "TestConfidence"` |
| T4 | Return the verdict over MCP and record it in telemetry | pending | — | `go test ./internal/mcpserver/ ./internal/palace/ -run "TestSearchResultCarriesConfidence\|TestSearchEventRecordsVerdict\|TestRecallStats"` |
| T5 | Produce the threshold with `eval --calibrate` | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestRiskCoverage\|TestCalibrate"` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

The Acceptance column is abbreviated for reading; the task file carries the full command including
the Docker invocation this repo builds under, and `adr-verify` runs that one.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T2 | `config.AbstainThreshold` / `WithAbstain` | T3 | T2 before T3 |
| T3 | `palace.Confidence` + populated verdict from `Search` | T4 | T3 before T4 |
| T1 | three-population labels (`reachable`/`unreachable`/`absent`) | T5 | T1 before T5 |

## Notes

- The whole ADR is falsifiable at T1: if identifier-preserving negatives collapse the cross-encoder
  separation, the honest outcome is that this gate cannot ship on this corpus. That is a result
  worth having for the cost of one task, and it is why T1 leads.
- No task changes ranking. Every eval arm must score identically before and after this ADR; a
  moved MRR means something was wired that should not have been.
- T4 touches persistent state. Its migration is nullable and additive, and the ADR's Rollback
  section applies from the moment it lands.
