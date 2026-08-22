# Install agentsmemory without a CLI (Windows, VS Code, Cursor, Claude Desktop)

> This guide is written for **the AI assistant reading it** — Claude, GitHub
> Copilot, Cursor — to set up agentsmemory for the human it is talking to. If you
> are a human, follow the same steps by hand; every one of them is a file you edit
> or a settings page you open.

The `aiagentmemory` CLI installer is a bash script and a Linux/macOS binary, so it
does not run on Windows. **You do not need it.** agentsmemory is a remote MCP
server: any client that speaks MCP over HTTP connects with nothing but a URL and a
token. That is what this guide sets up.

Everything here installs **globally** — into the user's own config, not into one
project — so the memory works in every workspace they open. There is no
per-project mode without the CLI.

**Endpoint:** `https://aiagentmemory.dev/mcp` (Streamable HTTP, bearer auth)

---

## 0. Rules for you, the assistant

- **Never invent, guess, or generate the token.** It is a real credential. Ask the
  human for it and wait.
- **Never send the token anywhere except the local config file** in step 3. Do not
  paste it into chat, a commit, a log, a code comment, or any web request.
- **Show the human the file path before you write it**, and say what you are about
  to add. They should be able to say no.
- **If a path in this guide does not exist on their machine, stop and check** —
  editors move their config directories between versions. Confirm before creating
  something in a new place.

## 1. Work out which client you are running in

The config file and its format differ per client. Pick the section in step 3 that
matches. If you are not sure, ask the human: *"Are you using VS Code, Cursor, or
the Claude desktop app?"*

If they use more than one, repeat step 3 for each — they share the same token.

## 2. Get the workspace token (ask the human)

Tell them:

1. Sign in at {{BASE_URL}} and open — or create — a project.
2. Press **Reveal** on the project's API key.
3. Copy the key and paste it here.

The reveal panel also shows an **OAuth Client ID** next to the key. It is only
needed for the Claude Desktop connector route in step 3c; ignore it otherwise.

Wait for the actual value. Do not continue with a placeholder.

## 3. Write the global MCP config

### 3a. VS Code (GitHub Copilot Chat)

User-level config file on Windows:

```
%APPDATA%\Code\User\mcp.json
```

Easiest route: run **MCP: Open User Configuration** from the Command Palette
(`Ctrl+Shift+P`) — that opens the right file, creating it if it is missing.

Prefer this form, which makes VS Code **prompt for the token and store it in the
OS credential vault** instead of leaving it in plaintext on disk:

```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "agentsmemory-token",
      "description": "agentsmemory workspace token",
      "password": true
    }
  ],
  "servers": {
    "agentsmemory": {
      "type": "http",
      "url": "https://aiagentmemory.dev/mcp",
      "headers": { "Authorization": "Bearer ${input:agentsmemory-token}" }
    }
  }
}
```

With this form you do **not** write the token into the file at all — VS Code asks
for it on first use. Hand the token back to the human and tell them to paste it
into that prompt.

If the file already has `inputs` or `servers`, merge into them rather than
replacing the file.

### 3b. Cursor

User-level config file on Windows:

```
%USERPROFILE%\.cursor\mcp.json
```

Cursor uses the `mcpServers` key and has no prompt-for-secret mechanism, so the
token goes in the file:

```json
{
  "mcpServers": {
    "agentsmemory": {
      "url": "https://aiagentmemory.dev/mcp",
      "headers": { "Authorization": "Bearer PASTE_TOKEN_HERE" }
    }
  }
}
```

Replace `PASTE_TOKEN_HERE` with the token from step 2. Merge into an existing
`mcpServers` object if one is already there. Tell the human this file now holds a
secret in plaintext, so it must not be committed to a repository.

### 3c. Claude Desktop

Claude Desktop has two routes. **Try the connector first** — it stores no secret
on disk and needs no Node.js.

**Route 1 — Custom connector (preferred).** Have the human open
**Settings → Connectors → Add custom connector** and enter:

- URL: `https://aiagentmemory.dev/mcp`

If it asks for OAuth credentials under advanced settings, they come from the same
reveal panel as the token:

- Client ID: the **OAuth Client ID** shown beside the key
- Client Secret: the **API key/token** itself

Custom connectors are not available on every Claude plan. If the option is
missing, use route 2.

**If the server is SELF-HOSTED, neither route below is the right one.** agentsmemory
ships its own bridge in the server binary, so a local server needs no Node.js and
no connector:

```json
{
  "mcpServers": {
    "agentsmemory": {
      "command": "C:\\path\\to\\aiagentmemory-server.exe",
      "args": ["mcp-stdio", "--url", "http://localhost:8080/mcp"]
    }
  }
}
```

`aiagentmemory install --agent claude-desktop` writes exactly that for you. The
two routes below are for the HOSTED service, where there is no local binary to
bridge through.

**Route 2 — `mcp-remote` bridge.** Claude Desktop's config file speaks to local
processes, so a remote server needs a small bridge. This requires **Node.js**
installed on the machine — check with `node --version` before recommending it.

Config file on Windows:

```
%APPDATA%\Claude\claude_desktop_config.json
```

```json
{
  "mcpServers": {
    "agentsmemory": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "https://aiagentmemory.dev/mcp",
        "--header",
        "Authorization:Bearer PASTE_TOKEN_HERE"
      ]
    }
  }
}
```

Replace `PASTE_TOKEN_HERE`. Merge into an existing `mcpServers` object rather than
overwriting it. If `npx` is not found when Claude Desktop starts the server, set
`"command": "cmd"` and put `"/c", "npx"` at the front of `args`.

## 4. Install the memory protocol

The MCP alone gives the tools. The **protocol** is what makes an agent actually
use them — recall before working, persist before stopping. Without it the memory
sits there unread.

Fetch it from:

```
https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/bootstrap.md
```

Then install it where the client will load it automatically:

| Client | Where the protocol goes |
|---|---|
| VS Code (Copilot) | `.github/copilot-instructions.md` in the repository |
| Cursor | `.cursor/rules/agentsmemory.mdc` in the repository |
| Claude Desktop | Project instructions, or Settings → personal preferences |

Note the asymmetry and say it out loud to the human: the **MCP is global**, but
for VS Code and Cursor the **protocol is per-repository** — it has to be added to
each project they want memory-backed work in. Claude Desktop has no repository, so
its protocol goes in the profile or a project's instructions.

The `bootstrap.md` text mentions CLI-only features (slash commands, a Stop hook).
Those do not exist in these clients — the rest of it, recall-before-work and
persist-before-stop, applies unchanged. Do not try to install hooks.

## 5. Restart and verify

MCP config is read at startup, so the client must be **fully restarted** — for VS
Code and Cursor that means closing every window, not just reloading.

Then confirm it actually works by calling the tool, not by assuming:

```
am_status
```

It returns the workspace name, the wing → room taxonomy, and the remaining quota.
Show the human what came back. If instead you see no `am_*` tools at all, the
config was not picked up — re-check the file path and that the client restarted.

Two more worth running once the connection is live:

- `am_search` — recall existing memories, and prove reads work.
- `am_diary_write` — write a first session entry, and prove writes work.

## What this gets, and what it does not

**You get** the full `am_*` tool surface: semantic recall over the team's memory,
the temporal knowledge graph, diary, drawers, tunnels, and shared skills — the
same server the CLI kit talks to, with the same workspace.

**You do not get** the CLI-only parts: the `/M` and `/am` slash commands, the Stop
hook that nudges an agent to persist at end of turn, and isolated per-project
sandboxes. Those need the `aiagentmemory` binary, which is Linux/macOS only.

**If the human wants those on Windows**, the CLI kit installs and runs normally
inside **WSL** (Windows Subsystem for Linux) — the bash installer at
{{BASE_URL}}/claude-guide works there untouched.
