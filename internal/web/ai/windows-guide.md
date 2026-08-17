---
type: Guide
title: Setup without the CLI — Windows, VS Code, Cursor, Claude Desktop
description: A pointer to the agent-facing Markdown guide for clients the bash installer cannot reach, which connect to the remote MCP server by hand-written config instead.
resource: {{BASE_URL}}/windows-guide
tags: [install, windows, vscode, cursor, claude-desktop, mcp, remote-mcp]
sources:
  - id: guide
    resource: {{BASE_URL}}/windows-guide
    title: Install agentsmemory without a CLI (full guide, Markdown)
generated:
  by: claude/opus-5
  at: 2026-08-17T00:00:00Z
status: stable
---

# Setup without the CLI

The full guide lives at **{{BASE_URL}}/windows-guide** and is already served as
raw Markdown — fetch that URL for the authoritative, always-current text rather
than relying on this summary.[^guide]

**Why a separate guide exists:** the installer is a bash script plus a
Linux/macOS binary, so Windows, VS Code, Cursor and Claude Desktop users have no
installer to run. They do not need one — agentsmemory is a *remote* MCP server,
so this guide walks an assistant through writing the user-level MCP config for
its own host instead.

## What the guide covers

0. **Rules for the assistant** — never invent or guess the token; never send it
   anywhere but the local config file; show the human the path before writing;
   if a documented path is missing, stop and check, because editors move config
   directories between versions.
1. **Work out which client you are running in.**
2. **Get the workspace token** — ask the human for it.
3. **Write the global MCP config** — per-client instructions for VS Code
   (GitHub Copilot Chat), Cursor, and Claude Desktop.
4. **Install the memory protocol** — the always-on operating protocol file.
5. **Restart and verify** — by calling `am_status` and showing the result, not
   by assuming.

It closes with an explicit statement of what this setup gets you and what it
does not, compared with the full CLI kit.

# Examples

Hand the whole job to the assistant in your editor by pasting this prompt:

```text
Read {{BASE_URL}}/windows-guide and set up agentsmemory for me globally in this
editor. Ask me for my workspace API token when you need it.
```

## Related documents

- [What AI Agent Memory is](./landing.md)
- [Sandboxed installs](./sandboxes.md)
- [Agent self-install guide](./claude-guide.md)

[^guide]: Install agentsmemory without a CLI (full guide, Markdown)
