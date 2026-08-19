# Task ADR-001-T5: Produce the threshold with `eval --calibrate`

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `agentsmemory eval --calibrate` risk–coverage report and recommended threshold
**Consumes:** three-population labels (T1)

## Goal

Turn the two score distributions into a defensible operating point an operator can paste into configuration, with its error rate and sample size stated beside it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | `--calibrate` mode and `--target-recall` flag; print the curve |
| `internal/palace/evalstats.go` | edit | risk–coverage computation over the three populations |
| `internal/palace/evalstats_test.go` | edit | pin the curve and the recommendation against a known distribution |

## Ordered Steps

1. Write the failing test first (TDD red): `TestRiskCoverageRecommendsThreshold` over a synthetic, fully-separable distribution where the correct threshold is known, plus an overlapping one where the recommendation must respect the declared recall target rather than maximise accuracy. Commit red.
2. Implement the curve in `internal/palace/evalstats.go`: for each candidate threshold, answer-recall on the **reachable**-answerable class and correct-refusal rate on absent; unreachable cases counted and reported, never scored as answerable.
3. Add `--calibrate` to the eval command: print the curve, the recommended threshold at `--target-recall` (default 0.95), the sample counts, and the backend the reranker is currently speaking.
4. Refuse to recommend from a case file generated with the easy-negative prompt, naming `--style absent` as the fix — a threshold from easy negatives over-answers in production.
5. Print the paste-ready `ABSTAIN_THRESHOLD` / `ABSTAIN_BACKEND` lines, and a caveat line carrying the sample size.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ ./cmd/server/ -run "TestRiskCoverage|TestCalibrate" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRiskCoverageRecommendsThreshold` | `internal/palace/evalstats_test.go` | the recommendation honours the recall target on separable and overlapping data | — |
| `TestCalibrateRefusesEasyNegatives` | `cmd/server/eval_test.go` | a case file from the easy-negative generator cannot produce a shipped threshold | — |

## Invariants

- No threshold is ever written to configuration by the tool; it prints, the operator decides.
- Unreachable-answerable cases are never counted as answerable.

## Risks

- With ~21 absent cases the recommended threshold is unstable. Mitigation: print the sample count and the interval alongside it, and say plainly that the number is an empirical quantile over a generated set, not a guarantee.

## Stop Condition

Stop if the curve on hard negatives shows no threshold achieving the target recall at any useful refusal rate — that means the gate cannot ship on this corpus, and the ADR needs revisiting rather than the command needing tuning.

## Out of Scope

- Applying the threshold automatically (permanent: an operator pastes it, so a bad calibration cannot silently become production behaviour).
- Learned multi-feature abstention (deferred: docs/adr/BACKLOG.md)

## Verification Log
