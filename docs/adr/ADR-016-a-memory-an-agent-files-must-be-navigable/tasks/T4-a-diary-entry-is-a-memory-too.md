# Task ADR-016-T4: A diary entry is a memory too

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** `drawers.entities` populated by `Service.WriteDiary`
**Consumes:** T2's extractor and its corrected lexicon
**Data dependency:** hermetic

## Goal

A diary entry participates in the derived graph exactly as an added memory does.

T2 fixed `Service.Add` and left `Service.WriteDiary` untouched, because the ADR scoped half 1 to `Add` and widening it silently would have stopped it being a decision. The consequence was measured the day T2 landed: **119 of 383 drawers on the live palace are in diary rooms, so 31% of the corpus stays outside the graph.** Half a feature is not the feature.

Diary entries are also the RICHEST source the graph could have. They are where a session records what it decided and which systems it touched, which is exactly the co-occurrence a hallway is made of — an ordinary memory names one thing, a session summary names the six that met.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | stamp `Entities: extractEntities(c.Content)` on each chunk `WriteDiary` builds |
| `internal/palace/diary_test.go` | edit | a diary entry feeds the graph, asserted end to end rather than by reading the field |

## Ordered Steps

1. Write the failing test first (TDD red): `TestHallwaysDeriveFromADiaryEntry` — write a diary entry naming two systems repeatedly, recompute, require a hallway. Commit it red.
2. Set the field in `WriteDiary`'s chunk loop, using the same `extractEntities` `Add` uses. One expression, and deliberately the same one: two extractors would drift, and the lexicon T2 corrected is the thing that makes either usable.
3. Per CHUNK, matching `Add` and mining, so a long entry does not give every chunk the whole entry's entities.
4. Falsify: remove the assignment and watch only the new test go red — the proof that `Add`'s tests never covered this path either.
5. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestHallwaysDeriveFromADiaryEntry|TestHallwaysDeriveFromDrawersAnAgentFiled|TestGraphHallwaysAndEntityTunnels" -count=1 -v 2>&1 | tee /tmp/a16t4.out
  grep -q -- "--- PASS: TestHallwaysDeriveFromADiaryEntry" /tmp/a16t4.out
  grep -q -- "--- PASS: TestHallwaysDeriveFromDrawersAnAgentFiled" /tmp/a16t4.out
  grep -q -- "--- PASS: TestGraphHallwaysAndEntityTunnels" /tmp/a16t4.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a16t4.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestHallwaysDeriveFromADiaryEntry` | `internal/palace/diary_test.go` | the diary path feeds the graph — the third producer, and the one holding 31% of a real corpus | — |
| `TestHallwaysDeriveFromDrawersAnAgentFiled` | `internal/palace/graph_test.go` | the `Add` path still does | — |
| `TestGraphHallwaysAndEntityTunnels` | `internal/palace/graph_test.go` | the mining path still does | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `extractEntities`, already corrected by T2 |
| 2 — something selects it | `WriteDiary` calls it — the missing line |
| 3 — the caller can discover it | nothing to discover; every `am_diary_write` takes this path |
| 4 — it is used | `RecomputeGraph` derives hallways from what it writes, asserted end to end |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop the assignment | yes | `TestHallwaysDeriveFromADiaryEntry` only — the proof the other two paths' tests are blind to this one |
| extract from the whole entry rather than the chunk | yes | `TestHallwaysDeriveFromADiaryEntry` (every chunk would carry the whole entry's entities) |

## Out of Scope

- Backfilling diary entries filed before this (deferred: docs/adr/BACKLOG.md — the same backfill the ADR already defers for drawers, and it is one job not two)
- Any further change to the extractor's lexicon (permanent: T2 owns it, measured it, and a second opinion arrived at by taste rather than measurement is how it got wrong the first time)

## Invariants

- One extractor, shared by all three write paths. Two would drift.
- Entities are derived per chunk.

## Risks

- Diary entries are long and name many things, so they may produce most of the graph's hallways and crowd out those from ordinary memories. Mitigated: `doctor --graph` reports per wing before and after, so the shape of what arrives is visible rather than assumed.

## Stop Condition

Stop and ask if diary entries turn out to produce an order of magnitude more hallways than every other source combined — that is a graph dominated by one producer, and whether it is a feature or a distortion is a decision rather than a detail.

## Verification Log

- 2026-08-21 · 4edbfe5* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:4b4645f7137d9adad6d7cae3fb7a8c00c066323e67434bf2039a5b3c4bbad915

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/palace/service.go` · a diary entry is a memory too, and the diary was the last write path filing rows the derived graph never saw; stamping nil entities on that path restores exactly the gap ADR-016 T4 closed, so hallways stop deriving from the richest source the graph has · acceptance-sha256:4b4645f7137d9adad6d7cae3fb7a8c00c066323e67434bf2039a5b3c4bbad915
