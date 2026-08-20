# Task ADR-007-T3: A run states its case set, and `BEST` states what it is best over

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `CaseSetID`, `CaseSetOrigin`, and the labelled `BEST`
**Consumes:** none
**Data dependency:** hermetic

## Goal

Every table and run record carries a case-set id, and a run that generated its own questions says so where the `BEST` label is read.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | stamp the id; print it in the header and beside `BEST`; write it into the run record |
| `cmd/server/eval_test.go` | edit | the id is content-derived and order-independent; generated runs are labelled |
| `internal/palace/eval.go` | edit | `EvalReport` carries the id so the record and the table cannot disagree |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCaseSetIDIsContentDerived` and `TestGeneratedRunSaysSoBesideBest`. Commit them red.
2. Derive the id from the sorted case content — query plus expected ids — not from the file, its path or its order. Two orderings of one set must agree, or replaying a file produces a different id from the run that wrote it.
3. Stamp `CaseSetOrigin`: `replayed` when `--cases` supplied an existing file, `generated` otherwise.
4. Print it in the header and next to the `BEST` verdict. Four runs labelled a winner with nothing saying the questions had changed, and the labels were read across runs as agreement; the label is where the reader is, so the caveat goes there.
5. Write both into the `.cells.json` run record.
6. Falsify: hash the file path instead of the content; drop the origin from the `BEST` line; stamp the id in the record and not the table.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestCaseSetIDIsContentDerived|TestGeneratedRunSaysSoBesideBest|TestRunRecordCarriesTheCaseSet" -count=1 -v 2>&1 | tee /tmp/a3.out
  grep -q -- "--- PASS: TestCaseSetIDIsContentDerived" /tmp/a3.out
  grep -q -- "--- PASS: TestGeneratedRunSaysSoBesideBest" /tmp/a3.out
  grep -q -- "--- PASS: TestRunRecordCarriesTheCaseSet" /tmp/a3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a3.out
  go test ./cmd/server/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCaseSetIDIsContentDerived` | `cmd/server/eval_test.go` | two orderings of one case set produce one id; a changed question changes it | — |
| `TestGeneratedRunSaysSoBesideBest` | `cmd/server/eval_test.go` | a generated run labels `BEST` with its case-set origin | — |
| `TestRunRecordCarriesTheCaseSet` | `cmd/server/eval_test.go` | the id reaches `.cells.json`, not only the terminal | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCaseSetIDIsContentDerived` |
| 2 — something selects it | `TestRunRecordCarriesTheCaseSet` — stamped in the record AND in the table, or the two disagree |
| 3 — the caller can discover it | the `BEST` label is where a reader looks, so the origin is printed there rather than in a header they skip |
| 4 — it is used | four runs were compared across different question sets before this existed; the id is what makes that visible next time |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| hash the file path rather than the case content | yes | `TestCaseSetIDIsContentDerived` |
| omit the origin from the `BEST` line | yes | `TestGeneratedRunSaysSoBesideBest` |
| stamp the record but not the table | yes | `TestRunRecordCarriesTheCaseSet` |

## Out of Scope

- Refusing to run without `--cases` (permanent: the generate-then-save flow is how a case set first exists; a tool that needs a file that does not exist yet is one nobody can start)
- Comparing two runs automatically by id (deferred: docs/adr/BACKLOG.md — this task makes incomparability visible; acting on it is a separate decision)

## Invariants

- The id in the table and the id in the run record are always the same value.
- Replaying a case file reproduces the id of the run that wrote it.

## Risks

- An id that changes on a cosmetic difference trains people to ignore it. Mitigated: content-derived and order-independent, pinned by the first test.

## Stop Condition

Stop and ask if the case set can change mid-run — that would make a single id a lie about part of the table and needs a different design.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
