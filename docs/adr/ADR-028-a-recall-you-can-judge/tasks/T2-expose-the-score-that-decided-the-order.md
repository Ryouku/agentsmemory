# Task ADR-028-T2: Expose the score the order was actually decided by

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `blended_score` on each `am_search` hit
**Consumes:** none
**Data dependency:** hermetic

## Goal

Each `am_search` hit carries `blended_score` — the value `BlendRerank` sorts on — so a returned order is explainable from the response instead of from the source.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | the hit shape and its field descriptions live here |
| `internal/palace/service.go` | edit only if `Blended` is not already carried out to the hit | `HybridScore.Blended` is computed at `service.go:1336`; it must survive into the `SearchHit` the MCP layer renders |
| `internal/mcpserver/drawers_test.go` | edit | the tool-description assertion — a schema string readable without a palace |
| `internal/mcptest/blended_test.go` | add | the ordering assertion — only this package drives a real `am_search` and can compare two hits' scores |

## Ordered Steps

1. Write the failing test first (TDD red): `TestHitCarriesTheScoreItWasOrderedBy` asserts that for a page of ≥2 reranked hits, every hit carries `blended_score` AND the page is non-increasing in it. Confirm it is red against `8c3167d`.
2. Carry `Blended` from `HybridScore` through to the rendered hit if it is not already present.
3. Add `blended_score` to the hit shape in `drawers.go`.
4. (Test locations follow T1's amendment: the ordering assertion needs a live palace and lives in `internal/mcptest`; the description assertion is a schema read and stays in `internal/mcpserver`.) State the POOL-RELATIVE caveat in the `am_search` TOOL description — `mcp.WithDescription(...)`, which is a runtime string an agent actually reads — and assert it with `TestSearchToolDescriptionSaysBlendedIsPoolRelative`.

   **Amended 2026-08-25, during execution.** This step originally said to put the caveat in the response field's description and assert that. A response field has no runtime description: it carries a Go struct tag and a source comment, and asserting a comment is the text-is-not-behaviour defect this repository has already been bitten by (a config assertion that matched a file's COMMENTS survived deletion of the real key). The tool description is the only string on this surface a caller reads at runtime, so that is where the caveat belongs and what the test reads.
5. Run the fence.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/mcpserver/ ./internal/palace/
  go test ./internal/mcpserver/ -run "TestSearchToolDescriptionSaysBlendedIsPoolRelative" -count=1 -v 2>&1 | tee /tmp/t2.out
  go test ./internal/mcptest/ -run "TestHitCarriesTheScoreItWasOrderedBy" -count=1 -v 2>&1 | tee -a /tmp/t2.out
  grep -q -- "--- PASS: TestHitCarriesTheScoreItWasOrderedBy" /tmp/t2.out
  grep -q -- "--- PASS: TestSearchToolDescriptionSaysBlendedIsPoolRelative" /tmp/t2.out
  ! grep -qE "no tests to run|^FAIL" /tmp/t2.out
  go test ./internal/mcpserver/ ./internal/mcptest/ ./internal/palace/ -count=1
'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHitCarriesTheScoreItWasOrderedBy` | `internal/mcptest/blended_test.go` | every reranked hit carries `blended_score`, and the page is non-increasing in it | — |
| `TestSearchToolDescriptionSaysBlendedIsPoolRelative` | `internal/mcpserver/drawers_test.go` | the registered `am_search` tool description states that `blended_score` is pool-relative | — |

The ordering half of the first test is the load-bearing one: asserting mere presence would pass if `blended_score` were populated from `rerank_score`, which is precisely the confusion this task exists to end.

**Fixture requirement, stated because the gate cannot see it:** the page must contain at least one pair where `rerank_score` and `blended_score` disagree on order. A fixture where the two coincide cannot distinguish a correct implementation from one that returns the rerank score under a new name — the same balanced-fixture defect found in ADR-005 T2 on 2026-08-25, where a 2-versus-2 split made an inverted room filter undetectable.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestHitCarriesTheScoreItWasOrderedBy` |
| 2 — something selects it | the hit renderer in `drawers.go`; mutation: populate it from `rerank_score` and the ordering assertion goes red |
| 3 — the caller can discover it | `TestEveryHitFieldIsOnTheWireOrExcused` (already in the repo) fails the moment `SearchHit.Blended` exists and is not rendered; `TestSearchToolDescriptionSaysBlendedIsPoolRelative` reads the tool description a caller actually receives |
| 4 — it is used | nothing measures this yet — it is an explanatory field; usage would show up as fewer "why did this rank here" questions, which nothing counts |

## Mutation Log

## Invariants

- `blended_score` is the value the page was sorted on, never a recomputation.
- Ranking, scoring and the retrieval unit are unchanged: this task reads a number that already decided the order.
- Hits that were not reranked carry no `blended_score` rather than a zero, for the same reason `rerank_score` is `omitempty` — an absent field and a real 0.0 must not be confusable.

## Risks

- A reader averages `blended_score` across pages. Mitigated by the caveat in the `am_search` tool description and its test; the ADR records this as a Neutral consequence.
- Populating the field from the wrong score would be invisible to a presence-only assertion; the ordering assertion and its fixture requirement exist for that.

## Stop Condition

Stop and ask if `Blended` is not reachable at the render site without widening a domain type beyond this ADR's scope — that would make this a `palace` change rather than an `mcpserver` one, and the Component/Boundary Impact would need revisiting.

## Out of Scope

- Exposing `rerankNorm` / `fusedNorm` (rejected in the ADR's Alternatives: pool-relative components are meaningless without the pool)
- Any change to how `Blended` is computed

## Verification Log
