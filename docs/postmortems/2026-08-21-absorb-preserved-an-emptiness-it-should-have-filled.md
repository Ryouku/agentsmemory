---
date: 2026-08-21
category: data-integrity
severity: medium
files_changed:
  - internal/palace/import.go
  - internal/palace/import_test.go
tags: [entities, derived-graph, import, migration, verbatim-contract, unrepairable-state]
---

## Symptom

Every memory absorbed from a palace that predated entity extraction landed with no entities, and
nothing in the system could ever repair it.

The absorb reported success and the count was right. The memories were searchable, because search
goes through the vector. They were simply invisible to every graph tool — `am_traverse`,
`am_list_hallways`, `am_graph_stats` — permanently, and `am_recompute_graph` reported success each
time it derived nothing from them.

## Context

`AbsorbDrawers` is the migration path: it files verbatim drawers from another palace under the
target tenant as rows only, with `embedded_at` NULL, and a background worker embeds them afterwards.
Its contract is preservation. The code says so at the one place it trims:

```go
// Validate emptiness on a trimmed copy, but store the content VERBATIM:
// the source palace preserved exact bytes and so must the migration.
```

`ImportDrawer.Entities` is documented as *"proper nouns the source palace already extracted"*, and
absorb copied it straight through. For a source that has entities, that is correct and deliberate:
re-deriving would overwrite another palace's extraction with whatever this build's extractor happens
to produce, which is not a migration.

`RecomputeGraph` derives hallways by reading `drawers.entities`. It never re-extracts from content.

## Root Cause

The verbatim contract was applied to a field that might not exist in the source, and the two cases
were never separated.

```go
Entities: r.Entities,
```

When the source supplies entities this is the contract working. When the source supplies none, this
preserves an emptiness — and because `RecomputeGraph` reads the column rather than the content, the
emptiness is terminal. There is no later pass, no repair job, and no user-facing action that fills
it. Delete and re-file is the only route back, which for a migration is no route at all.

**The empty-source case is not hypothetical, and this repository is the proof.** ADR-016 measured
this palace the day before its fix landed: 0 of 359 drawers carried an entity, because nothing on
the agent write path extracted any. An export taken from this palace on that day — exactly the
artifact a migration consumes — carries no entities in any record.

## Investigation

Found in the same sibling enumeration as the `Service.Update` defect: list every path that writes a
drawer's content, ask of each whether it derives entities. `AbsorbDrawers` was the fifth and last
row, and it was the only one where the answer was neither yes nor no but *"it depends on the
input"* — which is the answer worth stopping on.

The judgement call was whether this is a defect at all. Absorb's behaviour is deliberate, documented
and correct for its stated case. What settled it was asking what state the system ends in rather
than whether the line matches its comment: a memory that can never enter the graph, with no
mechanism anywhere that could put it there.

## Fix

Derive only into the gap. Nothing the source supplied is ever overwritten.

### Before

```go
drawers = append(drawers, Drawer{
    ...
    Entities:    r.Entities,
    ...
})
```

### After

```go
// The source's entities are replayed verbatim when it has them — this is a
// migration, and re-deriving would overwrite another palace's extraction with
// this build's. When it has NONE, derive: an export from a palace predating
// ADR-016 carries no entities at all, and RecomputeGraph reads this column and
// never re-extracts, so nothing downstream could ever repair it.
entities := r.Entities
if len(entities) == 0 {
    entities = extractEntities(r.Content)
}
drawers = append(drawers, Drawer{
    ...
    Entities:    entities,
    ...
})
```

`TestAbsorbDerivesEntitiesOnlyWhenSourceGaveNone` pins BOTH halves in one test, because each half is
a distinct way to get this wrong: a record whose source supplied `SourcesOwnWord` — a token
deliberately absent from its own content — must come back carrying it and nothing else, and a record
whose source supplied nothing must come back carrying what its content names.

The mutant (derive, then truncate the result to empty) was verified to compile and to turn the test
red.

## Lesson

**A "preserve verbatim" contract has to say what it does when there is nothing to preserve.** The
contract was written for the case where the field has a value, and applied unconditionally. Every
preservation rule has an empty case, and it is never the interesting one at the time it is written —
which is why it goes unspecified and then goes wrong.

**Rank a bad state by whether anything can repair it, not by how bad it looks.** Missing entities are
mild next to wrong ones. But wrong entities are corrected by the next update, and these were
corrected by nothing at all — no background pass, no recompute, no user action. Unrepairable is a
severity multiplier that a diff does not show.

**The test that made this real was written against a fixture that could not exhibit the behaviour.**
The first run went red for the wrong reason: the fixture named each entity once, and
`extractEntities` requires two occurrences, so it would have been red whether or not the fix worked.
Had the fix been written first, that red would have read as proof the fix was needed and the green
after a fixture change would have read as proof it worked — with the code doing nothing either way.
The fixture now carries a comment saying why each name appears twice.

**One line, deliberate and correct, can still be a defect** — when the case it was written for is
not the only case it runs in. "Is this line doing what its comment says?" was the wrong question.
"What state does the system end in?" was the right one.
