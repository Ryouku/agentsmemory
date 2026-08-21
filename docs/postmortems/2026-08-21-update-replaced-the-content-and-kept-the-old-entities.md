---
date: 2026-08-21
category: data-integrity
severity: medium
files_changed:
  - internal/palace/repo.go
  - internal/palace/graph_test.go
tags: [entities, derived-graph, update, stale-derived-data, sibling-not-enumerated, wrong-answer-not-missing-answer]
---

## Symptom

A memory whose content had been corrected went on joining the derived graph through the entities of
the text it no longer contained.

Reproduced before anything was changed. A memory filed as *"Redis beat Postgres for the session
cache"*, then updated through `am_update_drawer` to say *"Kafka beat Mongo for the event log"*,
stored:

```
entities: [Postgres Redis]
```

The content column held the new text. The vector held the new text — `Update` re-embeds. Only the
derived column was stale, and nothing anywhere reported a problem: the update returned success, the
drawer read back with its new content, and search returned it for the new terms because search goes
through the vector.

## Context

`drawers.entities` is a derived column: proper nouns extracted from the content, semicolon-joined.
`RecomputeGraph` reads it to derive hallways — an edge exists between two entities when enough
drawers name both. It reads the column. It does not re-extract from content, so nothing downstream
can ever repair a wrong value.

ADR-016 had already found this shape twice. `Service.Add` built its `Drawer` rows without
`Entities`, so a memory filed through the agent surface stayed outside the graph; T2 fixed it. T4
then fixed `Service.WriteDiary`, which had its own chunk loop with the identical omission — 119 of
383 drawers, 31% of the corpus.

Both were fixed. Neither fix asked what else writes content.

## Root Cause

`Service.Update` computes the post-patch state, re-embeds it, re-upserts the vector, and hands the
patch to `Repo.Update`, which builds its update map from the patch fields alone:

```go
updates := map[string]any{}
if patch.Content != nil {
    updates["content"] = *patch.Content
}
```

`DrawerPatch` carries `Content`, `Wing` and `Room`. There is no entities field on it, because
entities are derived rather than supplied — correctly, since a caller must not be able to set them.
The consequence was that the one column derived FROM content was the one column a content change
did not touch.

**This defect is worse in kind than the two before it.** `Add` and `WriteDiary` produced a memory
MISSING from the derived graph. `Update` produced a graph asserting an edge the text does not
support. An empty graph sends an agent to go and look. A wrong one tells it not to.

## Investigation

The bug was not found by a report or a failing test. It was found by asking a question the red-flag
table in the `work` skill demands after any fix — *if it came from a shared shape, enumerate the
siblings and RUN the same question against each* — and this repository had just closed the same
shape twice without asking it.

The enumeration was mechanical: every path that writes a drawer's content.

| producer | derives entities? |
|---|---|
| `Service.Mine` | yes, always did |
| `Service.Add` | yes, since ADR-016 T2 |
| `Service.WriteDiary` | yes, since ADR-016 T4 |
| `Service.Update` | **no** |
| `AbsorbDrawers` | takes them from the import record — separate postmortem |

Reading was not enough to settle it, and was not trusted to be: the symptom was reproduced with a
test that failed on the real stack before any fix existed. That matters here because reading alone
cannot distinguish a deliberate exception from an omission, and `Update` has several deliberate
exceptions nearby — it deliberately does not re-chunk, and deliberately refuses content changes on
multi-chunk memories.

## Fix

Derive in `Repo.Update`, in the same statement that replaces the content.

### Before

```go
updates := map[string]any{}
if patch.Content != nil {
    updates["content"] = *patch.Content
}
```

### After

```go
updates := map[string]any{}
if patch.Content != nil {
    updates["content"] = *patch.Content
    // Entities are DERIVED from content, so they are refreshed in the same
    // statement that replaces it — a future call site that forgets is not a
    // path this function has.
    updates["entities"] = strings.Join(extractEntities(*patch.Content), ";")
}
```

**Placed in the repo rather than the service on purpose,** although the house pattern derives in the
service (`Add` and `WriteDiary` both call `extractEntities` there). The two columns that must agree
are now written by one statement, so the invariant does not depend on a future call site
remembering. Deriving in the service would have fixed this instance and left the shape intact — which
is exactly what produced this bug: two correct call sites and a third nobody looked at.

Per-chunk is per-memory here, so extracting from the patch content matches what `Add` stores per
chunk: `Service.Update` already refuses a content change on any memory of more than one chunk.

`TestUpdateRefreshesEntities` asserts both directions — the new names present, the old names gone.
Asserting only the first would pass on an implementation that appended.

The mutant (`extractEntities` called, result discarded) was verified to COMPILE and to turn the test
red. A mutant that does not build has not been tested, it has been skipped.

## Lesson

**A derived column is only as correct as the least careful path that writes what it is derived
from.** The write paths were audited one at a time as each was reported, and each fix was verified
against the path it fixed. Nothing verified the set.

**Fixing an instance twice without enumerating the siblings is how a shape survives its own
postmortems.** ADR-016 named this defect, measured it, and fixed two producers across two tasks. The
third was reachable by the same one-line grep the whole time. The enumeration is cheap; not doing it
was the actual error, and it had already been written down as a red flag before this session began.

**Prefer the placement that makes the next omission impossible over the one that matches the
existing style.** House style put derivation in the service. Putting it beside the write instead
removes the class rather than the instance, and the cost is one comment explaining why this path
differs.

**"Missing" and "wrong" are different severities and the same-looking bug.** Three producers had the
same omission; only this one caused the system to state something untrue. When enumerating siblings,
rank them by what a stale value CLAIMS, not by how similar the code looks.
