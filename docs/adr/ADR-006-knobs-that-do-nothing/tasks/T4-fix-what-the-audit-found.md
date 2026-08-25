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
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ ./internal/palace/ -run "TestRerankedRecordsWhatHappened|TestRerankedIsTrueWhenItActuallyRan|TestDegradedRerankIsNotRecordedAsAPass|TestRerankPoolDoesNotWidenTheFetchForNothing|TestEveryConfigFieldIsPopulatedAndRead" -count=1 -v 2>&1 | tee /tmp/t4.out
  grep -q -- "--- PASS: TestRerankedRecordsWhatHappened" /tmp/t4.out
  grep -q -- "--- PASS: TestRerankPoolDoesNotWidenTheFetchForNothing" /tmp/t4.out
  grep -q -- "--- PASS: TestDegradedRerankIsNotRecordedAsAPass" /tmp/t4.out
  grep -q -- "--- PASS: TestEveryConfigFieldIsPopulatedAndRead" /tmp/t4.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t4.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRerankedRecordsWhatHappened` | `internal/palace/telemetry_test.go` | a configured reranker at weight 0 records `Reranked` false | — |
| `TestRerankedIsTrueWhenItActuallyRan` | `internal/palace/telemetry_test.go` | the true case, so the field cannot be hardcoded false | — |
| `TestDegradedRerankIsNotRecordedAsAPass` | `internal/palace/telemetry_test.go` | a failed rerank records false, on the path that fires when something is wrong | — |
| `TestRerankPoolDoesNotWidenTheFetchForNothing` | `internal/palace/telemetry_test.go` | a configured-but-disabled reranker stops paying for a wider fetch | — |
| `TestEveryConfigFieldIsPopulatedAndRead` | `cmd/server/wiring_test.go` | a field assigned only from `def.X` fails, as the message has always claimed | — |

<The config check was folded into the existing `TestEveryConfigFieldIsPopulatedAndRead` rather than
added beside it. The test exists and does what this table claims; only the planned name was stale.
Nothing caught it for a day because the lint's done-check could not read this README's table shape.>

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| `Reranked` back to "a reranker exists" | yes | `TestRerankedRecordsWhatHappened` |
| `Reranked` hardcoded false | yes | `TestRerankedIsTrueWhenItActuallyRan` |
| the fetch widens when the reranker will not run | yes | `TestRerankPoolDoesNotWidenTheFetchForNothing` |
| a degraded reranker reports success | yes | `TestDegradedRerankIsNotRecordedAsAPass` |
| assign `HTTPTimeout: def.HTTPTimeout` again | yes | `TestConfigFieldsAreSettableByAnOperator` (pending) |
| drop the scope resolution from the CLI search | yes | `TestCLISearchHonoursSearchScope` (pending) |

**Two mutants had to be rewritten to count.** Restoring `boolToInt(s.rerank != nil)` and hardcoding
`false` both left `reranked` unused, so neither compiled — a mutant that does not build has not been
tested, it has been skipped. `reranked || s.rerank != nil` and `reranked && false` keep every
identifier live and both die.

**And a third only died once a test existed for it.** A reranker that ERRORS fails open, which is
correct, but the search then did not rerank — recording that it did is the same lie on the path that
fires exactly when something is wrong. This palace has published an eval table with a silently
degraded reranker in it before. `TestDegradedRerankIsNotRecordedAsAPass` covers it.

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

- 2026-08-21 · aee8451* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
  ```
  2026/08/21 03:57:17 OK   00017_totp.sql (1.38ms)
  2026/08/21 03:57:17 OK   00018_webauthn.sql (5.16ms)
  2026/08/21 03:57:17 OK   00019_unlimited_plan.sql (8.4ms)
  2026/08/21 03:57:17 OK   00020_api_keys_team_user_idx.sql (1.98ms)
  2026/08/21 03:57:17 OK   00021_search_events.sql (1.22ms)
  2026/08/21 03:57:17 OK   00022_drawer_anchors.sql (1.12ms)
  2026/08/21 03:57:17 goose: successfully migrated database to version: 22
  --- PASS: TestRerankedRecordsWhatHappened (0.07s)
  PASS
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/palace	0.074s
  ```
- 2026-08-21 · aee8451* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:a81c7a24b416212c49fdea4f176042b2e1272f19f12ae813c72718fdbc764a5a
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	1.436s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.365s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.004s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.004s
  FAIL
  ```
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:8818422b653ecc7af32faa494d8e6d0f6834e7ece0cace57fbc641a0219f2a43

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/palace/service.go` · the search event must record whether a cross-encoder pass actually RAN, not whether one was configured; the mutant restores the exact historical defect the comment above it documents, where weight 0 logged a rerank that never happened and ADR-001 calibrates from those rows · acceptance-sha256:8818422b653ecc7af32faa494d8e6d0f6834e7ece0cace57fbc641a0219f2a43
