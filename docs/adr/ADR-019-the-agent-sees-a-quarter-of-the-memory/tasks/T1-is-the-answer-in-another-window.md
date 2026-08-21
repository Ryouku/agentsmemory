# Task ADR-019-T1: Is the answer in a window the chooser discarded?

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** S (few files)
**Owner:** unassigned
**Produces:** the coverage measurement that accepts or withdraws T2
**Consumes:** none
**Data dependency:** the 32 real queries and the live palace

## Goal

Know whether the answers agents miss are in windows `snippetWindow` scored and discarded, before changing what it returns.

The whole ADR rests on it. If the answer is usually in NO window of the returned memory — spread across it, or not there — then showing more windows buys nothing and the failure is synthesis, which this ADR explicitly does not address.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `cmd/server/doctor.go` | edit | a `--windows` report: for a memory and a query, what every candidate window scores and which the chooser took |
| `cmd/server/doctorwindowreport_test.go` | add | the report names the winner the real chooser would pick, not a re-implementation of it. NOT `doctor_windows_test.go`: Go reads `_windows` in a filename as a GOOS build constraint, so that file compiles only on Windows — it vetted clean, built clean and ran nothing |
| `docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md` | edit | the result is pasted into Context before T2 is written |

## Ordered Steps

1. Write the failing tests first (TDD red): `TestWindowReportNamesTheChosenWindow` and `TestWindowReportCoversTheWholeMemory`. Commit them red.
2. Report, for one (query, memory) pair: every candidate window `snippetWindow` scores, its term count, and which one it returns. Reuse the real function — a report built on a copy of the scoring loop measures the copy.
3. Run it over the queries the blind judge did not score as `answer`, against their top hit.
4. Classify each by hand: is the answer in the chosen window, in a DIFFERENT window, or in no window at all?
5. Paste the three counts into the ADR with the date, and state which of T2 and T3 they keep.
6. Falsify: report a window the chooser cannot return, or count a window as containing the answer without reading it.

## Acceptance

```bash
docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c '
  set -e
  apk add --no-cache bash >/dev/null
  gofmt -l cmd internal | grep -q . && { echo "gofmt"; exit 1; }
  go vet ./...
  go test ./cmd/server/ -run "TestWindowReportNamesTheChosenWindow|TestWindowReportCoversTheWholeMemory" -count=1 -v 2>&1 | tee /tmp/a19t1.out
  grep -q -- "--- PASS: TestWindowReportNamesTheChosenWindow" /tmp/a19t1.out
  grep -q -- "--- PASS: TestWindowReportCoversTheWholeMemory" /tmp/a19t1.out
  ! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/a19t1.out
  grep -qE "answer in the chosen window: [0-9]+" docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md
  grep -qE "answer in a different window: [0-9]+" docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md
  grep -qE "answer in no window: [0-9]+" docs/adr/ADR-019-the-agent-sees-a-quarter-of-the-memory.md
  go test ./cmd/server/ -count=1'
```

**Human-observed for the classification**, and the sign-off is named: step 4 requires reading whether a window answers a question, which no command decides. The command above proves the report exists, names the real chooser's pick, covers the whole memory, and that all three counts were recorded. A reviewer checks the classification against the pages.

## Tests

| Test name | File | Verifies | Covers |
|-----------|------|----------|--------|
| `TestWindowReportNamesTheChosenWindow` | `cmd/server/doctorwindowreport_test.go` | the window the report marks as chosen is the one `Snippet` actually returns — not a re-derivation that could disagree | — |
| `TestWindowReportCoversTheWholeMemory` | `cmd/server/doctorwindowreport_test.go` | every rune of the memory falls in at least one reported window, or the measurement has blind spots exactly where the answer might be | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestWindowReportNamesTheChosenWindow` |
| 2 — something selects it | it is a flag on the registered `doctor` command |
| 3 — the caller can discover it | `doctor --help` names it |
| 4 — it is used | T2 may not begin until its counts are in the ADR |

## Mutants

| Mutation | Compiles? | Test that goes red |
|----------|-----------|--------------------|
| report windows from a re-implemented loop rather than the real chooser | yes | `TestWindowReportNamesTheChosenWindow` |
| report only every other window | yes | `TestWindowReportCoversTheWholeMemory` |

## Out of Scope

- Changing what `Snippet` returns (deferred: T2 of this ADR)
- The wire fields (deferred: T3 of this ADR)

## Invariants

- The report uses the production chooser. A measurement of a copy measures the copy.
- The classification counts are written down whatever they say.

## Risks

- Classifying generously — "the window is about the right subject" counted as containing the answer — would make the ADR look justified whatever the truth. Mitigated: the same strict rule the judging rubric uses, and the reviewer checks it.

## Stop Condition

Stop and report if the answer is in NO window for most cases. That is the pre-registered falsification: the failure is synthesis or absence, this ADR is withdrawn, and more windows would have shipped a cost for nothing.

## Verification Log

- 2026-08-21 · 632c857* · exit 1 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
  ```
  === RUN   TestWindowReportNamesTheChosenWindow
  --- PASS: TestWindowReportNamesTheChosenWindow (0.00s)
  === RUN   TestWindowReportCoversTheWholeMemory
  --- PASS: TestWindowReportCoversTheWholeMemory (0.00s)
  PASS
  ok  	github.com/atvirokodosprendimai/agentsmemory/cmd/server	0.008s
  ```
- 2026-08-21 · 632c857* · exit 0 · `docker run --rm -v "$PWD":/src -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod -w /src golang:1.26-alpine sh -c ' …`
