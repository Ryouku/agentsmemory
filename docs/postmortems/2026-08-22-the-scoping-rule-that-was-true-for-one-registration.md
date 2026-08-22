---
date: 2026-08-22
category: logic-error
severity: medium
files_changed:
  - internal/mcpserver/server.go
  - internal/mcpserver/drawers.go
  - internal/mcpserver/instructions_test.go
tags: [mcp, instructions, wing-scoping, unfalsifiable-test, adr-021]
---

## Symptom

Two MCP clients — Claude Desktop and Cursor — independently reported that a
wing-less `am_search` on their registrations searched every wing, contradicting
the instructions the server had just started sending them:

> "Your registration names no wing (`default_wing` is empty)… I ran an unscoped
> search to confirm: eight hits came back spanning five different project wings."

(The clients named the wings; they are elided here because a postmortem is a
public artifact and those are real project names.)

The shipped text said the opposite: *"PASS NO WING. Recall and writes are already
scoped to the project this registration was created for."*

## Context

ADR-021 T1 added `server.WithInstructions` to the MCP handshake
(`internal/mcpserver/server.go:168`), because a client with no protocol file had
invented a wrong wing-scoping rule from the tool schema alone. The instructions
were written to correct exactly that.

Scope resolution for a recall lives in `searchWingFor`
(`internal/mcpserver/server.go:272`), and is separate from `wingFor`, which serves
writes. Registrations carry their wing as an `X-Agentsmemory-Wing` header, read
into the request context by `auth.DefaultWingFrom`.

## Root Cause

The instruction was written as an unconditional rule for a behaviour that is
conditional on the registration.

```go
func searchWingFor(ctx context.Context, passed string, scoped bool) (string, error) {
	if w := strings.TrimSpace(passed); w != "" { /* explicit wing, or "*" */ }
	if !scoped {
		return "", nil
	}
	if def := auth.DefaultWingFrom(ctx); def != "" {
		return palace.SanitizeName(def, "wing")
	}
	// Registered without a wing: there is nothing to narrow to, and refusing
	// would break every caller that never had one.
	return "", nil // ← empty means EVERY wing
}
```

Omitting the wing is scoped **only when the registration carries a wing header**.
Without one it returns the empty string, which the search layer reads as "every
wing". The function's own comment says so.

Most registrations on the machine did not carry one: the user-scope Claude entry
in `~/.claude.json`, Cursor's `~/.cursor/mcp.json`, and Claude Desktop's config
all lacked the header. The author's own Claude Code session *was* scoped, because
a **project-level** entry under `~/.claude.json` → `projects."…/agentsmemory-main"`
carries `X-Agentsmemory-Wing: wing_<this-repo>`. One registration's behaviour was
generalised to all of them.

The second, worse fault is the test that let it through:

```go
if !strings.Contains(lower, "no wing") {
	t.Errorf("the instructions do not tell a client to pass NO wing by default…")
}
```

It asserted the presence of a slogan, so it encoded the wrong rule and went green
on it. A test that pins wording cannot detect that the wording is false.

## Investigation

1. **Two independent clients agreed, with evidence, against the shipped claim.**
   That is the signal worth acting on: one agent asserting something is a claim,
   two agents producing the same *observation* (hits spanning five wings, and a
   probe returning two unrelated project wings for a single query) is data.
   Neither could see the other's report.

2. **Read the resolution function before believing either side.**
   `searchWingFor` settled it in six lines, and its comment stated the exact case
   the instructions denied — "Registered without a wing: there is nothing to narrow
   to". Confirmed the source of a wing is `auth.DefaultWingFrom`, i.e. a header.

3. **Reproduced through the clients' own path.** Piping an `am_status` call
   through `mcp-stdio --url` — the bridge Claude Desktop spawns — returned
   `default_wing: ''`, where the author's session reports `wing_<this-repo>`
   from the same server. Same server, different answer, so the difference had to
   be per-connection.

4. **Found the discrepancy's source.** `~/.claude.json`'s top-level `mcpServers`
   entry has no headers, but the *project-scoped* entry does. That single header
   is why the author's every observation had looked correctly scoped.

5. **Re-ran the original verification honestly.** The check that had "confirmed"
   scoping searched `"SubagentStop hook"` and got three `wing_<this-repo>` hits
   — but that phrase exists in exactly one wing, so a workspace-wide search returns
   the same three results. The test could not have failed. Re-probed with
   `"deploy"`, a term present across the corpus: **8 hits across 4 wings**, no wing
   passed. The defect appeared immediately.

## Fix

### Before

```go
const serverInstructions = `…
PASS NO WING. Recall and writes are already scoped to the project this registration was created for, so omit the wing argument unless you deliberately mean to look elsewhere. Passing wing:"*" searches every project at once and retrieves worse rather than safer… am_status names the wing you are in.
…`
```

```go
// the test that enforced it
if !strings.Contains(lower, "no wing") { /* … */ }
```

### After

```go
const serverInstructions = `…
CHECK YOUR SCOPE ONCE, with am_status. If default_wing names a wing, this registration is scoped to one project and omitting the wing argument keeps recall there. If default_wing is EMPTY, omitting it searches EVERY wing — so pass an explicit wing when you know which project the answer is in, because unrelated projects do not remove the answer, they add competitors ahead of it. wing:"*" is for genuinely cross-project questions, never a safe default.
…`
```

```go
// the test now pins the MECHANISM a client needs, not a slogan
if !strings.Contains(text, "am_status")      { /* how to find your scope */ }
if !strings.Contains(lower, "default_wing")  { /* the field that decides it */ }
if !strings.Contains(lower, "every wing")    { /* what empty actually means */ }
```

The `am_search` tool description carried the identical false claim
(`internal/mcpserver/drawers.go:516`) and was corrected in the same commit.

This fix is correct because it stops the server asserting a fact it cannot know
at construction time. `WithInstructions` is a construction-time option; whether a
given connection is wing-scoped is a property of that connection's headers. The
only honest instruction is to tell the client how to look, and `am_status` already
reports `default_wing` for exactly this purpose. It also preserves the original
goal — steering clients away from `wing:"*"` as a default — without buying it with
a falsehood.

## Lesson

Never let documentation state as unconditional a behaviour that depends on
per-caller configuration — and never verify such a claim with a probe whose data
cannot exhibit the failure. A search for a term that exists in exactly one
partition returns the same results whether or not partitioning is applied, so it
confirms nothing; pick an input present in *several* partitions, so a scoped and
an unscoped run must differ. Correspondingly, a test that asserts documentation
contains a phrase enforces the phrase, not its truth: pin the mechanism the reader
needs (the field to check, the value that changes the answer) so that a wording
which stops being true also stops passing.
