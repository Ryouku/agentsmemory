# Task ADR-006-T1: Make the ranking wiring drivable without a server

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `configureRanking(svc, cfg, newReranker) (*palace.Service, []string)`
**Consumes:** none
**Data dependency:** hermetic

## Goal

The block in `newServices` that applies closet boost, fusion, BM25 weight and the reranker becomes a function a test can call, emitting the same log lines in the same order.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/main.go` | edit | extract the wiring; `newServices` calls it and prints the returned lines |
| `cmd/server/configureranking_test.go` | add | the extraction is behaviour-preserving, and the reranker factory is injectable |

## Ordered Steps

1. Write the failing test first (TDD red): `TestConfigureRankingEmitsTheSameLines` — build a `config.Config` from `config.Default()`, call `configureRanking`, and compare the returned lines against the literal strings `newServices` prints today. Commit it red.
2. Extract the block. Take the reranker factory as a parameter (`func(url string, timeout time.Duration) palace.Reranker`) so the wiring can be driven with a fake and no network.
3. `newServices` calls it and `log.Printf`s each returned line, preserving today's text and order exactly.
4. Add `TestConfigureRankingHonoursTheRerankURLGuard`: with `RerankURL` empty the factory must not be called at all — that guard is what makes three rerank knobs inert at baseline, and T2's predicate depends on it being where we think it is.
5. Falsify: reorder two emitted lines; change one line's text; call the factory unconditionally. Each mutant must compile and turn a test red.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestConfigureRanking" -count=1 -v 2>&1 | tee /tmp/t1.out
  grep -q -- "--- PASS: TestConfigureRankingEmitsTheSameLines" /tmp/t1.out
  grep -q -- "--- PASS: TestConfigureRankingHonoursTheRerankURLGuard" /tmp/t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t1.out
  go test ./cmd/server/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestConfigureRankingEmitsTheSameLines` | `cmd/server/configureranking_test.go` | the extraction preserves the startup text and its order | — |
| `TestConfigureRankingHonoursTheRerankURLGuard` | `cmd/server/configureranking_test.go` | no rerank URL means the factory is never called, so no rerank knob is applied | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| swap two emitted log lines | yes | `TestConfigureRankingEmitsTheSameLines` |
| change one line's wording | yes | `TestConfigureRankingEmitsTheSameLines` |
| call the reranker factory unconditionally | yes | `TestConfigureRankingHonoursTheRerankURLGuard` |

## Out of Scope

- Changing what any line says (permanent: this task is an extraction; wording changes belong to T4 where they are the point)
- Extracting the storage or embedder wiring (deferred: docs/adr/BACKLOG.md — no gate needs it yet, and moving code nothing tests is churn)

## Invariants

- Startup output is byte-identical to today for every configuration.
- `newServices` remains the only caller in production code.

## Risks

- An extraction that silently drops a setter. Mitigated: the emitted lines are the observable, and each setter emits one.

## Stop Condition

Stop and ask if the block turns out to depend on state built later in `newServices` — that would make the extraction a reordering rather than a move, and reordering startup is a different decision.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
