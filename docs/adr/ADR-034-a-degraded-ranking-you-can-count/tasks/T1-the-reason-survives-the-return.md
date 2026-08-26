# Task ADR-034-T1: applyRerankWith returns WHY it did not rerank, and the span and the caller get the same word

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `applyRerankWith` returning `(ranked []HybridScore, ok bool, reason string)`
**Consumes:** none
**Data dependency:** hermetic

## Goal

The reason a page was not reranked survives past `sp.End` as a return value, so a caller can persist
what the span already reports.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `applyRerankWith` computes the reason for the span and discards it; return it. `applyRerank` and every call site updated — this is the line that SELECTS the new value, and without it the reason is computed and dropped exactly as today |
| `internal/palace/rerankreason_test.go` | add | the failing test, and the assertion that span and return agree |

## Ordered Steps

1. Write the failing test first: drive `Search` against a reranker that times out, and assert the
   reason returned by `applyRerankWith` equals `telemetry.ReasonTimeout` and equals the `am.reason`
   the rerank span carries. It must be RED — `applyRerankWith` returns two values today, so this
   does not compile until step 2, which is the strongest possible red.
2. Change `applyRerankWith` to return the reason on every path: `no_reranker`, `empty`,
   `weight_zero`, `timeout`, `error`, `score_count`, and `""` when the cross-encoder ran.
3. Update `applyRerank` and both call sites (`Search`, `RerankScoresFor`) to thread it.
4. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestTheReasonOnTheSpanIsTheReasonReturned' -count=1 2>&1 | tee /tmp/acc34a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc34a.out && go test ./internal/palace/ ./internal/mcpserver/ -count=1 2>&1 | tee /tmp/acc34b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc34b.out
```

The new test runs ALONE first, so the already-green palace suite in the second command cannot carry
the verdict by itself.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestTheReasonOnTheSpanIsTheReasonReturned` | `internal/palace/rerankreason_test.go` | span `am.reason` and the returned reason are the same string, for a timeout and for a sick endpoint | — |
| `TestAServedRerankReturnsNoReason` | `internal/palace/rerankreason_test.go` | reranking that actually ran returns `""`, so the healthy path writes nothing | — |

The timeout case must be driven by an `httptest` server that SLEEPS past a real budget, against the
real `tei` client — not a fake returning `context.DeadlineExceeded`. `tei` arms two timeout paths (a
context deadline over the whole call and `http.Client.Timeout` per request) and only one produces
that sentinel, so a stubbed fixture would pass green while production returned the wrong word.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestAServedRerankReturnsNoReason` |
| 2 — something selects it | the `Search` call site threading the third return; mutation: discard it with `_` and `TestTheReasonOnTheSpanIsTheReasonReturned` goes red |
| 3 — the caller can discover it | n/a: no declared interface — internal signature, T2 owns the external surface |
| 4 — it is used | nothing measures this yet; T2 is what makes it observable |

## Verification Log

<Tool-written by `adr-verify <task.md>`. Empty at authoring.>

## Mutation Log

## Invariants

- `reranked`'s existing meaning does not change — ADR-031 aggregates on it.
- Ranking is untouched: the same memories in the same order, before and after.
- The reason vocabulary is `telemetry.Reason*`; no new string literals.

## Risks

- Threading a third return through two call sites is mechanical but easy to drop at one of them; the
  mutation named above is exactly that mistake.

## Out of Scope

- Persisting the reason (deferred: docs/adr/ADR-034-a-degraded-ranking-you-can-count.md — T2 owns it)
- Any change to when reranking fails open (permanent: ADR-030 owns that rule and it is correct)

## Stop Condition

Stop and ask if `applyRerankWith` turns out to have call sites beyond `applyRerank`, `Search` and
`RerankScoresFor` — the contract is wider than this task assumes.
