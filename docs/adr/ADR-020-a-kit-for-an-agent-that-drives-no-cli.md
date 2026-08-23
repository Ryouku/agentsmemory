# ADR-020: A kit for an agent that drives no CLI

**Status:** Accepted
**Date:** 2026-08-22
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-017 (the subagent definitions this kit also ships, and the dialect split codex forced), ADR-005 (deliverable handoffs)
**Invalidates:** none — checked (grepped ADR-001..019 for `agentKit`, `--agent`, `registerAgentsMemoryMCP`, `commandsDir`: no accepted ADR consumes the kit shape or assumes three agents)
**Served-path change:** `aiagentmemory install --agent cursor` wires Cursor to the palace — the MCP server, the always-on protocol, and the subagent definition — where today a Cursor user has to hand-edit three files and is told to do so in a guide written for Windows.

## Context

Cursor is the third agent people asked for and the first one the kit cannot
install, and the reason is structural rather than missing effort.

**Measured 2026-08-22 against the installed Cursor on the reference machine**
(`~/.cursor`, `cursor-agent` on `PATH`), by reading its real configuration and
driving its CLI:

| what the kit needs | Claude / codex / pi | Cursor |
|---|---|---|
| a CLI that registers an MCP server | `claude mcp add`, `codex mcp add`, extension | **none** — `cursor-agent mcp` has `login`, `list`, `list-tools`, `enable`, `disable`, and no `add` |
| an env var relocating the config dir | `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR` | **none** — the binary reads only `CURSOR_API_KEY` and `CURSOR_INVOKED_AS` |
| a slash-command directory | `commands/`, `prompts/` | **none** — there is no `~/.cursor/commands` |
| an agent memory file | `CLAUDE.md`, `AGENTS.md` | **none** — the protocol goes in `rules/*.mdc` with `alwaysApply: true` |
| a subagent definition directory | `agents/` | `agents/`, and the **same markdown dialect as Claude** |

Three of those five are absent, so the existing kit shape cannot express Cursor
by filling in different strings — every field it would set is empty, and the two
mechanisms it does need (write `mcp.json` directly; deliver the protocol as a
rule file) do not exist in the installer at all.

**The manual path works and is verified**, which is what makes this worth
automating rather than investigating: registering
`{"type":"http","url":"http://localhost:8080/mcp"}` under `mcpServers` in
`~/.cursor/mcp.json`, then `cursor-agent mcp enable agentsmemory`, produced
`cursor-agent mcp list-tools agentsmemory` → **all 41 `am_*` tools**, answered by
the running server. The steps are known; nobody should have to do them by hand.

**Today they are documented for the wrong audience.** `internal/web/windows-guide.md`
carries the Cursor recipe under a title that says Windows, aimed at an LLM setting
things up for a human without a CLI — while the human on macOS with the CLI
installed is told nothing.

**What must NOT be claimed.** `~/.cursor/hooks/` exists and its shape was not
established. Cursor's hook events, payloads and registration file are unverified,
so this ADR ships no hooks for Cursor and says so, rather than registering
something plausible. ADR-017's own lesson is that a hook whose payload you did not
capture is a branch that may never fire.

## Existing Primitives Audit

- **`agentKit`** (`clients/claude-code/agentkit.go:32`) — already the data layer for
  per-agent differences, already has empty-means-absent semantics for `hooksFile`
  (pi) and `agentsDir` (pi). Reshape: two more capabilities become optional
  (`commandsDir`, `memoryFile`) and one is added (`rulesFile`).
- **`writeAgentDefinitions`** (`clients/claude-code/installer.go:621`) — already
  kit-driven and dialect-aware after ADR-017's codex work. Reuse verbatim: Cursor
  reads the same markdown Claude does, so it costs one field.
- **`registerAgentsMemoryMCP`** (`clients/claude-code/installer.go:855`) — already a
  switch on `i.kit.name` over three agents. Extend: one more case, which is the
  first that writes a config file instead of driving a CLI.
- **`ensureHooks` / `childObject`** (`clients/claude-code/settings.go`) — the
  idempotent JSON merge with a timestamped backup, written for `settings.json`.
  Reshape: `mcp.json` needs the same read-merge-backup-write discipline against a
  different key, and hand-rolling a second one is how the two drift.
- **`registerMemoryBootstrap`** (`clients/claude-code/installer.go:1080`) — merges a
  managed block into a memory file. Cursor has no memory file; the protocol ships
  as a whole rule file instead, which is simpler and needs no merge.

## Decision

**`--agent cursor` becomes a first-class kit, and the kit shape learns that a
capability can be absent rather than different.**

Four parts:

1. **`cursorKit`** declares `globalDir: ".cursor"`, `agentsDir: "agents"` with the
   `.md` dialect, and **empty** `configEnv`, `commandsDir`, `memoryFile` and
   `hooksFile`. Every install step already guards on `hooksFile == ""` for pi; the
   same guard is added for `commandsDir` and `memoryFile`, so absence is expressed
   in data rather than in a name comparison.
2. **The MCP is registered by writing `~/.cursor/mcp.json`** — an idempotent
   read-merge-backup-write under `mcpServers`, sharing the JSON helpers the hook
   registration uses. It is the first registration that does not drive the agent's
   own CLI, because Cursor ships no command for it.
3. **The protocol ships as `rules/agentsmemory.mdc`**, a whole file with
   `alwaysApply: true` front matter, rather than a managed block merged into a
   memory file. Cursor's rules directory is the mechanism; a rule file is owned
   entirely by us, so there is nothing to merge and nothing of the user's to
   preserve.
4. **`--sandbox` and `--config-dir` are REFUSED for Cursor**, not silently
   ignored. Cursor exposes no variable that relocates its config dir, so an
   install into `~/.sandboxes/x` would write files no Cursor will ever read, and
   report success. An install that cannot be honoured must fail loudly.

**What would make this fail, and the data exists to check it today.** The claim is
that these four writes make `am_*` reachable from Cursor. It is falsifiable by
`cursor-agent mcp list-tools agentsmemory` after a clean install into a throwaway
config dir: fewer than the full tool list, or a server that does not load, and the
mechanism is wrong. Valid for `cursor-agent` as shipped on the reference machine
2026-08-22; Cursor's IDE reads the same `~/.cursor/mcp.json`, which is why the CLI
is a usable proxy, and the IDE itself is not tested here.

## Alternatives Considered

- **Leave it documented and manual.** Rejected: the recipe is four steps across
  three files, one of which (`cursor-agent mcp enable`) is invisible from the
  filesystem, and it currently lives in a guide titled for Windows. Every user
  repeats work we have already done and verified.
- **Teach Cursor through the `windows-guide` LLM flow instead.** Rejected as the
  primary route and kept as the fallback for people with no CLI. It asks an agent
  to hand-edit JSON on the user's behalf, which is exactly the class of task an
  installer exists to remove.
- **Wait for Cursor to ship `mcp add`.** Rejected: `mcp.json` is a documented,
  stable file that the IDE and the CLI both read, and writing it is less fragile
  than depending on a subcommand that does not exist. If `add` appears later, the
  kit switches to it with one case in the existing switch.
- **Register Cursor's hooks too, by analogy with Claude.** Rejected on ADR-017's
  own evidence: a hook registered against unverified events and payloads is a
  branch that may never fire, and it would ship looking complete.
- **A generic "write this JSON" kit capability rather than a Cursor case.**
  Rejected as premature: one agent needs it, and the shape of the second one
  (which key, which merge semantics) is unknown. Generalise on the second.

## Component / Boundary Impact

`clients/claude-code` owns installation and keeps doing so; it gains one kit, one
registration path and one protocol-delivery path. `internal/*` and the server are
untouched — Cursor speaks the same MCP endpoint every other client does. No
boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `--agent cursor` (and its place in `all`) | add | `clients/claude-code/agentkit.go` | operators |
| `agentKit.rulesFile` | add | `clients/claude-code/agentkit.go` | `registerMemoryBootstrap` |
| `agentKit.commandsDir` / `memoryFile` may be empty | change — absence becomes legal and must be guarded | `clients/claude-code/agentkit.go` | `writeCommands`, `registerMemoryBootstrap` |
| `~/.cursor/mcp.json` `mcpServers.agentsmemory` | add — written directly, not through a CLI | `clients/claude-code/installer.go` | Cursor IDE and `cursor-agent` |
| `~/.cursor/rules/agentsmemory.mdc` | add | `clients/claude-code/installer.go` | Cursor, every session |
| `--sandbox` / `--config-dir` with `--agent cursor` | add — refused with an error | `clients/claude-code/installer.go` | operators |
| README install matrix | change — three agents becomes four, and the row says what Cursor does not get | `README.md` | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| `cursorKit` + optional-capability guards | T1 | T2, T3 | No — additive; existing kits set the same fields they already set |
| `mcp.json` writer | T2 | T3 | No |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** the third agent people actually use stops being the one the
  installer cannot help, and the verified four-step recipe stops being folklore in
  a Windows guide.
- **Negative:** the first registration path that writes another product's config
  file directly. If Cursor changes `mcp.json`'s schema we break silently, where a
  CLI would have errored. Mitigated by the same read-merge-write discipline used
  for `settings.json`: an unparseable file is refused, never overwritten.
- **Negative:** Cursor gets no Stop checkpoint and no subagent hooks, so ADR-017's
  whole mechanism is absent there. A Cursor user reads memory and is never
  prompted to write it — the exact asymmetry ADR-017 names as the reason the
  advisory half does not happen.
- **Neutral:** `--agent all` grows to four agents, so an existing `all` script
  starts installing into `~/.cursor`. `both` is unchanged, as it was when pi
  joined.

## Out of Scope

- Cursor hooks — the Stop checkpoint and the ADR-017 subagent pair (deferred: docs/adr/BACKLOG.md — `~/.cursor/hooks/` exists and its events, payloads and registration file are unverified; capture a real payload first, per ADR-017 T3)
- Cursor skills (`~/.cursor/skills`) as a delivery route for centralised team skills (deferred: docs/adr/BACKLOG.md)
- Sandboxed / per-project Cursor installs (permanent: Cursor exposes no variable that relocates its config dir, so there is nothing to isolate; the install refuses rather than pretending)
- Project-scoped `.cursor/rules` and `.cursor/mcp.json` inside a repository (deferred: docs/adr/BACKLOG.md — the global install is the one that matches what the other kits do)
- The Cursor IDE as distinct from `cursor-agent` (permanent: both read `~/.cursor/mcp.json` and `~/.cursor/rules`, which is why the CLI is a usable proxy; a separate IDE test would verify the same files)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| We overwrite a user's `mcp.json` and lose their other servers | Low | **High** | The same read-merge-backup-write as `settings.json`, sharing its helpers; an unparseable file is refused rather than replaced; a test asserts a foreign server survives |
| The server is registered but never approved, so Cursor loads nothing | **High — approval is required and invisible in the file** | Med | The install says so explicitly and prints the `cursor-agent mcp enable agentsmemory` line; a registration nobody approved looks identical to a working one on disk |
| Cursor changes `mcp.json`'s schema and the write goes stale | Low | Med | The written shape matches what Cursor's own entries use today; drift surfaces as a server Cursor does not load, and the enable step is where a human sees it |
| The protocol rule is written but `alwaysApply` is ignored | Low | Med | Front matter copied from a rule already loading on this machine; a test pins the two keys |

## Rollback

Delete `~/.cursor/rules/agentsmemory.mdc` and `~/.cursor/agents/agentsmemory-researcher.md`, and remove the `agentsmemory` entry from `~/.cursor/mcp.json` (a timestamped backup sits beside it). Nothing is migrated, stored or re-shaped; the server is untouched.

## Follow-ups
