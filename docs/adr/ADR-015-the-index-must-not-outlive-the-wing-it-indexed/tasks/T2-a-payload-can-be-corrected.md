# Task ADR-015-T2: A stored point's payload can be corrected without its vector

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `store.VectorStore.SetPayload` (promoted from `qdrant.Client`), and a `sync --repair-payload` that writes both stores
**Consumes:** none
**Data dependency:** hermetic

## Goal

Every vector backend can change a stored point's payload fields without being handed the vector again.

The vector is already correct after a wing relabel — the text did not change — so the only mechanism that should be needed is a payload write. Without this, correcting a label costs either an embedding call per drawer or a read-modify-write of every vector.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/store/store.go` | edit | the interface method and the contract its implementations must honour |
| `internal/store/qdrant/qdrant.go` | edit | widen the EXISTING `SetPayload` to the seam's signature and guard the reserved key |
| `internal/store/sqlitevec/sqlitevec.go` | edit | the source of truth's payload column |
| `internal/store/chromemvec/chromemvec.go` | edit | the local-default backend — BOTH copies of its payload |
| `internal/store/hybrid.go` | edit | patch both halves, source of truth first |
| `internal/store/storetest/conformance.go` | edit | one suite every backend runs, so a backend cannot silently skip the new method |
| `cmd/server/sync.go` | edit | `--repair-payload` writes through the Hybrid, so a plain `sync` no longer undoes it |

## Ordered Steps

1. Write the failing conformance test first (TDD red): `TestSetPayloadPatchesWithoutTouchingTheVector`, run against every backend through the shared suite. Commit it red.
2. Add `SetPayload(ctx, namespace string, ids []string, patch map[string]string) error` to `store.VectorStore`. An empty id list is a no-op; an unknown id is ignored, matching `Delete`'s contract so callers need no existence check.
3. Implement it per backend. The patch MERGES: fields not named are left alone, or a caller correcting `wing` would silently erase `room`.
4. Pin that the vector survives: search for the point after the patch and assert it is still the nearest neighbour of its own vector, or a backend that reinserts with a zero vector passes a payload-only assertion.
5. Pin that the patch changes what a FILTERED search matches, not only what a read returns. A backend may hold the payload twice — verbatim for readers, flattened so the index can filter — and patching only the readable copy leaves every scoped query matching the old value, which is this ADR's own bug reproduced one layer down. It survived the first version of this suite.
6. Falsify: make one backend's implementation a no-op returning nil; make another replace the payload instead of merging it; patch one copy of a two-copy payload; make the Hybrid write only the index.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/store/... -run "SetPayloadConformanceSuite|TestEveryBackendRunsTheConformanceSuite" -count=1 -v 2>&1 | tee /tmp/a15t2.out
  grep -q -- "PASS: TestSqlitevecRunsTheSetPayloadConformanceSuite/sqlitevec/SetPayload" /tmp/a15t2.out
  grep -q -- "PASS: TestQdrantRunsTheSetPayloadConformanceSuite/qdrant/SetPayload" /tmp/a15t2.out
  grep -q -- "PASS: TestChromemvecRunsTheSetPayloadConformanceSuite/chromemvec/SetPayload" /tmp/a15t2.out
  grep -q -- "PASS: TestHybridRunsTheSetPayloadConformanceSuite/hybrid/SetPayload" /tmp/a15t2.out
  grep -q -- "--- PASS: TestEveryBackendRunsTheConformanceSuite" /tmp/a15t2.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a15t2.out
  go test ./internal/store/... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `Test<Backend>RunsTheSetPayloadConformanceSuite` | each backend's `conformance_test.go` | the shared suite, per backend: the patch merges, the vector survives, a FILTERED search follows the patch, unknown ids and empty inputs are no-ops | — |
| `TestEveryBackendRunsTheConformanceSuite` | `internal/store/storetest/registry_test.go` | every implementation runs BOTH halves of the suite — read and write — and every backend the list claims is covered has a test that calls them | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSetPayloadPatchesWithoutTouchingTheVector` |
| 2 — something selects it | the conformance suite runs it per backend, not once against a favourite |
| 3 — the caller can discover it | it is on the interface every caller already holds |
| 4 — it is used | T3 is its consumer; until T3 lands this task ships an unused method, and that is stated here rather than discovered later |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| replace the whole payload instead of merging | yes | the suite, on every backend |
| patch a two-copy payload's readable half and not the filterable one | yes | the suite's filtered-search assertion — and it SURVIVED the first version, which had none |
| the Hybrid patches only the index | yes | the suite, through the Hybrid |
| a backend runs only the read half of the suite | yes | `TestEveryBackendRunsTheConformanceSuite` |

## Out of Scope

- Deleting a payload field (permanent: nothing needs it, and a merge-only contract cannot erase by accident)
- Patching payloads in bulk by filter rather than by id (deferred: docs/adr/BACKLOG.md)

## Invariants

- A patch never changes a point's vector.
- A patch merges; a field not named is unchanged.

## Risks

- A backend without a native payload-patch API forces a read-modify-write, which is not atomic. Mitigated: the only writer is a merge, which is already serialized, and the drift report from T1 makes a partial write visible.
- An equivalent mutant worth recording rather than chasing: dropping the empty-patch guard makes Qdrant receive a POST that merges nothing. It is a wasted HTTP call, not a wrong answer, and no assertion can distinguish it — so it is named here instead of being counted as a surviving mutant.

## Stop Condition

Stop and ask if a backend has no way to write a payload without the vector AND no way to read the vector back — that would mean the fallback is an embedding call and the ADR's cost argument no longer holds.

## Verification Log

- 2026-08-21 · 4e944e5* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
