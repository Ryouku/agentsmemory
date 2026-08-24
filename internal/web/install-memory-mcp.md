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

| Host | Where it goes | Status |
|---|---|---|
| **VS Code** (Copilot Chat) | `%APPDATA%\Code\User\mcp.json`, or Command Palette → *MCP: Open User Configuration* | **Verified.** Use the `${input:}` form in [/windows-guide]({{BASE_URL}}/windows-guide) so the token goes to the OS credential vault instead of plaintext. |
| **Cursor** | see [/windows-guide]({{BASE_URL}}/windows-guide) | **Verified** |
| **Claude Desktop** | see [/windows-guide]({{BASE_URL}}/windows-guide) | **Verified**, including the OAuth connector route |
| **Windsurf, Zed, and other MCP clients** | your host's own MCP settings | **Not verified by us.** The shape above is standard; the file location is not. Check your host's current documentation, and confirm the path with the human before writing. |

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

## 3. Restart, then verify

MCP config is read at startup. **Have the human fully restart the client** — not
reload the window.

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
Put the memory protocol wherever that host reads standing instructions
(`AGENTS.md`, `CLAUDE.md`, a custom-instructions box), which
[/bootstrap-memory]({{BASE_URL}}/bootstrap-memory) covers in its auto-load section.
