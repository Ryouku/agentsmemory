# Task ADR-006-T2: Discover which knobs are inert in which mode, by running them

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the discovered inert set — `(knob, gating knob, gating value)` triples computed per run
**Consumes:** `configureRanking` (T1)
**Data dependency:** hermetic — a seeded in-memory corpus and a deterministic fake embedder; no network

## Goal

A test sweeps the ranking knobs over the real wiring and computes which are inert under which mode, using the two-part predicate, with zero findings on any shipped configuration.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/modescope_test.go` | add | the sweep, the predicate, and the corpus fixture |
| `cmd/server/wiring_test.go` | edit | export nothing new; this task only reuses `configFields`, `repoRoot`, `goFilesUnder` |

## Ordered Steps

1. Write the failing test first (TDD red): `TestModeScopedKnobsAreDiscovered`, asserting the sweep finds the known case — `--bm25-weight` inert under `--fusion=rrf`. Commit it red.
2. Build the fixture: one seeded corpus (~24 drawers, deliberate term overlap so BM25 and vector disagree), shallow-copied per cell. 24 must exceed `limit*hybridCandidateMultiplier` or every cell sees the same candidates and nothing can differ.
3. Implement part 1 of the predicate — **K is live at baseline**. Vary K alone from `config.Default()` and require the result ordering to change. A knob that fails this is recorded as `inert at baseline` and is NOT attributed to any other knob.
4. Implement part 2 — **K is inert when D is set**. Only for knobs that passed part 1.
5. Assert the withdrawn one-part predicate stays withdrawn: `TestBaselineInertKnobsAreNotAttributed` requires `--rerank-pool`, `--rerank-weight` and `--rerank-timeout` to appear in the `inert at baseline` category and in NO mode-scoped pair, because `config.Default()` leaves `RerankURL` empty. This is the pre-registered falsification from the ADR: 13 cells were misattributed by the one-part version, one of them the shipped stack.
6. Record the wall-clock in the test output and fail above 90s — a sweep nobody waits for is a sweep nobody runs.
7. Falsify: drop part 1 of the predicate (the mutant must reproduce the misattribution and turn `TestBaselineInertKnobsAreNotAttributed` red); shrink the corpus below the candidate multiplier; make the baseline vary two knobs at once.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestModeScopedKnobsAreDiscovered|TestBaselineInertKnobsAreNotAttributed" -count=1 -v 2>&1 | tee /tmp/t2.out
  grep -q -- "--- PASS: TestModeScopedKnobsAreDiscovered" /tmp/t2.out
  grep -q -- "--- PASS: TestBaselineInertKnobsAreNotAttributed" /tmp/t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/t2.out
  go test ./cmd/server/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestModeScopedKnobsAreDiscovered` | `cmd/server/modescope_test.go` | the sweep finds `--bm25-weight` inert under `--fusion=rrf` without being told | — |
| `TestBaselineInertKnobsAreNotAttributed` | `cmd/server/modescope_test.go` | a knob inert at baseline is never blamed on an unrelated knob — the 13-cell false alarm cannot come back | — |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop part 1 (liveness at baseline) from the predicate | yes | `TestBaselineInertKnobsAreNotAttributed` |
| seed fewer drawers than `limit*hybridCandidateMultiplier` | yes | `TestModeScopedKnobsAreDiscovered` |
| vary two knobs per cell instead of one | yes | `TestBaselineInertKnobsAreNotAttributed` |

## Out of Scope

- Knobs needing a live Qdrant, TEI or OAuth issuer (permanent: unobservable here; T3 names them so the silence is deliberate rather than accidental)
- Value-range validation (deferred: docs/adr/BACKLOG.md)

## Invariants

- The inert set is computed, never declared. No list of pairs exists in the tree.
- A knob is attributed to a gating knob only when it is live at baseline.

## Risks

- The sweep grows quadratically with knob count. Mitigated: it sweeps ranking knobs only, and the budget assertion in step 6 fails loudly rather than degrading quietly.

## Stop Condition

Stop and ask if any shipped configuration produces a finding — that means the predicate is still wrong, and shipping a gate that fires on the default stack is the failure this ADR exists to avoid.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
