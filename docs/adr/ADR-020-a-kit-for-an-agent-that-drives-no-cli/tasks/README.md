# ADR-020 Tasks

Implementation tasks for ADR-020: a kit for an agent that drives no CLI.
See the parent ADR for the decision.

**Source of truth:** the task files' `Depends-on` / `Produces` / `Consumes` / `Covers` headers.
This README is a derived index — when it disagrees with a task file, the task file wins.

## Execution Order

| Order | Task | Depends-on |
|-------|------|------------|
| 1 | T1 | none |
| 2 | T2 | T1 |
| 3 | T3 | T2 |

Strictly sequential, and the ordering is a dependency rather than a preference. T1 makes an absent
capability legal; until it lands, declaring a kit with no `commandsDir` writes the slash commands
into the config dir's root. T2 needs the kit to exist before it can register anything for it, and T3
needs a registered server before "the protocol tells Cursor to call `am_search`" is true rather than
aspirational.

## Task Index

| Task | Goal | Produces | Consumes | Status |
|------|------|----------|----------|--------|
| [T1](T1-a-capability-can-be-absent.md) | An absent capability is legal kit data, not a name comparison | `cursorKit`, the guards, the refusal | none | done |
| [T2](T2-register-an-mcp-by-writing-the-file.md) | Cursor's MCP server is registered without a CLI to drive | the `mcp.json` writer | T1 | done |
| [T3](T3-the-protocol-reaches-cursor.md) | Cursor wakes with the protocol and can dispatch a memory-aware subagent | the rule file, the definition, the docs | T2 | done |
