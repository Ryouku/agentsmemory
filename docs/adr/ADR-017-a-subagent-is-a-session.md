# ADR-017: A subagent is a session, and must recall and persist like one

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-005 (deliverable handoffs — a subagent is the handoff case the write path already guards for humans), ADR-016 (the other "a whole surface exists and nothing reaches it" defect found the same day)
**Invalidates:** none — checked (grepped ADR-001..016 for `subagent`, `SubagentStart`, `hook`: no accepted ADR consumes the hook registration or assumes anything about subagents)
**Served-path change:** A subagent dispatched from any session currently reads no memory and writes none. After this it wakes knowing which wing it is in and what this project has already decided, and its findings reach the palace instead of dying with its transcript.

## Context

Reported by a colleague and confirmed 2026-08-21 against this machine's own configuration. Every finding below is a line of code or a line of config, not an inference.

**Read side — a subagent never recalls.**

- `clients/claude-code/installer.go:674,702,713` registers exactly three hook events: `Stop`, `SessionStart`, `SessionEnd`. All three are MAIN-session events. Nothing fires when a subagent starts.
- The fix's shape is already proven on this machine by a different product. `~/.claude/settings.json` carries `SubagentStart -> cbm-subagent-reminder`, installed by the codebase-memory MCP, which injects `hookSpecificOutput.additionalContext` into every subagent naming its tools and the order to call them in. Run by hand, it returns that context and exits 0. agentsmemory has no counterpart.
- The installer writes `CLAUDE.md`, `bootstrap.md`, `commands/`, `hooks/` and `extensions/`. It writes nothing under `agents/`. That matters more than "no instructions": an agent definition may declare an explicit `tools:` allowlist — the three installed `codebase-memory-*` definitions all do — and for any agent defined that way the `am_*` tools are **not callable at all**, however it is instructed. Only a `tools: *` agent can reach them, and then only after a `ToolSearch`, because this repo's own `CLAUDE.md` documents that they load deferred.

**Write side — a subagent never persists.**

- No `SubagentStop` hook. The "persist before you stop" nudge — diary, knowledge-graph facts, drawers — is registered on `Stop`, which is the MAIN agent finishing a turn.
- `clients/claude-code/mineclaude.go:84` drops `isSidechain` lines by design, documented as "subagent traffic, not the user's conversation". So a subagent's work is not merely unpersisted; it is unrecoverable afterwards.

**The documentation says otherwise.** `clients/claude-code/README.md:71` claims "the memory-first workflow applies **every session** — you never have to type `/am`." Documentation is load-bearing in this repository by policy, and this sentence is false for the fastest-growing kind of session.

**And the protocol has considered subagents exactly once — to excuse them.** `AGENTS.md` mentions them twice: a passing note about `cqrs`, and the read-only review exception, which explicitly permits an agent dispatched to review to proceed with no memory at all. There is no corresponding rule for an agent dispatched to WORK.

**Measured on this session, which is the uncomfortable part.** Two implementation subagents were dispatched an hour before this ADR with detailed instructions about which files to read and no instruction to recall anything. Nothing in their context would have prompted it. The author of this ADR produced the defect while investigating it, which is the strongest available evidence that instructing people is not the fix.

## Existing Primitives Audit

- **`ensureHook(path, event, cmd, isObsolete)`** (`clients/claude-code/settings.go:28`) — already registers any Claude Code hook event by name, idempotently, with supersession of older commands. Reuse verbatim: two more calls.
- **`agentsmemory-verify-hook.sh`** (SessionStart) — already the pattern for a hook that emits context and never blocks: fail-open, silent when it has nothing to say, always exit 0. Reuse its shape.
- **`agentsmemory-stop-hook.sh`** — already the persist nudge, already has a `once`-per-session mode and a loop guard. Reshape: the same script serves `SubagentStop` with a different default, because a subagent's Stop is its LAST, not one of many.
- **`aiagentmemory` CLI + `am_status`** — already resolves the wing for a registration. Reuse: the injected context must NAME the wing rather than tell the subagent to work it out, because Step 0c of the protocol is the step most likely to be skipped by an agent that has one job.
- **`mineclaude.go`'s sidechain filter** — reuse the parser, reconsider the filter. Dropping sidechains is right for "mine the user's conversation" and wrong for "recover what a subagent learned"; those are two jobs sharing one flag.

## Decision

**A subagent is a session.** It gets the same two guarantees the protocol already promises: it wakes knowing where it is and what is already decided, and it does not finish without offering what it learned back.

Three mechanisms, in the order they matter:

1. **A `SubagentStart` hook injects the wing and the recall instruction.** Not a copy of the whole bootstrap protocol — a subagent has one job and a budget, and a wall of text is how a reminder becomes scenery. It names the wing, states that `am_search` is the only source of cross-session *why*, and names the one call that answers it.
2. **A `SubagentStop` hook carries the persist nudge**, defaulting to every subagent stop rather than once per session, because a subagent stops once.
3. **Shipped agent definitions that name the `am_*` tools**, so an agent with a `tools:` allowlist can reach them. Without this, mechanisms 1 and 2 instruct an agent to call tools it does not have — which is worse than silence, because it produces a subagent that reports it could not comply.

**What would make this fail, and the data exists to check it today.** The claim is that a hook-injected instruction changes what a subagent does. It is falsifiable by dispatching a subagent with an ordinary task and reading its transcript for an `am_search` call: if the injection lands and the subagent still does not recall, the instruction is being ignored and more instruction is not the answer — the tools would need to be in the agent's definition and the recall done FOR it. That test is cheap, runs on this machine, and T1 performs it before T2 is written. Valid for Claude Code; codex and pi have their own hook models and are out of scope here.

## Alternatives Considered

- **Rely on `CLAUDE.md` reaching subagents.** Rejected on evidence: whatever reaches them, two subagents dispatched from this very session did not recall, and the repo's own gate ("first action of every session") did not fire for either. A protocol that depends on being read by an agent with one job is not a mechanism.
- **Tell the dispatcher to instruct its subagents.** Rejected: that is the current state, and the author of this ADR failed it twice in the hour before writing it. It also puts the burden on every future prompt rather than on the install.
- **Inject the whole bootstrap protocol into every subagent.** Rejected: it is thousands of tokens against a subagent's budget, most of it irrelevant to one task, and a reminder nobody can finish reading is a reminder nobody reads. The precedent on this machine injects roughly one paragraph.
- **Have the hook perform the recall itself and inject the RESULTS.** Genuinely attractive — it removes the compliance question entirely — and rejected for now only because the hook does not know the task, so it would have to guess the query. Recorded as a deferral rather than a rejection: it is the strongest version of this idea and T1's measurement may argue for it.
- **Mine sidechains retroactively instead.** Rejected as a substitute and kept as a complement: recovering what a subagent did after the fact is strictly worse than it recalling before it starts, but it is the only route for work already done.

## Component / Boundary Impact

`clients/claude-code` owns installation and hooks; it gains two hook registrations, two scripts and an agents directory. `internal/*` is untouched — this is a client-side defect and the server is not involved. No boundary moves.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `SubagentStart` hook registration | add | `clients/claude-code/installer.go` | every subagent, via `additionalContext` |
| `SubagentStop` hook registration | add | `clients/claude-code/installer.go` | every subagent's final turn |
| `agentsmemory-subagent-start-hook.sh` | add | `clients/claude-code/hooks/` | Claude Code |
| shipped agent definitions naming the `am_*` tools | add | `clients/claude-code/agents/` | agents with a `tools:` allowlist |
| `README.md`'s "applies every session" claim | change — it is false for subagents until this ships, and must state what it covers | `clients/claude-code/README.md` | operators |

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| the compliance measurement | T1 | T2, T3 | No — T1 is a measurement and may redirect both |
| `SubagentStart` context | T2 | T2 | No — additive; a harness that does not send the event simply never calls it |
| `SubagentStop` nudge | T3 | T3 | No — additive |

## Implementation

Three tasks: `tasks/README.md`.

## Consequences

- **Positive:** the participant doing a growing share of the work stops being the only one that neither reads nor writes memory. A dispatcher stops having to remember, which is the failure this was found by.
- **Negative:** every subagent pays a small context cost at start, on a budget that is already the reason subagents exist. The injected text must stay one paragraph and that limit has to be defended.
- **Neutral:** a subagent that writes to the palace makes the diary noisier — several entries per session rather than one. Whether that is an improvement or a regression is measurable and is T3's own risk row.

## Out of Scope

- codex and pi subagent models (deferred: docs/adr/BACKLOG.md — they have their own hook shapes, and this ADR fixes the harness the defect was reported on)
- Mining sidechains so past subagent work is recoverable (deferred: docs/adr/BACKLOG.md — the filter is one flag serving two jobs, and separating them is its own decision)
- Having the hook run the recall and inject the RESULTS rather than the instruction (deferred: docs/adr/BACKLOG.md — the strongest version of this idea; it needs the task text the hook does not have)
- The read-only review exception, which stays (permanent: an independent reviewer sharing none of our context is valuable BECAUSE it shares none of it, and `AGENTS.md` already states the conditions)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| The injected instruction is read and ignored, so the fix ships and changes nothing | Med | High | T1 measures compliance on real dispatches BEFORE T2 is written, and the ADR names what to do if it fails: put the tools in the definition and recall for the agent rather than asking it |
| The context injection grows until it is scenery | High | Med | One paragraph, and T2's test asserts a length ceiling rather than trusting the author |
| Subagent diary entries drown the human's own | Med | Med | T3 scopes what a subagent persists to findings and decisions, not a session summary; measured after one week of real use |
| A hook that fails breaks every subagent dispatch | Low | High | Fail-open and always exit 0, copying `agentsmemory-verify-hook.sh`, whose comment already states this rule |

## Rollback

Both hooks are registered by the installer into a settings file it already manages idempotently, and both scripts exit 0 when disabled by env. Removing the registrations restores today's behaviour exactly; nothing is stored, migrated or re-shaped. The agent definitions are files in a directory Claude Code reads — deleting them is the rollback.

## Follow-ups
- [ ] Received from ADR-018: attributing a SUBAGENT's recalls separately from its dispatcher's. ADR-018 adds a session identity to every recall; a subagent is dispatched BY a session and it is undecided whether its recalls should carry its own identity or its parent's. This ADR owns what a subagent is, so it owns the question — and it only arises once T2 here makes a subagent recall anything at all.
