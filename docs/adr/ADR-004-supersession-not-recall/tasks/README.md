# ADR-004 Tasks

Implementation tasks for ADR-004: Justify the knowledge graph on supersession, not on recall. See
the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T1 |
| 4 | T4 | none |
| 5 | T5 | T2, T3, T4 |

T4 depends on nothing and may run at any point before T5; it is listed fourth only because it is
the one task that can be dropped without breaking the others — and dropping it would leave the
gate unable to ask whether a date preference already closes the gap.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Score where the superseded version landed | done | — | `go test ./internal/palace/ -run "TestSupersessionRanks\|TestStaleAboveRate"` |
| T2 | Harden and grow the temporal pairs | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestOlderNeighbor\|TestPairVerified"` |
| T3 | Keep supersession out of the headline and give it its own table | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestHeadlineExcludesTemporal\|TestSupersessionTable"` |
| T4 | Add the recency arm — the cheap fix the graph must beat | pending | — | `go test ./internal/palace/ -run "TestRecencyArm"` |
| T5 | Turn the measurement into a pre-registered verdict | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestSupersessionGate"` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

The Acceptance column is abbreviated for reading; the task file carries the full command including
the Docker invocation this repo builds under, and `adr-verify` runs that one.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `EvalCase.Distractor` | T2 | T1 before T2 |
| T1 | `palace.SupersessionMetrics` | T3, T5 | T1 before T3 and T5 |
| T1 | `EvalCaseResult.DistractorPoolRank` | T5 | T1 before T5 — vacuity is a case-level fact the gate's floor counts against |
| T2 | `verified-pair meta` | T5 | T2 before T5 — including `readCases` returning it, which is what lets the gate refuse an unhardened file |
| T4 | `palace.ArmRecency` | T5 | T4 before T5 |

## Notes

- The `-run` regexes are deliberately disjoint across the five tasks: two tasks whose acceptance
  can pass on each other's tests are two tasks with one gate between them. The tests added after
  review keep their tasks' prefixes for the same reason — no acceptance command changed.
- T1 must land the per-arm SCOPE (pool / page / own-index) before T3 prints or T5 gates. A rank of
  0 does not mean the same thing for `ArmProduction` (off the ≤5-result page) or `ArmContextual`
  (outside its own index) as it does for the arms that re-order the shared pool, and every rate in
  this ADR depends on not conflating them.
- T5 resolves its arm by name, never by score. If a change makes the gate scan the table for a
  minimum, that change is the winner's curse coming back.
- No task changes production ranking. `internal/palace/service.go` and `rank.go` appear in no
  Affected Files table, and every non-temporal arm must score identically before and after.
- T5 is allowed to conclude `not justified`. That is the ADR working, not the ADR failing — the
  cost of learning it here is five measurement tasks rather than a populated graph and the wiring
  to read it.
- If T2's yield leaves fewer than 30 verified, non-vacuous pairs, stop at T5's Stop Condition. The
  ADR's declared response is more dated corrections in the corpus; nobody lowers the floor to get a
  verdict out of the command.
