# Task ADR-017-T1: Measure whether a subagent obeys an injected instruction

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the compliance measurement that shapes T2 and T3
**Consumes:** none
**Data dependency:** needs a real Claude Code dispatch on a machine with the MCP registered

## Goal

Know whether injecting a recall instruction into a subagent actually changes what it does, before building the mechanism that injects it.

The ADR's central risk is that the fix ships and changes nothing: a subagent has one job and a budget, and an instruction it did not ask for is the first thing a busy agent skips. If that is what happens, more instruction is the wrong answer and the tools have to be in the agent's definition with the recall done FOR it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `clients/claude-code/hooks/agentsmemory-subagent-start-hook.sh` | add | a minimal injector — the measurement needs the real mechanism, not a mock of it |
| `clients/claude-code/hooks_test.go` | add | the envelope is valid and the hook fails open, asserted rather than eyeballed |
| `docs/adr/ADR-017-a-subagent-is-a-session.md` | edit | the result is pasted into Context before T2 is written |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestSubagentStartHookEmitsAContextEnvelope` and `TestSubagentStartHookFailsOpen`. Commit them red — the hook does not exist yet, so they fail on its absence, which is the state this task ends.
2. Write the hook as the smallest thing that emits `hookSpecificOutput.additionalContext` on `SubagentStart` and exits 0. Copy the fail-open shape of `agentsmemory-verify-hook.sh` exactly.
3. Register it by hand (not through the installer — the installer change is T2) and confirm the event fires at all: dispatch one subagent and look for the injected text in its context.
4. Dispatch **five** subagents with ordinary, memory-silent coding tasks in a repo whose wing holds memories. Do not mention memory in any prompt.
5. Count how many called `am_search` or `am_status` before their first substantive action. That count, out of five, is the measurement.
6. Repeat once with the injection DISABLED, as the control. A compliance rate that is the same either way means the injection is not what caused it.
7. Paste both numbers into the ADR's Context, with the date and the harness version.

## Acceptance

```bash
# Human-observed: this measures a live agent's behaviour and cannot be asserted
# from a unit test. The sign-off is the two numbers in the ADR.
test -f clients/claude-code/hooks/agentsmemory-subagent-start-hook.sh &&
bash -n clients/claude-code/hooks/agentsmemory-subagent-start-hook.sh &&
echo '{"hook_event_name":"SubagentStart"}' | bash clients/claude-code/hooks/agentsmemory-subagent-start-hook.sh | grep -q additionalContext &&
grep -qE 'with-injection: [0-9]+ of 5' docs/adr/ADR-017-a-subagent-is-a-session.md &&
grep -qE 'without-injection: [0-9]+ of 5' docs/adr/ADR-017-a-subagent-is-a-session.md
```

**Human-observed acceptance, with the sign-off named:** the two counts are produced by dispatching real subagents and reading their transcripts. The command above proves the hook exists, parses, emits the right envelope, and that BOTH numbers were recorded — it cannot prove they were honestly measured, and no command can. The reviewer checks the transcripts.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestSubagentStartHookEmitsAContextEnvelope` | `clients/claude-code/hooks_test.go` | the script emits valid `hookSpecificOutput` JSON with `additionalContext`, and exits 0 | — |
| `TestSubagentStartHookFailsOpen` | `clients/claude-code/hooks_test.go` | no binary, no server, no wing — each exits 0 and blocks nothing | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSubagentStartHookEmitsAContextEnvelope` |
| 2 — something selects it | registered by hand here; the installer registration is T2 and is where this rung is really closed |
| 3 — the caller can discover it | the harness calls it; a subagent does not opt in |
| 4 — it is used | the measurement IS the use, and it is the point of this task |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| emit plain text instead of the JSON envelope | n/a (shell) | `TestSubagentStartHookEmitsAContextEnvelope` |
| exit 1 when the server is unreachable | n/a (shell) | `TestSubagentStartHookFailsOpen` |

## Out of Scope

- Registering the hook through the installer (deferred: T2 of this ADR)
- The `SubagentStop` half (deferred: T3 of this ADR)

## Invariants

- The hook never blocks a dispatch and never exits non-zero.
- The control run is taken. A measurement with no control cannot attribute the effect.

## Risks

- Five is a small sample and the agents are not independent — same model, same machine, similar tasks. Mitigated by reporting it as five, not as a percentage, and by taking the control.

## Stop Condition

Stop and report if compliance with injection is not clearly higher than without. That is the ADR's pre-registered falsification: it means instruction is not the mechanism, and T2 must put the tools in the agent definition and do the recall rather than ask for it.

## Verification Log

<Tool-written by adr-verify. Do not hand-edit.>
