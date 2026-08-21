# ADR-012: The agent surface enforces the role it reports

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** `internal/mcpserver/server.go` (the registrar and the shared admission), `internal/tenant/tenant.go` (role resolution), `internal/web` (the dashboard's enforcement, the model this copies), ADR-008 (the end-to-end harness this extends with a role)
**Invalidates:** none — checked. ADR-001..011 grepped for `Role`, `CanWrite`, `admit(`: none reasons about authorization. ADR-008 owns end-to-end tool coverage and gains a scenario rather than losing one.
**Served-path change:** Every mutating MCP tool refuses a caller whose role may not write, and the refusal names the role and the remedy. A member-role key that could delete any drawer now cannot. Shipped.

## Context

The server resolves a real role for every MCP call and enforced it on one tool out of forty-one.

`ResolveToken` (`internal/tenant/tenant.go:285`) looks up the API key, then `tenantFromKey`
(`tenant.go:305`) reads the caller's membership row and returns `Tenant{TeamID, UserID, Role}`,
defaulting to the least-privileged `RoleMember` when no membership exists. So the role is present,
correct, and least-privileged by default on every request.

`admit` (`internal/mcpserver/server.go:210`) — the shared admission path every tool calls — checked
authentication and the monthly usage cap, and never looked at `t.Role`. `am_status`
(`server.go:292`) reported `"role": <role>` back to the agent. The only role check anywhere in
`internal/mcpserver` was `skillCaller.CanWrite` (`skills.go:27`), consumed by `am_update_skill`
alone. `internal/web` enforces roles in about twenty places.

So a `member`-role key reached every mutating tool except `am_update_skill`: `am_add_drawer`,
`am_update_drawer`, `am_delete_drawer`, `am_diary_write`, `am_kg_add`, `am_kg_invalidate`,
`am_create_tunnel`, `am_delete_tunnel`, `am_delete_hallway`, `am_merge_wing`, `am_mark_anchors`,
`am_recompute_graph`, `am_mine`, `am_reconnect`. And a member can obtain such a key without help:
`postRotateKey` (`internal/web/keys.go:72`) is gated on membership alone, deliberately — "any member
may rotate their own without an admin role", which is correct for a key whose privileges match the
member's.

Two things kept this from being worse, and both are worth stating because they show the shape of the
mistake. `am_delete_wing` is registered only in local mode (`internal/mcpserver/admin.go:162`), with
a comment reasoning explicitly about who is on the far end of a shared deployment — someone thought
this through for the most destructive tool and did not generalise it. And every write is
workspace-scoped, so this is a privilege escalation inside a workspace, never across one.

This is the defect class this repository has been fixing all week, on the authorization surface: a
DECLARED set (three roles and a `CanWrite` predicate) and a SELECTING set (one tool) drifted apart,
and nothing compared them. It was found by an inventory sweep asking "which declared/selecting pairs
have no gate", not by any test.

## Existing Primitives Audit

- `tenant.Role` + `RoleMember`/`RoleWriter`/`RoleAdmin` (`tenant.go:29-34`) — reused, not redefined.
- `skillCaller.CanWrite` (`skills.go:27`) — the predicate already existed and was right. It now
  delegates to the one definition rather than spelling it a second time.
- `registrar.add` (`server.go:67`) — every tool already funnels through one registration point, which
  is why a guard could be placed once instead of in forty-one handlers.
- `admit` (`server.go:210`) — left alone. Authentication and metering apply to reads too; only the
  role question is write-specific, and putting it in `admit` would have made every read pay a check
  that must not refuse it.
- `internal/mcptest` (ADR-008) — the end-to-end harness resolves its tenant per request from a
  header, so a role could be threaded the same way the workspace already is.

## Decision

**A tool that changes stored memory is registered through `registrar.addWrite`, which refuses the
call when the caller's role may not write.** Read tools keep `registrar.add`.

The classification lives at the registration that performs the enforcement, not beside it. There is
no list of write tools to keep in step with a list of guards: registering a mutating tool with `add`
IS forgetting the check, and `TestEveryMutatingToolIsRegisteredAsAWrite` fails on it.

`canWrite(role)` is the single definition of "may change stored memory", so the MCP surface and the
dashboard cannot drift into two policies about the same word.

The guard fails closed on a missing tenant and refuses BEFORE the handler runs.

## Alternatives Considered

- **Check the role inside each mutating handler.** Rejected: it is a thing every future handler has
  to remember, which is exactly how the current state arose — `am_update_skill` remembered and
  thirteen others did not.
- **Put the role check in `admit`.** Rejected: `admit` serves reads too, so the check would need a
  parameter saying whether this call is a write — which is the same classification, moved somewhere
  it is not enforced, and readable as "authenticated" by a handler that passes the wrong value.
- **Derive the write set from `cmd/server/mcp.go`'s `readOnlyTools()`.** Attractive because it would
  also close the CLI/HTTP parity axis, and rejected because it points the dependency the wrong way:
  `internal/mcpserver` would learn about a CLI adapter, and the enforcement would live one package
  away from the registration. Filed instead — the parity axis is real and it is not this ADR's.
- **Treat the role as informational on MCP, and remove `am_update_skill`'s check.** Considered
  seriously, because `tenantFromKey`'s comment ("the key already proves team scope") can be read that
  way. Rejected: the dashboard offers three roles and describes `member` as read-only, so a member
  who can delete every drawer through an agent makes the dashboard's promise false. If roles were
  meant to be advisory the fix would be to stop offering them.

## Component / Boundary Impact

No new module. `internal/mcpserver` gains one registration path and one predicate; `internal/mcptest`
gains a role on its per-request tenant, defaulting to admin so every existing scenario keeps the
privileges it was written with.

## Wiring & Contract Changes

- `CatalogEntry` gains `Write bool`, set by the registration that enforces the role — so the flag and
  the enforcement cannot disagree. It reaches one wire surface: `am_skillset`'s `tools` payload,
  which serialises `reg.catalog` directly (`internal/mcpserver/skillset.go:48`). It does NOT appear
  in `tools/list`, which mcp-go builds from the registered tools and knows nothing of this field, nor
  in `am_status`, which carries no catalogue. An earlier version of this line claimed both; corrected
  2026-08-21 after an audit checked it.
- Mutating tools now return a tool-level error for a read-only role. This is a behaviour change for
  any deployment that has member-role keys writing today: those writes begin to fail, correctly, and
  the refusal names the role and says an admin can grant `writer`.
- `registerAnchors` is split: `list_anchors` (read) and `mark_anchors` (write) were registered
  together, and a registration that builds both cannot carry one classification honestly.

## Implementation

1. `registrar.addWrite` + `writeGuard` + `canWrite` in `internal/mcpserver/server.go`.
2. Split `registerAnchors`; convert the fifteen mutating registrations.
3. `skillCaller.CanWrite` delegates to `canWrite`.
4. Gates: `TestEveryMutatingToolIsRegisteredAsAWrite` (structural, AST),
   `TestOneToolPerRegistration` (keeps the attribution sound),
   `TestAReadOnlyRoleIsRefusedByEveryWriteTool` + `TestAnUnauthenticatedCallIsRefusedBeforeTheRoleCheck`
   (behavioural, drives the real guard), and end-to-end
   `TestScenarioAMemberMayReadAndMayNotWrite` / `TestScenarioAWriterMayWrite`.

## Consequences

A member can read everything and write nothing over MCP, matching what the dashboard says the role
means. An agent registered with a member's key will start failing its write-back step; the refusal
says why, which is the difference between a fixable error and a silent one.

The structural gate is the durable part: the next mutating tool is classified correctly or the build
fails, and neither the author nor the reviewer has to notice.

## Out of Scope

- CLI/HTTP parity for the read/write split — three hand-kept mirrors exist and nothing compares them (deferred: docs/adr/BACKLOG.md)
- A third level finer than read/write, e.g. admin-only destructive tools; `delete_wing` is already deployment-gated and a third level should be designed against real role usage rather than guessed (deferred: docs/adr/BACKLOG.md)
- Whether `member` should be able to READ every wing in a shared workspace (permanent: this ADR's question is writes; wing scoping is ADR-005's)
- An audit trail of who wrote what — writes and refusals are both unlogged, and that question is larger than authorization (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| A deployment has member-role keys writing today and they break on upgrade | Medium | High for that workspace | The refusal names the role and the remedy; an admin grants `writer` in the dashboard. Stated in the changelog rather than discovered. |
| The mutating-call list used by the structural gate goes stale, so a new write tool is not recognised | Medium | High — the gate silently stops covering | CLOSED 2026-08-21: `TestMutatingCallListIsComplete` derives the mutating set transitively from the domain packages and fails when a handler calls a write method absent from the list. Writing it found three methods the list had missed and three names in it that matched no method at all. |
| A read tool is registered with `addWrite` and refuses members a read | Low | Medium | The behavioural scenario asserts a member CAN read; a guard that refused everything fails it. |

## Rollback

Revert the commits. `addWrite` folds back into `add`, the `Write` field disappears from the
catalogue, and the four gates go with it. No persistent state changes, no migration: the role was
already stored and already resolved — this ADR only adds a consumer.

## Follow-ups

- [ ] Make the memory-write / observability-write split derivable instead of judged: `incidentalWrites` currently excuses `Search`, `RecallStats` and `CheckDuplicate` by hand, because "writes a row" is in the source and "changes what someone can recall" is not. Resolving each method's reachable `TableName()` values against a declared observability-table set would derive it.
- [ ] Report whether any real deployment had member-role keys performing writes, once anyone has
      upgraded — the risk table guesses "medium" with no evidence, and one operator's answer settles it.
