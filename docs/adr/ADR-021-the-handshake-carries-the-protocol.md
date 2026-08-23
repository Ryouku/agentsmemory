# ADR-021: The handshake carries the protocol

**Status:** Accepted
**Date:** 2026-08-22
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-020 (the kit for an agent that drives no CLI — Claude Desktop is the next rung down), ADR-017 (placement beats instruction: the same finding, one layer out)
**Invalidates:** none — checked (grepped ADR-001..020 for `initialize`, `instructions`, `WithInstructions`, `claude_desktop_config`: no accepted ADR consumes the handshake result or assumes what it carries)
**Served-path change:** Every MCP client — including ones that can take no protocol file at all — receives the wing rule and the recall-first instruction in the `initialize` response, instead of inferring rules from the tool schema. And `aiagentmemory install --agent claude-desktop` registers the server there instead of it being hand-written folklore.

## Context

**Measured 2026-08-22, and the failure is a real one that already happened.**

Claude Desktop was registered against the local server and asked what it could
see. It answered correctly about the palace — 531 drawers, 11 wings — and then
volunteered a rule nobody gave it:

> "this MCP registration names no default wing, so an `am_search` without an
> explicit wing scopes to an empty namespace and will come back with nothing.
> I'll pass `wing: "*"` or a specific wing on every recall."

**The empty-namespace claim is wrong, and `"*"` as an ordinary fallback is
harmful.** The later multi-wing control corrected the interpretation recorded in
the first version of this ADR: when a registration has no `default_wing`, omitting
`wing` searches every wing; the three `wing_agentmemories` hits observed through
Desktop did not prove that the registration was scoped there. A specific project
wing was the safe half of the old answer. `wing: "*"` deliberately searches the
whole workspace, which is measurably worse for ordinary project recall: unrelated
projects do not remove the answer, they add competitors ahead of it.

**Why it got there is the point.** Desktop has the 41 tools and NONE of the
protocol — no `CLAUDE.md`, no rules file, no hooks — because it has nowhere to put
one. So it reasoned about wing scoping from the tool schema alone and produced a
confident, plausible, wrong rule. A Claude Code or Cursor session has that answer
in its always-on protocol and never has to guess.

**The surface that reaches it already exists and we do not use it.** mcp-go
v0.55.1 has `server.WithInstructions(string)`, returned to every client in the
`initialize` response (`server/server.go:581`, `:1180`). Probed against the
running server: the initialize result carries `capabilities`, `protocolVersion`
and `serverInfo`, and **no `instructions`**. Every client that has ever connected
has been told nothing.

That is the same shape ADR-017 measured from the other direction. There, one
paragraph next to the task moved subagent recall from 0/5 to 5/5 while the entire
protocol, present and first, produced nothing. Here there is no protocol at all
for some clients, and a paragraph the transport itself delivers costs them
nothing to receive.

**And the registration is folklore.** Claude Desktop's config
(`~/Library/Application Support/Claude/claude_desktop_config.json`, key
`mcpServers`) took a hand-written entry, which is exactly what ADR-020 stopped
accepting for Cursor. Worse, the route the project's own `windows-guide.md`
recommends — the Custom-connector UI, or `npx mcp-remote` — is aimed at the hosted
service and needs Node.js, while the product already ships a better one:
`mcp-stdio --url`, a bridge in the server binary that needs nothing else.
Verified end to end: piping `initialize` + `tools/list` through it returned
`serverInfo {agentsmemory, 0.1.0}` and 41 tools.

## Existing Primitives Audit

- **`server.WithInstructions`** (mcp-go v0.55.1) — already the MCP-standard slot
  for exactly this, already returned on initialize. Reuse verbatim: one option at
  construction.
- **`ensureMCPServer`** (`clients/claude-code/settings.go`, ADR-020 T2) — already
  the idempotent read-merge-backup-write against an `mcpServers` key, already
  refuses an unparseable file. Reuse verbatim: Claude Desktop's config has the
  same key and the same shape as Cursor's.
- **`agentKit` with optional capabilities** (ADR-020 T1) — already expresses "this
  agent has no commands dir / memory file / hooks". Reuse: Desktop has none of
  them plus no agents dir, which is one more empty field and no new mechanism.
- **`mcp-stdio --url`** (`cmd/server/stdio.go:62`) — already a stdio↔HTTP bridge
  that opens no database. Reuse verbatim as the command Desktop spawns.
- **`am_skillset`** — already the server-side wake-up playbook. Reuse as the
  pointer the instructions send a client to, rather than restating it.

## Decision

**A client that cannot take a protocol file still gets the protocol, through the
handshake — and Claude Desktop stops being installed by hand.**

Two mechanisms:

1. **The server sets `instructions` on the MCP handshake.** Short, and short is a
   constraint rather than a preference: it lands in every client's context on
   every session, so it names the wing rule, says recall before acting, and points
   at `am_skillset` for everything else instead of restating it. It must state the
   thing Desktop got wrong — *pass no wing unless you mean to look elsewhere* —
   because that is the failure this ADR was opened by.
2. **`--agent claude-desktop` becomes a kit.** The thinnest one: it registers the
   server in `claude_desktop_config.json` via `ensureMCPServer` and installs
   nothing else, because Desktop has nowhere to put anything else. The entry
   spawns `mcp-stdio`, so the install resolves and names the server binary the way
   `--socket` installs already do, and fails loudly when it cannot find one rather
   than writing a `command` that does not exist.

**What would make this fail, and the data exists to check it today.** Mechanism 1
claims a client receives and uses `instructions`. It is falsifiable by probing the
initialize result for the text (it is there or it is not) and then by asking a
Desktop session the same wing question that produced the wrong answer. **If the
text arrives and the answer does not change, mechanism 1 is emphasis on a channel
nobody reads**, and the honest response is to say so in the ADR rather than keep
it — the deferred alternative is that clients with no protocol file are simply
told, in the docs, that they are read-only consumers. Mechanism 2 does not depend
on that result and ships either way. Valid for mcp-go v0.55.1, whose
`WithInstructions` is what fills the field.

### T3 measurement — the answer changed

Measured 2026-08-22 at 14:37 EEST with Claude Desktop 1.34493.1. Desktop was
fully quit and relaunched, a fresh chat was opened, and the unprimed prompt was:

> what happens to an am_search that names no wing

The exact bridge command from `claude_desktop_config.json` returned MCP server
`agentsmemory` 0.1.0 and an `initialize.instructions` value containing this rule:

> If `default_wing` names a wing, this registration is scoped to one project and
> omitting the wing argument keeps recall there. If `default_wing` is EMPTY,
> omitting it searches EVERY wing ... `wing:"*"` is for genuinely cross-project
> questions, never a safe default.

The running server artifact was container
`a660d4e2320c62be848f825d63269ed308513c8b3c4cbafe5738d02d752bed8c`
with image configuration
`sha256:87fbb597224301a34324fb60b0fe6eaa170edd544a1619b26c32e192f84c976d`;
the host bridge SHA-256 was
`bfc3bd15e820454b3c4a90d008ce6a055853b4aacd2dc448cf22c762a3555530`.

Desktop answered:

> Two different behaviours, decided by one thing: whether the MCP registration
> carries a `default_wing`.
>
> - Registered with a wing → omitting `wing` silently scopes the recall to that
>   wing. The filter is implicit, so an empty result means "not in this project,"
>   not "not in memory."
> - Registered without one (or `SEARCH_SCOPE=workspace`) → omitting `wing`
>   searches every wing.
>
> Your current registration reports `default_wing: ""`, so in this session a
> bare `am_search` sweeps all 12 wings — all 550 drawers. Same rule governs
> `am_list_drawers` and `am_list_anchors`, deliberately: enumeration and recall
> have to agree on scope or one of them leaks results the other hides. The mental
> model worth carrying: omission is not "no filter," it's "inherit the filter."
> That's the failure mode — the same call means different things depending on how
> the server was registered, and nothing in the call site tells you which.
> Practical consequence: make scope explicit rather than relying on the default.
> Pass a project wing when you want one project, `wing: "*"` when you deliberately
> want the sweep. `"*"` and omission happen to be equivalent for you right now,
> but `"*"` survives someone later registering the server against a project — the
> omitted version quietly changes meaning.

The treatment transcript above is verbatim except that named project-wing
examples and the unrelated retrieval-tuning coda were omitted so this ADR does
not reintroduce identifiers being removed from Git history. Neither omission
changes the scoping rule under test.

**Verdict: keep mechanism 1.** The answer changed on both material points. It
rejected the empty-namespace model, described the registration default correctly,
and made `"*"` a deliberate cross-project opt-in instead of ordinary recall.
This is one session and one question (`n = 1`); it establishes delivery and use
in this Claude Desktop build, not universal client behaviour. Mechanism 2 remains
independently useful.

## Alternatives Considered

- **Add `--wing` to the `mcp-stdio` bridge so the registration names one.**
  Rejected as the primary fix: it addresses one registration rather than the
  reason Desktop invented a rule. T3 later corrected the premise that this
  registration already had a narrow default and confirmed that the handshake
  solves the separate protocol problem. The follow-up was therefore delivered:
  Desktop and socket registrations pass `--wing`, while Codex uses the server's
  equivalent registration query parameter.
- **Put the protocol in the tool descriptions.** Rejected: they are per-tool and
  already long, the wing rule is not about any one tool, and a client that reads
  only the tools it calls would miss it.
- **Tell Desktop users to open the `/claude-guide` page.** Rejected: it is the
  current state. A protocol that requires a human to fetch it is the advisory half
  of a loop, which ADR-017 measured as the half that does not happen.
- **Ship the whole bootstrap protocol as the instructions text.** Rejected on
  ADR-017's own evidence AND on cost: it is thousands of words in every client's
  context on every session, and the full protocol reaching a subagent first and
  verbatim produced 0/5. Length is not what works.
- **A Desktop "extension" (`Claude Extensions/`) rather than a config entry.**
  Rejected as unexamined: the directory exists on the reference machine and its
  packaging format was not established, and ADR-017 T3's lesson is not to ship
  against a shape nobody captured.

## Component / Boundary Impact

`internal/mcpserver` gains one construction option and the text it serves —
server-side, and every transport inherits it because it is the handshake rather
than a route. `clients/claude-code` gains one kit that reuses ADR-020's writer.
The delivered follow-up gives `mcp-stdio` one registration-scope input; it remains
a raw JSON-RPC pipe and adds only the same wing header HTTP registrations carry.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| MCP `initialize` result `instructions` | add — the field has always been empty | `internal/mcpserver/server.go` | every MCP client, on every connection |
| `--agent claude-desktop` (and its place in `all`) | add | `clients/claude-code/agentkit.go` | operators |
| `claude_desktop_config.json` `mcpServers.agentsmemory` | add — written directly, as Cursor's is | `clients/claude-code/installer.go` | Claude Desktop |
| `agentKit.mcpConfigFile` | add — the config file a kit registers into when it drives no CLI | `clients/claude-code/agentkit.go` | `registerCursorMCP` / the Desktop path |
| `mcp-stdio --wing` / Codex `?wing=` | add — registration default, not a tool argument | installer / bridge | Desktop, socket clients, Codex and Codex subagents |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the instructions text | T1 | T3 (documents it) | No — additive; a client that ignores the field behaves exactly as today |
| `claudeDesktopKit` + the stdio entry | T2 | T3 | No |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** every client is told the wing rule, including the ones that cannot
  hold a protocol file, and including clients nobody has written a kit for.
  The handshake is the one channel that reaches all of them.
- **Positive:** the Desktop registration stops being folklore in a Windows guide,
  and stops recommending a Node.js bridge for a self-hosted server that ships its
  own.
- **Negative:** the instructions text is context every client pays for on every
  session, forever. It is capped and tested for length, and the cap has to be
  defended the way T2's was in ADR-017.
- **Negative:** the host now needs a server binary for the Desktop kit to work,
  which a Docker-only install does not have. The install must say so rather than
  writing a `command` that is not there.
- **Neutral:** `--agent all` grows to five. `both` is unchanged, as when pi and
  cursor joined.

## Out of Scope

- Claude Desktop extensions (`Claude Extensions/`) as a packaging route (deferred: docs/adr/BACKLOG.md — the directory exists and the format was not established)
- Per-session instructions naming the actual default wing (permanent: `WithInstructions` is a construction-time option and a hosted server serves many workspaces on one process, so the text must be true for all of them; `am_status` is where a client learns its own wing)
- Whether other MCP clients surface `instructions` to their model at all (deferred: docs/adr/BACKLOG.md — measured for Claude Desktop in T3, assumed nowhere else)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The instructions are delivered and ignored, so this ships and changes nothing | **Med — ADR-017 measured exactly this for a longer text** | Med | T3 re-asks Desktop the question that produced the wrong rule. Below a changed answer the ADR says so; mechanism 2 does not depend on it |
| The text grows until it is scenery | High | Med | A length ceiling asserted by a test, not intended by an author — the same defence ADR-017 T2 used |
| The Desktop entry names a binary that is not installed | **High — the reference machine had none** | High | The install resolves the server binary and fails with the build command rather than writing a broken `command` |
| A hosted client is told a wing rule that does not fit its workspace | Low | Low | The text states the RULE (pass none unless you mean elsewhere) and points at `am_status` for the specifics, rather than naming a wing |

## Rollback

Drop the `WithInstructions` option — the field returns to empty and every client behaves exactly as it did before, since an absent `instructions` is the state they all handled until now. Remove the `agentsmemory` entry from `claude_desktop_config.json` (a timestamped backup sits beside it). Nothing is stored, migrated or re-shaped.

## Follow-ups

- **Delivered 2026-08-22:** project-scoped registration for every shipped client.
  Cursor and Claude use headers, Pi uses its bridge environment, Codex uses the
  server's supported `?wing=` registration query (including its subagent TOML),
  and Desktop/socket registrations use `mcp-stdio --wing`. Omitting a tool-level
  wing now inherits that narrow default; `wing: "*"` remains the explicit opt-in.
