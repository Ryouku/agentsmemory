# ADR-001 Tasks

Implementation tasks for ADR-001: Recall answers or abstains. See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Wave | Tasks | Depends-on |
|------|-------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T3 |
| 5 | T5 | T4 |
| 6 | T6 | T5 |

The chain is strictly sequential and that is the point of the ordering. **T1, T2 and T3 are the
falsification half of this ADR and they run first**: honest negatives, then the curve and its gate,
then the gate run itself. Nothing that ships — no config key, no verdict, no wire field, no
migration — starts until T3's Verification Log holds a `ship` sign-off. An earlier draft of this
plan put the calibration command last, which meant four tasks could land before the ADR learned its
premise had failed.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Generate hard negatives, verify absence at retrieval depth, label the three populations | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestPopulation\|TestAbsentPrompt\|TestVerifyAbsent\|TestEvaluate"` |
| T2 | Build the risk–coverage curve, the calibration file, and the go/no-go gate | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestRiskCoverage\|TestGate\|TestCalibrat"` |
| T3 | Run the gate on the real corpus and decide whether the rest is built | pending | — | human-observed: `adr-verify --human "…exit <0\|1>… decision <ship\|withdraw>"` |
| T4 | Load the calibration file and validate its fingerprint at startup | pending | — | `go test ./cmd/server/ ./internal/palace/ ./internal/config/ -run "TestAbstain"` |
| T5 | Derive the confidence verdict inside Search | pending | — | `go test ./internal/palace/ -run "TestConfidence"` |
| T6 | Return the verdict over MCP and record what it was derived from | pending | — | `go test ./internal/mcpserver/ ./internal/palace/ -run "TestSearchResultCarriesConfidence\|TestSearchEventRecordsVerdict\|TestRecallStats"` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

The Acceptance column is abbreviated for reading; the task file carries the full command including
the Docker invocation this repo builds under, and `adr-verify` runs that one.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | population labels + per-case top-1 score/presence + absent-verification provenance | T2 | T1 before T2 |
| T2 | `eval --calibrate --gate` | T3 | T2 before T3 |
| T2 | `palace.Calibration` (thresholds + fingerprint + id) | T4, T6 | T2 before T4 |
| T3 | the `ship` / `withdraw` decision | T4 | T3 before T4 |
| T4 | `palace.Service.WithCalibration` + the confirmed canary | T5 | T4 before T5 |
| T5 | `palace.Confidence` populated by `Search` | T6 | T5 before T6 |

## Notes

- The whole ADR is falsifiable at T3, and the criterion is declared in the ADR before the run: at
  the threshold holding answer-recall ≥ 0.95 on reachable-answerable cases, the 90% Wilson lower
  bound on the correct-refusal rate over verified-absent cases must be ≥ 0.30. `--gate`'s exit code
  is the decision; a failing gate withdraws the ADR rather than lowering the target.
- No task changes ranking. Every eval arm must score identically before and after this ADR; a moved
  MRR means something was wired that should not have been.
- T6 touches persistent state. Its migration is nullable and additive, and the ADR's Rollback
  section applies from the moment it lands.
- T1 changes what a case file means, so case files generated before it are not comparable with ones
  generated after. `--style absent-easy` exists to reproduce the old regime deliberately, not to
  keep old files silently valid.
