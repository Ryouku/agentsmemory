# agentsmemory

[![Support on Open Collective](https://opencollective.com/it-uoga/tiers/badge.svg)](https://opencollective.com/it-uoga/projects/ai-agents-memory)

> A multi-tenant **memory palace** for AI agents — served as a remote **MCP** server, backed by **Ollama** and a swappable vector index (**Qdrant** for the SaaS, an **embedded** one for self-hosted).

`agentsmemory` is the Go SaaS rewrite of the original Python [`mempalace`](#provenance):
a semantic, long-term memory store that humans and AI agents read from and write
to. Where the Python tool was a single local user with no auth, this is built for
**teams**: each agent connects to a network MCP endpoint with a bearer token,
operates inside its team's isolated workspace, and can pull **centralised,
versioned skills** the team keeps up to date.

> **Status: early skeleton.** The tenancy, auth, skill registry, storage clients
> and MCP transport are wired and verified end-to-end, and the **core memory
> loop** (file a drawer → recall it semantically) now works end-to-end against
> Ollama + the vector store. Today the server exposes **36 of the planned 37 MCP
> tools** — the WRITE/FILE + SEARCH/RECALL families, the agent `diary`, the `am_mine`
> pipeline (text → chunked drawers + closet index), **hybrid** search (vector +
> BM25 + closet boost), the navigable **graph** (hallways + tunnels + traverse),
> the temporal **knowledge graph**, the skill-registry CRUD, and wing admin. Only
> the two single-user-local tools (`sync`, `hook_settings`) are intentionally not
> ported. See the [Roadmap](#roadmap).

---

## Why it exists

The "memory palace" metaphor *is* the data model:

| Concept | Meaning |
|---|---|
| **Wing** | a project / context namespace |
| **Room** | an aspect within a wing (e.g. `backend`, `decisions`) |
| **Drawer** | one **verbatim** memory chunk + rich metadata (never summarised) |
| **Closet** | a topic/quote pointer index used as a search rank-boost (never a gate) |
| **Hallway** | a within-wing link between entities that co-occur in drawers |
| **Tunnel** | a cross-wing link (author-made, or auto-generated from a shared topic) |
| **Knowledge Graph** | a separate temporal store of `subject → predicate → object` facts with validity windows |

Agents recall context with hybrid search (vector similarity + BM25 + closet
boost, fused), and file new memories that are embedded and indexed. The original
design notes live in the project's memory palace under the `agentsmemory` wing.

---

## Architecture

```
                       Authorization: Bearer <token>
   AI agent  ───────────────────────────────────────────►  POST /mcp
 (Claude, etc.)                                                 │
                                                                ▼
                                                   ┌────────────────────────┐
                                                   │  Streamable HTTP (MCP)  │  stateless
                                                   │  mark3labs/mcp-go       │
                                                   └────────────┬────────────┘
                                       HTTPContextFunc: token ──► Tenant on ctx
                                                                │ (fail closed if unresolved)
                                ┌───────────────────────────────┼───────────────────────────┐
                                ▼                               ▼                            ▼
                        internal/tenant                  internal/skill              internal/palace
                     teams · users · keys             load_skill registry         wings · rooms · drawers
                        · plans (price)              (centralised, versioned)        hallways · tunnels
                                │                            │                            │
                                ▼                            ▼                            ▼
                         SQLite (no-cgo)   ◄── relational source of truth ──►     Qdrant + Ollama
                       gorm + goose schema                                  collection-per-tenant · bge-m3
```

- **Stateless transport.** Every MCP request re-resolves its tenant from the
  bearer token, so there is no server-side session map and the service scales
  horizontally behind a load balancer.
- **One choke point for isolation.** A token becomes a `Tenant` in exactly one
  place (`tenant.Repo.ResolveToken`); every tool reads the tenant off the
  context and refuses to run without one.
- **Two stores, one source of truth.** SQLite holds tenancy, auth, plans and
  skills (the relational SoT) *and* every vector. The search index — Qdrant, or
  the embedded chromem index a self-hosted install defaults to — is derived from
  it and rebuildable without re-embedding.

---

## Multi-tenancy & plans

The unit of tenancy **and** billing is a **workspace** (the `teams` table):

- A workspace has a **kind** (`personal` | `enterprise`) and a **plan** (a price
  tier from the `plans` catalog, e.g. Personal `$0`, Enterprise `$50/mo`).
- A single user can own **several workspaces across plans** — a couple of cheap
  personal ones and one or more enterprise ones — and mint **multiple API keys**
  in each (one per agent or CI job, each independently revocable).
- Each workspace is **physically isolated**: it gets its own Qdrant collection,
  named `mempalace_<sha256(teamID)[:16]>_drawers`. A missing query filter can
  never leak across teams because the data is not even colocated.

```
user ──┬── workspace "personal"    (plan: Personal,  $0)   ── key… ── qdrant collection A
       ├── workspace "side-project"(plan: Personal,  $0)   ── key… ── qdrant collection B
       └── workspace "acme-corp"    (plan: Enterprise, $50) ── key… ── qdrant collection C
```

> Billing today is a `plan_id` column on the workspace. A dedicated
> `subscriptions` table is the planned evolution when payment lands.

---

## Authentication

Phase 1 is **per-agent bearer tokens**; the boundary is designed so OAuth 2.1
can slot in later without touching any tool.

- A user mints API keys from the (future) dashboard. Only `sha256(token)` is
  stored — the plaintext is shown once.
- An agent sends `Authorization: Bearer <token>` on its MCP connection. The
  token's workspace **is** the tenant scope for that session.
- Roles (`member` | `writer` | `admin`) gate writes to shared artifacts — e.g.
  updating a centralised skill requires `writer` or `admin`.

---

## Centralised skills (`am_load_skill`)

Instead of every developer copy-pasting local skill files, a team keeps **one
shared, versioned source of truth** and its agents pull from it:

- `am_load_skill(name)` → returns `{ id, name, version, description, content,
  updated_by, updated_at }` so the agent can drop the body straight into a skill
  slot. Read access for any team member; the lookup is a direct keyed query (no
  vector search).
- Skills are **relational, not memory drawers** — they are mutable, named,
  permissioned authored artifacts with an owner and an update workflow.
- `am_list_skills` (metadata for any member) and `am_update_skill` (version-bumping,
  writer/admin) complete the registry CRUD. The **`/load-skill <name>`** Claude
  command is the client-side nicety over the tool: it fetches a skill by name and
  uses its body directly in the session — no file written, always the live
  version (with no name, it lists what's available). Shipped by the
  `aiagentmemory` installer.

---

## MCP tools

Every tool is namespaced with the `am_` prefix (e.g. `am_status`, `am_search`)
so the server can run alongside other memory MCPs — notably mempalace, which
exposes same-named tools — without the client seeing two tools of the same name.

| Tool | Status | Description |
|---|---|---|
| `am_status` | ✅ | Liveness + the team this session is scoped to |
| `am_load_skill` | ✅ | Load a centralised, team-shared skill by name |
| `am_add_drawer` | ✅ | File a verbatim memory (chunked + embedded; idempotent by source) |
| `am_get_drawer` / `am_update_drawer` / `am_delete_drawer` | ✅ | Read, edit-in-place, or remove a drawer by id |
| `am_list_drawers` | ✅ | Paginate drawers, optionally filtered by wing/room |
| `am_search` | ✅ | Hybrid recall — vector candidates re-ranked by vector + BM25 + closet boost |
| `am_check_duplicate` | ✅ | Is content near-identical to an existing drawer? |
| `am_list_wings` / `am_list_rooms` / `am_get_taxonomy` | ✅ | Indexed wing/room aggregations of a team's memory |
| `am_get_aaak_spec` | ✅ | The AAAK compressed-memory dialect reference |
| `am_reconnect` | ✅ | Re-ready the workspace's vector store (stateless liveness probe) |
| `am_diary_write` / `am_diary_read` | ✅ | Append to / read an agent's append-only journal (timestamped, newest-first) |
| `am_mine` | ✅ | Mine a text payload into chunked drawers (entities + content date) + the closet index; idempotent by source |
| `am_list_hallways` / `am_delete_hallway` | ✅ | Within-wing entity co-occurrence links (derived from mined entities) |
| `am_create_tunnel` / `am_delete_tunnel` / `am_list_tunnels` / `am_find_tunnels` / `am_follow_tunnels` | ✅ | Cross-wing links — explicit (authored, symmetric) + derived (entity) |
| `am_traverse` / `am_graph_stats` / `am_recompute_graph` | ✅ | Walk the room↔wing graph, summarise it, rebuild hallways + entity tunnels |
| `am_kg_add` / `am_kg_invalidate` / `am_kg_query` / `am_kg_stats` / `am_kg_timeline` | ✅ | Temporal knowledge graph — subject→predicate→object facts with validity windows, queryable as-of a point in time |
| `am_list_skills` / `am_update_skill` | ✅ | List the team's centralised skills; create/version-bump a skill body (writer/admin) |
| `am_merge_wing` / `am_memories_filed_away` | ✅ | Fold wings together; summarise what the team has filed |
| `sync`, `hook_settings` | ⛔ | Not ported — single-user-local (on-disk source pruning / local hook config) with no multi-tenant meaning |

---

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.25+ |
| HTTP router | `github.com/go-chi/chi/v5` |
| MCP server | `github.com/mark3labs/mcp-go` (Streamable HTTP, stateless) |
| Relational store | SQLite **no-cgo** via `gorm.io/gorm` + `github.com/glebarez/sqlite` |
| Migrations | `github.com/pressly/goose/v3` (embedded `.sql`) |
| Vector store | **Qdrant** (REST, no SDK) — collection per tenant · or embedded **`chromem-go`** · or SQLite itself |
| Embeddings | **Ollama** `bge-m3` (1024-dim) via `/api/embed` |
| CLI / flags | `github.com/urfave/cli/v3` |
| Auth (planned humans) | `github.com/markbates/goth` |
| Web UI (planned) | `templ` + [datastar](https://data-star.dev) |

---

## Quick start

**Prerequisites:** Go 1.25+ and an **Ollama** with `bge-m3` pulled — every drawer
is embedded on the way in, so the memory loop needs it (see [Preparing Ollama
(embeddings)](#preparing-ollama-embeddings) below). A vector *service* is not a
prerequisite: searches are indexed in-process unless you ask for Qdrant.

```bash
# build
go build -o agentsmemory ./cmd/server

# run — migrates an embedded schema, seeds a demo workspace on first boot,
# and prints a one-time bearer token to the log
./agentsmemory --addr :8080 --db agentsmemory.db
```

On first run you'll see something like:

```
seeded demo team <team-id>
MCP bearer token (shown once): <64-hex-char token>
agentsmemory listening on :8080 (MCP at /mcp)
```

Call it like an MCP client would:

```bash
TOKEN=<paste the token>

# initialize
curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize",
       "params":{"protocolVersion":"2025-03-26","capabilities":{},
                 "clientInfo":{"name":"demo","version":"0"}}}'

# load the seeded "hello" skill
curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"am_load_skill","arguments":{"name":"hello"}}}'
```

A request without a valid token comes back as a fail-closed
`unauthenticated` tool error.

### Preparing Ollama (embeddings)

agentsmemory never embeds text itself — it calls **your** Ollama, so nothing you
remember leaves your machine. One install, one model pull:

```bash
# 1. Install and run Ollama — https://ollama.com/download
#    macOS/Windows: the app starts the server.
#    Linux:  curl -fsSL https://ollama.com/install.sh | sh
ollama --version

# 2. Pull the embedding model (bge-m3, 1024-dim, ~1.2 GB)
ollama pull bge-m3

# 3. Prove it answers on the endpoint the server will use
curl -s http://localhost:11434/api/embed \
  -d '{"model":"bge-m3","input":"hello"}' | head -c 120
```

Step 3 is the one worth running: a JSON array of floats means the server will
work, and it fails loudly here rather than on your first `am_add_drawer`. The
model must be *pulled*, not merely installed — Ollama does not fetch it on
demand for `/api/embed`; a missing one comes back as `model "bge-m3" not found`.
When that happens nothing is lost: writes return the embed error, and rows that
came through `/import` sit in the embed queue and drain by themselves once the
model is there.

**Why `bge-m3`, and why not to change it casually.** It matches the frozen Python
palace (1024-dim), so migrated memories and new ones share one vector space.
Swapping the model changes that space: old and new vectors stop being comparable
and every drawer needs re-embedding. Pick it before you fill the palace, not
after — `--ollama-model` exists for a fresh one.

**Running the server in Docker? `localhost` is not your machine.** Ollama binds
`127.0.0.1` by default, and a container cannot reach the host's loopback. Either
bind it wider and use the name compose maps for you (`OLLAMA_URL=http://host.docker.internal:11434`):

```bash
# macOS
launchctl setenv OLLAMA_HOST 0.0.0.0     # then restart the Ollama app

# Linux (systemd)
sudo systemctl edit ollama               # add: Environment="OLLAMA_HOST=0.0.0.0"
sudo systemctl restart ollama
```

…or, on Linux, skip the problem entirely with the host-network override below,
where `localhost:11434` inside the container *is* your machine's loopback.

A GPU box elsewhere works just as well — point `OLLAMA_URL` at it
(`http://192.168.1.50:11434`) and pull `bge-m3` there instead.

### Self-hosted single-workspace mode (`--local`)

Everything above is the multi-tenant SaaS shape: many workspaces, each behind a
token. If you are running this on your own machine for yourself, `--local`
collapses it to the simplest thing that still runs every tool.

Grab the server binary for your platform — every release publishes
`aiagentmemory-server-<os>-<arch>` for `linux` and `darwin` on `amd64` and
`arm64`, alongside the `aiagentmemory` CLI:

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); [ "$arch" = x86_64 ] && arch=amd64; [ "$arch" = aarch64 ] && arch=arm64
curl -fsSL -o agentsmemory \
  "https://github.com/atvirokodosprendimai/agentsmemory/releases/latest/download/aiagentmemory-server-${os}-${arch}"
chmod +x agentsmemory
```

Windows, or want checksums? The same releases carry
`agentsmemory_<os>_<arch>.tar.gz` / `.zip` archives (Windows included) with a
`SHA256SUMS.txt`. Prefer to build it yourself? `go build -o agentsmemory
./cmd/server`. Either way, run it:

```bash
./agentsmemory --local --db agentsmemory.db
# agentsmemory listening on 127.0.0.1:8080 (local mode: workspace "local", MCP /mcp, no token required, no dashboard)
```

Then point your agent at *that* server. It is the same kit as the hosted
service; `--local` aims it at `http://localhost:8080/mcp` and never asks for a
token, because there is none to ask for:

```bash
aiagentmemory install --local                                # global, Claude (~/.claude) — the default agent
aiagentmemory install --local --agent all                    # claude | codex | pi | both | all
aiagentmemory install --local --sandbox acme                 # isolated config at ~/.sandboxes/acme
aiagentmemory install --local --sandbox acme --agent codex   # …and choose the agent inside it
```

With `--local` and no target named, the install goes global and skips the
interactive global-vs-sandbox prompt — a self-hoster setting up their own machine
has an obvious answer to that question. Naming `--sandbox <name>` still wins (the
name is required), and `--mcp-url` overrides the endpoint for a server on another
port or host. Registering the MCP is only half the job, though: what makes an
agent actually *use* the tools is the protocol the kit installs alongside it —
see [the server is inert without the
protocol](#the-server-is-inert-without-the-protocol).

What changes:

| | default | `--local` |
|---|---|---|
| Workspaces | many, created from the dashboard | exactly one, slug `local`, provisioned on first boot |
| `/mcp` auth | Bearer token or OAuth | **none** — every request is the local workspace |
| API keys | minted per member | none exist; none are stored |
| Quota | per plan | uncapped (`plan_unlimited`) |
| Dashboard, OAuth, billing webhooks | mounted | **not registered** (404) |
| Listen address | `:8080` (all interfaces) | `127.0.0.1:8080` |
| Search index | `sqlite` (the source of truth scans itself) | **`chromem`** — embedded, no service to run |

All 37 MCP tools behave identically — they only ever see a resolved workspace,
and local mode injects one instead of resolving it from a credential.

Point any MCP client straight at it, with no header:

```bash
claude mcp add --transport http agentsmemory http://localhost:8080/mcp
```

Two guardrails worth knowing:

- **The endpoint is unauthenticated**, so reachability *is* authorization. That
  is why the default binds loopback. Overriding `--addr` to a routable interface
  still works (behind a reverse proxy or a private overlay network) but logs a
  loud warning — anyone who can reach the port owns every memory in the file.
  [`--socket`](#unix-socket-and-stdio-mcp---socket--mcp-stdio) tightens this
  further than loopback can.
- **It refuses to start** if the database already holds a workspace that is not
  `local` — including the `demo` workspace the multi-tenant path seeds on first
  boot. Use a fresh `--db` file, or drop `--local`.

Local mode also picks its own search index: **chromem**, a vector database that
runs inside the server process. It keeps the vectors in memory and persists them
next to the database — `agentsmemory.db` gets `agentsmemory.chromem/` beside it,
one directory per workspace inside — so a self-hosted install is one binary, one
file and one folder, with no service to install, start or monitor. `sqlite` and
`qdrant` remain one `--vector-backend` away ([choosing the index](#choosing-the-index)).

### Unix socket and stdio MCP (`--socket` / `mcp-stdio`)

HTTP on a port stays the default and nothing about it changed. But a port is a
weak boundary for an endpoint with no authentication: *every* user and process on
the machine can open `127.0.0.1:8080`. `--socket` replaces it with a Unix socket
created at mode `0600`, so the operating system restricts the server to the
account that started it:

```bash
./agentsmemory --local --socket /tmp/agentsmemory.sock --db agentsmemory.db
# agentsmemory listening on unix:/tmp/agentsmemory.sock (local mode: workspace "local", …)
```

A socket has no URL, so agents reach it through `mcp-stdio` — a bridge shipped in
the same binary that speaks MCP on stdin/stdout and forwards to the server:

```bash
claude mcp add agentsmemory -- /path/to/agentsmemory mcp-stdio --socket /tmp/agentsmemory.sock
codex  mcp add agentsmemory -- /path/to/agentsmemory mcp-stdio --socket /tmp/agentsmemory.sock
```

The server prints both lines on startup with its own absolute path filled in, so
they can be copied straight out of the log. The installer can wire it for you
instead — it registers the same bridge and installs the memory protocol alongside
it:

```bash
aiagentmemory install --local --socket /tmp/agentsmemory.sock
```

`--socket` requires `--local` (the bridge carries no token, so it only reaches a
self-hosted server) and finds the server binary on `PATH`, or takes
`--server-bin /path/to/agentsmemory`. It works for Claude and codex; pi has no MCP
client of its own, so install that one with `--mcp-url` against `--addr`.

Worth knowing:

- **It is a raw JSON-RPC passthrough.** The bridge carries no tool catalogue, so
  a tool added to the server works over stdio the moment the server restarts —
  there is nothing to regenerate and no proxy to rebuild.
- **One server, many agents.** Each agent spawns its own bridge process, but they
  all share the one server — and therefore one SQLite writer and one embedding
  queue, rather than each opening the database itself.
- **It works over HTTP too.** `mcp-stdio --url http://host:8080/mcp` (with
  `--token` for a multi-tenant server) bridges any endpoint, which is the escape
  hatch for a client that only supports stdio transport.
- **`AGENTSMEMORY_SOCKET` configures both halves** — the server's listen path and
  the bridge's dial path — so the pair cannot drift apart.
- **Socket paths are short.** The kernel caps them near 104 bytes (macOS) or 108
  (Linux); a deeply nested path fails to bind with a bare `invalid argument`.

### Docker Compose (one container)

```bash
cp .env.docker.example .env.docker   # point OLLAMA_URL at your Ollama
docker compose up -d
claude mcp add --transport http agentsmemory http://localhost:8080/mcp
```

Brings up `--local` with the embedded chromem index, so the whole stack is **one
container and one volume** — `/data/agentsmemory.db` (truth) and
`/data/agentsmemory.chromem` (index) live side by side in it. Ollama is
deliberately **not** a service: most people already run one, and a second copy
would re-download gigabytes of models — so `.env.docker.example` covers both
`host.docker.internal` (Ollama on your machine) and a URL for a remote box.
Both of the things that bite are on the Ollama side, and both are handled in
[Preparing Ollama](#preparing-ollama-embeddings): it binds `127.0.0.1` by
default, which a container cannot reach, and the model must be pulled first.

The port is published as `127.0.0.1:8080:8080`, and the loopback prefix is
load-bearing for the same reason as above — plain `8080:8080` would offer an
unauthenticated memory server to your whole network. Inside the container the
process binds `:8080` (a published port cannot reach a loopback-bound process),
so it logs the non-loopback warning on boot; there, the published interface is
the boundary, and the warning is expected.

**On Linux**, an override removes the Ollama friction entirely:

```bash
# start (both -f flags, every time — the override alone is not a complete stack)
docker compose -f docker-compose.yml -f docker-compose.host.yml up -d

# follow the logs / stop
docker compose -f docker-compose.yml -f docker-compose.host.yml logs -f agentsmemory
docker compose -f docker-compose.yml -f docker-compose.host.yml down
```

Repeating both files gets tedious, so either export it once per shell —
`export COMPOSE_FILE=docker-compose.yml:docker-compose.host.yml`, after which
plain `docker compose up -d` uses both — or put that same line in a `.env` beside
the compose files to make it the permanent default for this directory.

Without compose at all, the equivalent single container (the embedded index, so
nothing else needs to run):

```bash
docker build -t agentsmemory:local .
docker run -d --name agentsmemory --network host --restart unless-stopped \
  -v agentsmemory-data:/data \
  -e VECTOR_BACKEND=chromem -e OLLAMA_URL=http://localhost:11434 \
  agentsmemory:local serve --local --addr 127.0.0.1:8080 --db /data/agentsmemory.db
```

`network_mode: host` puts the container in the host's network namespace, so
`localhost:11434` inside it *is* your machine's loopback — Ollama works on its
default `127.0.0.1` bind, with no `OLLAMA_HOST=0.0.0.0` and no
`host.docker.internal`. It is Linux-only: Docker Desktop on macOS and Windows
runs containers in a VM, where host networking is an opt-in feature (4.34+)
rather than the default. Note that host networking **ignores `ports:`**, so the
loopback publish stops protecting anything and the server's own `--addr
127.0.0.1:8080` becomes the boundary — which is exactly what the override pins.
The embedded index needs no network at all, so nothing else changes; a Qdrant
service, if you uncomment one, stays on the bridge network and is reached through
its published loopback port.

### Choosing the index

`VECTOR_BACKEND` (or `--vector-backend`) picks what answers searches. SQLite is
written either way, so this is never a decision about your data — switching costs
an index rebuild, never a re-embedding:

| Value | What runs | Choose it when |
|---|---|---|
| `chromem` *(default with `--local`)* | nothing extra — an in-process index, held in memory, persisted to `<db>.chromem/` | self-hosted on one machine |
| `sqlite` *(default otherwise)* | nothing at all — the source of truth scans its own vectors per query | you want the smallest possible footprint |
| `qdrant` | a separate Qdrant service | the palace outgrows memory, or several machines share one index |

The chromem index is derived and disposable: delete the directory and the server
refills it from SQLite on the next boot, logging `rebuilt namespace … from the
SQLite source of truth`. Switching *to* Qdrant is the one case that needs a
command — `agentsmemory sync` — because refilling a remote index at boot would
mean blocking startup on a service that may be down.

### Backing up the SQLite volume

**Back up SQLite; ignore the index.** SQLite is the source of truth and every
index is rebuildable from it — a chromem directory refills itself on the next
boot, and `agentsmemory sync --recreate` replays every vector into Qdrant without
re-embedding. Losing an index costs a restart or one command, not your memory.

The volume name is `<project>_agentsmemory-data`, where the project is `name:` in
`docker-compose.yml` — so `agentsmemory_agentsmemory-data` by default. Confirm
with `docker volume ls`.

**Stop and copy** — simplest and unconditionally consistent. A single-user server
will not miss two seconds:

```bash
docker compose stop agentsmemory
docker run --rm -v agentsmemory_agentsmemory-data:/data -v "$PWD:/backup" \
  alpine:3.20 tar czf "/backup/agentsmemory-$(date +%F).tar.gz" -C /data .
docker compose start agentsmemory
```

**Hot backup** — no downtime, using SQLite's online backup API. The runtime image
is bare Alpine with no `sqlite3`, so borrow one:

```bash
docker run --rm -v agentsmemory_agentsmemory-data:/data -v "$PWD:/backup" alpine:3.20 \
  sh -c 'apk add --no-cache sqlite >/dev/null &&
         sqlite3 /data/agentsmemory.db ".backup /backup/agentsmemory-$(date +%F).db"'
```

Do **not** just `cp` the database file while the server is running. `.backup`
(and `VACUUM INTO '/backup/out.db'`, which additionally compacts) coordinate with
writers; a plain copy can catch a write mid-transaction and hand you a file that
only fails later, when you need it.

**Verify** the backup before you trust it — an unreadable backup discovered at
restore time is not a backup:

```bash
sqlite3 agentsmemory-2026-08-16.db "PRAGMA integrity_check; SELECT count(*) FROM drawers;"
```

**Restore** into a fresh volume:

```bash
docker compose down
docker volume rm agentsmemory_agentsmemory-data
docker volume create agentsmemory_agentsmemory-data
docker run --rm -v agentsmemory_agentsmemory-data:/data -v "$PWD:/backup" alpine:3.20 \
  sh -c 'tar xzf /backup/agentsmemory-2026-08-16.tar.gz -C /data && chown -R 10001:10001 /data'
docker compose up -d
docker compose exec agentsmemory agentsmemory sync --recreate   # only if using Qdrant
```

⚠ The `chown` is not optional. The image runs as uid **10001**, while the restore
container writes as root — skip it and the server starts, then fails on the first
write to a database it cannot open. Restoring a single `.db` file instead of the
tarball needs the same treatment.

### The server is inert without the protocol

Connecting the MCP gives your agent 37 tools and **no reason to call any of
them**. Nothing about a tool catalogue tells an agent to recall before it acts or
to write down what it learned; without that instruction the memory simply never
gets opened. Delegation comes in three layers, and self-hosting only gets you the
first one for free:

| Layer | What it does | How you get it |
|---|---|---|
| `am_skillset` | Server-side wakeup playbook — which tool, in what order — returned over MCP itself | **Automatic.** Seeded on first boot, including `--local` |
| `CLAUDE.md` / `AGENTS.md` | The always-on protocol: recall at session start, persist before stopping | `aiagentmemory install` writes `agentsmemory-bootstrap.md` and merges an import into your memory file |
| `/M`, `/am`, `/load-skill` + the Stop hook | Task-scoped grounding and the end-of-turn checkpoint that stops memory being lost | Same installer |

So after `docker compose up`, run the kit as well — `--local` wires it to your
own server:

```bash
aiagentmemory install --local
```

That points the MCP at `http://localhost:8080/mcp`, never asks for a token (and
drops any `AGENTSMEMORY_TOKEN` it finds in your environment, rather than writing
a credential into a config where it would imply the server checks one), and
installs globally without the interactive global-vs-sandbox prompt — a
self-hoster is setting up their own machine, so that question has an obvious
answer. An explicit `--sandbox <name>` still wins if you want a local server in
an isolated config, and `--mcp-url` overrides the endpoint for a server on
another port or host.

The registration it writes carries no `Authorization` header at all, on all three
agents: Claude stores a bare `{"type":"http","url":...}`, codex registers the URL
with no bearer-token variable and no token file, and pi's bridge extension gets
`AGENTSMEMORY_LOCAL=1` so it treats the missing token as intentional and connects
anyway instead of reporting "memory tools are off".

---

## Connect Claude Code, Codex or pi (the `aiagentmemory` kit)

The `aiagentmemory` binary wires [Claude Code](https://claude.com/claude-code),
[Codex](https://developers.openai.com/codex) or [pi](https://pi.dev) into your
workspace: it installs the memory-grounded slash commands (`/M`, `/am`,
`/load-skill`) and the Stop hook, registers the agentsmemory MCP, and can wrap the
agent CLI so each project runs against its own isolated configuration. It replaces
the old shell installer; everything ships in one downloadable binary.

Claude is the default. `--agent codex` installs the same kit into codex's layout
(`~/.codex`, `prompts/`, `AGENTS.md`, `hooks.json`) and `--agent pi` into pi's
(`~/.pi/agent`, `prompts/`, `AGENTS.md`, a bridge extension). `--agent both` is
Claude + codex; `--agent all` is all three — see [Codex](#codex-agent-codex) and
[pi](#pi-agent-pi).

Full reference: [`clients/claude-code/README.md`](clients/claude-code/README.md).

### Install in one line

```bash
curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
```

The bootstrap script detects your OS/arch, downloads the latest
`aiagentmemory-<os>-<arch>` from
[GitHub Releases](https://github.com/atvirokodosprendimai/agentsmemory/releases)
into `~/.local/bin`, then runs `aiagentmemory install`. Anything after `--` is
forwarded to `install`. Prefer to build it yourself?

```bash
go build -o aiagentmemory ./clients/claude-code
./aiagentmemory install
```

`install` prompts for your **workspace API token** (create a project in the
dashboard and copy or **Reveal** its key), then registers the agentsmemory MCP in
one shot. Supply it non-interactively with `--token <key>` or the
`AGENTSMEMORY_TOKEN` environment variable. Add `--recommended` to also install the
companion tools: the [codebase-memory](https://github.com/DeusData/codebase-memory-mcp)
MCP and the eidos and codex plugins. Preview any run with `--dry-run` — it prints
every file write and command without touching anything.

### Two ways to install

| Mode | Command | What it does |
|------|---------|--------------|
| **Global** | `aiagentmemory install` | Wires the MCP, commands, and Stop hook into the global `~/.claude`. Wraps the Claude you already run. |
| **Sandboxed** | `aiagentmemory install --sandbox <name>` | Installs a self-contained config under `~/.sandboxes/<name>`, isolated from every other project and from the global `~/.claude`. |

### Sandboxed installation (per-project isolation)

A **sandbox** is just a Claude config directory under `~/.sandboxes/<name>`.
Running Claude with `CLAUDE_CONFIG_DIR` pointed at it isolates that project's
slash commands, settings, MCP servers, and agentsmemory token from everything
else — so a client project and an internal project never share memory, tools, or
credentials. Set one up once, with or without the recommended tools:

```bash
aiagentmemory install --sandbox acme               # core: commands, hook, our MCP
aiagentmemory install --sandbox acme --recommended # + codebase-memory, eidos, codex
```

The installer writes into `~/.sandboxes/acme/` and runs every `claude`
registration with `CLAUDE_CONFIG_DIR` pinned there, so nothing leaks into your
global config. Sandbox names are plain identifiers (letters, digits, dash,
underscore).

### Run a sandbox without re-installing

Installing is a one-time setup. To **launch Claude against an existing sandbox**,
just name it — no re-install:

```bash
aiagentmemory run acme                     # open Claude in the acme sandbox
aiagentmemory run acme -p "summarise repo" # args after the name pass straight to claude
```

`run <name>` sets `CLAUDE_CONFIG_DIR=~/.sandboxes/<name>`, then exec-replaces the
process with the Claude CLI — inheriting your terminal and its exit code, so it
behaves exactly like running `claude`, only against that sandbox. It errors with a
hint if the sandbox hasn't been installed yet. The global counterpart is:

```bash
aiagentmemory wrap                         # run Claude against the global ~/.claude
```

The Claude CLI it drives is resolved from `AIAGENTMEMORY_CLAUDE_BIN`, then
`claude` on your `PATH`.

### Read your memory from the shell

`aiagentmemory mcp` calls the memory tools yourself — same endpoint, same token,
same transport your agents use — so you can see exactly what a tool returns
without asking an agent to relay it:

```bash
aiagentmemory mcp                          # the tools you can call
aiagentmemory mcp status                   # workspace, wings, quota
aiagentmemory mcp search "auth bug" -a limit=3
aiagentmemory mcp search "auth bug" | jq '.hits[].room'
```

The bare positional fills the tool's first required argument; everything else is
`-a key=value`. Output is indented JSON on stdout (notes go to stderr, so it
pipes), and the workspace token is read from an install already on this machine —
`--sandbox <name>` picks one. It is **read-only**: the write tools exist on the
endpoint but the CLI refuses them, so a mistyped command can never mutate team
memory. Full flag reference in [`clients/claude-code/README.md`](clients/claude-code/README.md).

### Codex (`--agent codex`)

Codex is configured the same way Claude is, under different names, so the kit is
the same content in different places:

```bash
aiagentmemory install --agent codex                  # into ~/.codex
aiagentmemory install --agent both --sandbox acme    # one sandbox, both agents
aiagentmemory run --agent codex acme                 # launch codex with CODEX_HOME pinned
```

| | Claude Code | Codex |
|---|---|---|
| Config dir | `~/.claude` (`CLAUDE_CONFIG_DIR`) | `~/.codex` (`CODEX_HOME`) |
| Slash commands | `commands/*.md` → `/M`, `/am` | `prompts/*.md` → `/prompts:M`, `/prompts:am` |
| Always-on memory | `CLAUDE.md` + managed `@import` | `AGENTS.md` with the protocol inlined — codex has no `@import` |
| Stop hook | `settings.json` | `hooks.json` (same shape and `Stop` semantics) |
| MCP auth | `Authorization: Bearer <token>` header | `bearer_token_env_var = "AGENTSMEMORY_TOKEN"` |

Two things codex needs that Claude does not, both printed by the installer:
**trust the hook** (codex skips non-managed hooks until reviewed in `/hooks`), and
**have the token in the environment** — it is written to
`<CODEX_HOME>/agentsmemory.env` (`0600`) and exported for you by
`aiagentmemory run --agent codex …`; for plain `codex`, source it from your shell
rc. A codex sandbox is a whole `CODEX_HOME`, so it also needs its own login:
`CODEX_HOME=~/.sandboxes/acme codex login`.

### Inheriting your global setup (`--copy`)

A new sandbox starts signed out, with no MCP servers, plugins or skills. `--copy`
seeds it from that agent's global config dir first:

```bash
aiagentmemory install --agent pi --sandbox acme --copy
```

Credentials, settings, `.claude.json` (Claude's MCP servers), plugins, skills,
extensions and prompts travel; conversation history, logs, `*.sqlite*` stores and
caches stay behind. Existing files in the target are never overwritten, modes are
preserved (`auth.json` stays `0600`) — and note the consequence: **the sandbox can
act as you** until you sign it out.

### Sharing one login (`--shared-auth`)

`--copy` snapshots credentials; `--shared-auth` links them, so a login in any
sandbox is a login everywhere:

```bash
aiagentmemory install --agent pi --sandbox acme --shared-auth
```

Claude on macOS already shares its keychain, so the flag is a no-op there; codex
links `auth.json`, pi links `auth.json` and `models-store.json`. If an agent ever
replaces the link with a real file, `aiagentmemory run` says so at launch and
prints the command that re-shares it.

### pi (`--agent pi`)

pi looks like codex — `prompts/` for commands, `AGENTS.md` for memory — except
that it ships **no MCP client and no hooks**, both by design. So the installer
writes a bridge extension into `<config dir>/extensions/agentsmemory.ts`: at
startup it handshakes with your workspace MCP, lists the tools, and re-registers
each one as a native pi tool, so `am_*` calls work unchanged. The same extension
fires the end-of-turn memory checkpoint that the Stop hook fires elsewhere.

```bash
aiagentmemory install --agent pi                   # into ~/.pi/agent
aiagentmemory install --agent all --sandbox acme   # one sandbox, all three agents
aiagentmemory run --agent pi acme                  # launch pi with PI_CODING_AGENT_DIR pinned
```

| | Codex | pi |
|---|---|---|
| Config dir | `~/.codex` (`CODEX_HOME`) | `~/.pi/agent` (`PI_CODING_AGENT_DIR`) |
| Slash commands | `prompts/*.md` → `/prompts:M` | `prompts/*.md` → `/M` |
| Stop hook | `hooks.json` | none — the checkpoint ships in the extension |
| MCP | native, `--bearer-token-env-var` | bridged by the extension |

The token and endpoint are written to `<config dir>/agentsmemory.env` (`0600`)
and exported by `aiagentmemory run --agent pi …`. A pi sandbox is the whole agent
dir including `auth.json`, so it starts with no provider credentials.
`--recommended` adds nothing for pi: codebase-memory is a stdio MCP and the
eidos/codex plugins are Claude marketplaces.

---

## Configuration

All flags have sensible local defaults:

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` (`127.0.0.1:8080` with `--local`) | HTTP / MCP listen address |
| `--local` | `false` | Self-hosted single-workspace mode: one `local` workspace, unauthenticated `/mcp`, no dashboard |
| `--db` | `agentsmemory.db` | SQLite database path |
| `--vector-backend` | `sqlite` (`chromem` with `--local`) | Search index: `sqlite` \| `chromem` \| `qdrant` — SQLite is always the source of truth |
| `--qdrant-url` | `http://localhost:6333` | Qdrant base URL |
| `--qdrant-api-key` | *(empty)* | Qdrant API key (optional) |
| `--ollama-url` | `http://localhost:11434` | Ollama base URL |
| `--ollama-model` | `bge-m3` | Embedding model (1024-dim) |

---

## Migrating from mempalace

Bring an existing local Python `mempalace` into a workspace — every drawer, diary
entry, closet, knowledge-graph fact and explicit tunnel. The vehicle is a small
**read-only** CLI that reads your palace and streams it to the server's
`/import` endpoint with your project's API key; the server **re-embeds** each
memory with its own model (the bundle carries text, not vectors) and rebuilds the
derived graph (hallways/entity-tunnels) afterwards.

```bash
# Run where the mempalace package is installed. Inspect first:
python clients/migrate/mempalace_export.py --out palace.ndjson

# Then stream it into your workspace (token = the project's API key):
python clients/migrate/mempalace_export.py --push \
  --server https://your-host --token sk_live_xxx

# Or push a bundle exported earlier on another machine:
python clients/migrate/mempalace_export.py --file palace.ndjson --push \
  --server https://your-host --token sk_live_xxx
```

`POST /import` sits behind the same Bearer gate as `/mcp`, takes streaming NDJSON
(one record per line, `kind`-discriminated), and streams progress back. The
import is **idempotent** — drawer ids are recomputed under the target tenant, so
re-running a partial migration finishes it rather than duplicating. The project
page surfaces the exact command (with your host filled in) under *Bring your
mempalace*.

Full step-by-step guide, flag reference and troubleshooting:
[`clients/migrate/README.md`](clients/migrate/README.md).

---

## Data export & BDAR/GDPR compliance

A workspace member can download **everything the workspace holds** as a single,
self-contained **SQLite file** — the *right of access* and *data portability*
under **BDAR** (the Lithuanian implementation of the EU GDPR). The project page
surfaces it under *Download your data*; it maps to a membership-gated
`GET /projects/{teamID}/export`. It is the **outbound counterpart to `/import`**:
import brings a palace in, export takes your workspace out.

The archive is a standalone, valid SQLite database — open it with any SQLite
browser — built from the live source of truth:

- **Schema** is replayed **verbatim** from the source `sqlite_master`, so the
  export is byte-faithful to the running schema (no goose re-run, no drift).
- **Rows** are copied through an explicit, reviewed manifest, each **scoped to the
  requesting tenant** — workspace-owned memory (drawers, diary, closets, hallways,
  tunnels, knowledge-graph facts, vectors, skills, usage, subscriptions, merge
  jobs) by `team_id` / namespace, plus the requester's own identity rows (account,
  membership, API-key metadata). No other tenant's data can enter the archive.
- **Credentials are redacted**: the password hash is blanked, an API key's
  `token_hash` is replaced and `token_enc` blanked — the export carries *your
  data*, never usable secrets.

```bash
# From the browser: project page → "Download your data".
# Or with an authenticated session cookie:
curl -b session.jar https://your-host/projects/<teamID>/export \
  -o agentsmemory-<workspace>-<date>.sqlite
```

Implementation: [`internal/dataexport`](internal/dataexport/dataexport.go)
(scoping manifest + redaction) and `internal/web/export.go` (the download route).

---

## Project layout

```
cmd/server/            entrypoint: cli flags → migrate → seed → serve
db/                    embedded goose migrations (.sql)
internal/
  config/              runtime configuration
  tenant/              teams (workspaces) · users · memberships · api_keys · plans
  skill/               centralised skill registry (load_skill)
  store/qdrant/        Qdrant REST client, collection-per-tenant naming
  store/chromemvec/    embedded chromem-go index (the --local default)
  store/sqlitevec/     SQLite vector source of truth
  embed/ollama/        Ollama bge-m3 embedder
  auth/                bearer token → tenant context injection
  palace/              core memory domain types (wing/room/drawer/hallway/tunnel)
  mcpserver/           MCP tool wiring (status, load_skill, …)
  dataexport/          per-workspace SQLite data export (BDAR right of access)
  web/                 dashboard (templ + datastar): projects, keys, export
```

Bounded contexts are kept apart (DDD): `tenant` and `skill` share only tenancy
and auth, never storage internals; interfaces are declared at the consumer.

---

## Development

```bash
go build ./...     # compile everything
go vet ./...       # static checks
go test ./...      # unit tests (skill scoping + role gate, qdrant naming)
```

`goose` owns the schema; `gorm` is the query layer only (`AutoMigrate` is never
called). Schema changes are additive migrations under `db/migrations/`.

---

## Support

AI Agent Memory is open source and free to self-host. If the hosted service
helps you, you can support development on
[Open Collective](https://opencollective.com/it-uoga/projects/ai-agents-memory)
— contributions fund the always-on GPU the hosted recall runs on. The Pro plan's
checkout is the project's €50/month contribution tier, so paying subscribers and
donors land on the same page.

---

## Roadmap

- [x] Tenancy (workspaces, users, memberships, API keys) + plan/price tiers
- [x] Bearer-token auth → tenant resolution; fail-closed tools
- [x] `am_load_skill` centralised skill registry
- [x] Qdrant (collection-per-tenant) + Ollama (`bge-m3`) clients
- [x] Stateless Streamable-HTTP MCP server (`am_status`, `am_load_skill`)
- [x] Core memory loop — drawer CRUD + semantic recall + taxonomy (12 tools, vector-only search)
- [x] Agent diary — `am_diary_write` / `am_diary_read` (timestamped, append-only journal) (16 of 37)
- [x] Hybrid search — vector candidates re-ranked by vector + BM25 + closet boost (RRF-style convex blend)
- [x] Mining pipeline — `am_mine` text → chunked drawers (entities + content date) + closet index, idempotent by source (17 of 37)
- [x] Graph — hallways (entity co-occurrence) + tunnels (explicit + entity) + traverse/find/stats/recompute (10 tools, 27 of 37)
- [x] Knowledge graph — temporal subject→predicate→object facts with validity windows (5 tools, 32 of 37)
- [x] Skill registry CRUD — `am_list_skills` + `am_update_skill` (role-gated)
- [x] Admin — `am_merge_wing` + `am_memories_filed_away` (36 of 37; `sync`/`hook_settings` are single-user-local, not ported)
- [x] Web dashboard — local (`goth`) login, project create + one-time API key, monthly usage metering — `templ` + datastar
- [x] Web skill management — per-project list / create / edit (role-gated to writer/admin), membership-checked routes
- [x] Migration — read-only `mempalace` exporter + streaming `POST /import` (drawers, diary, closets, KG facts, tunnels; re-embedded, graph rebuilt)
- [x] Data export (BDAR/GDPR) — download a workspace's data as a self-contained SQLite file (`GET /projects/{teamID}/export`, membership-gated, tenant-scoped, secrets redacted)
- [x] Web — per-member API-key reveal + rotation (each member reveals/rotates their own key — scoped to `(team, user)`, secret shown once, destructive-confirm flow)
- [x] Subscriptions / billing — provider-agnostic (Stripe + OpenCollective): hosted checkout, signature-verified webhooks (Stripe) / operator-activated contributions (OpenCollective), self-service customer portal, FREE + PRO monthly/annual ladder
- [x] 2FA — per-user TOTP (Google-Authenticator compatible) + one-time recovery codes; enforced on password *and* social login
- [x] Passwordless — WebAuthn passkeys (passwordless primary login + passkey as a 2nd factor)
- [x] Operator plan override — unlimited (`-1` cap) plan + superadmin `set-plan` CLI
- [x] `/load-skill` Claude command — client-side wrapper over `am_load_skill`: fetch a team-shared skill by name and install it as a local `.claude/skills/<name>/SKILL.md` (shipped in the `aiagentmemory` installer)
- [x] Web — team/member management with per-member API keys: add a registered user by email (admin-gated) to mint them their own token, set roles (member/writer/admin) with a last-admin guard, and remove a member to revoke their keys in the same transaction (they can no longer connect)

---

## Provenance

A faithful Go SaaS rewrite of the original single-user Python
[`mempalace`](https://github.com/MemPalace/mempalace) (frozen) — that repository
is the upstream source this project is derived from. The domain model
(wings/rooms/drawers/closets/hallways/tunnels/KG/AAAK dialect), the 37-tool MCP
contract, the hybrid ranking, and idempotent mining are ported; Chroma, local
ONNX embeddings, and the HNSW repair tooling are dropped in favour of Qdrant +
Ollama from the start. Reference Go stack patterns follow the sibling
`forumchat` project (chi · templ · datastar · Ollama · Qdrant · MCP · RRF).
