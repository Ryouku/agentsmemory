---
type: Product
title: AI Agent Memory — long-term, team-wide memory for AI agents over MCP
description: An open-source, multi-tenant memory server that lets AI agents file verbatim memories and recall them across sessions with hybrid semantic search.
resource: {{BASE_URL}}/
tags: [ai-agent-memory, long-term-memory, multi-agent-memory, mcp, open-source, vector-search, knowledge-graph]
sources:
  - id: landing
    resource: {{BASE_URL}}/
    title: AI Agent Memory — landing page
  - id: repo
    resource: https://github.com/atvirokodosprendimai/agentsmemory
    title: agentsmemory source code (Go)
generated:
  by: claude/opus-5
  at: 2026-08-17T00:00:00Z
status: stable
---

# AI Agent Memory

**Long-term team-wide memory for your AI agents.** Open source, multi-tenant,
served over the Model Context Protocol.

A large language model is stateless: it only knows what currently fits in its
context window, and the moment that window fills or the session ends, everything
is discarded — the model itself never learns from the conversation. That is why
an agent re-asks about your project, re-opens settled decisions and repeats old
mistakes.

AI Agent Memory fixes that by storing what matters *outside* the model, in a
persistent store the agent writes to and recalls from over MCP, so each session
starts with everything the last one learned instead of a blank slate.[^landing]

- **Brand / site:** AI Agent Memory — {{BASE_URL}}
- **Go package and repository:** `agentsmemory`
- **Licence / model:** open source, self-hostable, hosted plan available

# Schema

The memory palace *is* the schema. Seven concepts describe everything stored:

| Concept | Meaning |
| --- | --- |
| **Wing** | A project or context namespace — one isolated workspace per team. |
| **Room** | An aspect within a wing, like `backend` or `decisions`. |
| **Drawer** | One verbatim memory chunk plus rich metadata. Never summarised. |
| **Closet** | A topic and quote pointer index that boosts ranking — never a gate. |
| **Hallway** | A within-wing link between entities that co-occur in drawers. |
| **Tunnel** | A cross-wing link — authored, or auto-derived from a shared topic. |
| **Knowledge graph** | Temporal subject→predicate→object facts with validity windows. |

Memories are stored **verbatim**. Nothing is summarised on the way in, because a
summary decides in advance what a future session will be allowed to remember.

## How it works

1. **Connect over MCP.** Point any MCP client — Claude, your own agent — at
   `POST /mcp` with an `Authorization: Bearer <token>` header.
2. **Resolve the tenant.** The token becomes a workspace in exactly one place.
   Every tool reads that tenant off the context and fails closed without it.
3. **File and recall.** Write verbatim drawers that get embedded and indexed,
   then recall them with hybrid search across the whole team's memory.
4. **Stay isolated.** SQLite is the relational source of truth; Qdrant holds
   per-tenant vectors, rebuildable from it. The transport is stateless, so it
   scales out.

## Capabilities

- **Hybrid semantic search** — vector similarity, BM25 lexical match and a
  closet boost, fused into one ranking, so agents recall by meaning *and* by
  exact term.
- **Memory that can't leak** — every workspace gets its own Qdrant collection,
  named by a hash of the team id. A missing filter can't cross tenants; the data
  isn't even colocated.
- **Centralised, versioned skills** — one shared source of truth for prompts and
  skills. Agents pull the latest with `am_load_skill` instead of copy-pasting
  local files.
- **An append-only agent diary** — a timestamped journal per agent, so sessions
  thread across time and the next run reads what the last one learned.
- **Temporal knowledge graph** — subject→predicate→object facts with validity
  windows, queryable as-of any point in time. Know what was true *then*, not
  just now.
- **Idempotent mining pipeline** — `am_mine` turns raw text into chunked,
  embedded drawers plus a closet index, keyed by source, so re-running finishes
  rather than duplicates.
- **A navigable memory graph** — hallways link co-occurring entities; tunnels
  bridge wings. Traverse the graph to surface context a flat search would miss.
- **Bring your mempalace** — a read-only exporter streams an existing local
  mempalace into your workspace over `/import`, re-embedded server-side, graph
  rebuilt, fully idempotent.
- **Own and export your data** — download everything a workspace holds as one
  self-contained SQLite file, scoped to your tenant, secrets redacted. Your
  BDAR/GDPR right of access and data portability, in one click.

Figures quoted on the public page: 36 of 37 MCP tools shipped; 3-way hybrid
recall (vector · BM25 · closet); a per-team isolated vector store; €0 to start
with 1,000 requests per month.

## Pricing

| Plan | Price | For | Includes |
| --- | --- | --- | --- |
| **Free** | €0 forever | Solo agents and side projects | 1,000 requests / month · unlimited drawers & diary · hybrid search + knowledge graph · centralised skills |
| **Pro** | €50 / month, or €500 / year (two months free) | Teams running agents in production | 1,000,000 requests / month · everything in Free · per-team isolated vector store |

# Examples

Install the kit and register the MCP server:

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

Register the MCP endpoint by hand against a running server:

```bash
claude mcp add --transport http agentsmemory http://localhost:8080/mcp
```

Typical recall-then-persist cycle an agent runs per session:

```text
am_skillset   → the server's wake-up playbook and live tool catalogue
am_status     → the team, the wing→room taxonomy, remaining quota
am_search     → recall past decisions and rationale for the task at hand
…work…
am_diary_write / am_kg_add / am_add_drawer  → persist what this session learned
```

## Questions and answers

**What is AI agent memory?**
AI agent memory is persistent, long-term storage that lets an AI agent remember
context across sessions — past decisions, facts and learnings — instead of
starting cold every run. AI Agent Memory provides it as a remote MCP server:
agents file verbatim drawers of memory and recall them later with semantic
search.

**What is long-term agent memory?**
Long-term agent memory is persistent storage that outlives a model's context
window, letting an AI agent keep what it learned — decisions, facts, open
threads — across sessions instead of forgetting when the window closes. AI Agent
Memory stores each memory verbatim and recalls it later with hybrid semantic
search, so later runs build on earlier ones.

**What is multi-agent memory?**
Multi-agent memory is one shared memory store that a whole team of agents reads
and writes, rather than a private notebook per agent. AI Agent Memory is
multi-tenant: every agent connecting with a team's token shares the same wings,
drawers and knowledge graph, so one agent recalls what another filed — while
memory stays isolated between teams in physically separate vector stores.

**Is AI Agent Memory open source?**
Yes. The full Go server is on GitHub, so you can read exactly how memories are
stored, embedded and ranked, and self-host it with no proprietary core. Run the
hosted service or your own copy; your agents' memory is portable and never
locked in.[^repo]

**What is an MCP memory server?**
An MCP (Model Context Protocol) memory server exposes memory operations — write,
search, recall — as tools any MCP-compatible agent can call over HTTP.
agentsmemory speaks stateless Streamable HTTP MCP, so Claude and other agents
read and write memory with a bearer token.

**Why do AI agents forget everything between sessions?**
Because a large language model is stateless: it only "knows" what currently fits
in its context window, and the moment that window fills or the session ends,
everything is discarded — the model itself never learns from the conversation.
That is why an agent re-asks about your project, re-opens settled decisions and
repeats old mistakes. AI Agent Memory fixes it by storing what matters outside
the model, in a persistent store the agent writes to and recalls from over MCP.

**How do AI agents remember things long-term?**
They externalise memory to a store outside the model's context window.
agentsmemory embeds each memory with the bge-m3 model and indexes it in Qdrant,
then ranks recall with a hybrid of vector similarity, BM25 and a closet boost —
so agents retrieve the most relevant past context on demand.

**Is my agent's memory isolated from other teams?**
Yes. Each workspace gets its own physically separate Qdrant collection, named by
a hash of the team id. There is no shared collection to mis-filter, so memory
cannot leak across tenants.

**Do my teammates need my sandbox name?**
No, and that is deliberate. `aiagentmemory init` splits the record in two: the
agent and its flags go into a `.aiagentmemory` file you commit, while your
sandbox name is written to `~/.sandboxes/agents` on your machine alone. A
teammate clones the repository, runs `init` once with whatever they call their
own sandbox, and `aiagentmemory load` then opens the same agent with the same
flags inside their own isolated config.

**Can I migrate an existing memory palace?**
Yes. A read-only exporter streams an existing local Python mempalace — drawers,
diary, closets, knowledge-graph facts and tunnels — into your workspace over
`/import`. The server re-embeds each memory and rebuilds the graph, and the
import is idempotent.

**Can I export all my data (BDAR / GDPR)?**
Yes. Any workspace member can download everything the workspace holds — drawers,
diary, closets, knowledge-graph facts, tunnels, skills and account details — as
a single self-contained SQLite file, scoped to your own tenant with credentials
redacted. One click from the project page, and the file opens in any SQLite
tool.

**What does agent memory cost to start?**
The Free plan is free forever with 1,000 requests per month. Teams running
agents in production upgrade to Pro at €50 per month, or €500 per year (two
months free).

**Why does agent memory cost money?**
Because hybrid semantic recall runs on real hardware. Every memory you file and
every search you run is embedded by the bge-m3 model on a GPU that draws
hundreds of watts, and each team's vectors are kept hot in a Qdrant store on a
server that runs 24/7 — physically isolated per team, so the compute can't be
shared. The Free plan absorbs that cost for small use; Pro covers the always-on
GPU and electricity for teams that lean on it. And because the server is open
source, you can always self-host and pay your own hardware bill instead.

## Related documents

- [Sandboxed installs](./sandboxes.md)
- [Agent self-install guide](./claude-guide.md)
- [Setup without the CLI](./windows-guide.md)

[^landing]: AI Agent Memory — landing page
[^repo]: agentsmemory source code (Go)
