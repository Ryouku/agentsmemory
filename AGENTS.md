# agentsmemory — project protocol (repo root)

This is the **agentsmemory** repository: the multi-tenant memory palace that AI
agents read from and write to over MCP. Working here without agent memory is
building the thing while refusing to use it.

This file is the source of truth for how agents work in this repo. Claude Code
reads it through the `@AGENTS.md` import in `CLAUDE.md`; codex, pi and anything
else that honours `AGENTS.md` read it directly. It sits **on top of** the global
`agentsmemory-bootstrap.md` protocol, and its one addition is the hard gate
below.

---

## Gate — verify the `am_*` tools before you do anything else

**First action of every session in this repo.** Before you read a file, plan, or
write a line of code, confirm the `am_*` MCP tools are actually reachable:

1. **Look for them in your tool list.** They are named `am_status`, `am_search`,
   `am_skillset`, `am_add_drawer`, `am_diary_write`, `am_list_skills`,
   `am_load_skill`, and ~30 more. On a harness that namespaces MCP tools they
   appear as `mcp__agentsmemory__am_*`.
2. **A name you cannot call yet is not an absent tool.** Some harnesses load MCP
   tools **deferred** — the name is listed but the schema is not, so a direct
   call fails with a validation error. Load the schema first
   (`ToolSearch "select:am_skillset,am_status,am_search"` on Claude Code), *then*
   call. Do not conclude the tools are missing because the first call errored on
   its arguments.
   Likewise, a server that answers is not the right server: a workspace token
   from another project still returns OK. Only step 4's workspace check proves
   you're home.
3. **Probe, don't assume.** Call `am_skillset` and then `am_status`. A non-error
   return from both means the tools are present and the workspace token is valid
   — for *some* workspace. That is not enough.
4. **Verify the workspace identity.** `am_status` names the workspace it is
   scoped to: `mode` (`local` for a self-hosted server, `hosted` for the SaaS)
   and a `workspace` block carrying its `slug` and `name`. **That** is what
   proves you are home — a global registration carrying another project's token
   answers every probe happily, and only the workspace it names tells you whose
   palace you just opened. Working on your own machine expects
   `mode: "local"`; a hosted session expects your team's workspace slug.

   A workspace you do not recognise is worse than a connection error: you would
   recall another project's decisions as if they were this team's, and every
   write would land in the wrong palace. Stop, run the absent path, and write
   nothing (no diary, no KG, no drawers — that's poisoning another project).

   **A missing `wing_agentmemories` is NOT a wrong workspace.** A wing comes into
   existence when something is first written to it, so on a fresh install the
   wing this protocol tells you to create is necessarily absent — the very first
   session in any repo would otherwise trip a gate that can only be satisfied by
   violating it. Read an empty or missing wing as "first session here; my writes
   will create it", say so in one line, and get on with the work.
5. **Likewise for skills.** A skill missing from your harness's *local* list is
   usually **centralised**, not absent — `am_list_skills` is the catalogue,
   `am_load_skill(<name>)` fetches the body. Check it before you decide the team
   has no convention for your stack.

**Present and correctly scoped** → follow
*[When the tools are present](#when-the-tools-are-present)* below and get on
with the work.

**Absent or wrong workspace** — no `am_*` names in the tool list at all,
`am_skillset` / `am_status` fail with a transport, auth, or connection error,
or `am_status` names a workspace that is not yours — → stop and run
*[When the tools are absent](#when-the-tools-are-absent)*. Do not start the
task. (An unfamiliar *wing* is not this case; an unfamiliar *workspace* is.)

---

## When the tools are absent

### Step 1 — tell the user, before anything else

Say this in your own words, but say all of it:

> **The agentsmemory (`aiagentmemory`) tools are not connected in this session.**
>
> These tools now exist, and this project's protocol is built on them. Without
> them I cannot work on tasks here the way this repo expects, because I am
> missing:
>
> - **`am_search` — cross-session *why*.** Every past decision, tradeoff and
>   gotcha this team recorded is unreachable. I will re-derive things that were
>   already settled, and I may quietly contradict a choice you made last week.
> - **`am_list_skills` / `am_load_skill` — the team's centralised conventions.**
>   The house skills (`effective-go`, `cqrs`, and whatever else the catalogue
>   holds) are versioned server-side, not in this repo. Without them I fall back
>   to generic conventions and my output will drift from house style.
> - **`am_diary_write` / `am_kg_add` / `am_add_drawer` — the write side.**
>   Nothing I learn this session gets persisted. The next session starts exactly
>   as blind as this one, and this session's work is memory lost.
>
> **Fixing it takes about two minutes:**
>
> ```bash
> # install the kit (commands + protocol + Stop hook) and register the MCP
> curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
>
> # or, if the binary is already installed — pick your agent
> aiagentmemory install --agent claude    # claude | codex | pi | both | all
>
> # or register the MCP by hand against a running server
> claude mcp add --transport http agentsmemory http://localhost:8080/mcp
> ```
>
> Run one of those, restart the session, and I will have full recall.

Then ask whether they want to install, or continue without memory.

### Step 2 — if they want to continue anyway, ask six times

Working memory-blind in *this* repo is a real cost, not a formality, so the
opt-out is deliberately expensive. Ask **all six** questions below.

**Rules — these are what make the gate a gate:**

- **One question per turn.** Ask, stop, wait for the user's answer, then ask the
  next. Never batch two into one message and never present all six as a list.
- **Six distinct questions**, in order. Each names a different thing they are
  giving up. Do not paraphrase one question six times.
- **A blanket "yes to everything" up front does not satisfy the gate.** Thank
  them and ask question 1 anyway. The point of six asks is six moments to
  reconsider, and one pre-emptive yes is zero of them.
- **Anything that is not a clear affirmative ends it.** "Maybe", "I guess",
  "whatever", silence, a change of subject, or a question back — treat as *no*,
  stop asking, and go back to offering the install.
- **Any "no" or "stop" at any point ends it immediately.** Do not re-ask, do not
  argue, do not restart the count. Offer the install and wait.
- **Never work around the gate** — no proceeding "just to look at one file", no
  starting the task while you ask, no substituting your own notes or a scratch
  file for the palace.

The six questions:

1. **Understanding.** "The agentsmemory tools are not connected. I'd be working
   with no memory of anything this team has decided before. Do you understand
   that, and still want to continue?"
2. **Losing the *why*.** "Without `am_search` I cannot recall why this code is
   shaped the way it is — I will reconstruct it from the source and I may get the
   reasoning wrong or reopen a settled decision. Is that acceptable?"
3. **Losing the house conventions.** "The team's centralised skills won't load,
   so I'll be writing to generic conventions instead of this team's. The result
   may not match the style of the surrounding code. Continue?"
4. **Losing the write side.** "Nothing from this session will be persisted — no
   diary entry, no drawers, no knowledge-graph facts. Whatever we work out today
   is gone by the next session and someone will pay for it twice. Still go
   ahead?"
5. **The cheap alternative, one more time.** "Installing takes about two minutes
   and then I'd have full recall for this and every future session. Would you
   rather do that now than have me work blind?"
6. **Final confirmation.** "Last check: proceed with this task **without** agent
   memory tools and without the team's skills, accepting all of the above?"

### Step 3 — after six explicit yeses

Only then may you start the task, and only in a degraded mode you keep visible:

- **Open every subsequent response with one line** stating you are working
  without agent memory — e.g. `⚠ no agentsmemory — working from source only`.
  Not once at the start; every turn, so it never fades into the background.
- **Say what you re-derived.** When you work something out that the palace would
  have told you, flag it as reconstructed and therefore unverified against past
  decisions.
- **Keep a written handoff.** Since nothing can be persisted, end the session
  with a summary the user can paste into the palace later — the decisions, the
  gotchas, the open threads.
- **Re-probe on any new session.** The gate is per-session. Six yeses today do
  not carry into tomorrow.

---

## When the tools are present

Normal operation. Recall before you act, persist before you stop.

**Recall, in this order:**

1. `am_skillset` — the server's own wake-up playbook and live tool catalogue.
2. `am_status` — workspace identity (`mode` + `workspace`), palace shape, quota.
   This repo's wing is **`wing_agentmemories`**; if it is not in the list yet,
   this is the first session here and your first write creates it.
3. `am_search(<task>)` — past decisions and rationale. This is the *only* source
   of cross-session *why*; don't reconstruct from code what memory explains.
4. `am_list_skills` → `am_load_skill(<name>)` — the team's centralised
   conventions for the stack you're touching. This repo is Go, so `effective-go`
   at minimum; add `cqrs` when the work is live/realtime or fans out across
   subagents.

**Recall mid-session too, not just at the start.** Before any broad grep over
unfamiliar code, `am_search` for the symbol or subsystem first and grep only the
gap. Same for tools: if your hand hesitates on a tool's parameters, that
hesitation is the cue to `am_search` for its usage before guessing.

**Persist before you stop:**

- `am_diary_write` — an AAAK session entry (what you built, decided, learned, and
  any open thread) under a stable `agent_name` so the journal threads.
- `am_kg_add` — durable facts as `subject → predicate → object`.
- `am_add_drawer` — notable decisions and code, verbatim, into the right
  wing/room.
- `am_create_tunnel` — when the work connects to another project; check
  `am_find_tunnels` / `am_follow_tunnels` first so you reinforce rather than
  duplicate.

A verified change that isn't written back is memory lost. Skip only when the
session produced nothing worth recalling — and say so.
