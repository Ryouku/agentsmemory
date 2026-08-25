# Task ADR-029-T3: A stage list that is an identity, in both directions

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (two packages, one production edit)
**Owner:** unassigned
**Produces:** `StageEvidence` emitted unconditionally and declared in `SearchStages()`; a set-equality gate over the emitted and declared stage names
**Consumes:** none
**Data dependency:** hermetic

## Goal

The set of `am.search*` spans a real recall emits equals `telemetry.SearchStages()` exactly, in both directions, so a stage cannot be declared and unemitted or emitted and undeclared.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/service.go` | edit | `applyRerankWith` emits `StageEvidence` on its three bypass paths, matching the pattern `closet`, `recency` and `rerank` already follow |
| `internal/telemetry/telemetry.go` | edit | `SearchStages()` gains `StageEvidence`, making it eleven names |
| `internal/telemetry/span_test.go` | edit | `TestSearchStagesIsTheWiringList`'s `len(...) < 8` against ten declared names is an assertion two deletions survive |
| `internal/palace/otel_test.go` | edit | the presence loop gains its reverse direction; the hand-written `searchKids` literal is derived from `SearchStages()` instead of repeating it |

## Ordered Steps

1. **TDD red.** Rewrite `TestSearchEmitsSemanticStageSpans` to assert SET EQUALITY between the `am.search*` span names a real `Search` emits and `SearchStages()`. Add the reranker-configured case. Confirm it is red for the right reason — `am.search.evidence` emitted and undeclared — and record the failure.

   **Measured before writing, 2026-08-25.** A recorded probe over the default fixture returned `emitted-not-declared=[] declared-not-emitted=[]`; the same probe with `WithReranker(&fakeReranker{}, 50).WithRerankWeight(0.5)` returned `emitted-not-declared=[am.search.evidence]`. So the one-directional gate is green today by coincidence of fixture configuration, which is the whole finding.
2. Emit `StageEvidence` on every path through `applyRerankWith`, including the three bypasses (`no_reranker`, `empty`, `weight_zero`), with the reason mirroring rerank's. A stage that vanishes when its feature is off is indistinguishable from a stage that was deleted — which is exactly how this one stayed out of the list unnoticed — and unconditional emission makes its absence detectable in PRODUCTION, not only in a maximally-configured test fixture.
3. Add `StageEvidence` to `SearchStages()`.
4. Replace `TestSearchStagesIsTheWiringList`'s length threshold with a name-by-name identity check against the declared `Stage*` constants whose value carries the `am.search` prefix. The list must not be both the subject and the sole authority of its own check.
5. Derive `otel_test.go`'s `searchKids` parent/child list from `SearchStages()` rather than repeating eight of its ten names inline. Two declarations of "the Search stages" that reference neither each other nor any test are one drift away from masking a removal.
6. Run the acceptance fence and confirm it is green only after steps 2–5.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  apk add --no-cache bash git >/dev/null 2>&1 || true
  set -e
  gofmt -l cmd internal clients | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./internal/telemetry/ ./internal/palace/
  go test ./internal/telemetry/ -run "TestSearchStagesIsTheWiringList" -count=1 -v 2>&1 | tee /tmp/t3.out
  go test ./internal/palace/ -run "TestSearchEmitsSemanticStageSpans|TestEmittedSearchStagesAreAllDeclared" -count=1 -v 2>&1 | tee -a /tmp/t3.out
  grep -q -- "--- PASS: TestSearchStagesIsTheWiringList" /tmp/t3.out
  grep -q -- "--- PASS: TestSearchEmitsSemanticStageSpans" /tmp/t3.out
  grep -q -- "--- PASS: TestEmittedSearchStagesAreAllDeclared" /tmp/t3.out
  ! grep -qE "no tests to run|^FAIL" /tmp/t3.out
  go test ./internal/telemetry/ ./internal/palace/ ./internal/mcpserver/ ./internal/mcptest/ -count=1
'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSearchStagesIsTheWiringList` | `internal/telemetry/span_test.go` | every `Stage*` constant whose value starts `am.search` appears in `SearchStages()` — identity, not length | — |
| `TestSearchEmitsSemanticStageSpans` | `internal/palace/otel_test.go` | declared→emitted, under both the default and the reranker-configured fixture | — |
| `TestEmittedSearchStagesAreAllDeclared` | `internal/palace/otel_test.go` | emitted→declared: the direction that did not exist, and the one that would have caught `StageEvidence` | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `StageEvidence` has one producer and, before this task, zero test references anywhere in the tree |
| 2 — something selects it | the three bypass emissions — mutation: delete one and the reverse gate goes red under the corresponding fixture |
| 3 — the caller can discover it | `SearchStages()` is the declared list a trace reader and every gate consult; the identity check is what makes it authoritative rather than decorative |
| 4 — it is used | `am.search.evidence` present with an outcome on every recall in the deployed container's trace, including recalls with no reranker configured — which is most of them |

## Mutation Log

_(populated by `adr-verify --mutant` during execution)_

## Invariants

- No ranking changes. The evidence span's bypass emissions carry no decision; they report one that was already made.
- `SearchStages()` stays a plain list, not a predicate. Unconditional emission is what allows that — a conditionally-emitted member would force the gate to know the fixture's configuration, reintroducing the coupling that let the stage hide.
- The gate is measured against the current tree before shipping: both directions must be empty today under the default fixture, or it is crying wolf rather than catching something.

## Risks

- **Set equality is a stricter gate and changes a workflow.** Adding a stage now fails the gate until the stage is declared. That is the intent, and it is stated here so the failure reads as designed rather than as a bug.
- A future stage that legitimately cannot emit on some path would break the identity. If that case arrives, the answer is an explicit excused-list with a reason per entry — the pattern `TestEveryHitFieldIsOnTheWireOrExcused` already uses in this tree — not a return to a length threshold.

## Stop Condition

Stop and ask if emitting `StageEvidence` unconditionally turns out to change the parent/child shape assertions in a way that cannot be expressed by deriving `searchKids` from the list. That would mean the two declarations encode genuinely different things, which is a finding, not a merge conflict.

## Out of Scope

- Any stage outside the `am.search*` prefix. `am.tool`, `am.kg.*`, `am.graph.*` and `am.drawer.*` have their own reachability questions and are not covered by `SearchStages()`.
- An anchor/staleness stage. The sweep found the anchor pass has no span at all; adding one is a new stage, not a list repair, and it is receipted.

## Verification Log
