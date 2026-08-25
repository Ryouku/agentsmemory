# Task ADR-007-T3: A run states its case set, and `BEST` states what it is best over

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `CaseSetID`, `CaseSetOrigin`, the labelled `BEST`, and the resolved ranking profile in the run record
**Consumes:** none
**Data dependency:** hermetic

## Goal

Every table and run record carries a case-set id, a run that generated its own questions says so where the `BEST` label is read, and the record names the ranking it was taken under.

The ranking half was added to this task after a reviewer found it: the run record carried the closet scale, the BM25 weight and the rerank settings and named neither the fusion nor the lexical normaliser, so two runs at the same commit — one on rrf, one on linear — produced different numbers and identical records. That is this task's own defect one level down: a run that does not state what it measured.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/eval.go` | edit | stamp the id; print it in the header and beside `BEST`; write it and the resolved ranking profile into the run record |
| `cmd/server/eval_test.go` | edit | the id is content-derived and order-independent; generated runs are labelled; the record names the ranking |
| `internal/palace/eval.go` | edit | `CaseSetID`, and `EvalReport` carries it so the record and the table cannot disagree |
| `internal/palace/eval_test.go` | edit | the production evaluator actually stamps it — the reachability half |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestCaseSetIDIsContentDerived` and `TestGeneratedRunSaysSoBesideBest`. Commit them red.
2. Derive the id from the sorted case content — query plus expected ids — not from the file, its path or its order. Two orderings of one set must agree, or replaying a file produces a different id from the run that wrote it.
3. Stamp `CaseSetOrigin`: `replayed` when `--cases` supplied an existing file, `generated` otherwise.
4. Print it in the header and next to the `BEST` verdict. Four runs labelled a winner with nothing saying the questions had changed, and the labels were read across runs as agreement; the label is where the reader is, so the caveat goes there.
5. Write both into the `.cells.json` run record.
6. Record the RESOLVED ranking profile (`Service.RankingProfile()`), not the requested config: a reranker that was configured and did not come up is exactly the case where the two differ, and only the resolved one describes what ranked.
7. Falsify: hash the file path instead of the content; make the id order-dependent; hash a nil `ExpectAny` differently from an empty one; drop the origin from the `BEST` line; stamp the id in the record and not the table; delete the stamp from `EvaluateWith`; stop assigning `Ranking` at the call site.
8. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestCaseSetIDIsContentDerived|TestGeneratedRunSaysSoBesideBest|TestRunRecordCarriesTheCaseSet|TestRunRecordNamesTheRankingItMeasured" -count=1 -v 2>&1 | tee /tmp/a3.out
  go test ./internal/palace/ -run "TestEvaluateStampsTheCaseSetItScored" -count=1 -v 2>&1 | tee -a /tmp/a3.out
  grep -q -- "--- PASS: TestCaseSetIDIsContentDerived" /tmp/a3.out
  grep -q -- "--- PASS: TestGeneratedRunSaysSoBesideBest" /tmp/a3.out
  grep -q -- "--- PASS: TestRunRecordCarriesTheCaseSet" /tmp/a3.out
  grep -q -- "--- PASS: TestRunRecordNamesTheRankingItMeasured" /tmp/a3.out
  grep -q -- "--- PASS: TestEvaluateStampsTheCaseSetItScored" /tmp/a3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a3.out
  go test ./cmd/server/ ./internal/palace/ -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestCaseSetIDIsContentDerived` | `cmd/server/eval_test.go` | two orderings of one case set produce one id; a nil and an empty `ExpectAny` agree; a changed question changes it; save-then-replay reproduces it | — |
| `TestGeneratedRunSaysSoBesideBest` | `cmd/server/eval_test.go` | the `BEST` LINE — not the header — carries the id and the origin, and a replayed run is not labelled generated | — |
| `TestRunRecordCarriesTheCaseSet` | `cmd/server/eval_test.go` | the id reaches `.cells.json` EQUAL to the report's, not merely present | — |
| `TestRunRecordNamesTheRankingItMeasured` | `cmd/server/eval_test.go` | the record names the resolved ranking, and the eval assigns it from `RankingProfile()` rather than leaving the field for tests to fill by hand | — |
| `TestEvaluateStampsTheCaseSetItScored` | `internal/palace/eval_test.go` | the PRODUCTION evaluator stamps the report it returns — every cmd/server test builds its report by hand and passes without this | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestCaseSetIDIsContentDerived` |
| 2 — something selects it | `TestEvaluateStampsTheCaseSetItScored` — the evaluator calls it; deleting the stamp left every cmd/server test green, because each builds its own report |
| 3 — the caller can discover it | the `BEST` label is where a reader looks, so the id and origin are printed there rather than in a header they skip |
| 4 — it is used | four runs were compared across different question sets before this existed; the id is what makes that visible next time |

## Mutants

Each was applied to the real source, confirmed to COMPILE (`go vet` clean), and run.

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| drop `sort.Strings(lines)` so the id is order-dependent | yes | `TestCaseSetIDIsContentDerived` |
| hash a nil `ExpectAny` differently from an empty one | yes | `TestCaseSetIDIsContentDerived` |
| omit the id and origin from the `BEST` line, leaving them in the header | yes | `TestGeneratedRunSaysSoBesideBest` |
| write `""` for `case_set_id` in the run record | yes | `TestRunRecordCarriesTheCaseSet` |
| stop assigning `Ranking` at the eval call site | yes | `TestRunRecordNamesTheRankingItMeasured` |
| delete the stamp from `EvaluateWith` (`_ = CaseSetID` keeps it compiling) | yes | `TestEvaluateStampsTheCaseSetItScored` — and NOTHING else. This mutant survived the first four tests and is why the fifth exists. |

## Out of Scope

- Refusing to run without `--cases` (permanent: the generate-then-save flow is how a case set first exists; a tool that needs a file that does not exist yet is one nobody can start)
- Comparing two runs automatically by id (deferred: docs/adr/BACKLOG.md — this task makes incomparability visible; acting on it is a separate decision)
- Writing a run record for a run that named no case file (deferred: docs/adr/BACKLOG.md)

  `cellsPath("")` is empty, so a generated run writes no `.cells.json` at all and there is no stem
  to derive one from. Inventing a path writes files the operator did not ask for; until a `--record`
  flag exists the printed table is the only surface a generated run has, which is exactly why the id
  is printed on the `BEST` line rather than only written to disk.

## Invariants

- The id in the table and the id in the run record are always the same value — and note that a run given no `--cases` writes no record at all, so this binds only the runs that have one. See Out of Scope.
- Replaying a case file reproduces the id of the run that wrote it.
- The id is a one-way hash. It is the only entry in the committed run record derived from palace content, and it is admissible for that reason: it identifies a question set to anyone who already holds it and discloses nothing to anyone who does not.

## Risks

- An id that changes on a cosmetic difference trains people to ignore it. Mitigated: content-derived and order-independent, pinned by the first test.

## Stop Condition

Stop and ask if the case set can change mid-run — that would make a single id a lie about part of the table and needs a different design.

## Verification Log

- 2026-08-21 · 754c60b* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:641aa6f3c541df386184faa943c8f93c3d6d369d4ae0ce4e5fa8337998c697b2

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/palace/eval.go` · CaseSetID sorts its per-case lines so that reordering a case set cannot change the id; without the sort the id becomes order-dependent, which is precisely what TestCaseSetIDIsContentDerived exists to reject · acceptance-sha256:641aa6f3c541df386184faa943c8f93c3d6d369d4ae0ce4e5fa8337998c697b2
