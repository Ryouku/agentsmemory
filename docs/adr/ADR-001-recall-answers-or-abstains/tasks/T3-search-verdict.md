# Task ADR-001-T3: Derive the confidence verdict inside Search

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `palace.Confidence` type, `palace.Verdict` constants, `Search` returning a populated confidence
**Consumes:** `config.AbstainThreshold` / `WithAbstain` (T2)

## Goal

Compare the top hit's cross-encoder score against the configured threshold and return a four-valued verdict, without changing which hits are returned or their order.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/palace.go` | edit | `Confidence` struct (`Verdict`, `TopScore`, `Threshold`, `Backend`) and the four constants |
| `internal/palace/service.go` | edit | derive it after reranking; `unknown` when unset or `!Reranked` |
| `internal/palace/service_test.go` | edit | pin all four verdicts and that hits are untouched |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestConfidenceVerdicts` covering answered / no_answer / uncertain / unknown, and `TestConfidenceNeverFiltersHits` asserting the returned page is identical with and without a threshold. Commit red.
2. Add the `Confidence` type and constants to `internal/palace/palace.go`.
3. In `Search`, after `applyRerank`, derive the verdict from the top hit: `unknown` when no threshold is configured or the top hit's `Reranked` is false; otherwise compare `RerankScore` to the threshold, with the `uncertain` band around it.
4. Return it alongside the hits without altering their content, order or count.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./internal/palace/ -run "TestConfidence" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestConfidenceVerdicts` | `internal/palace/service_test.go` | each of the four verdicts is produced in its own condition | — |
| `TestConfidenceNeverFiltersHits` | `internal/palace/service_test.go` | the verdict annotates, never suppresses | — |

## Invariants

- The returned hits are byte-identical with and without a configured threshold.
- `unknown` is returned whenever presence is false — never inferred from a zero score.

## Risks

- A page whose top hit was outside the rerank pool has no score; `Reranked` already distinguishes this, and the verdict must be `unknown` rather than `no_answer`.

## Stop Condition

Stop if the `uncertain` band width cannot be expressed without a second knob — a band derived from the calibration curve is T5's output and should not be invented here.

## Out of Scope

- Surfacing it over MCP — that is T4's job.
- Recording it in telemetry — that is T4's job.

## Verification Log
