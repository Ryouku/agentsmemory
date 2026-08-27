# Task ADR-038-T3: A gate that fails when an id is re-derived, or a mint path forgets its key

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `TestNoPathRederivesADrawerID`, `TestEveryDrawerMintWritesAContentKey`, and the drift query
**Consumes:** `Repo.Save` upserting on `(team_id, content_key)` (T2)
**Data dependency:** hermetic for both gates. The drift query is run against a real corpus and its result recorded in the sign-off line, not in the gate.

## Goal

The two properties this ADR decides are checked by exit code rather than by prose: nothing re-derives
a drawer id, and no mint path writes a drawer without its content key.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/palace/identityrole_test.go` | add | both gates |
| `internal/palace/chunk.go` | edit | `DrawerID`'s doc comment names the one legal use, so the gate's rule is readable where the function is |
| `AGENTS.md` | edit | the Reachability section lists the gates in this tree and `TestAgentsMdNamesGatesThatExist` pins it — two new gates means two new lines, in this commit |

## Ordered Steps

1. Write the failing test(s) first and confirm they are RED — both gates, before any implementation:
   - `TestNoPathRederivesADrawerID` parses `internal/palace/*.go` (go/ast, not grep — a comment
     mentioning the name must not satisfy or trip it) and fails when `DrawerID(...)` appears anywhere
     other than an assignment to a `ContentKey` field or to a variable passed as one. Confirm it is
     red by adding a `DrawerID` call used as a lookup and watching it fail.
   - `TestEveryDrawerMintWritesAContentKey` **derives its universe** from the source: every composite
     literal of type `palace.Drawer` that sets `ID` must also set `ContentKey`. Derived, not
     hand-listed, so a mint path added tomorrow joins the check on the same commit. The diary mint
     sets `ContentKey: ""` explicitly, which satisfies the gate and documents the exemption at the
     site rather than in a list beside it.
2. Make them pass.
3. Add the drift query to the task as a recorded command (not part of the fence): count rows where
   `content_key != ''` and it does not equal the hash of the row's current fields. On 2026-08-27 the
   equivalent ad-hoc script found 27 of 1,705. Record the number this run produces in the sign-off.
4. Add the two gate names to `AGENTS.md`'s list and confirm `TestAgentsMdNamesGatesThatExist` passes.
5. Run the fence.

## Acceptance

```bash
go test ./internal/palace/ -run 'TestNoPathRederivesADrawerID|TestEveryDrawerMintWritesAContentKey' -count=1 2>&1 | tee /tmp/acc38e.out; ! grep -qE "no tests to run|^FAIL|^--- FAIL|no test files" /tmp/acc38e.out && go test ./... -count=1 2>&1 | tee /tmp/acc38f.out && ! grep -qE "^FAIL|^--- FAIL" /tmp/acc38f.out
```

The whole tree runs in the second command because this task edits `AGENTS.md`, which
`TestAgentsMdNamesGatesThatExist` reads from another package.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestNoPathRederivesADrawerID` | `internal/palace/identityrole_test.go` | the id is never recomputed for a lookup or a comparison | — |
| `TestEveryDrawerMintWritesAContentKey` | `internal/palace/identityrole_test.go` | a mint path that forgets the key fails the build's gate, derived from the source | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | both gates run and pass |
| 2 — something selects it | `go test ./...` runs them; mutation: add a `DrawerID` lookup and a `Drawer{ID: ...}` literal with no `ContentKey`, and each gate goes red for its own reason |
| 3 — the caller can discover it | `AGENTS.md`'s Reachability list names both, pinned by `TestAgentsMdNamesGatesThatExist` — the gate on the documentation is the one that stops the list rotting into a claim about tests nobody kept |
| 4 — it is used | the drift query, run against a real corpus and recorded in the sign-off. Its result is a number, not a pass — a corpus with drift is information, not a failure. |

## Mutation Log

## Invariants

- The gates derive their universe from the source, never from a hand-kept list.
- A comment naming `DrawerID` neither satisfies nor trips `TestNoPathRederivesADrawerID` — that is why it parses instead of grepping.
- The drift query stays OUT of the acceptance fence: it depends on a corpus, and a gate whose verdict depends on data nobody controls is a gate people learn to skip.

## Risks

- An ast-based gate is easy to write so permissively that it cannot fail. Mitigated by step 1's explicit red run, and by the Mutation Log entry required before this task may be marked done.
- `TestEveryDrawerMintWritesAContentKey` sees composite literals only. A mint built field-by-field on a `var d Drawer` escapes it. Say so in the test's own doc comment rather than claiming coverage the shape does not have.

## Stop Condition

Stop and ask if either gate cannot be made to fail. A gate that passes against the state it exists
to reject is decoration, and shipping it is worse than shipping nothing, because it reads as coverage.

**What would make these criteria impossible to fail?** For the ast gate: matching on a pattern no
real call site uses. Step 1's red run against a deliberately added violation is the check, and its
mutant entry is the evidence.

## Out of Scope

- Repairing the drifted rows the query finds (deferred: `docs/adr/BACKLOG.md`)
- Re-chunking on update (deferred: `docs/adr/BACKLOG.md`)

## Verification Log
