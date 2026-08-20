# ADR-005: Make cross-project handoffs deliverable

**Status:** Accepted
**Date:** 2026-08-20
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** `clients/claude-code/bootstrap.md` (the inbox convention), centralised skills `writing-memories` and `memory-orchestration`
**Invalidates:** none — checked (grepped ADR-001..004 for `inbox`, `am_status`, `am_add_drawer`: no accepted task consumes either surface)

## Context

The bootstrap protocol added an inbox convention: a session that finds a problem in another project files it as a drawer into that project's wing, room `inbox`, rather than editing a repository it has no context for. Measured 2026-08-20 against this palace (217 drawers, 10 wings), the convention has delivered **nothing**:

- Two wings, `wing_to-<project>` for two different projects, hold **6 drawers** of real findings between them. Both are unreachable: Step 0c's resolution rungs can only ever produce `wing_<project>`, so no session will look there. Two independent sessions made the same substitution — the placeholder is `wing_<target>`, and both wrote the *direction of travel* rather than the project name.
- All **3** explicit tunnels report `access_count: 0` since creation.

Both halves of the loop are broken and they fail independently. A handoff written to a wing nobody resolves to cannot be read however hard the reader tries; and a handoff in the right wing is still only seen if something makes the reader look. Fixing the read side alone would leave the six existing drawers exactly as lost as they are now.

Neither centralised skill mentions `inbox` at all: `writing-memories` is authoritative on what to file where and omits the handoff; `memory-orchestration` covers the read side and omits reading your own. The convention exists only in the locally-installed bootstrap, so an agent whose bootstrap is stale or absent never learns it.

## Existing Primitives Audit

- **`wingFor` / `palace.SanitizeName`** (`internal/mcpserver/server.go:164`) — resolves and validates a wing. `wing_to-<project>` is a perfectly *valid* name; the defect is semantic, not syntactic. Reuse unchanged; the new check sits above it.
- **`am_status` taxonomy** (`internal/mcpserver/server.go:279`) — already returns every wing with per-room drawer counts, and already returns `default_wing`. The inbox count needs no new query, only promotion out of a nested array. Reshape.
- **`palace.ArmScope`** — precedent for the shape used here: classify once, exhaustively, and have readers consult the classification instead of naming cases. Reuse the pattern, not the code.
- **`am_merge_wing`** — relabels drawers in place; the reconciliation of the six orphans needs no new tool. Reuse.

## Decision

Two changes, one on each side of the loop.

**Write side.** `am_add_drawer` refuses when the target wing holds no drawers *and* the room is `inbox`, naming the likely mistake and listing existing wings. An optional `confirm_new_wing` boolean proceeds anyway.

The discriminator was chosen because a bare "this wing is new" warning is a false alarm on exactly the case the protocol protects in three separate paragraphs — a wing comes into existence on first write, so on a fresh install every wing is missing. **Falsification, checked before adopting:** if any legitimate wing's first write were an inbox item, the rule fires on correct behaviour and must be withdrawn. Measured across all 217 drawers on 2026-08-20: eight legitimate wings, first write `decisions` (2) or `diary` (6); the only two wings whose first write is `inbox` are the two malformed ones. Zero false positives. This is a property of a palace where agents file their own project's memories before handing work elsewhere; it is valid for that usage and would need re-checking for a deployment whose first act is federation.

**Read side.** `am_status` gains a top-level `inbox` block — the count in the session's own wing — and a `hint` that changes when there is something waiting. The count exists today only inside the `wings` array, where it is one number among sixty.

`am_status` is the site because it is the one call the protocol mandates first and it is server-side, so it cannot drift per-harness. It is explicitly **not** continuous: `am_status` fires at wake-up, so an item arriving mid-session is not visible to that session *through this mechanism*. That is a property of the mechanism chosen here, not of the transport — see Out of Scope, where the original wording claimed the transport could not carry a push and was wrong. This ADR does not promise a mid-session nudge; it also does not establish that one is impossible.

## Alternatives Considered

- **Warn on write, file anyway.** Rejected: the response is prose, and a warning attached to a success is precisely what both existing sessions would have read past. The drawer still lands unreachable.
- **Reject any wing matching `to-*`.** Rejected: it pattern-matches the two failures we happen to have rather than the mistake, and a project legitimately named `to-do-service` is refused forever.
- **Refuse with no override.** Rejected: handing off to a project that genuinely has no wing yet is legitimate and must stay possible without an admin.
- **Fuzzy-match the wing name and suggest the nearest existing wing.** Rejected on evidence: in both real failures the correct target wing did not exist in the palace either, so nearest-neighbour matching would have had nothing to match against and would have caught neither case.
- **A Stop hook or per-harness reminder for the read side.** Rejected: it drifts per harness and per install, which is how the convention came to live in one local file in the first place.

## Component / Boundary Impact

`internal/mcpserver` keeps sole ownership of tool argument validation and response shape; `internal/palace` gains one counting method and keeps ownership of storage. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `am_add_drawer` argument `confirm_new_wing` (bool, optional) | add | `internal/mcpserver/drawers.go` | any agent filing a handoff |
| `am_add_drawer` error path: new-wing + `room=inbox` | add | `internal/mcpserver/drawers.go` | any agent filing a handoff |
| `am_status` response field `inbox` (object) | add | `internal/mcpserver/server.go` | every agent's wake-up |
| `am_status` response field `hint` | change (conditional text) | `internal/mcpserver/server.go` | every agent's wake-up |
| `palace.Repo.WingIsEmpty` / `RoomCount` | add | `internal/palace/repo.go` | `internal/mcpserver` |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `palace.Service.WingIsEmpty` | T1 | T1 | No — new method |
| `palace.Service.InboxCount` | T2 | T2 | No — new method |
| `confirm_new_wing` argument | T1 | T3 (documents it) | No — optional, absent means today's behaviour |

## Implementation

`tasks/README.md` — three tasks.

## Consequences

- **Positive:** a handoff that would have been unreachable is refused at the moment it is made, when the filer still has the context to correct it. A waiting inbox is named in the one call every session already makes.
- **Negative:** one legitimate flow — first-ever handoff to a project with no wing — costs one extra round trip and an argument. The refusal message carries the argument, so the cost is one retry, not a lookup.
- **Neutral:** `am_status` responses grow by one small object. Consumers that ignore unknown fields are unaffected.

## Out of Scope

- Mid-session inbox delivery — notifying a running session that an item arrived (deferred: docs/adr/BACKLOG.md)
- Fixing the anchored-BM25 wiring, the closet default, or any retrieval config (permanent: unrelated decision, ADR-002/ADR-003 own it)
- Auto-correcting a wrong wing name to a guessed right one (permanent: the server cannot know the target project's name, and a silent rewrite is a worse failure than a refusal)
- Reconciling the six existing orphaned drawers (deferred: T3 does it by hand via `am_merge_wing`; no code change, so no gate can hold it)
- An inbox count for wings other than the session's own (deferred: docs/adr/BACKLOG.md — nobody has asked, and every extra count dilutes the one that matters)

### Amendment 2026-08-20 — the mid-session bullet was tagged `permanent` on a false premise

It read "permanent: MCP is request/response here; a server cannot wake a session". That is wrong.
This server runs `server.NewStreamableHTTPServer` (`cmd/server/main.go:293`), and mcp-go v0.55.1
exposes `SendNotificationToClient`, `SendNotificationToAllClients` and
`SendNotificationToSpecificClient` (`server/session.go:301-377`). This repository calls none of
them. The transport can carry a push; we simply do not send one.

What is genuinely unknown is the client half — whether a given agent harness surfaces an
unsolicited notification to the model mid-turn — and that is a question you answer by testing a
harness, not a property of the protocol.

The correction matters beyond the wording. `permanent` is the one disposition `adr-debt` never
resurfaces, so a wrong reason there does not merely mislead a reader: it removes the item from
every future sweep. It is now `deferred`.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The discriminator fires on a legitimate first handoff into a genuinely new wing | Med | Low | `confirm_new_wing` in the refusal text; one retry, no admin |
| The refusal text is read as "this wing is wrong" when the wing is right | Med | Low | Message states both readings and names the argument that proceeds |
| `am_status` inbox count grows stale within a session | High | Low | Documented as wake-up-only in the field itself, not only in the ADR |
| A future arm/room type inherits the check by accident | Low | Med | The room is compared to one constant, tested; no list to maintain |

## Rollback

Both changes are additive to one process and hold no persistent state. Revert the commit and restart the server: the `confirm_new_wing` argument becomes an ignored extra field on `am_add_drawer` (harmless), and the `inbox` field disappears from `am_status`. Drawers written under either version are unaffected — nothing about them is new. T3's wing renames are undone with `am_merge_wing` in the opposite direction.

## Follow-ups

