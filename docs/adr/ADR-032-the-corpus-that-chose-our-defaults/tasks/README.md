# ADR-032 Tasks

Implementation tasks for ADR-032: The corpus that chose our defaults could not disagree with them. See the parent ADR for the decision, and `evidence/two-corpora-2026-08-25.md` for the two tables that motivate it.

**Source of truth:** the task files' headers. This README is a derived index.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |

T2 genuinely depends on T1: it has no defensible content until the measurement exists, and its Precondition says to change nothing if the contrasts do not resolve.

## Task Index

| Task | Goal | Produces | Consumes | Status | Acceptance |
|------|------|----------|----------|--------|------------|
| T1 | A real corpus large enough to resolve a default | a committed 54-case real-query file and its arm table | none | **done (2026-08-26, n=54)** | the evidence file exists, reports n≥40, and carries the rows for both disputed knobs |
| T2 | Ship what the real corpus says, or record that it said nothing | **a recorded NULL — nothing flipped** | T1's table | **done (2026-08-26): precondition failed, no change** | `go test ./cmd/server/ -run "^(TestShippedDefaultsCiteTheirCorpus)$"` |

## Outcome, 2026-08-26

T1 ran at n=54. `rrf` came back **inconclusive** against the linear arms, so T2's Precondition applied and **no default changed**. The null is recorded in `evidence/real-corpus-large.md` and in the parent ADR's amendment.

T2's `TestShippedDefaultsCiteTheirCorpus` was NOT written, because it belongs with a default change and there was none. It is carried to `BACKLOG.md` §"From ADR-032" as the mechanical form of "re-examine every default annotated as measured", which remains worth doing on its own.

## Not a task here

**The 14-of-40 unanswered real queries.** The largest single number in the run and the least interpretable: the judge sees only the retrieved pool, so "no relevant memory" conflates a memory that is not there with one the judge missed. It needs its own instrument before it means anything. Receipted in `BACKLOG.md` §"From ADR-032".

**The recalls that never happened.** An agent that does not know a thing exists never searches for it, so no corpus built from `search_events` can contain that case and no eval can see it. It is the one failure mode on this ADR's subject with no metric at all, and naming it is the most this record can honestly do.
