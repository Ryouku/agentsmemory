# Connect agentsmemory to any agent (MCP install, every harness)

> This page is written for **the AI assistant reading it**. If a human handed you
> this URL, they are asking you to connect them to agentsmemory. Work through it
> and do the steps. A human reading it directly can follow the same steps by hand
> — each one is a command to run or a file to edit.

**agentsmemory is a remote MCP server. There is nothing to install.** No binary, no
package, no build. Any client that speaks MCP over HTTP connects with two things:

| | |
|---|---|
| **Endpoint** | `{{BASE_URL}}/mcp` — Streamable HTTP |
| **Auth** | `Authorization: Bearer <workspace token>` |

Everything else on this page is one harness's syntax for writing those two facts
into its own config. **Windows is not a special case here** — the bash installer
does not run there, but nothing on this page needs it.

---

## 0. Rules for you, the assistant

- **Never invent, guess, or generate the token.** It is a real credential. Ask the
  human and wait for the actual value. A placeholder will appear to work and then
  fail at first use, somewhere far from here.
- **Never send the token anywhere except the local config** — not into chat, a
  commit, a log, a comment, a screenshot, or any web request.
- **Show the human the file or command before you write it**, and say what it will
  change. They should be able to say no.
- **If a path here does not exist on their machine, stop and ask.** Hosts move
  their config directories between versions. Do not create one somewhere new on a
  guess.
- **Prefer your host's own `mcp add` command over hand-editing JSON.** It writes
  the file its own version expects, which is the part most likely to have changed.

## 1. Get the workspace token

Tell the human:

1. Sign in at {{BASE_URL}} and open — or create — a project.
2. Press **Reveal** on the project's API key.
3. Paste it here.

Wait for the value. Do not continue with a placeholder.

*(The reveal panel also shows an OAuth Client ID. Ignore it — it is only for the
Claude Desktop connector route in [/windows-guide]({{BASE_URL}}/windows-guide).)*

## 2. Register the server

**First ask: does your host have a command that registers MCP servers?** If yes,
use it — that is §2a. If not, you are writing JSON — that is §2b.

### 2a. Hosts with an `mcp add` command

**Claude Code** — takes the header inline:

```
claude mcp add --transport http --scope user agentsmemory {{BASE_URL}}/mcp \
  --header "Authorization: Bearer <TOKEN>"
```

`--scope user` installs it for every project. Use `--scope local` for one project
only. `mcp add` is not idempotent by name, so if a server called `agentsmemory`
already exists, run `claude mcp remove --scope user agentsmemory` first.

**Codex** — has **no** static-header flag, so it reads the token from the
environment at launch:

```
codex mcp add agentsmemory --url {{BASE_URL}}/mcp \
  --bearer-token-env-var AGENTSMEMORY_TOKEN
```

That persists the variable *name* in `config.toml`; the human must also export the
value where codex will see it:

```
export AGENTSMEMORY_TOKEN="<TOKEN>"
```

`codex mcp add` fails on a name that already exists — `codex mcp remove
agentsmemory` first if needed.

**pi** — has no `mcp add` at all. It needs a bridge extension that re-registers
the remote tools as native pi tools, which is more than a config edit. Use the CLI
kit: [`/claude-guide`]({{BASE_URL}}/claude-guide), then `aiagentmemory install
--agent pi`.

### 2b. Hosts that take a JSON config

Most MCP clients accept this shape. Names differ — some call the top-level key
`servers`, others `mcpServers` — but the server object is the same:

```json
{
  "agentsmemory": {
    "type": "http",
    "url": "{{BASE_URL}}/mcp",
    "headers": { "Authorization": "Bearer <TOKEN>" }
  }
}
```

**Merge into the existing file. Do not replace it** — it almost certainly holds
other servers the human depends on.

#### VS Code (GitHub Copilot Chat)

User config on Windows: `%APPDATA%\Code\User\mcp.json`. Easiest route: Command
Palette (`Ctrl+Shift+P`) → **MCP: Open User Configuration**, which opens the right
file and creates it if missing.

Prefer this form — VS Code prompts for the token and stores it in the **OS
credential vault**, so it never lands in plaintext on disk:

```json
{
  "inputs": [
    { "type": "promptString", "id": "agentsmemory-token",
      "description": "agentsmemory workspace token", "password": true }
  ],
  "servers": {
    "agentsmemory": {
      "type": "http",
      "url": "{{BASE_URL}}/mcp",
      "headers": { "Authorization": "Bearer ${input:agentsmemory-token}" }
    }
  }
}
```

With this form you do **not** write the token into the file at all. Hand it back to
the human and tell them to paste it at the prompt. Merge into existing `inputs` /
`servers` rather than replacing them.

#### Cursor

User config on Windows: `%USERPROFILE%\.cursor\mcp.json`. Cursor uses the
`mcpServers` key and has **no** prompt-for-secret mechanism, so the token goes in
the file:

```json
{
  "mcpServers": {
    "agentsmemory": {
      "url": "{{BASE_URL}}/mcp",
      "headers": { "Authorization": "Bearer PASTE_TOKEN_HERE" }
    }
  }
}
```

**Tell the human this file now holds a secret in plaintext and must not be
committed.**

#### Claude Desktop

Two routes. **Try the connector first** — it stores no secret on disk and needs no
Node.js.

**Route 1 — Custom connector (preferred).** Settings → Connectors → **Add custom
connector**, URL `{{BASE_URL}}/mcp`. If it asks for OAuth credentials under
advanced settings, both come from the same reveal panel: Client ID is the **OAuth
Client ID**, Client Secret is the **API key** itself. Custom connectors are not on
every Claude plan; if the option is missing, use route 2.

**Route 2 — `mcp-remote` bridge.** Claude Desktop's config speaks to local
processes, so a remote server needs a bridge. This requires **Node.js** — check
`node --version` before recommending it. Config on Windows:
`%APPDATA%\Claude\claude_desktop_config.json`.

```json
{
  "mcpServers": {
    "agentsmemory": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "{{BASE_URL}}/mcp",
               "--header", "Authorization:Bearer PASTE_TOKEN_HERE"]
    }
  }
}
```

If `npx` is not found when Claude Desktop starts the server, set
`"command": "cmd"` and put `"/c", "npx"` at the front of `args`.

**A SELF-HOSTED server needs neither route.** agentsmemory ships its own bridge in
the server binary, so there is no Node.js and no connector:

```json
{
  "mcpServers": {
    "agentsmemory": {
      "command": "C:\\path\\to\\aiagentmemory-server.exe",
      "args": ["mcp-stdio", "--url", "http://localhost:8080/mcp", "--wing", "wing_acme"]
    }
  }
}
```

`aiagentmemory install --agent claude-desktop --wing wing_acme` writes exactly that.

#### Any other MCP client

**Windsurf, Zed and the rest are not verified by us.** The server object above is
standard; the file location is not. Check your host's current documentation and
**confirm the path with the human before writing** — inventing a config location is
the failure this page is most able to cause.

If the human uses more than one client, repeat this for each. They share one token.

### 2c. Optional — scope this registration to one project

The palace holds many projects. A registration can name the one it belongs to, so
recall stays scoped to it without the agent having to remember:

```
--header "X-Agentsmemory-Wing: wing_<project>"
```

For a host that cannot send custom headers (codex, for one), put it on the URL
instead: `{{BASE_URL}}/mcp?wing=wing_<project>`.

Skip this if you are unsure — an unscoped registration searches every project,
which is noisier but never wrong.

### 2d. Where the memory protocol goes (per-client)

The MCP gives the tools; the **protocol** is what makes an agent use them — recall
before working, persist before stopping. Install it where the client loads standing
instructions automatically:

| Client | Where the protocol goes |
|---|---|
| VS Code (Copilot) | `.github/copilot-instructions.md` in the repository |
| Cursor | `.cursor/rules/agentsmemory.mdc` in the repository |
| Claude Desktop | Project instructions, or Settings → personal preferences |

**Say the asymmetry out loud to the human:** the MCP is **global**, but for VS Code
and Cursor the protocol is **per-repository** — it must be added to each project
they want memory-backed work in. Claude Desktop has no repository, so its protocol
goes in the profile or a project's instructions.

## 3. Restart, then verify

MCP config is read at startup. **Have the human fully restart the client** — for VS
Code and Cursor that means closing every window, not reloading one.

Then call `am_status` and read the answer:

- **`workspace.slug` / `.name`** — is this the human's workspace? An unrecognised
  one means the wrong token. **Stop, and write nothing.**
- **`mode`** — `hosted` for the SaaS, `local` for a self-hosted server.
- **`default_wing`** — empty means recall spans every project unless you pass a
  wing per call.

If the tools do not appear at all: on some harnesses MCP tools load **deferred** —
the name is listed but the schema is not, and a direct call fails with a
validation error that reads exactly like "no such tool". Load the schemas first
(on Claude Code: `ToolSearch "select:am_status,am_search,am_skillset"`), then call.

## 4. Now set the memory up — this is the part that matters

**Connecting the server gives an empty palace. It does not give the team memory.**
What makes it work is the model built inside it: the rooms, the two auto-loaded
skills every session reads, how recall is scoped, and how a session picks up work
the last one left unfinished.

That is a separate document, written to be handed to you the same way this one
was:

> ### → {{BASE_URL}}/bootstrap-memory

Fetch it and work through it. It assumes exactly what you have just finished doing
— a connected MCP — and installs nothing.

---

## What this gets, and what it does not

**You get** the full `am_*` tool surface over HTTP: recall, filing, the knowledge
graph, diaries, the team's centralised skills. Every client above reaches the same
palace with the same token.

**You do not get**, without the CLI kit ([/claude-guide]({{BASE_URL}}/claude-guide),
macOS and Linux only): the end-of-turn checkpoint hook that reminds an agent to
persist before it stops, the slash commands, or the auto-loaded protocol file. On
a host with no hook mechanism nothing is being skipped — there is nothing to skip.
§2d has the per-client protocol locations, and
[/bootstrap-memory]({{BASE_URL}}/bootstrap-memory) covers the auto-load question in
general.

**If the human wants the CLI-only parts on Windows**, the kit installs and runs
normally inside **WSL** (Windows Subsystem for Linux): the bash installer at
[/claude-guide]({{BASE_URL}}/claude-guide) works there untouched.
