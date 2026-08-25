---
date: 2026-08-25
category: silent-failure
severity: medium
files_changed:
  - scripts/redeploy.sh
tags: [deploy-gate, false-alarm, unfalsifiable-probe, otel, tooling]
---

## Symptom

The server had been running a binary built before the OpenTelemetry work for
several hours. `scripts/redeploy.sh` — the script whose entire purpose is to
prove the running artifact carries the change — refused to deploy, printed

    ==> tests must pass before anything is built

and nothing else. No test name, no package, no reason. CI on the same commit was
green.

## Context

The staleness was real and independently confirmed before any of this: the
running container binary contained none of `AGENTSMEMORY_OTEL_ENDPOINT`,
`RETRIEVE_K` or `otel-endpoint`, while containing `RERANK_POOL`, `CLOSET_BOOST`
and `MEMORY_EVIDENCE_SELECTOR` — so the grep was looking in the right place and
could distinguish present from absent. A fresh `go build ./cmd/server` scored
3/3 on the same needles.

Two independent defects in the gate stood between that diagnosis and a deploy.

## Root Cause

**1. The test image had no `git`.** `internal/contractaxis` drives a real
repository — `git init`, `commit`, a disposable worktree — to prove a mutation
actually applied and was actually restored. `golang:1.26-alpine` ships no `git`,
and the gate's `apk add` line installed only `bash`:

```sh
apk add --no-cache bash >/dev/null 2>&1 || true
```

Fifteen tests failed with

```
--- FAIL: TestMutationRunnerRejectsASurvivingMutant (0.00s)
    mutation_test.go:74: git init -q: exec: "git": executable file not found in $PATH
```

The suite was red for an environment reason, on a commit whose CI was green.

**2. The reason was discarded.** The step read

```sh
go test ./... -count=1 >/dev/null 2>&1
```

so a red suite produced exactly one line of output — its own banner — and an
exit code. The script that exists to replace trust with evidence was suppressing
the evidence. That is why defect 1 could persist: nothing ever named it.

**3. (Found while fixing 1 and 2.) The kit freshness check could not pass.** It
compared `aiagentmemory --version` against `git rev-parse --short HEAD`:

```sh
want_rev="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
have_ver="$(aiagentmemory --version 2>/dev/null | sed -n 's/.* //p')"
case "$have_ver" in
  *"$want_rev"*) echo "    binary  $have_ver" ;;
  *) echo "    binary  STALE: $have_ver, checkout is $want_rev"; kit_stale=1 ;;
esac
```

`main.version` is stamped only by `-ldflags` in the release workflow
(`cmd/server/version.go`, `clients/claude-code/main.go` both default to `"dev"`),
so any locally built kit reports `dev` and can never match a SHA. The failure
message prescribed

```sh
go build -o $HOME/.local/bin/aiagentmemory ./clients/claude-code
```

which produces a binary reporting `dev`, so following the gate's own remedy left
the gate red. A binary built from the exact commit under test was reported STALE.

## Investigation

The first run was itself misread: `scripts/redeploy.sh … 2>&1 | tail -40`
reported exit 0, because that is `tail`'s exit code, not the script's. Re-running
with `set -o pipefail` and the status captured directly gave `REDEPLOY_EXIT=1`.

With the suite run visibly, the failure was one package and one cause.
`docker run --rm golang:1.26-alpine sh -c 'command -v git'` printed `NO_GIT`,
which settles it without reading any test.

For defect 3, `go version -m` on each binary separates "which program is this"
from "built from what" in one shot, and disproved a staleness verdict that was
about to be extended too far:

```
$HOME/.local/bin/aiagentmemory          clients/claude-code   vcs.revision=ea15ea8   (HEAD)
$HOME/.claude/bin/aiagentmemory-server  cmd/server            vcs.revision=65aaaa7   (stale)
```

The CLI's `0` on the otel needles meant *the needle does not apply to this main
package*, not *this binary is old*. Without the control, one artifact would have
been rebuilt for a defect it did not have.

## Fix

All three in `scripts/redeploy.sh`.

### Before

```sh
apk add --no-cache bash >/dev/null 2>&1 || true
gofmt -l cmd internal | grep -q . && { echo "gofmt dirty"; exit 1; }
go vet ./... || exit 1
go test ./... -count=1 >/dev/null 2>&1

# ... later, the kit freshness check:
want_rev="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
have_ver="$(aiagentmemory --version 2>/dev/null | sed -n 's/.* //p')"
case "$have_ver" in
  *"$want_rev"*) echo "    binary  $have_ver" ;;
  *) echo "    binary  STALE: $have_ver, checkout is $want_rev"; kit_stale=1 ;;
esac
```

### After

```sh
apk add --no-cache bash git >/dev/null 2>&1 || true
gofmt -l cmd internal | grep -q . && { echo "gofmt dirty"; exit 1; }
go vet ./... || exit 1
go test ./... -count=1 >/tmp/suite.log 2>&1 || {
  echo "--- suite RED ---"
  grep -E "^(--- FAIL|FAIL|panic:|.*\[build failed\])" /tmp/suite.log | head -40
  exit 1
}

# ... later, the kit freshness check:
want_rev="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
bin_path="$(command -v aiagentmemory)"
have_rev="$(go version -m "$bin_path" 2>/dev/null | sed -n 's/.*vcs\.revision=//p' | head -n1)"
have_dirty="$(go version -m "$bin_path" 2>/dev/null | sed -n 's/.*vcs\.modified=//p' | head -n1)"
if [ -n "$have_rev" ]; then
  if [ "$have_rev" = "$want_rev" ] && [ "$have_dirty" != "true" ]; then
    echo "    binary  $(printf '%.7s' "$have_rev")"
  elif [ "$have_rev" = "$want_rev" ]; then
    echo "    binary  STALE: built from $(printf '%.7s' "$have_rev") with uncommitted changes"; kit_stale=1
  else
    echo "    binary  STALE: built from $(printf '%.7s' "$have_rev"), checkout is $(printf '%.7s' "$want_rev")"; kit_stale=1
  fi
else
  have_ver="$(aiagentmemory --version 2>/dev/null | sed -n 's/.* //p')"
  echo "    binary  UNVERIFIED: reports $have_ver; no vcs stamp readable (need go on PATH)"
fi
```

`git` joins `bash` for the same stated reason, with the incident named in the
comment. The test step is quiet on success and, on failure, prints the failing
tests and any `[build failed]` line — a build error prints no `--- FAIL` at all,
so anything counting those reads a broken build as a clean pass. The kit check
now reads the artifact instead of its self-report, fails a binary built from a
dirty tree, and prints `UNVERIFIED` when no Go toolchain can read the stamp: a
check that cannot run must not look like one that ran.

Both fixes were confirmed falsifiable against the state they reject: the needle
grep scored 3/3 on a fresh build and 0/3 on the shipped one while the control
scored 2/1/1; the vcs extraction returned `ea15ea8` for the current binary and
`65aaaa7` for the stale one.

Result: `REDEPLOY_EXIT=0`, all three otel needles present in the running binary,
image digest matched, smoke search HTTP 200.

## Lesson

**A gate that fails for its own reasons is worse than no gate, and a gate whose
prescribed remedy cannot satisfy it teaches people to skip it.** Both shapes were
present here, and both were invisible because the third shape — a gate that
discards the reason it failed — hid them.

Three rules follow, and the repository already had the first in prose:

- **The reason a gate is red must reach the operator.** `>/dev/null 2>&1` on a
  test run turns a diagnosis into a shrug. Suppress output on success, never on
  failure.
- **Check the remedy, not just the check.** Run the fix the failure message
  prescribes and confirm the gate goes green. Here it could not, and had not
  since the check was written.
- **`| tail` eats the exit code.** A pipeline reports its last command's status.
  Any gate invoked through a pipe needs `set -o pipefail` or the status captured
  before the pipe — otherwise a red gate reports success.
