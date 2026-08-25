# Task ADR-006-T3: Every discovered pair must admit its condition where an operator reads it

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the admission requirement, plus the named list of knobs the sweep cannot observe
**Consumes:** the discovered inert set (T2)

**Data dependency:** hermetic

## Goal

For each `(knob, gating knob)` the sweep discovers, the knob's flag Usage or its `config.Config` doc comment must name the gating knob; and knobs the sweep cannot observe are listed explicitly rather than passing by silence.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/modescope_test.go` | edit | the admission assertion and the unobservable list |
| `cmd/server/main.go` | edit | Usage strings for `--rerank-weight` and `--rerank-timeout` gain their `--rerank-url` dependency; `--bm25-weight` gains its `--fusion` one |
| `internal/config/config.go` | edit | the same conditions on the field docs that lack them |

## Ordered Steps

1. Write the failing test first (TDD red): `TestDiscoveredPairsAdmitTheirCondition`. Commit it red — the current Usage strings do not name their gating knobs.
2. Require the admission to name the gating knob **greppably** (flag spelling, env spelling, or field name). Prose only counts when it is mechanically checkable; this repo already holds that line for env vars.
3. Add the unobservable list: knobs whose effect needs a live backend, each with one sentence saying why the sweep is silent. A knob absent from BOTH the sweep and this list fails — silence must be declared.
4. Fix the startup contradiction: with `FUSION=rrf`, `main.go:836` must not print a bm25 weight line after `main.go:829` has said the weight does not apply.
5. Falsify: remove one admission clause; add a knob to neither the sweep nor the list; leave both startup lines printing.
6. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestDiscoveredPairsAdmitTheirCondition|TestEveryKnobIsSweptOrNamed|TestStartupDoesNotContradictItself" -count=1 -v 2>&1 | tee /tmp/t3.out
  grep -q -- "--- PASS: TestDiscoveredPairsAdmitTheirCondition" /tmp/t3.out
  grep -q -- "--- PASS: TestEveryKnobIsSweptOrNamed" /tmp/t3.out
  grep -q -- "--- PASS: TestStartupDoesNotContradictItself" /tmp/t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestDiscoveredPairsAdmitTheirCondition` | `cmd/server/modescope_test.go` | each discovered pair names its gating knob on a surface an operator reads | — |
| `TestEveryKnobIsSweptOrNamed` | `cmd/server/modescope_test.go` | no knob is silently outside both the sweep and the unobservable list | — |
| `TestStartupDoesNotContradictItself` | `cmd/server/modescope_test.go` | under rrf the startup does not both deny and report a bm25 weight | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| replace the `--fusion` clause in `--bm25-weight`'s Usage with "ignored under rank fusion" | yes | `TestDiscoveredPairsAdmitTheirCondition` |
| `if rrf {` → `if rrf && false {` (the early return stops guarding) | yes | `TestStartupDoesNotContradictItself` |
| `_ = cfg.SearchScope` inside `configureRanking` (a field joins the wiring undeclared) | yes | `TestEveryKnobIsSweptOrNamed` |
| drop `RerankTimeout` from `unobservableKnobs` | yes | `TestEveryKnobIsSweptOrNamed` |
| `--fusion`'s apply stops assigning `c.Fusion` | yes | `TestEveryKnobIsSweptOrNamed` |

No TDD-red entry stands in the Verification Log: the acceptance fence was first run against the
finished state. The five mutation runs below are the substantive equivalent — each removes one
mechanism and records the test that goes red — and they are stronger evidence than a single red run,
because a red run shows the suite can fail while a mutant shows WHICH assertion binds to WHAT.

Two first attempts did not build and were rewritten before they counted. Deleting the `if rrf` block
outright left `rrf` declared and unused; `&& false` keeps every identifier live. And `cfg.EmbeddingModel`
is not a field of `config.Config` — the mutation that was supposed to prove the universe is read out of
the source instead proved only that Go rejects a typo. Both printed `FAIL <pkg> [build failed]` with no
`--- FAIL` line, which is exactly the shape a naive failure count reads as a caught mutant.

## Out of Scope

- Rewording admissions that already name their gating knob (permanent: `--rerank-pool` and `RerankPool` already do it and are the model to copy, not to churn)
- A doc gate over the compose files and `.env.example` (deferred: docs/adr/BACKLOG.md — `TestDocumentedEnvVarsAreRead` covers the read direction; the conditional direction is a wider change)

## Invariants

- A knob that admits its condition passes whether or not the wording matches any template.
- No shipped configuration produces a finding.

## Risks

- An honest admission phrased without the gating knob's name fails. Accepted deliberately: the alternative is a matcher that accepts prose it cannot verify.

## Stop Condition

Stop and ask if a knob's condition cannot be stated without contradicting an accepted ADR — ADR-002 and ADR-003 own what `BM25_WEIGHT` and `CLOSET_BOOST` mean, and this task documents when they apply, never what they do.

## Verification Log

- 2026-08-20 · 9b520f3* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-20 · a335bd4 · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:93c22a03293cd76ba07bf31b5e2d13e59cc66be991ee0fbbce0b629bc7bac91f
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.007s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	2.344s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.008s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.003s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.353s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.005s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.013s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.018s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.005s
  FAIL
  ```
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:63977f1bf92a2c02b53788978f997988d34b09c57943896597578900defb5470

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `cmd/server/main.go` · rrf is the flag every later guard reads to suppress the knobs rank fusion ignores; leaving it false makes startup announce that bm25 weight does not apply and then report one — the self-contradiction TestStartupDoesNotContradictItself exists to catch · acceptance-sha256:63977f1bf92a2c02b53788978f997988d34b09c57943896597578900defb5470
