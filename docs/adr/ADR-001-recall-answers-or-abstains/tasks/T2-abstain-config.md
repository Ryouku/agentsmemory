# Task ADR-001-T2: Add the abstention threshold as operator configuration, with backend validation

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `config.AbstainThreshold`, `config.AbstainBackend`, `palace.Service.WithAbstain(threshold float64, backend string)`
**Consumes:** none

## Goal

Let an operator set a calibrated threshold and the backend it was calibrated on, and refuse to start when that backend does not match the reranker actually configured.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/config/config.go` | edit | two new keys; unset threshold is valid and means "no verdict" |
| `cmd/server/main.go` | edit | flags + env (`ABSTAIN_THRESHOLD`, `ABSTAIN_BACKEND`), startup validation, `WithAbstain` wiring |
| `internal/palace/service.go` | edit | store threshold/backend; setter follows the `WithReranker` contract |
| `cmd/server/abstain_test.go` | add | pin that a backend mismatch refuses startup and that unset is accepted |
| `.env.example`, `.env.docker.example` | edit | document the knob and that there is deliberately no default |

## Ordered Steps

1. Write the failing test first (TDD red): `TestAbstainBackendMismatchIsRefused` asserting that a configured threshold whose `ABSTAIN_BACKEND` disagrees with the reranker dialect is rejected with an error naming both, and `TestAbstainUnsetIsValid` asserting an unset threshold configures cleanly. Commit red.
2. Add `AbstainThreshold float64` and `AbstainBackend string` to `config.Config`; leave both zero/empty in `Default()` — there is no defensible default.
3. Add `WithAbstain` to `palace.Service` beside `WithRerankWeight`, storing both values; place the doc comment on its own declaration (the doclint gate enforces this).
4. In `cmd/server/main.go`, wire the flags and validate: a non-zero threshold with an empty backend, or a backend that does not match the configured reranker, is a startup error.
5. Document both keys in the two env examples, stating plainly that an uncalibrated palace returns `unknown` rather than guessing.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'go vet ./... && go test ./cmd/server/ ./internal/palace/ ./internal/config/ -run "TestAbstain" -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAbstainBackendMismatchIsRefused` | `cmd/server/abstain_test.go` | a threshold calibrated elsewhere cannot be served silently | — |
| `TestAbstainUnsetIsValid` | `cmd/server/abstain_test.go` | absence of calibration is a supported state, not an error | — |

## Invariants

- An unset threshold changes nothing about today's behaviour.
- No default threshold value exists anywhere in the tree.

## Risks

- Backend detection from `RERANK_URL` is heuristic; a proxy in front of either server could defeat it. Mitigation: the operator states the backend explicitly and the check only compares that statement to the dialect the client speaks.

## Stop Condition

Stop if backend identity cannot be determined without probing the live reranker at startup — adding a network call to boot is a bigger decision than this task owns.

## Out of Scope

- Deriving the verdict — that is T3's job.
- Producing the number the operator pastes in — that is T5's job.

## Verification Log
