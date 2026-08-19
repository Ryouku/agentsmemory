# Task ADR-001-T1: Generate hard negatives and label the three calibration populations

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `evalPromptAbsentHard` (identifier-preserving negative generator), `palace.EvalReport.PoolRanks`-derived population labels (`reachable`/`unreachable`/`absent`) on `EvalCaseResult`
**Consumes:** none

## Goal

Make the calibration set honest: negatives that keep the identifiers a real near-miss would carry, and every answerable case labelled by whether its gold was retrievable at all.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | `evalPromptAbsent` instructs "do not reuse the note's distinctive identifiers", which manufactures easy negatives; add an identifier-preserving prompt and make it the default for `--style absent` |
| `internal/palace/eval.go` | edit | add `Population` to `EvalCaseResult`, set from the case category and `poolRank` |
| `internal/palace/eval_test.go` | edit | pin the three-way labelling, including that a gold outside the pool is `unreachable` and not counted as answerable |
| `cmd/server/eval_test.go` | add | pin that the absent prompt no longer forbids identifier reuse |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestPopulationLabelsSeparateUnreachable` in `internal/palace/eval_test.go` asserting a case whose gold is absent from the pool is labelled `unreachable` rather than `reachable`; and `TestAbsentPromptKeepsIdentifiers` in `cmd/server/eval_test.go` asserting the absent prompt does not instruct the generator to drop identifiers. Commit them red.
2. Add `Population string` to `EvalCaseResult` with constants `PopReachable`, `PopUnreachable`, `PopAbsent` in `internal/palace/eval.go`; populate from `cat` and the existing `poolRank` at the point `PoolRanks` is appended.
3. Replace `evalPromptAbsent` with a version that asks for a neighbouring-topic question which **keeps** the note's identifiers, file names and flags, and additionally asks for the question to be plausible against a different project's notes (cross-wing near-miss).
4. Keep the old prompt available as `--style absent-easy` so previously generated case files remain reproducible and the two regimes can be compared.
5. Run the acceptance command; both new tests green.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace cmd/server | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestPopulation|TestAbsentPrompt|TestEvaluate" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestPopulationLabelsSeparateUnreachable` | `internal/palace/eval_test.go` | a gold outside the retrieved pool is `unreachable`, not `reachable` | — |
| `TestAbsentPromptKeepsIdentifiers` | `cmd/server/eval_test.go` | the absent prompt does not instruct identifier removal | — |

## Invariants

- Existing case files stay replayable: `--style absent-easy` reproduces the previous generator exactly.
- No arm's MRR changes — this task adds labels and changes generation, never ranking.

## Risks

- Hard negatives may prove *too* hard, collapsing the measured separation to nothing. That is a finding, not a failure: it would mean the gate cannot ship, and it is far better learnt here than after calibration. Mitigation: report both regimes side by side.

## Stop Condition

Stop and ask if the identifier-preserving generator yields negatives that another memory actually answers at a rate above ~30% after `verifyAbsent` — that would mean the corpus cannot supply hard negatives and the calibration plan needs rethinking rather than patching.

## Out of Scope

- The calibration report itself — that is T5's job.
- Growing the absent corpus beyond what `--n` produces (deferred: docs/adr/BACKLOG.md)

## Verification Log
