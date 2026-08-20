# ADR-003 Tasks

Implementation tasks for ADR-003: Retire the closet curation prior from default ranking. See the
parent ADR for the decision, and its Decision section for the truth table T3's records are read
against.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins and the
README must be regenerated.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |
| 4 | T4 | T3 |
| 5 | T5 | T4 |

Strictly sequential, and each step gates the next for a different reason. T1 makes each arm mean
what its name says; without it the measurement in T3 would be taken through an instrument that
mixes the closet prior into twelve arms that never name it and blinds the one that does. T2 makes the run reproducible and its
deciding statistic preselected, so the table cannot be read after the fact. T3 is the gate: it can
end the ADR. T4 is the only task that changes behaviour. T5 makes the prose match it, and must ship
in the same release as T4 — a default that moves while the README still describes the old one is
the failure this ADR spends a task preventing.

## Task Index

| ID | Title | Status | Covers | Acceptance |
|----|-------|--------|--------|------------|
| T1 | Make closet use an explicit arm dimension | done | — | `go test ./internal/palace/ -run "TestArmBoostsDimension\|TestClosetArmMeasuresClosets…"` |
| T2 | Print, persist and de-bias the evidence the flip is gated on | pending | — | `go test ./internal/palace/ ./cmd/server/ -run "TestClosetDelta…\|TestCandidateUnionPoolsTheClosetHead\|TestRunRecord…"` |
| T3 | Take the four runs the truth table is read from | pending | — | `go test ./cmd/server/ -run "TestClosetEvidenceIsComplete"` |
| T4 | Flip the closet prior's default to off, end to end | pending | — | `go test ./cmd/server/ ./internal/config/ ./internal/palace/ -run "TestClosetPrior…\|TestClosetBoostReachesTheService…\|TestClosetFlipIsBackedByEvidence"` |
| T5 | Make the documentation describe the ranking that ships | pending | — | `go test ./cmd/server/ ./internal/web/views/ -run "TestClosetDocs…\|TestLandingConcepts…"` |

Status: `pending` | `running` | `blocked` | `done` | `failed`.

The Acceptance column is abbreviated for reading; the task file carries the full command including
the Docker invocation this repo builds under, and `adr-verify` runs that one.

## Contract Coupling

| Producer | Contract | Consumer(s) | Ordering note |
|----------|----------|-------------|---------------|
| T1 | `closetBoostsAt` + `armBoosts` — closet boosts at a named scale, and the arm → boosts classification | T2 | T1 before T2 |
| T1 | `ArmHybridRerank` — the closet-off reranked arm | T2 | T1 before T2 |
| T2 | `palace.ClosetDelta`, `EvalCaseResult.PoolRank`, the closet-aware `CandidateUnion` and the `cells.json` run record | T3 | T2 before T3 |
| T3 | the four `cells.json` records under `evidence/` | T4 | T3 before T4 |
| T4 | `palace.DefaultClosetBoost` / `config.Default().ClosetBoost` = 0, `Service.ClosetBoostScale()` | T5 | T4 before T5 |

## Notes

- **T3 can end this ADR.** Its records are read against the ADR's Table 2, and two of the four
  outcome rows end the ADR with the table attached: the default stays at 1 and the ADR is
  withdrawn. That is a result worth three tasks, and it is why the measurement runs before the
  change rather than after it.
- **T1 and T2 fix defects that exist today**, independent of the flip: twelve arm names carry a curation
  prior their names do not mention, an operator running `CLOSET_BOOST=0` reads a `hybrid+closet`
  column that measures no closets, the judged real-query pool contains nothing that only the closet
  prior would surface, and the results file records nothing about the code or the ranking config a
  run was taken under. They stand alone.
- **No task deletes anything.** `closetBoosts`, `closetRankBoosts`, `closetBoostStrength` and the
  arm all survive, because they are what a curated palace would use to argue the other way.
- **T4 is the only behaviour change**, and its rollback is `CLOSET_BOOST=1` — a restart, not a
  migration.
