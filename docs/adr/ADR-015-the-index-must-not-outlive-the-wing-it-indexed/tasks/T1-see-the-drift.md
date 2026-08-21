# Task ADR-015-T1: A command that reports where the index disagrees with the rows

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `store.VectorStore.PointsByIDs` (promoted from `SourceOfTruth`), `store.Hybrid.Halves`, `palace.IndexDrift`, and `agentsmemory doctor --index` — exit 1 when any point's payload wing disagrees with its drawer
**Consumes:** none
**Data dependency:** hermetic (its own migrated SQLite + in-memory index)

## Goal

An operator can ask whether the search index still describes the memories it indexes, and get an exit code rather than a paragraph.

This task comes first because it is the instrument. The fix in T3 is a write that returns `nil` whether or not it corrected anything; without a reader that goes to the index and looks, "the merge now patches payloads" is a claim about the code and not about the palace.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/doctor.go` | add | the subcommand, its flags and its report |
| `cmd/server/doctor_test.go` | add | a drifted point is reported and exits 1; a consistent one exits 0 |
| `cmd/server/main.go` | edit | register the subcommand — a command nothing registers is a command nobody can run |
| `internal/store/store.go` | edit | promote `PointsByIDs` from `SourceOfTruth` to `VectorStore` — only the durable store could be read, and the INDEX is what a scoped search filters on |
| `internal/store/hybrid.go` | edit | `Halves()`, so a checker can compare the two copies rather than use one |
| `internal/store/storetest/conformance.go` | add | one suite every backend runs, so a backend cannot satisfy the seam with a method body that does nothing |
| `cmd/server/main.go` | edit | extract `rootCommand` — the command list lived inside `main`, where no test could ask what is registered |
| `internal/store/qdrant/vector.go` | edit | retrieve by the derived point UUIDs |
| `internal/store/sqlitevec/sqlitevec.go` | edit | select the payload column by id |
| `internal/store/chromem/chromem.go` | edit | the local-default backend |
| `internal/palace/indexdrift.go` | add | the comparison itself, so both the command and a test can call it |
| `internal/palace/indexdrift_test.go` | add | drift is found in either store, and a clean palace reports none |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestIndexDriftIsFound`, `TestDoctorIndexExitsNonZeroOnDrift`. Commit them red.
2. Promote `PointsByIDs` to `store.VectorStore` and implement it for the backends that lacked it. An unknown id is omitted rather than an error, matching `Delete`'s contract, so a caller need not check existence first. A driver's internal payload keys — a reserved id, a JSON blob beside a flattened copy — must be hidden here exactly as they already are on `Search`, or the same point reads differently depending on which method fetched it.
3. `palace.IndexDrift(ctx, teamID)` walks the drawers and, for each, asks the index what wing it holds for that id. Report the count and a bounded sample — never the memory text, because a doctor report is pasted into issues.
4. Read BOTH stores where both exist: the SQLite source of truth and the search index. A repair that fixed one and not the other is the failure mode this must be able to see.
5. Wire the subcommand and make it exit 1 on any drift, 0 on none.
6. Falsify: point the report at a palace with a deliberately patched payload and confirm it goes red; delete the registration in `main.go` and confirm the command disappears from `--help`.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/store/... -run "Conformance|TestEveryBackendRunsTheConformanceSuite" -count=1 -v 2>&1 | tee /tmp/a15t1.out
  go test ./internal/palace/ -run "TestIndexDriftIsFound|TestIndexDriftIsSilentOnACleanPalace|TestIndexDriftReadsEveryStore" -count=1 -v 2>&1 | tee -a /tmp/a15t1.out
  go test ./cmd/server/ -run "TestDoctorIndexExitsNonZeroOnDrift|TestDoctorIsRegistered|TestDoctorRefusesWithNoCheckSelected" -count=1 -v 2>&1 | tee -a /tmp/a15t1.out
  grep -q -- "PASS: TestSQLiteVecRunsTheConformanceSuite/sqlitevec/PointsByIDs" /tmp/a15t1.out
  grep -q -- "PASS: TestQdrantRunsTheConformanceSuite/qdrant/PointsByIDs" /tmp/a15t1.out
  grep -q -- "PASS: TestChromemVecRunsTheConformanceSuite/chromemvec/PointsByIDs" /tmp/a15t1.out
  grep -q -- "PASS: TestHybridRunsTheConformanceSuite/hybrid/PointsByIDs" /tmp/a15t1.out
  grep -q -- "--- PASS: TestEveryBackendRunsTheConformanceSuite" /tmp/a15t1.out
  grep -q -- "--- PASS: TestIndexDriftReadsEveryStore" /tmp/a15t1.out
  grep -q -- "--- PASS: TestIndexDriftIsFound" /tmp/a15t1.out
  grep -q -- "--- PASS: TestIndexDriftIsSilentOnACleanPalace" /tmp/a15t1.out
  grep -q -- "--- PASS: TestDoctorIndexExitsNonZeroOnDrift" /tmp/a15t1.out
  grep -q -- "--- PASS: TestDoctorIsRegistered" /tmp/a15t1.out
  grep -q -- "--- PASS: TestDoctorRefusesWithNoCheckSelected" /tmp/a15t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a15t1.out
  go test ./cmd/server/ ./internal/palace/ ./internal/store/... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `Test<Backend>RunsTheConformanceSuite` | each backend's `conformance_test.go` | every backend reads a payload back by id, omits ids it does not hold, and returns EXACTLY the keys written | — |
| `TestEveryBackendRunsTheConformanceSuite` | `internal/store/storetest/registry_test.go` | every type declaring itself a `VectorStore` runs the suite, AND every backend the list claims is covered has a test that calls it | — |
| `TestIndexDriftReadsEveryStore` | `internal/palace/indexdrift_test.go` | both halves of a Hybrid are read — a drift in the source of truth alone survives a check that reads only the index, and returns on the next sync | — |
| `TestDoctorRefusesWithNoCheckSelected` | `cmd/server/doctor_test.go` | `doctor` with no check selected does not exit 0; a command that ran nothing must not read as a healthy palace | — |
| `TestIndexDriftIsFound` | `internal/palace/indexdrift_test.go` | a point whose payload wing differs from its drawer's is reported, in either store | — |
| `TestIndexDriftIsSilentOnACleanPalace` | `internal/palace/indexdrift_test.go` | a consistent palace reports nothing, so the check is not noise | — |
| `TestDoctorIndexExitsNonZeroOnDrift` | `cmd/server/doctor_test.go` | the exit code carries the verdict, not the prose | — |
| `TestDoctorIsRegistered` | `cmd/server/doctor_test.go` | the subcommand is reachable from the CLI — a command nothing registers is this repo's signature defect | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestIndexDriftIsFound` |
| 2 — something selects it | `TestDoctorIsRegistered` — the command appears in the CLI's own command list |
| 3 — the caller can discover it | it is a named subcommand with `--help`, and T3's acceptance runs it |
| 4 — it is used | T3 cannot be accepted without it |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| compare the drawer's wing against itself instead of the payload | yes | `TestIndexDriftIsFound` |
| one backend's `PointsByIDs` returns nothing rather than the stored payload | yes | that backend's `Test<Backend>RunsTheConformanceSuite` |
| a backend returns its internal payload keys to the caller | yes | the conformance suite (exact-keys assertion) |
| drop a backend from `coveredBackends` | yes | `TestEveryBackendRunsTheConformanceSuite` |
| keep a backend on the list and delete its conformance test | yes | `TestEveryBackendRunsTheConformanceSuite` |
| report drift but exit 0 | yes | `TestDoctorIndexExitsNonZeroOnDrift` |
| drop the `main.go` registration | yes | `TestDoctorIsRegistered` |
| read only the index, never the source of truth | yes | `TestIndexDriftIsFound` |

## Out of Scope

- Repairing the drift (deferred: T3 of this ADR — a reader that also writes cannot be trusted to report honestly about its own writes)
- Checking anything other than the wing — content, room, dimensions (deferred: docs/adr/BACKLOG.md)

## Invariants

- The report never prints memory text. It is pasted into issues.
- A clean palace produces no output and exit 0, so the command is safe to put in a cron.

## Risks

- A drift report that is expensive on a large palace becomes one nobody runs. Mitigated: it is one pass over drawer ids and one payload read per id, no embedding and no search.

## Stop Condition

Stop and ask if a backend cannot report a point's payload without a vector search — that would make the check a scan and needs a different design.

## Verification Log

- 2026-08-21 · f5c33c3* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
