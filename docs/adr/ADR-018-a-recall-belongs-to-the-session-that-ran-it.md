# ADR-018: A recall belongs to the session that ran it

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-007 (a printed number carries the population it was computed over — this is that rule broken in the telemetry rather than the eval), ADR-017 (the same missing session identity, seen from the write end)
**Invalidates:** none — checked (grepped ADR-001..017 for `search_events`, `recall_stats`, `/stats`: ADR-006 T4 fixes a NUMBER in this report and does not depend on how it is scoped)
**Served-path change:** The Stop hook's recall report stops describing other sessions' work as yours, and stops handing you a list of their unanswered questions to write memories for.

## Context

Found 2026-08-21 by a peer session on the reference machine. It was handed a "memories to write" list naming failed searches in two wings it had never touched, noticed they were not its own, and refused to file drawers for them. That refusal is the only reason two fabricated memories do not exist.

The mechanism is one missing column.

- `db/migrations/00021_search_events.sql` records `team_id`, `wing`, `room`, `query`, `candidates`, `hits`, `top_score`, `reranked`, `created_at`. **There is no session identity.**
- `/stats?hours=N` (`cmd/server/main.go:1091`) therefore filters by team and time only.
- `agentsmemory-stop-hook.sh` computes a window from the transcript file's birth time — carefully, and with a comment explaining why a fixed window would be wrong — and then asks an endpoint that cannot honour it.

So on any machine running more than one session against one local palace, every session's Stop hook reports the whole palace's traffic as its own. The reference machine had eleven sessions live when this was found.

**The hook's comment states the opposite of what the code does:**

> The window is THIS SESSION, measured from the transcript file the event names, not a fixed number of hours. A fixed window at the first Stop of a session reports mostly the PREVIOUS session's work — the numbers looked plausible and described the wrong thing, which is worse than no numbers.

Every sentence of that is right about the problem and wrong about the solution. Narrowing the WINDOW cannot separate sessions that overlap in TIME, and concurrent sessions are the normal case, not the exception. This is the same shape as the merge doc comment fixed the same day: a false premise justifying a step that was never taken.

**Two consequences, and the second is the serious one.**

The percentages are wrong — ADR-007's rule broken in the telemetry: a number that means something other than what it says, read by the person deciding whether memory is earning its place.

And the "memories to write" section is not a statistic. It is a TASK LIST, presented under a heading that reads as this session's gaps, and following it means writing a memory about a question you never asked, into a wing you never opened, from no evidence you hold. The hook is well designed for the single-session case and actively harmful in the concurrent one, because the more diligent the agent, the worse the outcome.

## Existing Primitives Audit

- **`search_events` + `recordSearch`** (`internal/palace/recallstats.go:133`) — already best-effort, already fire-and-forget, already carries everything except who asked. Reshape: one nullable column, one more field on the row.
- **`SearchQuery.SkipTelemetry`** (`internal/palace/service.go:763`) — already the precedent for the CALLER telling the search something about itself that the search cannot work out. Reuse the shape: a session id travels the same way.
- **`RecallStats`** (`internal/palace/recallstats.go`) — already aggregates by wing over a window. Reshape: the window gains an optional session filter; the aggregation is unchanged.
- **The MCP transport** — an MCP session already has an identity at the protocol level. Reuse it if it reaches the handler; mint one at the client if it does not. Deciding which is T1's job, because guessing it wrong makes every later task wrong.
- **`agentsmemory-stop-hook.sh`** — already parses the Stop event JSON for `transcript_path`. Reuse: the same event carries a session id, and the transcript path is itself a stable per-session key if nothing better exists.

## Decision

**A recall event records which session ran it, and the report refuses to attribute what it cannot.**

Three parts, and the third is what makes it safe before the first two land:

1. `search_events` gains a `session_id`, written by `recordSearch` from a value the caller supplies. Nullable, because rows already written have no session and inventing one for them would be the same fabrication in a different place.
2. `/stats` accepts `session=` and, when given one, reports only that session's recalls. Without it the endpoint reports the whole team as it does today, which is the right answer to a question that named no session.
3. **The hook asks for its own session and says so.** When it cannot — an older server, a session with no id — it prints the palace-wide numbers under a heading that says palace-wide, and **prints no "memories to write" list at all**. A statistic that names its population is useful; a task list that cannot name its population is the defect.

**What would make this fail, and the data exists to check it today.** The claim is that a session id is available at the point `recordSearch` runs. If the MCP transport does not carry one to the handler and the client cannot supply one, then part 1 is unbuildable as designed and the honest fallback is part 3 alone — palace-wide numbers, honestly labelled, no task list. T1 answers that before anything is migrated, and the ADR is explicit that shipping only part 3 is an acceptable outcome rather than a failure.

Valid for the self-hosted single-palace deployment where the defect was found; a hosted workspace with one session per token has the same missing column and a less acute symptom.

## Alternatives Considered

- **Narrow the time window further.** Rejected: it is what the hook already does, and it cannot work. Two sessions searching in the same minute are indistinguishable by time at any resolution.
- **Filter by wing instead of by session.** Rejected, and it is worth naming because it looks plausible: a session works in one wing, so its searches are mostly in that wing. Mostly. The peer session's report listed nine wings, and the two entries it correctly refused were in wings it had never opened — which is exactly the case wing-filtering would still have got wrong.
- **Drop the "memories to write" section entirely.** Rejected: it is the most useful thing the hook prints — the questions a team asked and could not answer are the memories it should have written. The section is right; its attribution is wrong.
- **Have the hook diff the transcript to find this session's searches.** Rejected: it makes the hook a transcript parser, duplicates what the server already records, and breaks the moment a search happens through any other client.
- **Do nothing until someone is actually harmed.** Rejected on the evidence that someone nearly was, and only avoided it by being careful in a way nothing required.

## Component / Boundary Impact

`internal/palace` owns what a recall event records and how it is aggregated; it gains a column and an optional filter. `cmd/server` owns the endpoint. `clients/claude-code` owns the hook. No boundary moves and no component gains a reason to change that it did not already have.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `search_events.session_id` | add — nullable | `db/migrations/` | `internal/palace/recallstats.go` |
| `SearchQuery.SessionID` | add | `internal/palace/service.go` | `recordSearch` |
| `/stats?session=` | add | `cmd/server/main.go` | the Stop hook |
| the Stop hook's report heading | change — names the population it describes | `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | every operator |
| the "memories to write" section | change — printed only when the recalls can be attributed to this session | `clients/claude-code/hooks/agentsmemory-stop-hook.sh` | every agent that reads it |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| whether a session id is reachable at `recordSearch` | T1 | T2, T3 | No — T1 is a measurement and may withdraw T2 |
| `search_events.session_id` and `/stats?session=` | T2 | T3 | No — additive; a caller that names no session gets today's behaviour |
| the honest-attribution report | T3 | T3 | No — and it ships whether or not T2 does, which is the point |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** the recall report becomes trustworthy, and the one part of it that is a task list stops being a source of invented memories. A team can finally read "is memory earning its place" per session rather than per machine.
- **Negative:** one more column on the highest-write table in the palace, and one more thing a caller must pass. Both are small; neither is free.
- **Neutral:** existing rows carry no session and are excluded from any session-scoped report. That is the correct reading of them — they are attributable to nobody — and the report says so rather than folding them in.

## Out of Scope

- Attributing a SUBAGENT's recalls separately from its dispatcher's (deferred: docs/adr/ADR-017-a-subagent-is-a-session.md — that ADR owns what a subagent is, and until it lands a subagent runs no recalls to attribute)
- Per-session write statistics — drawers filed, facts added (deferred: docs/adr/BACKLOG.md — the same column would serve it, and it is a different report)
- Retro-attributing rows already written (permanent: they carry no session identity, and inventing one is the fabrication this ADR exists to prevent)
- The hosted multi-workspace deployment's session model (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| No session id is reachable at the point recalls are recorded | Med | High | T1 establishes this BEFORE the migration is written, and part 3 ships regardless — honest labelling needs no schema change |
| The column is added and nothing populates it, so reports silently return nothing | Med | High | T2's test asserts a real search through the real path writes a non-empty session id; a column nobody fills is this repository's signature defect |
| The hook stays wrong because the server is older than the client | High | Low | T3 detects the missing capability and degrades to palace-wide-and-labelled, which is the honest answer rather than a broken one |

## Rollback

The column is nullable and additive; dropping the filter restores today's behaviour with the data still there. The hook change is text and a conditional, revertible on its own — and it is the half worth keeping even if the rest is reverted, because it removes the fabrication path without depending on anything.

## Follow-ups
