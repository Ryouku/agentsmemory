# Task ADR-019-T2: A hit carries its matching regions

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `palace.SnippetRegions` and `palace.MemoryIdentity`
**Consumes:** T1's measurement — this task does not begin until it supports the change
**Data dependency:** hermetic

## Goal

Every part of a memory that matched is available to the agent, verbatim, with the score that ranked it — and `content` still means exactly what it means today.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/rank.go` | edit | `snippetWindow` returns its ranked candidates; a new `SnippetRegions` renders them |
| `internal/palace/regions.go` | add | `SnippetRegions`, `MemoryIdentity`, and the neighbourhood/alignment rules |
| `internal/palace/regions_test.go` | add | regions are verbatim, ranked, non-overlapping, position-ordered, budgeted |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestRegionsAreVerbatimSlicesOfTheMemory`, `TestRegionsCoverEveryMatch`, `TestContentIsUnchangedByRegions`, `TestIdentityIsTheMemorysOwnFirstLine`. Commit them red.
2. Have the window chooser return its ranked candidates. It scores them all already; keeping them is the change.
3. Build regions from those candidates: verbatim text, the score that ranked it, and the rune offset it starts at. Merge overlapping candidates so no two regions repeat the same text.
4. Order regions by POSITION, not score, and put the score in the field. A list that jumps backwards through a memory reads as nonsense; the agent can sort by score itself, and cannot un-jumble prose.
5. Identity is the memory's own first line, bounded by `SnippetHeadChars`. Not generated, not derived — the line the author wrote, which by convention says what the memory IS.
6. `content` must be byte-identical to what it is today. This task ADDS; anything that changes what existing readers see belongs to a different decision.
   Do NOT put the regions on `SearchHit`: the budget that governs them is the caller's `snippet_chars`, which lives at the transport, so a domain field would be one the domain cannot fill.
7. A floor on region size: below it, prefer one larger region. A list of fragments is worse than a passage.
8. Falsify: return regions that are not slices of the memory; order by score; let regions overlap; change `content`.
9. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  apk add --no-cache bash >/dev/null
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestRegionsAreVerbatimSlicesOfTheMemory|TestRegionsCoverEveryMatch|TestRegionsAreOrderedByPositionNotScore|TestRegionsKeepOneRegionWhole|TestRegionsRespectTheBudget|TestContentIsUnchangedByRegions|TestIdentityIsTheMemorysOwnFirstLine|TestSnippetDoesNotEndMidWord" -count=1 -v 2>&1 | tee /tmp/a19t2.out
  grep -q -- "--- PASS: TestRegionsAreVerbatimSlicesOfTheMemory" /tmp/a19t2.out
  grep -q -- "--- PASS: TestRegionsCoverEveryMatch" /tmp/a19t2.out
  grep -q -- "--- PASS: TestRegionsAreOrderedByPositionNotScore" /tmp/a19t2.out
  grep -q -- "--- PASS: TestRegionsKeepOneRegionWhole" /tmp/a19t2.out
  grep -q -- "--- PASS: TestRegionsRespectTheBudget" /tmp/a19t2.out
  grep -q -- "--- PASS: TestContentIsUnchangedByRegions" /tmp/a19t2.out
  grep -q -- "--- PASS: TestIdentityIsTheMemorysOwnFirstLine" /tmp/a19t2.out
  grep -q -- "--- PASS: TestSnippetDoesNotEndMidWord" /tmp/a19t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a19t2.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestRegionsAreVerbatimSlicesOfTheMemory` | `internal/palace/regions_test.go` | every region is `strings.Contains` of the memory — nothing on this path is written by the machine, which is the ADR's refusal made mechanical | — |
| `TestRegionsCoverEveryMatch` | `internal/palace/regions_test.go` | a memory matching in three places yields three regions, in position order, non-overlapping, INCLUDING the match nearest the end — the one the single-window chooser could never reach | — |
| `TestRegionsAreOrderedByPositionNotScore` | `internal/palace/regions_test.go` | position order on a memory whose BEST region is the last one; the first version of this could not tell the two orderings apart because every region happened to score the same | — |
| `TestRegionsKeepOneRegionWhole` | `internal/palace/regions_test.go` | one matching place returns one region — the budget is not shredded when there is nothing to spread it over | — |
| `TestRegionsRespectTheBudget` | `internal/palace/regions_test.go` | the caller's ceiling holds at every budget | — |
| `TestContentIsUnchangedByRegions` | `internal/palace/regions_test.go` | `content` is byte-identical to today for the same input — every existing reader keeps working | — |
| `TestIdentityIsTheMemorysOwnFirstLine` | `internal/palace/regions_test.go` | identity is the author's first line, bounded — not a summary, not a derivation | — |
| `TestSnippetDoesNotEndMidWord` | `internal/palace/snippethead_test.go` | both snippet entry points still honour the word boundary — this path has been broken twice in a day | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestRegionsCoverEveryMatch` |
| 2 — something selects it | NOT YET — T3's call in `drawers.go` is what selects them. Recorded here rather than claimed: at the end of this task the functions exist and nothing invokes them |
| 3 — the caller can discover it | T3 puts them on the wire — until then these are exported functions nothing calls, and the ADR says so rather than letting them look finished |
| 4 — it is used | T3's re-judging of the 32 |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| return only the best candidate as a region | yes | `TestRegionsCoverEveryMatch` |
| order regions by score rather than position | yes | `TestRegionsCoverEveryMatch` |
| let two regions overlap | yes | `TestRegionsCoverEveryMatch` |
| trim or normalise a region's text | yes | `TestRegionsAreVerbatimSlicesOfTheMemory` |
| set `content` to the joined regions | yes | `TestContentIsUnchangedByRegions` |
| take identity from the highest-scoring region rather than the first line | yes | `TestIdentityIsTheMemorysOwnFirstLine` |
| drop the neighbourhood suppression, so several regions view one paragraph | yes | `TestRegionsCoverEveryMatch` |
| drop the match alignment, so a region starts at the candidate boundary | yes | `TestRegionsCoverEveryMatch` |
| drop the minimum region size | yes | `TestRegionsCoverEveryMatch` |

## Out of Scope

- The wire (deferred: T3 of this ADR — a domain field the wire discards is this repo's signature defect, and T3 is where that closes)
- Letting a cross-encoder score the regions (deferred: docs/adr/BACKLOG.md)

## Invariants

- Every region is a verbatim slice of the memory. Nothing on this path is generated.
- `content` is unchanged.
- Regions are position-ordered and never overlap.

## Risks

- A domain field nothing serves looks like a finished feature. Mitigated: named in the Reachability table above as rung 3 unmet until T3, rather than left to be discovered.

## Stop Condition

Stop and ask if the caller's budget cannot hold two regions above the floor at the shipped default — that means the default is the thing to change, which is a different decision this ADR explicitly rejected as its primary fix.

## Verification Log

- 2026-08-21 · 95277e5* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-21 · 5ee6ad5* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
