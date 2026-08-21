---
date: 2026-08-21
category: data-integrity
severity: critical
files_changed:
  - internal/palace/admin.go
  - internal/palace/indexdrift.go
  - internal/store/store.go
  - cmd/server/doctor.go
tags: [search, index-drift, merge, silent-failure, doc-comment-was-wrong, found-by-telemetry]
---

## Symptom

Thirteen memories were filed correctly, indexed, returned by an unscoped search — and returned by
NOTHING when an agent searched the wing they were actually filed in. Scoped recall is the default,
so for the agent that owned those memories they did not exist.

There was no error, no warning, no log line and no failed request. The palace reported the right
drawer counts, the index held exactly one point per drawer, and every health check passed.

A second symptom, which is what led to it: a recall scoped to a wing holding nothing came back with
`candidates=4, hits=0` — the index matched four points for a wing with no drawers, and the caller
paid four of a ten-candidate pool for an empty page.

## Context

The palace keeps a vector for every memory in two places: SQLite is the source of truth, Qdrant is
the derived search index. Both store a payload beside the vector, and the payload carries the wing.

`Service.Search` passes the caller's wing to the index as a filter, so the index answers "which
points are in this wing" from the PAYLOAD. It then re-checks each survivor against the drawer row,
which is described in the code as a belt-and-braces guard against a stale index.

`MergeWing` folds one or more wings into another by relabelling `drawers.wing` in place. Drawer ids
are deliberately unchanged.

## Root Cause

The merge relabelled the rows and left every stored payload alone. Its doc comment said so, as a
justification:

> Vectors are not re-written: their payload wing is advisory (search filters on the drawer row's
> wing), so a merge needs no re-embedding.

The parenthesis is false, and everything else followed from it. Search filters on the payload FIRST:

```go
// Service.Search — the wing goes to the INDEX as a filter…
hits, err := s.vectors.Search(ctx, teamID, vec, candidateK, searchFilter(q))
…
// …and this loop can only REMOVE what the index already nominated.
for _, h := range hits {
	d, ok := rows[h.ID]
	if !ok {
		continue
	}
	if q.Wing != "" && d.Wing != q.Wing {
		continue
	}
	…
}
```


The drawer-row comparison that follows can only remove candidates — it iterates the points the index
returned — so it can never add back a memory the index declined to return. That makes the payload
the primary filter and the row check a second opinion, which is the exact opposite of what the
comment claims.

So after a merge, each affected memory was:

- **retrievable from the wing it had left**, because the payload still said so — and then dropped by
  the row check, which is the `candidates=4, hits=0` above; and
- **unreachable from the wing it now lived in**, because the index filter never nominated it.

Two writes were needed and one was made. The one that was skipped was skipped because a comment
said it did not matter.

## Investigation

Not found by a test, a gate or an error. Found by asking a different question: *are agents actually
succeeding with this?*

The palace records one row per recall in `search_events`. Reading 116 of them showed an 8% empty
rate, almost all explained by wings that were empty at the time. One was not: a wing holding zero
drawers had retrieved four candidates.

That is impossible if the index filter and the rows agree, so they did not. Comparing every point's
payload against its drawer row found 13 disagreements, all in wings that had been merged, and — the
part that mattered — **in both stores**. Two of the four stale names were `wing_to-<project>` inbox
wings, the misdelivery the handoff protocol warns about, which somebody had later merged into the
project's real wing.

Then the finding was confirmed the only way that counts, against the live server rather than the
data: take a phrase from a drifted memory, search its own wing, and search every wing.

```
scoped to the wing it is filed in : not found  (all three probed)
scoped to *                       : found      (all three)
```

Two false starts are worth recording because both nearly produced a wrong report:

1. **Copying `agentsmemory.db` without its `-wal` gave a stale snapshot.** The first comparison
   claimed 31 ghost points in the index. With the WAL copied too, the counts matched exactly and the
   real defect was different in kind. A SQLite file alone is not the database.
2. **The first payload comparison matched raw drawer ids against Qdrant point ids**, which are
   derived UUIDs, so nothing matched and it reported zero mismatches — a check that measured nothing
   and reported clean. The drift only appeared once the ids were mapped through the same `uuid5`
   the driver uses.

## Fix

### Before

```go
// Vectors are not re-written: their payload wing is advisory (search filters on
// the drawer row's wing), so a merge needs no re-embedding.
drawers, err := s.repo.RelabelDrawerWing(ctx, teamID, clean, tgt)
if err != nil {
	return MergeWingResult{}, fmt.Errorf("relabel drawers: %w", err)
}
```

### After

```go
// Collected BEFORE the relabel: afterwards nothing distinguishes a drawer that
// has just moved from one that was always in the target.
moved, err := s.drawerIDsInWings(ctx, teamID, clean)
…
drawers, err := s.repo.RelabelDrawerWing(ctx, teamID, clean, tgt)
…
for start := 0; start < len(moved); start += deleteBatch {
	…
	if err := s.vectors.SetPayload(ctx, teamID, moved[start:end], map[string]string{"wing": tgt}); err != nil {
		// Rows relabelled over a stale index is a half-done state nobody can see.
		return MergeWingResult{}, fmt.Errorf(…)
	}
}
```

The false comment is deleted rather than softened, because it is the cause and a softened version
leaves the next reader the same premise.

`VectorStore` gained `SetPayload`, so the correction is a payload write and not a re-embedding — the
text did not change, so the vector was already right. `store.Hybrid` writes both halves, source of
truth first: correcting only the index is undone by the next `sync`, which replays the source of
truth forward.

Two supporting changes, both of which found further defects on their own:

- **`agentsmemory doctor --index`** compares every point's payload against its drawer, in both
  stores, and exits 1 when they disagree. It was verified by drifting a real point on the live
  palace and watching it go red, then restoring — a check that has only ever passed has not been
  tested.
- **`TestMergedMemoryIsFoundInTheTargetWing`** asserts the user-visible property rather than a count
  of writes: file a memory in one wing, merge it into another, search THAT wing, require it back.

The 13 already adrift were repaired by hand in both stores before any of this shipped, and 12 of the
13 verified as returned by a search of their own wing through the live endpoint. The thirteenth is a
middle chunk of a four-chunk memory whose other three chunks all return, so the memory is reachable
and the miss is the probe's ranking.

## Lesson

**A doc comment that justifies NOT doing something is load-bearing, and this one was wrong.** The
merge did not forget to update the payload — it declined to, on a stated premise, and the premise
was checkable in one line: does `Search` pass the wing to the index or apply it afterwards? Whenever
a comment explains why a write is unnecessary, that explanation is a claim about another function
and deserves the same suspicion as a claim about a value.

**Two copies of the same fact need one writer.** The wing lived in the drawer row, the source-of-
truth payload and the index payload. Three places, one updater, and no check that they agreed until
one was written.

**A "belt and braces" second check can hide the failure of the first.** The row comparison was added
so a stale index could not surface another wing's memory, and it did that perfectly — the wrong-wing
candidates were dropped. It made the drift produce an empty page instead of a wrong one, which is
much harder to notice and impossible to attribute.

**Read the telemetry you already collect.** `search_events` had been recording the anomaly for two
days. Nothing was wrong with the instrument; nobody had asked it a question.

**And a checker must not repair the evidence.** The first version of `doctor --index` built its
services the ordinary way, which reconciles the chromem index from the source of truth at
construction — so it rebuilt a broken index and then reported it clean. A check that cannot fail on
the fault it exists for is worse than no check, because it is also an assurance.
