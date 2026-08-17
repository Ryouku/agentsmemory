---
type: Guide
title: Sandboxed installs — one agent config per project
description: How aiagentmemory installs into an isolated per-project agent config under ~/.sandboxes, what each supported agent CLI needs, and what a recorded launch commits to a repository.
resource: {{BASE_URL}}/sandboxes
tags: [sandbox, install, claude-code, codex, pi, mcp, agent-config]
sources:
  - id: sandboxes
    resource: {{BASE_URL}}/sandboxes
    title: Sandboxed installs — public guide
generated:
  by: claude/opus-5
  at: 2026-08-17T00:00:00Z
status: stable
---

# Sandboxed installs

**One sandbox per project. Whichever agent you run.**

A sandbox is a complete agent config of its own — commands, settings, MCP
servers and workspace token — living under `~/.sandboxes/<name>`. Your global
agent stays exactly as it was, and the project's memory never mixes with anyone
else's.[^sandboxes]

Every command works either way. Add `--sandbox` to get an isolated config, or
leave it off to wire the kit into the agent you already run.

**One project, one config** (`~/.sandboxes/<name>`)

- Commands, settings and MCP servers scoped to this project
- Its own workspace token — revoke it without touching the others
- Delete the directory and the sandbox is gone, cleanly

**Your global agent, untouched** (`aiagentmemory install`)

- No sandbox? The kit merges into the config you already run
- Managed blocks merge — your own memory file is preserved
- Switch modes any time; the installer is idempotent

# Schema

Three agent CLIs are supported. They differ in their config-dir variable, their
commands directory, whether their memory file resolves imports, and — most
consequentially — whether they speak MCP at all. Every value below was verified
against the shipping CLIs (Claude Code, codex-cli 0.137, pi 0.84.2) rather than
inferred from documentation.

| | Claude Code | Codex | pi |
| --- | --- | --- | --- |
| **Config variable** | `CLAUDE_CONFIG_DIR` | `CODEX_HOME` | `PI_CODING_AGENT_DIR` |
| **Global config dir** | `~/.claude` | `~/.codex` | `~/.pi/agent` |
| **Slash commands** | `commands/` — invoked `/M`, `/am`, `/load-skill` | `prompts/` — invoked `/prompts:M` | `prompts/` — invoked `/M` |
| **Memory file** | `CLAUDE.md` — `@imports` the protocol beside it | `AGENTS.md` — protocol inlined (no import directive) | `AGENTS.md` — protocol inlined (no import directive) |
| **Session gate** | `settings.json` — Stop hook | `hooks.json` — Stop hook | none native — the checkpoint ships in the extension |
| **Our MCP** | native: `claude mcp add --transport http`, bearer header | native: `codex mcp add --bearer-token-env-var` | bridged: `extensions/agentsmemory.ts` registers each remote tool natively |

## Claude Code

The reference install: native MCP, native hooks, and a memory file that resolves
imports.

- `--recommended` also installs the codebase-memory MCP and the eidos + codex
  plugins.
- A sandbox keeps its own commands, settings, MCP servers and token — nothing
  leaks into `~/.claude`.

## Codex

Same shape as Claude, but the token travels through the environment and hooks
need trusting.

- `codex mcp add` has no header flag, so the token is stored `0600` in
  `agentsmemory.env` and exported by `run`.
- Codex skips untrusted hooks: open `/hooks` once and trust the agentsmemory
  Stop hook.
- A sandbox is a whole `CODEX_HOME` — including `auth.json` — so it starts
  logged out: `CODEX_HOME=<dir> codex login`.

## pi

pi ships no MCP client and no hooks by design, so the kit brings its own bridge
extension.

- The bridge lists the workspace tools at startup and re-registers them as
  native pi tools, so `am_*` calls work unchanged.
- A sandbox is the whole agent dir — including `auth.json` — so sign in inside
  it or pass a provider key.
- `--recommended` adds nothing for pi: codebase-memory is a stdio MCP and
  eidos/codex are Claude plugins.

## Pinning a project's launch

`aiagentmemory init` splits the record in two, because a committed sandbox name
would be wrong on every machine but its author's.

**Committed — the team's half**
(`aiagentmemory init --sandbox acme --agent codex -- --model opus`)

- Writes `.aiagentmemory`: which agent, and the flags it launches with
- Everything after `--` is stored verbatim and replayed by `load`
- Safe to commit — it names no sandbox and carries no token

**Machine-local — your half** (`~/.sandboxes/agents`)

- One line per project: its absolute path, then your sandbox name
- Never in the repository, so it needs no `.gitignore` entry
- An entry on a parent directory covers every project beneath it

## Carrying an existing setup in

**Inherit your setup**
(`aiagentmemory install --agent pi --sandbox acme --copy`)

- Logins, MCP servers, plugins, skills and settings, copied in
- History, logs and caches stay behind — the bulk never travels
- Nothing already in the sandbox is overwritten

**Share one login**
(`aiagentmemory install --agent pi --sandbox acme --shared-auth`)

- Credential files link back to the global config
- Re-authenticate once; every sandbox sees it
- Claude on macOS already shares its keychain

# Examples

```bash
# Create an isolated config at ~/.sandboxes/<name>.
aiagentmemory install --sandbox <name>

# Choose which agent CLIs the kit is wired into.
aiagentmemory install --agent pi|codex|both|all

# Open an agent in that sandbox — args pass through to the CLI.
aiagentmemory run [--agent codex|pi] <name>

# Open an agent against its own global config instead.
aiagentmemory wrap [--agent codex|pi]

# Record this project's launch: agent and flags committed, sandbox name local.
aiagentmemory init --sandbox <name> [-- agent flags]

# Open the recorded agent and sandbox from anywhere inside the project.
aiagentmemory load [-- extra flags]
```

## Related documents

- [What AI Agent Memory is](./landing.md)
- [Agent self-install guide](./claude-guide.md)
- [Setup without the CLI](./windows-guide.md)

[^sandboxes]: Sandboxed installs — public guide
