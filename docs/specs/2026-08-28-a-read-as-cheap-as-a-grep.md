# Spec: Make a palace read as cheap to justify as a grep

> **Date:** 2026-08-28 · **Status:** Draft
> **Owner:** Zy · **Becomes:** ADR-NNN (allocate at merge)
> **Gate:** Status may become Ready-for-ADR only after `spec-verify --spec docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md` exits 0.
> **Cross-references:** `docs/adr/ADR-041-the-recall-that-does-not-depend-on-remembering.md` (owns pushed recall; its instrument's limits are inherited below), `docs/adr/ADR-038-refer-by-the-id-and-end-instead-of-overwrite.md` (ended records already excluded from default reads), `docs/adr/ADR-010-supersede-do-not-overwrite.md`, `docs/adr/ADR-017-a-subagent-is-a-session.md` (prose is the weakest lever), `internal/palace/chunk.go`, `internal/palace/memory_search.go`, `internal/mcpserver/drawers.go`

## Problem

Measured on session `ee8f1fc1`, one long working session in this repository: **7,521 Bash calls against 369 palace calls**, and of those 369, **226 writes against 143 reads**. The agent writes to the palace more than it reads from it. `am_search` ran 52 times in 8,256 tool calls.

The palace's competitor is not "no memory" — it is `grep`, which is faster and reports what the code *does* rather than what someone once said about it. Three measured properties make a read expensive to justify: a hit discloses ~3% of the memory it names, memories fragment at a 1,600-character boundary, and a superseded record can outrank the record that corrected it.

### Evidence

Measured on session `ee8f1fc1` against the live palace and this tree, 2026-08-28. These are
observations, not requirements — they motivate the Facts below and are cited by them.

| ID | Observation | Evidence |
|----|-------------|----------|
| M-1 | F-7 | An agent reaches for Bash roughly 20× more often than the palace. |
| M-2 | F-8 | An agent writes to the palace more than it reads from it. |
| M-3 | F-9 | A search hit discloses a small fraction of the memory it names. |
| M-4 | F-10 | Memories fragment at a fixed character boundary sized for the embedder. |
| M-5 | F-11 | A superseded record can outrank the record that corrected it. |
| M-6 | F-12 | Filing a correction as a new drawer leaves the incorrect record current. |
| M-7 | F-13 | Ended records are already excluded from default reads. |

## Goal

An agent reads from the palace without first deciding whether the read is worth it: a hit is actionable on arrival, and what it returns is current.

## Actors

| Actor | Kind | Goal |
|-------|------|------|
| Working agent | system | Answer "what was decided / what was corrected" without a second round trip |
| Record author | system | File a correction that supersedes the record it corrects, not one that competes with it |
| Measurement owner | human role | Know whether a mechanism moved read behaviour, before spending a window on it |

## Use Cases

### UC-1: Working agent recalls a decision mid-task

- **Trigger:** the agent is about to assert something about this repository whose subject resolves in the working tree · **Preconditions:** the palace holds at least one relevant memory
- **Main flow:**
  1. The agent issues one recall.
  2. Each hit arrives disclosed above the coverage floor, whole rather than chunked.
  3. Any hit whose memory was superseded is marked, and ranks below the record that superseded it.
  4. The agent acts without a second call.
- **Failure paths:** a. at step 2, the response budget cannot disclose every hit above the floor → return fewer memories whole and report the withheld count; b. at step 3, a superseded memory would outrank its successor → the successor ranks first and the superseded hit is marked
- **Postconditions:** the agent has acted on whole, current content, or knows exactly what was withheld.

### UC-2: Measurement owner establishes the baseline before a mechanism ships

- **Trigger:** a mechanism intended to change read behaviour is proposed · **Preconditions:** none
- **Main flow:**
  1. The counting rule is written down as an artifact — what a read is, and the window it is attributed to.
  2. The baseline is collected under that published rule.
  3. Only then may a mechanism ship.
- **Failure paths:** a. at step 3, a mechanism is marked done with no baseline recorded → the gate fails; b. at any step, the counting rule changes after collection → the baseline is invalidated and must be retaken
- **Postconditions:** every quoted rate names the rule it was measured under.

## Scenarios

### UC1-S1 [happy] A recall hit arrives disclosed enough to act on [@spec] → `internal/palace/readcost_spec_test.go::TestF1AHitIsDisclosedAboveTheFloor`

```gherkin
Given a palace holding memories longer than one chunk
When an agent issues one recall that matches them
Then every returned hit discloses its memory above the coverage floor
And no hit requires a second call to be acted on
```

### UC1-S2 [failure] A constrained budget returns fewer memories, not fragments [@spec] → `internal/palace/readcost_spec_test.go::TestF2FewerWholeNotMoreFragments`

```gherkin
Given a response budget too small to disclose every match above the floor
When an agent issues one recall
Then fewer memories are returned, each above the floor
And the response reports how many were withheld
```

### UC1-S3 [failure] A superseded memory never outranks its correction [@spec] → `internal/palace/readcost_spec_test.go::TestF3SupersededNeverOutranksItsCorrection`

```gherkin
Given a memory that has been superseded by a later record
When a recall matches both
Then the superseding record ranks above the superseded one
And the superseded hit is marked as superseded
```

### UC1-S4 [happy] A memory is returned as one unit [@spec] → `internal/palace/readcost_spec_test.go::TestF4AMemoryIsOneUnitToItsCaller`

```gherkin
Given a memory several times longer than the chunk size
When a recall matches text inside it
Then one hit is returned carrying the whole memory
And no chunk index is required to reassemble it
```

### UC2-S1 [happy] The counting rule is published before the baseline is collected [@spec] → `internal/palace/readcost_spec_test.go::TestF5CountingRuleIsAnArtifactBeforeCollection`

```gherkin
Given a proposed mechanism that intends to change read behaviour
When the baseline is collected
Then the counting rule already exists as a committed artifact
And it names what a read is and the window it is attributed to
```

### UC2-S2 [failure] A mechanism marked done with no baseline fails the gate [@spec] → `internal/palace/readcost_spec_test.go::TestF5NoMechanismShipsBeforeItsBaseline`

```gherkin
Given no baseline record exists under the published counting rule
When a mechanism task is marked done
Then the gate fails and names the missing baseline
```

## Facts

| ID | Assertion (invariant / behavior) | Test (`path::name`) | Tag | Cmd (optional) |
|----|----------------------------------|---------------------|-----|----------------|
| F-1 | A recall discloses enough of each memory to be acted on without a second call; a hit below a stated coverage floor is a defect, not a preview. | `internal/palace/readcost_spec_test.go::TestF1AHitIsDisclosedAboveTheFloor` | @spec | |
| F-2 | When the budget cannot disclose every hit above the floor, a recall returns fewer memories whole rather than more as fragments, and reports the withheld count. | `internal/palace/readcost_spec_test.go::TestF2FewerWholeNotMoreFragments` | @spec | |
| F-3 | A recall marks a hit whose memory has been superseded, and a superseded memory never outranks the record that superseded it. | `internal/palace/readcost_spec_test.go::TestF3SupersededNeverOutranksItsCorrection` | @spec | |
| F-4 | A memory is returned to a caller as one unit; chunking is an embedding-time detail that never reaches the read contract. | `internal/palace/readcost_spec_test.go::TestF4AMemoryIsOneUnitToItsCaller` | @spec | |
| F-5 | No mechanism ships before a baseline is recorded, and the counting rule — what a read is, and the window it is attributed to — is a committed artifact fixed before collection. | `internal/palace/readcost_spec_test.go::TestF5CountingRuleIsAnArtifactBeforeCollection` | @spec | |
| F-6 | Changing the counting rule invalidates the baseline taken under it, the way changing a fence invalidates its recorded evidence. | `internal/palace/readcost_spec_test.go::TestF5NoMechanismShipsBeforeItsBaseline` | @spec | |

## Domain

**Memory** — one record with an identity, a wing, a room and content. **Chunk** — an embedding-time division of a memory; not a unit a caller addresses. **Coverage** — the fraction of a memory a hit discloses. **Supersession** — a directed relation from a correcting record to the record it corrects. **Counting rule** — the published definition under which a read rate is measured.

## Contracts Touched

| Surface | Change | Consumers |
|---------|--------|-----------|
| `am_search` hit shape (`content_truncated`, `content_coverage`, `chunk_index`, `parent_id`) | modify | every MCP client; the bootstrap protocol |
| `am_get_drawer` (`whole` parameter) | modify | agents following `AGENTS.md`'s read guidance |
| Recall ranking with respect to supersession | modify | every recall caller |
| The counting rule artifact | add | whoever quotes a read rate |

## Non-Goals

- **Retrieval ranking quality.** ADR-001/002/003 own it, and ADR-041 established the answer was already at rank 1 when it was not asked for. This spec is about access cost and trust, not about what comes back first.
- **Making hooks louder in the common case.** ADR-041 F-6's scarcity rule stands.
- **Deciding which entry-point layer is canonical.** Filed at `BACKLOG.md`, "Four spellings of one entry point"; an ADR-level decision. This spec proceeds independently — F-1..F-6 hold whichever layer wins. If that decision materialises a new read path, F-1 and F-2 extend to it by amendment.
- **Raising any number the ADR-041 instrument reports.** That instrument latches once per session and cannot see what a proximity mechanism changes; see `BACKLOG.md`.
- **Changing what matches.** Chunking may remain the embedding and matching unit; F-4 constrains what a caller receives, not how retrieval finds it.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A coverage floor plus whole memories shrinks a page from ten hits to three | High | Med | F-2 makes the trade explicit and requires the withheld count, so a short page is legible rather than silently narrow |
| Returning whole memories inflates response size for long records | Med | Med | The floor is on disclosure, not on count; the budget still bounds the response and reports what it withheld |
| Supersession ranking is gamed by a chain of corrections, each superseding the last | Low | Med | F-3 constrains order, not count; a chain resolves to its head, which is the intended reading |
| The new counting rule is itself insensitive, repeating ADR-041's failure | Med | High | F-5 requires the rule to be an artifact fixed before collection, and a stated demonstration that a relevance-improving mechanism moves it |
| Callers depend on `chunk_index` / `parent_id` today | Med | Med | Contracts Touched names them; deprecate to diagnostics rather than removing |

## Open Questions


## Verify

```bash
spec-verify --spec docs/specs/2026-08-28-a-read-as-cheap-as-a-grep.md
```

## Grill Log (appendix)

| # | Question | Fact | Decision |
|---|----------|------|----------|
| 0 | Scouted measurements presented as one batch for veto | non-behavioral | Accepted without amendment; they are observations, recorded under Problem > Evidence as M-1..M-7 rather than as Facts requiring bindings |
| 1 | Must a hit be actionable without a second call? | F-1 | Accept — 3% coverage measured, and partial content was acted on twice in one session |
| 2 | Under a constrained budget, fewer whole or more fragments? | F-2 | Accept — fewer whole, plus a withheld count; a fragment that cannot be acted on has negative value |
| 3 | Must a superseded memory be marked and prevented from outranking its correction? | F-3 | Accept — measured 0.334 superseded above 0.355 correction on the same query |
| 4 | Is a memory one unit to its caller, or N chunks? | F-4 | Accept — chunking is an embedding-time detail; it may remain the matching unit |
| 5 | What is the success criterion, and what stops it being insensitive? | F-5, F-6 | Accept — the counting rule is an artifact fixed before collection, and changing it invalidates the baseline |
| 6 | Does this spec wait on the entry-point decision? | non-behavioral | Proceed independently; recorded in Non-Goals, with amendment named if a new read path lands |
