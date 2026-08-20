# Task ADR-006-T4: Fix the three defects the audit found while looking for inert knobs

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** honest `Reranked` telemetry, a settable `HTTPTimeout`, a wing-scoped CLI search
**Consumes:** none
**Data dependency:** hermetic

## Goal

Telemetry stops claiming reranking that did not happen; `HTTPTimeout` becomes settable; the CLI search honours `SEARCH_SCOPE` as the HTTP path does.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `Reranked` records whether reranking HAPPENED, not whether a reranker exists |
| `internal/palace/service_test.go` | edit | pin it at weight 0 with a reranker configured |
| `cmd/server/main.go` | edit | add `--http-timeout` / `HTTP_TIMEOUT`; assign it |
| `cmd/server/mcp.go` | edit | **the selection**: the CLI search path must consult `cfg.SearchScope`, as `searchWingFor` does for MCP |
| `cmd/server/mcp_test.go` | add | the CLI path scopes by default and `SEARCH_SCOPE=workspace` widens it |
| `cmd/server/wiring_test.go` | edit | the assignment check must distinguish an operator source from a default |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestRerankedRecordsWhatHappened`, `TestCLISearchHonoursSearchScope`, `TestConfigFieldsAreSettableByAnOperator`. Commit them red.
2. `Reranked` becomes true only when `applyRerankWith` scored something. Rows written before the fix over-claim in one direction only; record the cutover date in `docs/adr/BACKLOG.md` so a later reader of `am_recall_stats` can separate the two populations.
3. While there: with `RERANK_WEIGHT=0` the pool must not widen `candidateK` (`service.go:714`), since nothing will cross-encode and the fetch plus `GetMany` join are paid for nothing.
4. Add `--http-timeout` with `HTTP_TIMEOUT`, defaulting to today's 30s.
5. Strengthen `TestEveryConfigFieldIsPopulatedAndRead`: assignment must come from a flag accessor, not from `def.X`. Its failure message already promises this — the check must now deliver it. Expect other fields to fail; fix or tag each.
6. Make the CLI `mcp search` resolve its wing the same way the MCP path does.
7. Falsify each: restore `boolToInt(s.rerank != nil)`; assign `HTTPTimeout` from `def` again; drop the scope resolution from the CLI path.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ ./internal/palace/ -run "TestRerankedRecordsWhatHappened|TestCLISearchHonoursSearchScope|TestConfigFieldsAreSettableByAnOperator" -count=1 -v 2>&1 | tee /tmp/t4.out
  grep -q -- "--- PASS: TestRerankedRecordsWhatHappened" /tmp/t4.out
  grep -q -- "--- PASS: TestCLISearchHonoursSearchScope" /tmp/t4.out
  grep -q -- "--- PASS: TestConfigFieldsAreSettableByAnOperator" /tmp/t4.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t4.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRerankedRecordsWhatHappened` | `internal/palace/service_test.go` | a configured reranker at weight 0 records `Reranked` false | — |
| `TestCLISearchHonoursSearchScope` | `cmd/server/mcp_test.go` | the CLI path scopes to the registration wing by default | — |
| `TestConfigFieldsAreSettableByAnOperator` | `cmd/server/wiring_test.go` | a field assigned only from `def.X` fails, as the message has always claimed | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| restore `Reranked: boolToInt(s.rerank != nil)` | yes | `TestRerankedRecordsWhatHappened` |
| assign `HTTPTimeout: def.HTTPTimeout` again | yes | `TestConfigFieldsAreSettableByAnOperator` |
| drop the scope resolution from the CLI search | yes | `TestCLISearchHonoursSearchScope` |

## Out of Scope

- Rewriting historical `search_events` rows (permanent: they record what the code did; a migration that rewrites history destroys the only evidence of the bug's blast radius)
- Making the CLI a full parity surface for every MCP tool (deferred: docs/adr/BACKLOG.md — this task fixes the one divergence that was reproduced; a general parity gate is a separate decision)

## Invariants

- No historical telemetry row is modified.
- The CLI `--team` admin path may still read across wings; what changes is that it does so because it was asked to, not by omission.

## Risks

- Step 5 may fail several fields at once and balloon the task. Mitigated: each is either given a flag or tagged in the same commit with why it is not operator-facing; the stop condition below covers a large blast radius.

## Stop Condition

Stop and report if step 5 fails more than five fields — that is a finding about the config surface, not a task, and it deserves its own decision rather than being absorbed here.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
