---
type: Guide
title: Connect agentsmemory to any agent — MCP install for every harness
description: A pointer to the harness-agnostic Markdown document that connects any MCP client to agentsmemory with a URL and a bearer token, including Windows hosts the bash installer cannot reach, and hands off to the memory-model setup afterwards.
resource: {{BASE_URL}}/install-memory-mcp
tags: [install, mcp, windows, vscode, cursor, claude-desktop, codex, windsurf, onboarding]
sources:
  - id: guide
    resource: {{BASE_URL}}/install-memory-mcp
    title: Connect agentsmemory to any agent (MCP install, every harness, Markdown)
generated:
  by: claude/opus-5
---

# Connect agentsmemory to any agent

`/install-memory-mcp` is the front door for connecting any MCP client to
agentsmemory. Hand its URL to a coding assistant and it performs the registration.

**There is nothing to install.** agentsmemory is a remote MCP server, so every
client connects with the same two facts — the endpoint `{{BASE_URL}}/mcp` over
Streamable HTTP, and an `Authorization: Bearer <workspace token>` header. Each
host differs only in where that pair is written. Windows is not a special case:
the bash installer does not run there, and nothing in this route needs it.

What the document covers:

- **Rules for the assistant** — never invent the token, never send it anywhere but
  the local config, show the human the path before writing it.
- **Hosts with an `mcp add` command** — the exact `claude mcp add` and
  `codex mcp add` invocations, including that codex has no static-header flag and
  authenticates from an environment variable instead.
- **Hosts that take a JSON config** — the standard server object, with verified
  locations for VS Code, Cursor and Claude Desktop, and an explicit note that
  locations for other clients are not verified and must be confirmed first.
- **Scoping a registration to one project** by wing header, or by query parameter
  where a host cannot send custom headers.
- **Verification** — what `am_status` must say before trusting the connection, and
  the deferred-tool trap where a schema-less tool name looks exactly like a missing
  tool.
- **The handoff** — a connected server is an empty palace, so it points at
  `/bootstrap-memory` for the memory model that makes it useful.

Related: [`/claude-guide`]({{BASE_URL}}/claude-guide) installs the CLI kit on macOS
and Linux; [`/windows-guide`]({{BASE_URL}}/windows-guide) carries the full
per-client config; [`/bootstrap-memory`]({{BASE_URL}}/bootstrap-memory) is what to
do once connected.
