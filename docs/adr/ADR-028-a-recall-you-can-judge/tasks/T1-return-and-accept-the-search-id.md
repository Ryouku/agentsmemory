# Task ADR-028-T1: Return the recall's identifier, and accept it back

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (single file)
**Owner:** unassigned
**Produces:** `search_id` on the `am_search` response; the optional `search_id` argument on `am_get_drawer`
**Consumes:** none
**Data dependency:** hermetic

## Goal

`am_search` returns the `search_id` its page was recorded under, and `am_get_drawer` advertises and accepts an optional `search_id`, so a fetch can name the recall that led to it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/mcpserver/drawers.go` | edit | the response shape and both tool schemas live here; this is the file that SELECTS what a caller can see and send |
| `internal/palace/service.go` | edit | `Search` must return the id it minted — today it is written to the row and dropped on the way out |
| `internal/telemetry/span.go` | edit | `Annotate` — put attributes on a span a wrapper opened, without threading the `*Span` through every handler signature |
| `internal/mcpserver/searchidspan_test.go` | add | the accepted id reaches the tool span, and an absent one does not set an empty attribute |
| `internal/mcptest/searchid_test.go` | add | all three assertions — this package drives the real tools and is the repository's existing home for schema audits (`scoping_audit_test.go`) as well as end-to-end responses |

The reachability line for this task is the SCHEMA, not the handler: `mcp.WithString("search_id", …)` on `am_get_drawer` is the one call that makes the argument discoverable, and deleting it leaves a handler that works for anyone who guesses the name and is invisible to everyone who reads the tool definition.

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSearchResponseCarriesItsSearchID` (in `internal/mcptest`) drives the real `am_search` and asserts the envelope carries a non-empty `search_id`; `TestGetDrawerSchemaAdvertisesSearchID` (in `internal/mcpserver`) reads the registered tool definition for `am_get_drawer` and asserts a `search_id` property exists with a description. Confirm both are red.

   **Amended 2026-08-25, during execution.** The tests were authored against `internal/mcpserver`, which builds its server with `New(Deps{})` and has no palace behind it — it can read a schema, not a response. All three now live in `internal/mcptest`, which drives the real tools, already decodes `{count, hits}` from a real search (`regions_test.go`), and is where this repository already keeps its schema audits (`scoping_audit_test.go`).

   Also found while scouting: `TestEveryArgumentAHandlerReadsIsDeclared` (`internal/mcpserver/argreach_test.go`) ALREADY fails when a handler reads an argument the tool does not advertise. So rung 3 for `search_id` is guarded by an existing gate the moment step 5 reads the argument, and the new schema test is narrower on purpose — it asserts the argument is DESCRIBED, which the generic gate does not check.
2. Surface the id from the domain: have `Search` return the minted `searchID` alongside the hits (or expose it on the result struct), without changing what is written to `search_events` — the row's `ID` already holds it.
3. Add `search_id` to the `am_search` response envelope in `drawers.go`.
4. Register the optional `search_id` argument on `am_get_drawer` with a description naming what it is for ("the `search_id` of the recall that led you to this memory").
5. Read the argument in the handler and put it on the tool span; do not STORE it — durable recording is the deferred task's job.

   **Amended 2026-08-25, after the work.** This step originally said to read the argument and ignore it, and the implementation did exactly that (`_ = req.GetString(...)`). Checked against the running server: a fetch quoting a valid `search_id` produced `am.tool 0ms ran` with nothing tying it to any recall, while the searches above it printed their id on every child. That shipped a signal whose adoption cannot be observed — by the very instrument this repository had just made mandatory at deploy time — and left this ADR's own deferral trigger ("the first week a non-test client sends one") unanswerable. One span attribute fixes it, with no storage and no schema change. The general rule: a change that adds a signal extends the instrument in the same commit.
6. Run the acceptance fence and confirm it is green only after steps 2–5.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/mcpserver/ ./internal/palace/
  go test ./internal/mcptest/ -run "TestSearchResponseCarriesItsSearchID|TestGetDrawerSchemaAdvertisesSearchID|TestGetDrawerIgnoresAnUnknownSearchID" -count=1 -v 2>&1 | tee /tmp/t1.out
  go test ./internal/mcpserver/ -run "TestGetDrawerSpanCarriesTheSearchIDItWasGiven|TestGetDrawerHandlerReachesTheAnnotation" -count=1 -v 2>&1 | tee -a /tmp/t1.out
  grep -q -- "--- PASS: TestSearchResponseCarriesItsSearchID" /tmp/t1.out
  grep -q -- "--- PASS: TestGetDrawerSchemaAdvertisesSearchID" /tmp/t1.out
  ! grep -qE "no tests to run|^FAIL" /tmp/t1.out
  grep -q -- "--- PASS: TestGetDrawerIgnoresAnUnknownSearchID" /tmp/t1.out
  grep -q -- "--- PASS: TestGetDrawerSpanCarriesTheSearchIDItWasGiven" /tmp/t1.out
  grep -q -- "--- PASS: TestGetDrawerHandlerReachesTheAnnotation" /tmp/t1.out
  go test ./internal/mcpserver/ ./internal/mcptest/ ./internal/palace/ -count=1
'
```

The three `grep -q -- "--- PASS:"` lines are what make this fence red before the work: a `-run` filter matching nothing exits 0 with a summary, and the greps then fail on the absent PASS lines. The new units run alone first; the package suites run second, chained with `&&` via `set -e`, so the regression suites cannot carry the verdict by themselves.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSearchResponseCarriesItsSearchID` | `internal/mcptest/searchid_test.go` | the real `am_search` envelope carries a non-empty `search_id` | — |
| `TestGetDrawerSchemaAdvertisesSearchID` | `internal/mcptest/searchid_test.go` | the registered `am_get_drawer` tool definition declares `search_id` AND gives it a description | — |
| `TestGetDrawerIgnoresAnUnknownSearchID` | `internal/mcptest/searchid_test.go` | an unrecognised id does not fail the fetch | — |
| `TestGetDrawerSpanCarriesTheSearchIDItWasGiven` | `internal/mcpserver/searchidspan_test.go` | the accepted id reaches the tool span; absent and blank set no attribute | — |
| `TestGetDrawerHandlerReachesTheAnnotation` | `internal/mcpserver/searchidspan_test.go` | the REGISTERED handler calls it — a mutant deleting the call survived the unit test alone | — |

The schema test is the one that matters and the one a behavioural test cannot replace: a handler honouring an argument the schema never advertises passes every end-to-end test and is unreachable by any caller that reads the definition.

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSearchResponseCarriesItsSearchID`, driving the real tool rather than the renderer |
| 2 — something selects it | the response builder in `drawers.go`, and the get_drawer handler's call to `annotateSearchID` — mutation: blank the assignment, or delete the call, and a test goes red. The second of those SURVIVED until `TestGetDrawerHandlerReachesTheAnnotation` existed |
| 3 — the caller can discover it | the pre-existing `TestEveryArgumentAHandlerReadsIsDeclared` fails if the handler reads an undeclared argument; `TestGetDrawerSchemaAdvertisesSearchID` adds the description requirement |
| 4 — it is used | `am.search_id` on the tool span — a search followed by a fetch is one traceable pair, so the deferred task's trigger is answerable from traces rather than from nothing |

## Mutation Log

- 2026-08-25 · 84d6ecc* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · the page must name the recall it came from with the value the search_events row was keyed by; an empty string keeps the key present and the join impossible, which is exactly the shape a presence-only assertion would accept · acceptance-sha256:e9384385f841db4ae2fdd618d24241a38d4bf1632bfea1aa1e872213d7272575
- 2026-08-25 · 029deaf* · mutant survived · exit 0 · `internal/mcpserver/drawers.go` · restores the original defect exactly: the handler still reads its arguments and the accepted search_id reaches no span, so a fetch quoting a recall is indistinguishable in a trace from one that quoted nothing and the deferral trigger stays unanswerable · acceptance-sha256:b694e3648f8a87ad9e1c258fc2b402cfbd8fb7a8fac984bb763465f9eb66cc70
  ```
  the fence passed with the mechanism broken
  ```
- 2026-08-25 · 029deaf* · mutant killed · exit 1 · `internal/mcpserver/drawers.go` · deletes the CALL while leaving the function and its unit test intact — the component-works-and-nothing-selects-it defect. This exact mutant SURVIVED before TestGetDrawerHandlerReachesTheAnnotation drove the registered handler · acceptance-sha256:f7995d9626d2033a93221b4b3e507693f37af1249aa4e450bb9725be7fedb95c
- 2026-08-25 · 029deaf* · mutant killed · exit 1 · `internal/mcpserver/server.go` · restores the discarded span context: the handler then runs under a different span, no downstream code can annotate am.tool, and the search_id annotation becomes inert in production exactly as it was when the live trace showed a bare am.tool with nothing linking it to a recall · acceptance-sha256:f7995d9626d2033a93221b4b3e507693f37af1249aa4e450bb9725be7fedb95c

## Invariants

- The id returned is the id under which the page was recorded — the same value, never a fresh one minted for the response.
- `am_get_drawer` behaves identically whether `search_id` is present, absent, or unrecognised. Until T3 the argument changes nothing observable.
- No write to `search_events` changes in this task.
- The transport stays stateless: this identifies a recall, never a session (ADR-018 T2, withdrawn 2026-08-22).

## Risks

- Returning an id that does not match the recorded row would make every future join silently wrong; the invariant above is asserted by the response test reading the same value the row holds.
- Accepting an argument that does nothing invites a reader to assume it does. Mitigated by the description, the ADR's trigger, and — since the amendment — by the span attribute, which makes "nothing is sending it" a fact rather than an assumption.

## Stop Condition

Stop and ask if surfacing the id from `Search` cannot be done without changing what `search_events` records — that is a migration and belongs with T3, not here.

## Out of Scope

- Recording the fetch against the recall (that is T3's job)
- `profile_id` on the durable row (deferred: `docs/adr/BACKLOG.md` §"From ADR-028")

## Verification Log
- 2026-08-25 · 84d6ecc* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:e9384385f841db4ae2fdd618d24241a38d4bf1632bfea1aa1e872213d7272575
  ```
  2026/08/25 16:14:19 OK   00021_search_events.sql (2.14ms)
  2026/08/25 16:14:19 OK   00022_drawer_anchors.sql (1.39ms)
  2026/08/25 16:14:19 OK   00023_search_events_repair.sql (2.08ms)
  2026/08/25 16:14:19 OK   00024_drawers_team_parent_idx.sql (1.22ms)
  2026/08/25 16:14:19 OK   00025_kg_valid_to_idx.sql (1.19ms)
  2026/08/25 16:14:19 goose: successfully migrated database to version: 25
  --- PASS: TestGetDrawerIgnoresAnUnknownSearchID (0.06s)
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	0.202s
  FAIL
  ```
- 2026-08-25 · 84d6ecc* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:e9384385f841db4ae2fdd618d24241a38d4bf1632bfea1aa1e872213d7272575
  ```
  2026/08/25 16:16:10 OK   00022_drawer_anchors.sql (2.08ms)
  2026/08/25 16:16:10 OK   00023_search_events_repair.sql (3.1ms)
  2026/08/25 16:16:10 OK   00024_drawers_team_parent_idx.sql (2.15ms)
  2026/08/25 16:16:10 OK   00025_kg_valid_to_idx.sql (2.91ms)
  2026/08/25 16:16:10 goose: successfully migrated database to version: 25
  FAIL
  FAIL	github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver	1.059s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/mcptest	19.942s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/palace	27.246s
  FAIL
  ```
- 2026-08-25 · 84d6ecc* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:e9384385f841db4ae2fdd618d24241a38d4bf1632bfea1aa1e872213d7272575
- 2026-08-25 · e9299bb* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:e9384385f841db4ae2fdd618d24241a38d4bf1632bfea1aa1e872213d7272575
- 2026-08-25 · 029deaf* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:b694e3648f8a87ad9e1c258fc2b402cfbd8fb7a8fac984bb763465f9eb66cc70
- 2026-08-25 · 029deaf* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:f7995d9626d2033a93221b4b3e507693f37af1249aa4e450bb9725be7fedb95c
- 2026-08-25 · 029deaf* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:f7995d9626d2033a93221b4b3e507693f37af1249aa4e450bb9725be7fedb95c
