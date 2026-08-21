# Task ADR-019-T2: A snippet shows every place that matched

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** multi-window snippets within the caller's existing budget
**Consumes:** T1's measurement — this task does not begin until it supports the change
**Data dependency:** hermetic

## Goal

When a memory matches in several places, the snippet shows those places rather than the best one.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/rank.go` | edit | `snippetWindow` returns the ranked windows; `Snippet` renders the ones that fit |
| `internal/palace/rank_test.go` | edit | several matching regions all appear; one region still returns one window |
| `internal/palace/snippethead_test.go` | edit | the head path composes with multiple windows without delivering the opening twice |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSnippetShowsEveryMatchingRegion`, `TestSnippetKeepsOneRegionWhole`, `TestSnippetRespectsTheBudget`. Commit them red.
2. Have the window chooser return its ranked candidates rather than only the winner. It already scores them all; the discard is the change.
3. Select greedily by score until the budget is spent, then order the selected windows by POSITION — a snippet that jumps backwards through a memory reads as nonsense even when every fragment is relevant.
4. Merge windows that overlap or touch, so the join marker never appears between adjacent text.
5. A floor on window size: below it, prefer one larger window. A budget spent on many fragments is worse than one passage, and this is where that happens.
6. Compose with `SnippetWithHead`: the opening still leads for chunk zero, and must not be delivered twice when a chosen window overlaps it — the defect that path already had once.
7. Falsify: return only the best window; order by score rather than position; drop the merge; drop the floor.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  apk add --no-cache bash >/dev/null
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestSnippetShowsEveryMatchingRegion|TestSnippetKeepsOneRegionWhole|TestSnippetRespectsTheBudget|TestSnippetDoesNotEndMidWord|TestSnippetWithHeadDoesNotRepeatTheHead" -count=1 -v 2>&1 | tee /tmp/a19t2.out
  grep -q -- "--- PASS: TestSnippetShowsEveryMatchingRegion" /tmp/a19t2.out
  grep -q -- "--- PASS: TestSnippetKeepsOneRegionWhole" /tmp/a19t2.out
  grep -q -- "--- PASS: TestSnippetRespectsTheBudget" /tmp/a19t2.out
  grep -q -- "--- PASS: TestSnippetDoesNotEndMidWord" /tmp/a19t2.out
  grep -q -- "--- PASS: TestSnippetWithHeadDoesNotRepeatTheHead" /tmp/a19t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a19t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSnippetShowsEveryMatchingRegion` | `internal/palace/rank_test.go` | a memory matching in three places shows all three, in position order | — |
| `TestSnippetKeepsOneRegionWhole` | `internal/palace/rank_test.go` | one matching region still returns ONE window — the budget must not be shredded when there is nothing to spread it over | — |
| `TestSnippetRespectsTheBudget` | `internal/palace/rank_test.go` | the caller's `snippet_chars` is still the ceiling, joins included | — |
| `TestSnippetDoesNotEndMidWord` | `internal/palace/snippethead_test.go` | both entry points still honour the word boundary — this path has been broken twice | — |
| `TestSnippetWithHeadDoesNotRepeatTheHead` | `internal/palace/snippethead_test.go` | the opening is still not delivered twice when a window overlaps it | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSnippetShowsEveryMatchingRegion` |
| 2 — something selects it | `Search` calls `SnippetWithHead` for every hit; no flag, no opt-in |
| 3 — the caller can discover it | the snippet is what an agent already reads |
| 4 — it is used | every recall, which is why the two existing regression tests are in this task's acceptance |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| return only the best window | yes | `TestSnippetShowsEveryMatchingRegion` |
| order selected windows by score rather than position | yes | `TestSnippetShowsEveryMatchingRegion` |
| drop the overlap merge | yes | `TestSnippetShowsEveryMatchingRegion` (a join marker between adjacent text) |
| drop the minimum window size | yes | `TestSnippetKeepsOneRegionWhole` |
| let the joins push the total past the budget | yes | `TestSnippetRespectsTheBudget` |

## Out of Scope

- The wire fields reporting coverage (deferred: T3 of this ADR)
- Letting a cross-encoder choose windows (deferred: docs/adr/BACKLOG.md)

## Invariants

- The caller's `snippet_chars` is a ceiling, joins included.
- A memory that matched in one place returns one window, unchanged from today.
- Windows appear in position order and never overlap.

## Risks

- Choppier snippets read worse even while carrying more. Mitigated: position ordering, overlap merging, a size floor, and T3 re-judging the 32 through the same blind judge.

## Stop Condition

Stop and ask if the budget cannot hold two windows above the floor at the shipped default — that would mean the default is the thing to change, which is a different decision and one this ADR rejected as the primary fix.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
