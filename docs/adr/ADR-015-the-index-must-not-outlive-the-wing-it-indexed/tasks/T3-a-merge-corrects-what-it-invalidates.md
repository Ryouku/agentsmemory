# Task ADR-015-T3: A merge corrects the index it invalidates

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** a `MergeWing` that leaves no drift
**Consumes:** `store.VectorStore.SetPayload` (T2), the drift report (T1)
**Data dependency:** hermetic for the test; the live palace's existing 13 drifted points are repaired by running the shipped path

## Goal

After a wing merge, a recall scoped to the target wing returns the memories that were merged into it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/admin.go` | edit | patch the payloads of the relabelled ids; delete the doc comment that asserts this is unnecessary |
| `internal/palace/admin_test.go` | edit | the end-to-end property: merge, then find it by searching the target wing |

## Ordered Steps

1. Write the failing test first (TDD red): `TestMergedMemoryIsFoundInTheTargetWing` — add a drawer to wing A, merge A into B, search scoped to B, require the drawer. Commit it red. This is the user-visible property; assert it, not the number of payload writes.
2. Add `TestMergeLeavesNoIndexDrift`, using T1's `palace.IndexDrift` — the check that reads the store rather than trusting the write.
3. In `MergeWing`, after `RelabelDrawerWing` succeeds, collect the relabelled ids and call `SetPayload` with `{"wing": target}` on both the index and the source of truth.
4. **Delete** the doc comment claiming the payload is advisory. It is the reason the bug exists, and softening it leaves the next reader the same false premise.
5. A payload patch that fails must fail the merge loudly rather than leaving rows relabelled over a stale index — the half-done state is the one nobody can see.
6. Falsify: skip the patch and watch both tests go red; patch the index and not the source of truth, and watch `TestMergeLeavesNoIndexDrift` still go red.
7. Run the acceptance command.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; 
  set -e
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./internal/palace/ -run "TestMergedMemoryIsFoundInTheTargetWing|TestMergeLeavesNoIndexDrift|TestMergeFailsLoudlyWhenTheIndexCannotBeCorrected" -count=1 -v 2>&1 | tee /tmp/a15t3.out
  grep -q -- "--- PASS: TestMergedMemoryIsFoundInTheTargetWing" /tmp/a15t3.out
  grep -q -- "--- PASS: TestMergeLeavesNoIndexDrift" /tmp/a15t3.out
  grep -q -- "--- PASS: TestMergeFailsLoudlyWhenTheIndexCannotBeCorrected" /tmp/a15t3.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a15t3.out
  go test ./... -count=1'
```

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestMergedMemoryIsFoundInTheTargetWing` | `internal/palace/admin_test.go` | the user-visible property: a merged memory is recallable from the wing it was merged into | — |
| `TestMergeLeavesNoIndexDrift` | `internal/palace/admin_test.go` | read back from the store rather than trusting the write's nil return | — |
| `TestMergeFailsLoudlyWhenTheIndexCannotBeCorrected` | `internal/palace/admin_test.go` | a failed correction fails the merge — relabelled rows over a stale index is the state nobody can see | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestMergeLeavesNoIndexDrift` |
| 2 — something selects it | `MergeWing` calls it; removing the call fails both tests |
| 3 — the caller can discover it | nothing to discover — the correction is not optional and has no knob |
| 4 — it is used | every `am_merge_wing` and every dashboard merge takes this path |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| skip the payload patch entirely | yes | both |
| patch the index but not the source of truth | yes | `TestMergeLeavesNoIndexDrift` |
| patch with the source wing instead of the target | yes | `TestMergedMemoryIsFoundInTheTargetWing` |
| swallow the patch error and return success | yes | `TestMergeFailsLoudlyWhenTheIndexCannotBeCorrected` |
| collect the moved ids AFTER the relabel, when nothing distinguishes them | yes | both drift tests |

## Out of Scope

- Repairing an already-drifted palace automatically at startup (permanent: a silent write to somebody's index on boot is worse than a check they run; T1's report tells them, and a merge of the wing into itself repairs it)
- The derived graph a merge also invalidates (deferred: docs/adr/ADR-016-a-memory-an-agent-files-must-be-navigable.md)

## Invariants

- After any successful `MergeWing`, `palace.IndexDrift` reports zero.
- A failed payload patch fails the merge; rows relabelled over a stale index is never a state the caller is left in silently.

## Risks

- A very large merge makes many payload writes. Mitigated: they are batched by id the same way `deleteBatch` bounds a wing delete, and they are payload-only.

## Stop Condition

Stop and ask if a merge can interleave with a concurrent write to the same drawers — the relabel and the patch would need one transaction, which the store interface cannot express.

## Verification Log

- 2026-08-21 · 534aea2* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-21 · 534aea2* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
- 2026-08-25 · 8c3167d* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …` · acceptance-sha256:3a7adbd3a2d618f54822983c5c32fc18504d3297abc5e096115dd8d3b004cd04
  ```
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant	0.013s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec	3.641s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest	0.014s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/telemetry	0.003s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/tenant	0.476s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/usage	0.006s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web	0.018s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/web/views	0.010s
  ok  	github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle	0.006s
  FAIL
  ```
- 2026-08-25 · 8c3167d* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c 'apk add --no-cache bash git >/dev/null 2>&1 || true; …` · acceptance-sha256:5c62d7426867ce3dfbb6c3a6e89b438d1b8aea44257546c1a446b7c23b237e6e

## Mutation Log
- 2026-08-25 · 8c3167d* · mutant killed · exit 1 · `internal/palace/admin.go` · a merge is not complete until every index that filters on wing agrees with the relabelled rows; patching the drawer points to an empty wing leaves the index disagreeing with the rows the merge just moved, which TestMergeLeavesNoIndexDrift pins · acceptance-sha256:5c62d7426867ce3dfbb6c3a6e89b438d1b8aea44257546c1a446b7c23b237e6e
