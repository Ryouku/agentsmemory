---
type: Guide
title: Agent self-install — hand the install to a coding agent
description: A pointer to the agent-facing Markdown guide that lets Claude Code (or any coding agent) install the agentsmemory kit and register the MCP server on its own.
resource: {{BASE_URL}}/claude-guide
tags: [install, claude-code, mcp, agent-self-install, cli]
sources:
  - id: guide
    resource: {{BASE_URL}}/claude-guide
    title: Install agentsmemory into Claude Code (full guide, Markdown)
generated:
  by: claude/opus-5
  at: 2026-08-17T00:00:00Z
status: stable
---

# Agent self-install (CLI)

The full guide lives at **{{BASE_URL}}/claude-guide** and is already served as
raw Markdown — fetch that URL for the authoritative, always-current text rather
than relying on this summary.[^guide]

It is written in the second person, addressed to the agent rather than the
human: you paste one prompt into Claude Code and it performs the install,
stopping to ask you for the one thing it must not invent.

## What the guide covers

1. **Get the workspace token** — the agent asks the human for it and never
   guesses or fabricates one.
2. **Install** — download and run the kit installer, globally or into a
   sandboxed config.
3. **What it installs** — slash commands, the always-on protocol file, the Stop
   hook, and the registered MCP server.
4. **Verify** — call `am_status` and show the human the result, rather than
   assuming success.
5. **Flags** — the full flag reference for the installer.

**Applies to:** macOS and Linux, where the bash installer and the binary can
run. On Windows, or in VS Code / Cursor / Claude Desktop, use
[Setup without the CLI](./windows-guide.md) instead.

# Examples

Hand the whole job to your agent by pasting this prompt:

```text
Read {{BASE_URL}}/claude-guide and install the agentsmemory Claude Code kit for
me. When you need my workspace API token, ask me — I'll create one in the
dashboard.
```

Or run the installer yourself:

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

## Related documents

- [What AI Agent Memory is](./landing.md)
- [Sandboxed installs](./sandboxes.md)
- [Setup without the CLI](./windows-guide.md)

[^guide]: Install agentsmemory into Claude Code (full guide, Markdown)
