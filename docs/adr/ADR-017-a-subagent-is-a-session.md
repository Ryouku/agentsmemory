# ADR-017: A subagent is a session, and must recall and persist like one

**Status:** Accepted
**Date:** 2026-08-21
**Owner:** unassigned
**Spec:** None — no spec stage
**Cross-references:** ADR-005 (deliverable handoffs — a subagent is the handoff case the write path already guards for humans), ADR-016 (the other "a whole surface exists and nothing reaches it" defect found the same day)
**Invalidates:** none — checked (grepped ADR-001..016 for `subagent`, `SubagentStart`, `hook`: no accepted ADR consumes the hook registration or assumes anything about subagents)
**Served-path change:** A subagent dispatched from any session currently reads no memory and writes none. After this it wakes knowing which wing it is in and what this project has already decided, and its findings reach the palace instead of dying with its transcript.

## Context

**T1's measurement, 2026-08-22. Treatment 5 of 5. Control 0 of 5.**

Ten subagents dispatched on this machine against a palace holding 449 memories,
with ordinary "why is this code like this" questions about this repository. No
prompt mentioned memory, recall, or any tool. Compliance counted from
`search_events`, which is the server's own log — not self-report.

| arm | `SubagentStart` injection | subagents that called `am_search` |
|---|---|---|
| treatment | on | **5 / 5** |
| control | off (documented switch) | **0 / 5** |

Every treatment query maps one-to-one onto its task, so the recalls are the
subagents doing the work rather than an artefact:

```
Service.Update drawer content change recompute entities palace
absentVerifyDepth absence verification depth cmd/server
wing scoping filter applied during Search in internal/palace
eval --style absent-easy hard negatives generator
closet prior search ranking
```

**What makes this worth acting on is what the control arm already contained.** It
was not "no instruction". Every one of those five subagents received the global
`CLAUDE.md`, the bootstrap protocol inlined, and this repo's `CLAUDE.md` and
`AGENTS.md` — including the hard gate that says to verify the `am_*` tools before
doing anything else — verbatim, in its first system-reminder block, ahead of the
task. **All of that produced zero recalls.** One short paragraph, placed next to
the task, produced five.

So the conclusion T1 was written to allow — *"if it does not move, more
instruction is the wrong answer and the tools go in the agent definition"* — does
not apply. Instruction works; PLACEMENT is what was missing. T2 (make the
injection standard through the installer) is justified as designed, and T3 keeps
its purpose rather than becoming the only mechanism.

**A bound T2 found, which the injection cannot cross.** An agent definition with a
`tools:` allowlist can only call what the list names, so a subagent defined that
way cannot recall however it is instructed — the instruction arrives and the tool
does not exist to obey it. This repository ships one definition and it names the
`am_*` tools; the reference machine carries three from another project whose
allowlists name none, and those are not this ADR's to edit. The injection is
therefore effective for `general-purpose` subagents (which carry the full tool
set, measured) and inert for restricted definitions elsewhere. That half is a
packaging problem in whoever ships the definition, not a compliance one.

**Three limits, stated because the numbers are small and clean enough to be
over-read.**

*n = 5 per arm.* 5/5 against 0/5 is unambiguous in direction, but it puts no
interval on the effect, and a rate near either end is exactly where small samples
flatter themselves.

*The arms used DIFFERENT tasks.* Both sets are "why is this code shaped this way"
questions of comparable difficulty, but a stronger design runs the SAME five tasks
through both arms. Task difficulty is therefore not fully controlled, and the
honest reading is "the injection is the plausible cause", not "the injection is
the proven sole cause".

*Another hook was already registered on `SubagentStart`* — a codebase-memory
reminder — and fired in both arms. It does not mention `am_*` and cannot explain
a 0-to-5 swing, but the control arm was not pristine.



Reported by a colleague and confirmed 2026-08-21 against this machine's own configuration. Every finding below is a line of code or a line of config, not an inference.

**Read side — a subagent recalls nothing, and NOT because it cannot.**

This section was written before the diagnostic below and said the opposite. It is corrected here rather than quietly rewritten, because what it got wrong changes which fix is worth building.

A `general-purpose` subagent was dispatched with one instruction: report what it can see. It reported, and the report contradicts the first draft of this ADR:

- **The protocol reaches it, in full, first.** The global `CLAUDE.md`, `agentsmemory-bootstrap.md` (inlined, not merely referenced), and this repo's `CLAUDE.md` and `AGENTS.md` were all present in the first system-reminder block, ahead of its task text. `AGENTS.md`'s hard gate — *"First action of every session in this repo… Before you read a file, plan, or write a line of code, confirm the `am_*` MCP tools are actually reachable"* — arrived verbatim.
- **The tools are there and they work.** All 41 `am_*` names were visible as deferred tools; `ToolSearch` loaded a schema and `am_status` returned a real answer.

So the read-side defect is not delivery. **It is compliance.** The instruction arrives complete, unconditional, and before anything else, and a subagent with one job reads its files and starts. That is a harder finding than a missing hook, and it invalidates the obvious fix: injecting more text into a context that already contains the whole gate verbatim is the least likely thing to change the outcome.

Two structural gaps survive the correction, and only two:

- **An agent whose definition declares a `tools:` allowlist cannot call `am_*` at all**, whatever reaches it. The three installed `codebase-memory-*` definitions are exactly that shape, and this repository ships no agent definitions of its own — the installer writes `CLAUDE.md`, `bootstrap.md`, `commands/`, `hooks/` and `extensions/`, and nothing under `agents/`.
- **No `SubagentStart` hook.** `clients/claude-code/installer.go:674,702,713` registers `Stop`, `SessionStart` and `SessionEnd` — all main-session events. The reference machine shows what one is for: `SubagentStart -> cbm-subagent-reminder`, installed by a different product, injecting `additionalContext` that names its tools and the order to call them in. Whether that helps HERE is now an open question rather than an assumption, because the thing it would inject is already present.

**The probe's own opinion is not evidence.** Asked whether it would have recalled unprompted, it said "likely yes". An agent asked whether it would have complied says yes; that is what the question selects for, and it is worth nothing next to a count of what dispatched agents actually did. T1 measures behaviour and does not ask.

**Write side — a subagent never persists.**

- No `SubagentStop` hook. The "persist before you stop" nudge — diary, knowledge-graph facts, drawers — is registered on `Stop`, which is the MAIN agent finishing a turn.
- `clients/claude-code/mineclaude.go:84` drops `isSidechain` lines by design, documented as "subagent traffic, not the user's conversation". So a subagent's work is not merely unpersisted; it is unrecoverable afterwards.

**The documentation says otherwise.** `clients/claude-code/README.md:71` claims "the memory-first workflow applies **every session** — you never have to type `/am`." Documentation is load-bearing in this repository by policy, and this sentence is false for the fastest-growing kind of session.

**And the protocol has considered subagents exactly once — to excuse them.** `AGENTS.md` mentions them twice: a passing note about `cqrs`, and the read-only review exception, which explicitly permits an agent dispatched to review to proceed with no memory at all. There is no corresponding rule for an agent dispatched to WORK.

**Measured on this session, which is the uncomfortable part.** Two implementation subagents were dispatched an hour before this ADR. Both received the full protocol and both had the tools — the probe establishes that — and neither reported recalling anything. The dispatcher gave them careful instructions about which files to read and said nothing about memory. So the failure had two independent causes at once: an instruction that arrived and was not followed, and a dispatcher who did not repeat it. The author of this ADR produced the defect while investigating it, which is the strongest available evidence that relying on either party to remember is not a mechanism.

## Existing Primitives Audit

- **`ensureHook(path, event, cmd, isObsolete)`** (`clients/claude-code/settings.go:28`) — already registers any Claude Code hook event by name, idempotently, with supersession of older commands. Reuse verbatim: two more calls.
- **`agentsmemory-verify-hook.sh`** (SessionStart) — already the pattern for a hook that emits context and never blocks: fail-open, silent when it has nothing to say, always exit 0. Reuse its shape.
- **`agentsmemory-stop-hook.sh`** — already the persist nudge, already has a `once`-per-session mode and a loop guard. Reshape: the same script serves `SubagentStop` with a different default, because a subagent's Stop is its LAST, not one of many.
- **`aiagentmemory` CLI + `am_status`** — already resolves the wing for a registration. Reuse: the injected context must NAME the wing rather than tell the subagent to work it out, because Step 0c of the protocol is the step most likely to be skipped by an agent that has one job.
- **`mineclaude.go`'s sidechain filter** — reuse the parser, reconsider the filter. Dropping sidechains is right for "mine the user's conversation" and wrong for "recover what a subagent learned"; those are two jobs sharing one flag.

## Decision

**A subagent is a session.** It gets the same two guarantees the protocol already promises: it wakes knowing where it is and what is already decided, and it does not finish without offering what it learned back.

Three mechanisms — and the order below is the corrected one. The first draft led with injecting context, which the diagnostic has since made the LEAST promising of the three.

1. **Shipped agent definitions that name the `am_*` tools.** This is the one unambiguous structural fix: an agent whose definition restricts tools cannot call memory however it is instructed, and this repository ships no definitions at all. It is also the only one of the three that cannot fail for compliance reasons, because it changes what is possible rather than what is asked.
2. **A `SubagentStop` hook carries the persist nudge**, defaulting to every subagent stop rather than once per session, because a subagent stops once. This is a harness prompt at a moment the agent is already stopping, not another paragraph competing with the ones it already skipped — a different mechanism from instruction, which is why it survives the correction.
3. **A `SubagentStart` injection — ONLY if T1 shows an instruction changes behaviour.** The full protocol already reaches every subagent, first and verbatim, so this adds emphasis to text that is present and ignored. T1 measures exactly that. If compliance does not move, this part is WITHDRAWN and the deferred alternative is promoted: have the hook run the recall and inject the RESULTS, which removes the compliance question rather than restating it.

**What would make this fail, and the data exists to check it today.** For mechanism 3 the claim is that an injected instruction changes what a subagent does, and the diagnostic has already weakened it: the same instruction, at greater length, is present and not followed. It is falsifiable by dispatching subagents with ordinary tasks and counting how many recall before their first substantive action, with and without the injection. **Below a clear difference, mechanism 3 is withdrawn** — not softened, not shipped hopefully — and the honest reading becomes that a subagent will not be instructed into recalling and must either have it done for it or not have it. Mechanisms 1 and 2 do not depend on that result and ship either way. Valid for Claude Code; codex and pi have their own hook models and are out of scope.

## Alternatives Considered

- **Rely on `CLAUDE.md` reaching subagents.** Rejected on evidence, and the evidence is stronger than expected: it DOES reach them — global `CLAUDE.md`, the bootstrap protocol inlined, the repo's `CLAUDE.md` and `AGENTS.md`, all in the first system-reminder block ahead of the task, with the hard gate verbatim. Two subagents dispatched from this session received all of it and recalled nothing. A protocol that arrives complete, first, and unconditionally, and is still not followed, has already been tried at full strength.
- **Tell the dispatcher to instruct its subagents.** Rejected: that is the current state, and the author of this ADR failed it twice in the hour before writing it. It also puts the burden on every future prompt rather than on the install.
- **Inject the whole bootstrap protocol into every subagent.** Rejected, and now for a second and better reason: it is already there. The first rejection was cost — thousands of tokens against a subagent's budget, most of it irrelevant to one task. The diagnostic supplies the stronger one: this is the current state, and it is what does not work.
- **Have the hook perform the recall itself and inject the RESULTS.** The strongest version, because it removes the compliance question instead of restating it — a subagent cannot skip a recall that already happened. Held back only because the hook does not know the task and would have to guess the query, and a wrong recall injected as fact is worse than none. It is now the named FALLBACK for mechanism 3 rather than a distant deferral: if T1 shows instruction does not move compliance, this is what replaces it.
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
| The injected instruction is read and ignored, so the fix ships and changes nothing | **High — already observed at full strength** | High | The whole protocol already reaches every subagent and is not followed, so this is no longer a risk but a measured starting condition. T1 counts real dispatches with a control; below a clear difference the injection is WITHDRAWN and the recall is done for the agent instead. Mechanisms 1 and 2 do not depend on the result |
| The context injection grows until it is scenery | High | Med | One paragraph, and T2's test asserts a length ceiling rather than trusting the author |
| Subagent diary entries drown the human's own | Med | Med | T3 scopes what a subagent persists to findings and decisions, not a session summary; measured after one week of real use |
| A hook that fails breaks every subagent dispatch | Low | High | Fail-open and always exit 0, copying `agentsmemory-verify-hook.sh`, whose comment already states this rule |

## Rollback

Both hooks are registered by the installer into a settings file it already manages idempotently, and both scripts exit 0 when disabled by env. Removing the registrations restores today's behaviour exactly; nothing is stored, migrated or re-shaped. The agent definitions are files in a directory Claude Code reads — deleting them is the rollback.

## Follow-ups
- [ ] Received from ADR-018: attributing a SUBAGENT's recalls separately from its dispatcher's. ADR-018 adds a session identity to every recall; a subagent is dispatched BY a session and it is undecided whether its recalls should carry its own identity or its parent's. This ADR owns what a subagent is, so it owns the question — and it only arises once T2 here makes a subagent recall anything at all.
