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
| Concurrent correction author | system | Correct a record without racing another session into two competing current claims |
| External MCP client | external service | Read hits without depending on chunk-level fields |

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

### UC-3: Measurement owner quotes a rate

- **Trigger:** a read rate is about to be quoted or compared · **Preconditions:** a baseline exists
- **Main flow:**
  1. The baseline names the counting rule by content.
  2. The current rule is compared against it.
  3. The rate is quoted with the rule it was measured under.
- **Failure paths:** a. the rule's content differs from the baseline's → refuse the comparison and name the change
- **Postconditions:** no rate is quoted across a rule change.

## Scenarios

### UC1-S1 [happy] A hit reports every range it disclosed [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF1CoverageCountsEveryDisclosedRange`

```gherkin
Given a memory long enough to be disclosed as a window plus regions
When a caller issues one recall that matches text in several places
Then the reported coverage counts the primary window and every region returned
And a caller comparing it against a threshold is comparing against the truth
```

### UC1-S2 [failure] A partial hit says so, and says how to complete it [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF2NoHitIsSilentlyPartial`

```gherkin
Given a memory larger than the response budget allows to be disclosed whole
When a caller issues one recall that matches it
Then the hit is marked partial, reports the memory's full length
And carries the id that fetches the remainder
```

### UC1-S3 [happy] A caller never joins chunks [@spec] → `internal/mcpserver/readcost_spec_test.go::TestF4ChunkingCreatesNoReassemblyObligation`

```gherkin
Given a memory several times longer than the chunk size
When a recall matches text inside its last chunk
Then one hit is returned whose content is the memory's content
And no caller-side reassembly is required to obtain it
```

### UC2-S1 [happy] A correction leaves one current successor [@spec] → `internal/palace/readcost_spec_test.go::TestF3ACorrectionLeavesOneCurrentSuccessor`

```gherkin
Given a memory that a later record corrects
When the correction is written through the advertised correction operation
Then exactly one record about that subject is current
And it is linked to the ended predecessor
```

### UC2-S2 [failure] A correction that fails part-way leaves no fork [@spec] → `internal/palace/readcost_spec_test.go::TestF3ACorrectionLeavesOneCurrentSuccessor`

```gherkin
Given a correction whose predecessor spans several chunks
When ending one of those chunks fails, or a second correction races it
Then the operation does not leave two competing current records
And the failure is reported rather than half-applied
```

### UC3-S1 [happy] A baseline names the rule it was measured under [@spec] → `internal/repohygiene/readrule_spec_test.go::TestF5ABaselineNamesItsCountingRule`

```gherkin
Given a counting rule committed as an artifact
When a baseline is recorded
Then the baseline names that rule by its content, not by description
```

### UC3-S2 [failure] A rate quoted across a rule change is refused [@spec] → `internal/repohygiene/readrule_spec_test.go::TestF6ARuleChangeInvalidatesItsBaselines`

```gherkin
Given a baseline recorded under one counting rule
When the counting rule's content changes
Then that baseline is invalid and a rate quoted from it is a defect
And the gate names the rule change rather than reporting a comparison
```

## Facts

| ID | Assertion (invariant / behavior) | Test (`path::name`) | Tag | Cmd (optional) |
|----|----------------------------------|---------------------|-----|----------------|
| F-1 | A hit's reported coverage counts every disclosed range — the primary window and every returned region — so a caller deciding whether it needs a second call decides on the truth. | `internal/mcpserver/readcost_spec_test.go::TestF1CoverageCountsEveryDisclosedRange` | @spec | |
| F-2 | No hit is silently partial: a hit that does not carry its whole memory reports that, its full length, and the id that fetches the rest. | `internal/mcpserver/readcost_spec_test.go::TestF2NoHitIsSilentlyPartial` | @spec | |
| F-3 | An advertised correction leaves exactly ONE current successor, linked to the ended predecessor — including under partial failure and concurrent correction. | `internal/palace/readcost_spec_test.go::TestF3ACorrectionLeavesOneCurrentSuccessor` | @spec | |
| F-4 | Chunking creates no reassembly obligation: a caller never has to join chunks to obtain a memory's content. Chunk metadata may remain as diagnostics. | `internal/mcpserver/readcost_spec_test.go::TestF4ChunkingCreatesNoReassemblyObligation` | @spec | |
| F-5 | No mechanism ships before a baseline is recorded, and the baseline names the counting rule it was measured under by content, not by description. | `internal/repohygiene/readrule_spec_test.go::TestF5ABaselineNamesItsCountingRule` | @spec | |
| F-6 | Changing the counting rule invalidates every baseline taken under the previous one; a rate quoted across a rule change is a defect. | `internal/repohygiene/readrule_spec_test.go::TestF6ARuleChangeInvalidatesItsBaselines` | @spec | |

## Domain

**Memory** — one record with an identity, a wing, a room and content. **Chunk** — an embedding-time division of a memory; not a unit a caller addresses. **Coverage** — the fraction of a memory a hit discloses. **Supersession** — a directed relation from a correcting record to the record it corrects. **Counting rule** — the published definition under which a read rate is measured.

## Contracts Touched

| Surface | Change | Consumers |
|---------|--------|-----------|
| `am_search` hit shape (`content_truncated`, `content_coverage`, `chunk_index`, `parent_id`) | modify | every MCP client; the bootstrap protocol |
| `am_get_drawer` (`whole` parameter) | modify | agents following `AGENTS.md`'s read guidance |
| Recall ranking with respect to supersession | modify | every recall caller |
| The counting rule artifact | add | whoever quotes a read rate |
| `am_update_drawer` / `supersedeInto` atomicity | modify | any writer correcting a memory |
| `am_list_drawers`, `am_bootstrap` | modify | same disclosure reporting as `am_search` |
| `snippet_chars`, `regions`, `memory_id`, `chunks_matched` | retain | ADR-024 compatibility — F-4 does not remove them |

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
| Callers depend on `chunk_index` / `parent_id` today | Med | Med | F-4 retains them as diagnostics; a repo-wide search found no production consumer, but external clients cannot be ruled out |
| A memory larger than the response budget can never be whole | High | Med | F-2 makes it explicitly partial with its full length and fetch id, rather than silently fragmentary. `am_search` has no cursor, so completion is `am_get_drawer`, not paging |
| F-3's atomicity requirement is larger than it looks | Med | High | `supersedeInto` is not atomic and has no compare-and-swap; the fact names partial failure and concurrency deliberately so the ADR cannot scope them away |

## Open Questions

- Should a memory larger than the response budget be returnable at all, or always partial-with-fetch-id? · owner: Zy · blocks: F-2
- Is `am_search` gaining a cursor in scope, or is `am_get_drawer` the only completion path? · owner: Zy · blocks: F-2
- Does F-3's atomicity requirement belong in this spec or as an amendment to ADR-038, which owns supersession? · owner: Zy · blocks: F-3


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
