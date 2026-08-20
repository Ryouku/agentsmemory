# Task ADR-001-T1: Generate hard negatives, verify absence at retrieval depth, and label the three calibration populations

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `evalPromptAbsent` (identifier-preserving), `absentVerifyDepth` verification that DROPS unverified cases, `palace.AbsentVerification` provenance on `EvalCase`, `EvalCaseResult.Population` (`reachable`/`unreachable`/`absent`) and `EvalCaseResult.TopRerank`/`RerankScored`
**Consumes:** none

## Goal

Make the calibration set honest: negatives that keep the identifiers a real near-miss carries, absence checked as deep as the palace can retrieve, no case kept whose absence was not positively confirmed, and every case labelled with the population and the score the curve will be built from.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | `evalPromptAbsent` instructs "do not reuse the note's distinctive identifiers", which manufactures easy negatives; `verifyAbsent` (line ~389) searches with `Limit: 3` while the ADR claimed a corpus-wide check; and its caller (line ~538) prints `kept UNVERIFIED` and **keeps** the case when the verifier errors |
| `internal/palace/eval.go` | edit | add `Population`, `TopRerank` and `RerankScored` to `EvalCaseResult`, and `AbsentVerification` provenance to `EvalCase`; populate the population from the category and the existing `poolRank` |
| `internal/palace/eval_test.go` | edit | pin the three-way labelling, including that a gold outside the pool is `unreachable` and not counted as answerable |
| `cmd/server/eval_test.go` | add | pin that the absent prompt keeps identifiers, that a verifier error drops the case, and that the saved case file carries per-case verification provenance |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestPopulationLabelsSeparateUnreachable` in `internal/palace/eval_test.go` asserting a case whose gold is absent from the pool is labelled `unreachable` rather than `reachable`; `TestAbsentPromptKeepsIdentifiers` and `TestVerifyAbsentDropsOnVerifierError` in `cmd/server/eval_test.go` asserting the absent prompt does not instruct the generator to drop identifiers and that a verifier error removes the case instead of keeping it. Commit them red.
2. Add `Population string` to `EvalCaseResult` with constants `PopReachable`, `PopUnreachable`, `PopAbsent` in `internal/palace/eval.go`, populated from `cat` and the existing `poolRank` where `PoolRanks` is appended; carry the production arm's `prodRerank` / `prodScored` onto the same struct as `TopRerank` / `RerankScored`, so the curve consumes labelled `(score, population)` rows rather than the two flat `GoldRerank` / `AbsentRerank` arrays, which lose the label.
3. Replace `evalPromptAbsent` with a version that asks for a neighbouring-topic question which **keeps** the note's identifiers, file names and flags, and that would be plausible against a different project's notes (cross-wing near-miss). Keep the old prompt as `--style absent-easy` so existing case files stay reproducible and the two regimes can be compared.
4. Widen `verifyAbsent` from `Limit: 3` to `absentVerifyDepth = 20`, with the reason at the constant: the eval's retrieval ceiling measured 2026-08-18 on the then-current ~5,020-drawer palace put 98% of answerable golds inside the top 20 by vector distance (top-1 75%, top-5 92%, top-20 98%, 1 of 40 never retrieved), so depth 20 checked everything that palace could retrieve at all. **Re-measure the ceiling before writing the constant**: that corpus was reset on 2026-08-19 and the figures above describe a palace that no longer exists — the comment must cite the ceiling of the corpus the constant is actually chosen for, with its date. It is not a corpus-wide proof and the constant's comment must say so — a memory the dense channel never surfaces is one recall could not have returned either.
5. Make a verifier failure remove the case: the first failure aborts the run with the generator hint (matching the preflight doctrine already in this file — a checker that cannot score one note is misconfigured, not unlucky), and any later failure drops that single case with a counted, printed reason. No path may append a case labelled absent whose check did not return a positive "nothing answers this".
6. Persist per-case provenance: `EvalCase.AbsentVerification` carrying the checker model, the depth searched and the timestamp, written into the JSONL alongside the case, plus the same depth in `caseFileMeta`. A case file merged from several runs must let T2 tell a verified case from an unverified one row by row.
7. Run the acceptance command; all three new tests green and the existing eval tests unchanged.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'gofmt -l internal/palace cmd/server | grep -q . && exit 1; go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestPopulation|TestAbsentPrompt|TestVerifyAbsent|TestEvaluate" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestPopulationLabelsSeparateUnreachable` | `internal/palace/eval_test.go` | a gold outside the retrieved pool is `unreachable`, not `reachable` | — |
| `TestAbsentPromptKeepsIdentifiers` | `cmd/server/eval_test.go` | the absent prompt does not instruct identifier removal | — |
| `TestVerifyAbsentDropsOnVerifierError` | `cmd/server/eval_test.go` | a verifier error drops the case; nothing labelled absent survives unverified | — |

## Invariants

- Existing case files stay replayable: `--style absent-easy` reproduces the previous generator exactly.
- Every `CatAbsent` case written to a file carries verification provenance; a case without it is a case T2 must refuse.
- No arm's MRR changes — this task adds labels and changes generation, never ranking.

## Risks

- Hard negatives may prove *too* hard, collapsing the measured separation to nothing. That is a finding, not a failure: it would mean the gate cannot ship, and it is far better learnt here than after four tasks of wiring. Mitigation: report both regimes side by side; T3 is where the finding is acted on.
- Depth 20 costs 20 checker calls per candidate instead of 3, and drop-on-failure discards cases that were previously kept, so the verified-absent count will fall below the 21 the old top-3 check produced. Both are the price of a label that means what it says; the new count is reported and is what T2's sample size is stated from.

## Stop Condition

Stop and ask if the identifier-preserving generator yields negatives that another memory actually answers at a rate above ~30% at depth 20 — that would mean the corpus cannot supply hard negatives and the calibration plan needs rethinking rather than patching. Stop too if fewer than 15 verified-absent cases survive a `--n 25` run: T2's interval is already the weakest part of this ADR, and a smaller sample makes the gate criterion unmeasurable rather than merely noisy.

## Out of Scope

- The calibration report, the gate criterion and the refusal of unverified cases — that is T2's job.
- Growing the absent corpus beyond what `--n` produces, and mining hard negatives from real queries instead of generating them (deferred: docs/adr/BACKLOG.md)

## Verification Log
