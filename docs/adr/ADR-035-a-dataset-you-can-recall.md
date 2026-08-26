# ADR-035: A dataset you can recall

**Status:** Proposed
**Date:** 2026-08-26
**Owner:** unassigned
**Spec:** None — no spec stage; grounded in the shipped import surfaces read at `84d6ecc` and in a stated need: a generated project repository whose reference and seed data ship as JSONL beside the code that loads them.
**Cross-references:** `internal/wingbundle/wingbundle.go` (the portable record format), `internal/importer/importer.go` (the kinds it accepts, `:288`), `cmd/server/wing.go` (`wing export` / `wing import`), `internal/mcpserver/mine.go` (`am_mine`, the arbitrary-text path), ADR-013 (chunk collapse and the ranking unit), ADR-014 (the shipped default is the measured one), `docs/adr/BACKLOG.md`
**Numbering:** next free after ADR-034. Read live across `main` and every open branch on 2026-08-26: #49 holds 027, #57 holds 028–032 and 034, #58 holds 033. `main` holds 001–026.
**Invalidates:** none — checked. The bundle format, `POST /import`, `wing import` and `am_mine` are all reused unchanged; this ADR adds a producer of the existing format, not a second format.
**Served-path change:** none. No recall, ranking or MCP tool behaviour changes. A new CLI subcommand is added; nothing existing is altered.

## Context

A project's data arrives as JSONL — reference sets and seed data sitting in the repository beside the code that loads them and builds the SQL. The rows end up in a database, which is the right home for rows: it answers *"which invoices are overdue"* better than any vector search will.

**What no store answers is what the data MEANS.** Why the seed invoices all fall in one quarter, which of the twelve status values are reachable in practice, what `minor_units` is for, which reference set is authoritative and which is a fixture someone generated once. An agent opening that repository can read the rows and still not know any of it, and the person who did know is not in the session.

**The palace has four import paths and none of them fit.**

| Path | Shape it accepts | Why it does not fit |
|---|---|---|
| `POST /import` | kind-discriminated NDJSON (`manifest`/`drawer`/`diary`/`closet`/`kg`/`tunnel`, `importer.go:288`) | palace-shaped records, not domain rows |
| `agentsmemory wing import --file --as` | the same bundle, direct-DB | same, and local only |
| `agentsmemory wing export` | produces that bundle | it is the round-trip, not an entry point |
| `am_mine` | one text blob, over MCP | no file, no bulk, and one call per dataset by hand |

So the gap is narrow and specific: **there is no producer that turns domain JSONL into the format the palace already imports.** Everything downstream of that format works today, on both surfaces.

**Two facts about the existing format decide most of this design.** The bundle `Record` carries **no vector** — it is text only, and `internal/importer` embeds on the way in with a background worker draining the queue. So a producer cannot corrupt a palace by shipping vectors from a different embedding model, which is the failure the wing-bundle work already recorded as silent. And a drawer's id is **deterministic** — `sha256(team, wing, room, source_file, chunk, content)` (`palace.DrawerID`) — so a stable `source_file` plus a body that is a pure function of the data makes a re-import over an unchanged file an upsert rather than a second copy. That is a narrower guarantee than "idempotent by source", and the difference is load-bearing: `Add`/`Mine` purge a source before rewriting it, the **import** path deliberately does not (a batched migration would delete the earlier batches of the source it is still uploading). So a dataset that has CHANGED files a new profile and leaves the previous one recallable. Review caught this claim stated as the stronger one; the weaker one is the true one, and the gap is receipted in `docs/adr/BACKLOG.md` §"From ADR-035" rather than assumed away.

## Existing Primitives Audit

| Primitive | Where | Reused? |
|---|---|---|
| Bundle `Record` (no wing, no vector) | `internal/wingbundle/wingbundle.go` | Yes — emitted verbatim. No new format is defined. |
| `POST /import` + bearer gate | `internal/importer` | Yes, unchanged. This is the SaaS path and it needs no server change. |
| `wing import --file --as` | `cmd/server/wing.go` | Yes, unchanged. This is the self-hosted path. |
| Deterministic drawer ids | `palace.DrawerID`, via `internal/importer` | Yes, for what they actually promise: an unchanged file re-imports as an upsert, which is what makes a committed mapping file worth committing and a scheduled re-import safe. They do not replace a profile whose data changed — the import path absorbs and never purges by source. |
| Background embed worker | `internal/importer:62-65` | Yes. The producer emits text; embedding stays where it is. |
| `am_mine` | `internal/mcpserver/mine.go` | **No, deliberately.** Mining chunks one blob; this composes one deliberate drawer per dataset. Reusing it would put the profile at the mercy of chunk boundaries. |
| TOML parsing | `github.com/pelletier/go-toml/v2`, already direct (`clients/claude-code/settings.go`) | Yes — no new module. |

## Decision

**A dataset enters the palace as a measured profile plus a human explanation, one drawer per dataset. Rows stay where rows belong.**

Three parts, and the split between the first two is the point:

1. **What the tool MEASURES from the JSONL** — field names, inferred types, row count, distinct counts, date ranges. Derived on every run, so it cannot drift from the data it describes.

   **The values themselves are quoted only for the fields the mapping file names in `show_values`.** A profile is filed into a wing every agent recalls from and is embedded and indexed on arrival, so a column quoted here is published to every future session in that wing. A profiler cannot tell an enumeration from a small population — `status`, `country` and `manager_email` are all "twelve distinct strings" from inside it — and only the person who wrote the dataset knows which may be published. Naming a field is that person saying so.
2. **What a PERSON WRITES** in a mapping file committed beside the data — what the dataset is for, why it looks like this, what a field means that its name does not say. No tool can infer this, and it is the half worth recalling.
3. **A producer that emits the existing bundle format**, so `wing import` files it self-hosted and `POST /import` files it into SaaS, both unchanged.

**Bulk rows are refused, and that is a measurement rather than a taste.** This corpus already records that a larger, more heterogeneous corpus retrieves measurably worse — unrelated entries do not remove the answer, they add competitors ahead of it. Importing tens of thousands of seed rows would degrade recall for every other memory in that wing to answer questions SQL already answers better. The refusal is the feature.

## Alternatives Considered

- **One drawer per row, with a field mapping.** Rejected as the default for the recall-degradation reason above, and because the rows are already queryable in the database the same JSONL builds. Kept as a receipted follow-up for genuinely small lookup sets (currencies, statuses) where the row *is* the knowledge — with a row-count ceiling, so it cannot quietly become a bulk path.
- **Convention over configuration — infer content, room and date from field names.** Rejected. It is silent when it guesses wrong, and the *why* is the half that matters and cannot be inferred at all. A mapping file that must be written is a mapping file someone thought about.
- **Extend `POST /import` to accept arbitrary JSONL with a mapping in the request.** Rejected. It puts a schema-inference engine behind an authenticated write endpoint on the server, where a malformed mapping becomes a server-side failure mode, and it would need the same work again for the CLI. Converting on the client keeps the server's contract exactly as it is.
- **A second bundle format for datasets.** Rejected outright. Two formats is how one goes stale; the existing `Record` already carries every field this needs (`Room`, `SourceFile`, `Content`, `Entities`, `ContentDate`).
- **Import the JSONL as a closet document.** Rejected: closets are a mined index over drawers, not an entry point, and the profile is a composed statement rather than a chunked source.
- **A distinct-count threshold as the only control on quoting values, plus one verbatim example row.** This is what the first implementation did, and review caught it: `users.jsonl` and `contacts.jsonl` are exactly the seed files a project most wants documented, and both would have shipped their first row verbatim plus the full value set of every low-cardinality column — `country`, `email_domain`, `manager_email`. A threshold cannot distinguish an enumeration from a small population, and the mapping file offered no way to opt a field out short of not importing the dataset at all, which is the one thing the tool exists to make easy.
- **A list of EXCLUDED fields, keeping the example row behind an opt-in.** Rejected in favour of the allowlist above, though it is the smaller diff. The two fail in opposite directions and only one failure is recoverable: a column added to the dataset after the mapping file was written is merely *absent* from an allowlist's next memory, while an exclusion list written before that column existed *publishes* it. Publishing is the half that cannot be undone — a re-import replaces the drawer, but nothing un-embeds what the first one already filed. Once value sets are an allowlist, an example row is precisely the complement of what was allowed, so an opt-in to re-enable it would be an opt-in to defeat the control; the example row is deleted rather than gated. The allowlist's own failure — a silent omission — is answered by still reporting the distinct **count** of an unnamed field and stating out loud that values were withheld, so the gap is a pointer rather than a blank.

## Component / Boundary Impact

No boundary moves. A new package produces `wingbundle.Record` values; `cmd/server` gains one subcommand. `internal/importer`, `internal/palace` and every MCP tool are untouched. No migration, no schema change, no new response shape.

## Wiring & Contract Changes

- New subcommand `agentsmemory import`, registered in `cmd/server/main.go`'s command list.
- New flags, each read or the flag gate fails: `--config`, `--out`, `--push`, `--token`, `--as`.
- `--as` is **required with `--push`**, and `--push` without it is refused rather than attempted. A bundle carries no wing, `?as=` is where the destination is named, and `internal/importer` *skips* a record it cannot address instead of refusing it — so the unrefused combination uploads everything, stores nothing, and answers 200. Found by review; it is this repository's named defect (reachable, tested, does nothing) inside the commit whose gates exist to catch that class.
- The push reads the endpoint's **summary**, not its status code. `POST /import` consumes the whole body before replying, so it reports a storage failure inside a 200 as `Result.Error` and counts an unaddressable record into `Skipped`; a client that stops at 2xx cannot tell a full import from an empty one. The push also sets `recompute=1`, because hallways and entity tunnels are derived from the drawers and a single-shot import that skips the rebuild leaves its memories outside the graph.
- `--push` refuses a cleartext non-loopback endpoint: the workspace token travels with the bundle, and it is read/write access to the whole palace.
- The mapping file format is operator-facing and therefore documented where an operator reads it, with a gate that the documented keys are the parsed ones.
- `show_values` is part of that format, and therefore carries the same gate: it is documented in the README example, and a test asserts that no value of an unnamed field reaches the emitted drawer.

## Inter-task Contracts

| Contract | Producing task | Consuming task(s) | Breaking? |
|----------|----------------|-------------------|-----------|
| The measured profile of one JSONL file | T1 | T2 — composes it into a drawer | No — additive |
| Mapping file schema + bundle emitter | T2 | T3 — the CLI that drives it | No |

## Implementation

**T1 — measure a dataset.** A profiler over one JSONL file: fields, inferred types, row count, distinct counts, date ranges, and the value sets of the fields the caller names. Streaming, so a large seed file costs memory in rows rather than in files. The allowlist is applied where a measured value becomes a reported one, so the disclosure has a single gate a test can hold shut.

**T2 — the mapping file and the bundle it produces.** Parse the committed TOML, compose one drawer per dataset from the measured profile plus the human text, emit bundle NDJSON with a stable `source_file`.

**T3 — the CLI, and proof it is reachable.** `agentsmemory import` wired into the command list, with a test that fails when the registration is removed. This repository's characteristic defect is a capability that is finished and unreachable; the mutant is the proof, not the test's existence.

## Consequences

An agent opening a project repository can ask the palace what a dataset is and get an answer that was true at import time, with the fields and value sets measured rather than remembered. The mapping file makes the import reproducible and reviewable in the same pull request as the data it describes.

The cost is that the profile is a snapshot. It carries the date it was taken as the record's `content_date` — not inside its text, because the text is what the drawer's id is hashed from, and a date in there would make every night's re-import a new memory instead of the same one. Re-importing an unchanged file is therefore a no-op however often it runs. Re-importing a CHANGED file is not a replacement: it files the new profile and leaves the old one recallable, because the import path absorbs without purging by source. Until that gap closes (backlog), a dataset re-imported after a real change wants the superseded drawer deleted by hand.

## Out of Scope

- **Row-level import** (deferred: `docs/adr/BACKLOG.md` §"From ADR-035" — trigger: a reference set under a stated row ceiling that someone actually wants recallable row-by-row)
- **Watching the JSONL and re-importing on change** (deferred: same section — a scheduled or hook-driven re-import is a deployment concern, not a format one)
- **Nested structures below the first level** (deferred: the profiler reports a nested object's presence and type, not its interior; deep schema inference is its own decision)
- **Making the 25-value cap configurable** (permanent, and deliberate: the cap is no longer what decides how much data escapes — a field is quoted only when someone named it, so the cap bounds a disclosure that was already chosen. As a threshold standing alone it would be the whole control and 25 would be a number worth arguing about; behind the allowlist it is an upper bound on a deliberate act)
- **Any change to ranking, recall, or an MCP tool** (permanent: this ADR adds a producer of an existing format and nothing else)
- **A SaaS dashboard upload** (permanent for this ADR: the endpoint it would call is the one this already targets)

## Risks

- **A date range still discloses two real values.** `Earliest`/`Latest` are the minimum and maximum of a date column, and those are two actual cells from the file. It is the fact the profile most exists to carry — *why every seeded date falls in one quarter* — so it is reported unconditionally rather than gated behind `show_values`. The residual is stated rather than mitigated: a dataset whose date column is itself sensitive (a birth date, a diagnosis date) is a dataset to describe in prose and not to profile.
- **The human half goes stale while the measured half does not.** A re-import refreshes the profile and carries the old prose forward unchanged, so a description can quietly outlive the data it describes. Mitigated only by the drawer carrying its import date (`content_date`, returned beside the text on every recall); genuinely fixing it needs a person to re-read, and no gate can do that.
- **A partial read looks like a small file.** A line over the 8 MiB cap ends the scan, and keeping the rows already read beats returning nothing because row 40,000 was oversized — but a row count that stopped at ten is indistinguishable from a file with ten rows. The profile therefore reports the truncation in as many words, and the counts below it are explicitly about the part that was reached.
- **One dataset is one drawer, and the number of COLUMNS is not bounded.** A value is clipped to 256 bytes and a field contributes at most 25 of them, so a single field's line is bounded — but a JSONL with hundreds of keys produces a correspondingly long memory, and the import path absorbs a record as one chunk rather than chunking it the way `am_add_drawer` does. An embedder truncates at its own limit, so the tail of such a profile would be stored and unfindable. Left unbounded deliberately: capping the field list means silently dropping columns chosen by nothing better than alphabetical order, and a profile that omits fields without saying which is worse than a long one. Revisit with a real dataset wide enough to hit it.
- **Low-cardinality detection is a heuristic.** A field with few distinct values in the seed data may have many in production, and the profile would state a value set that is really a fixture artefact. The profile therefore reports what it saw *in this file*, phrased as such, never as a domain constraint.
- **A mapping file that lists a dataset which no longer exists** would silently describe nothing. The producer must fail on a missing file rather than skip it.

## Rollback

Delete the subcommand and the package. Nothing persists on the producing side; drawers already filed are ordinary drawers and are removed like any other.

## Follow-ups

- Row-level import for small reference sets, with a ceiling.
- A check that a mapping file's datasets all still exist, runnable in the PO repository's own CI.
