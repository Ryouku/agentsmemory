# Task ADR-038-T5: Recall returns what is current — and carries the reason forward

> Re-authored 2026-08-27 from ADR-010's T3, which this record supersedes. ADR-010's own mid-flight
> amendment is kept verbatim in intent: hiding history behind a flag AND expecting retractions to
> stop re-litigation cannot both hold, because a session about to redo a rejected thing does not
> know to ask for history. So the CURRENT record carries what it superseded and why.

**Depends-on:** T4
**Covers:** none — no spec
**Estimated scope:** L (every default read route)
**Owner:** unassigned
**Produces:** current-only recall across every default route; the superseded reason carried on the live record; the explicit history flag
**Consumes:** supersede semantics (T4); `current()` (T1)
**Data dependency:** hermetic

## Goal

An ended record is unreachable by every default route and reachable by one explicit one, and the live
record names what it replaced and why.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/memory_search.go` | edit | `searchCandidates` composes `current()` — the vector and BM25 halves both, since a filter applied to one leaks through the other |
| `internal/palace/service.go` | edit | `Get`, `GetMemory`, `ListDrawers` compose `current()`; the live row's `supersedes` + reason are resolved for the response |
| `internal/mcpserver/drawers.go` | edit | `include_history` on `am_search`, `am_list_drawers`, `am_get_drawer`; the `supersedes` field, reason **truncated to 200 characters** |
| `internal/palace/currentonly_test.go` | add | the end-to-end failing tests |

## Ordered Steps

1. Write the failing tests first — RED, and the end-to-end one is the task's real gate:
   - **an ended record is returned by NO default route.** Drive it through `am_search`,
     `am_list_drawers` and `am_get_drawer` end to end, not by unit test — this exact failure shipped
     once already, as a live chunk 1 with its own embedding ranking above the correction that
     replaced it;
   - `include_history: true` returns it, with its ending reason;
   - the CURRENT record carries `supersedes` and the reason on the DEFAULT path;
   - a reason longer than 200 characters is truncated in the recall response and whole under the
     history flag.
2. Compose `current()` into every default read route.
3. Resolve and attach `supersedes` + the truncated reason.
4. Add `include_history`, and declare it in every tool schema that honours it — **a handler that
   honours an argument the schema never advertises is a capability nobody will ever send** (rung 3).
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -run 'TestAnEndedRecordIsReturnedByNoDefaultRoute|TestIncludeHistoryReturnsItWithItsReason|TestTheLiveRecordCarriesWhatItReplaced|TestTheCarriedReasonIsTruncatedTo200Chars|TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt' -count=1 2>&1 | tee /tmp/acc38t5a.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38t5a.out && go test ./... -count=1 2>&1 | tee /tmp/acc38t5b.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38t5b.out
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestAnEndedRecordIsReturnedByNoDefaultRoute` | `internal/palace/currentonly_test.go` | the ADR's own falsification, end to end across all three routes | — |
| `TestIncludeHistoryReturnsItWithItsReason` | `internal/palace/currentonly_test.go` | history is reachable by exactly one explicit route | — |
| `TestTheLiveRecordCarriesWhatItReplaced` | `internal/palace/currentonly_test.go` | the reason reaches the DEFAULT path — the correction ADR-010 made to its own first draft | — |
| `TestTheCarriedReasonIsTruncatedTo200Chars` | `internal/palace/currentonly_test.go` | accumulation never grows the payload | — |
| `TestIncludeHistoryIsDeclaredInEveryToolThatHonoursIt` | `internal/mcpserver/catalog_test.go` | **rung 3** — a schema check; a behavioural test that sends the argument passes whether or not the schema advertises it | — |

**Shapes the creation path can already produce:** a multi-chunk memory where one chunk is ended and
others are not (should be impossible after T4 — assert it, do not assume it); a superseded record
whose successor is itself superseded (a chain — the live record names its immediate predecessor, and
the full chain is history-flag territory); an ended record that is the `source_drawer_id` of a
current KG fact — **decided 2026-08-27: the fact KEEPS the pointer.** Provenance is historical; the
fact was extracted from that text, and re-pointing it at the successor would assert that the
corrected text still supports it, which a correction may have removed. `am_kg_query` already returns
`source_drawer_id` (ADR-026 T6) so the reader can see it resolves to an ended record.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | the unit tests |
| 2 — something selects it | `current()` composed into each read route; mutation: remove it from the BM25 half alone and the end-to-end test must still go red — that is what proves the test drives both halves |
| 3 — the caller can discover it | `include_history` declared in every tool schema that honours it, asserted by a schema check |
| 4 — it is used | the ratio of `include_history` recalls to plain ones. **Deliberately NOT a retraction trigger** — ADR-010 struck that and the strike is kept: an archive's payoff is rare and large, and retiring it on call count is cancelling insurance because no claim was filed. |

## Mutation Log

## Invariants

- Ended TEXT never competes with its correction on any default route.
- The ending REASON always reaches the default route, on the live record.
- A KG fact's `source_drawer_id` is never rewritten by an ending. Provenance records where a claim came from, not where it is still true.
- The recall payload does not grow with corpus size: 200-character cap, `limit × snippet_chars` unchanged.

## Risks

- A filter applied to the vector half and not the lexical one leaks ended records back. The mutation in rung 2 exists for that specific shape.
- **An ended drawer keeps its vector, so the pool can shrink silently.** The vector store returns N candidates keyed by drawer id; `current()` runs in SQL and drops the ended ones, so a page can come back shorter than `limit` with nothing saying why. ADR-034 already settled the shape for the ranking half — count the degradation, never let it be silent — and this is that shape one layer up. Decide in this task: over-fetch to compensate, or report how many candidates the filter removed. Do not leave it to be found as "search returns fewer results than it used to".
- Truncating at 200 characters mid-word produces an unreadable fragment. Truncate on a boundary and mark it; a reason nobody can read is a reason nobody will act on.

## Stop Condition

Stop and ask if composing `current()` into `searchCandidates` measurably changes ranking for
CURRENT-only corpora — it must not. The ADR's falsification allows a ~0.01 MRR noise floor; a shift
larger than that means the filter is doing more than filtering.

**What would make this criterion impossible to fail?** Measuring it on a corpus with no ended records
at all, where the filter is a no-op by construction. The falsification requires ended records to
outnumber current ones 2:1 for exactly that reason.

## Out of Scope

- The corpus-integrity gate — T6.
- Ranking ended records when history IS requested (deferred: `docs/adr/BACKLOG.md` — inherited from ADR-010, which received it from ADR-004)

## Verification Log
