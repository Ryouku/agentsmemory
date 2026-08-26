#!/usr/bin/env bash
# redeploy.sh — build, restart, and PROVE the running server carries the change.
#
# This exists because the server ran a 17-hour-old binary through an entire day of
# work, and nothing noticed. A build's success is a claim about the build; the only
# evidence that a change is live is reading the artifact that is serving.
#
# Usage: scripts/redeploy.sh [needle ...]
#   Each needle is a string the NEW binary must contain — one per change, so an
#   absent one names which change is missing. With no arguments it checks a
#   standing set plus a control.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE=(docker compose -f docker-compose.yml -f docker-compose.full.yml)
SVC=agentsmemory
CONTAINER=agentsmemory-agentsmemory-1
BIN=/usr/local/bin/agentsmemory

# A needle that MUST be present whatever changed. Without it, "absent" cannot be
# told apart from "the grep is looking in the wrong place", which is how a wrong
# path once reported every change as missing.
CONTROL=am_search

needles=("$@")
if [ ${#needles[@]} -eq 0 ]; then
  needles=("ranking: " "chunks_matched" "reranked" "lex-norm" "BEST over " "case_set_id")
fi

echo "==> tests must pass before anything is built"
docker run --rm -v "$PWD":/src \
  -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod \
  -w /src golang:1.26-alpine sh -c '
    # The hook tests execute the SHIPPED shell hooks, whose shebang is bash. The
    # base image has only ash, and those tests FAIL LOUDLY without it rather than
    # skipping — which is why this line exists: the first deploy after they landed
    # was correctly refused, instead of shipping over a suite that had not run.
    # git joins it for the same reason: internal/contractaxis drives a real
    # repository (git init, commit, worktree) to prove a mutation actually
    # applied, and alpine ships no git — so those 15 tests failed on the
    # environment, not the code, and the gate refused every deploy for a day.
    apk add --no-cache bash git >/dev/null 2>&1 || true
    gofmt -l cmd internal | grep -q . && { echo "gofmt dirty"; exit 1; }
    go vet ./... || exit 1
    # The reason a red suite is red must reach the operator. This line used to
    # end in >/dev/null 2>&1, so a failing gate printed its banner and nothing
    # else: the script whose whole purpose is proof was hiding the proof.
    go test ./... -count=1 >/tmp/suite.log 2>&1 || {
      echo "--- suite RED ---"
      grep -E "^(--- FAIL|FAIL|panic:|.*\[build failed\])" /tmp/suite.log | head -40
      exit 1
    }
  '
echo "    suite green"

echo "==> build"
"${COMPOSE[@]}" build "$SVC" >/dev/null
echo "==> restart"
"${COMPOSE[@]}" up -d "$SVC" >/dev/null

# The host port and the local token are configurable, and this script probed
# 8080 with no Authorization header whatever they were set to: a server on
# another port looked dead, and a server with AGENTSMEMORY_LOCAL_TOKEN set
# answered 401 and was reported as "did not answer".
PORT="${AGENTSMEMORY_HOST_PORT:-8080}"
BASE="http://localhost:${PORT}"
# Written as a single string rather than an array: macOS ships bash 3.2, where
# "${ARR[@]}" on an EMPTY array is an unbound-variable error under `set -u`, and
# this script died at the smoke step the first time it ran with no token set.
AUTH_HEADER=""
if [ -n "${AGENTSMEMORY_LOCAL_TOKEN:-}" ]; then
  AUTH_HEADER="Authorization: Bearer ${AGENTSMEMORY_LOCAL_TOKEN}"
fi

echo "==> wait for health"
for _ in $(seq 1 60); do
  curl -fsS -m 2 "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS -m 5 "$BASE/healthz" >/dev/null || { echo "    server did not come back on $BASE"; exit 1; }

echo "==> read the ARTIFACT that is serving, not the build log"
if ! docker exec "$CONTAINER" grep -ac -- "$CONTROL" "$BIN" >/dev/null 2>&1; then
  echo "    control needle '$CONTROL' not found — the grep is wrong, not the build. Refusing to report."
  exit 1
fi
missing=0
for n in "${needles[@]}"; do
  if docker exec "$CONTAINER" grep -ac -- "$n" "$BIN" >/dev/null 2>&1; then
    printf "    present  %s\n" "$n"
  else
    printf "    MISSING  %s\n" "$n"
    missing=1
  fi
done
[ "$missing" -eq 0 ] || { echo "    a change is not in the running binary"; exit 1; }

# Needles only prove a change that introduces a STRING. A pure-code change — a
# switch rewritten, a constant derived instead of declared — adds no literal, so
# no needle can distinguish it and "all present" would be a true statement about
# nothing. Comparing the running binary against a fresh build of the image is what
# covers those. It must be IMAGE-to-IMAGE: a host `go build` of the same source
# produces a different digest (the docker context excludes .git, and the two do
# not share a layer cache), so that comparison reports a false mismatch.
echo "==> digest: the running binary against the image just built"
# The image name comes from the CONTAINER, not from `compose config --images`,
# which lists every service's image and would depend on ordering.
# REDEPLOY_IMAGE is an override so this comparison can be DRIVEN — pointing it at
# a different image must make the check fail. A gate nobody can make fail is not a
# gate, and editing the script to test it tests the edit.
image=${REDEPLOY_IMAGE:-$(docker inspect "$CONTAINER" --format '{{.Config.Image}}' 2>/dev/null || true)}
fresh=""
live=$(docker exec "$CONTAINER" sha256sum "$BIN" 2>/dev/null | awk '{print $1}' || true)
if [ -n "$image" ]; then
  fresh=$(docker run --rm --entrypoint sha256sum "$image" "$BIN" 2>/dev/null | awk '{print $1}' || true)
fi
if [ -n "$fresh" ] && [ -n "$live" ]; then
  if [ "$fresh" = "$live" ]; then
    printf "    match %s\n" "$(printf %s "$live" | cut -c1-16)"
  else
    printf "    MISMATCH  image=%s  running=%s\n" \
      "$(printf %s "$fresh" | cut -c1-16)" "$(printf %s "$live" | cut -c1-16)"
    echo "    the container is not running what the image contains"
    exit 1
  fi
else
  # Failing to compare is not a pass. It means the check did not run, and a check
  # that did not run must not look like one that succeeded.
  echo "    could not compare digests: image=${image:-<unknown>} fresh=${fresh:-<unreadable>} running=${live:-<unreadable>}"
  echo "    the check did not run, so this deploy is unverified"
  exit 1
fi

echo "==> what the running server resolved"
# Informational, and NEVER fatal. Under `set -o pipefail` a grep that matches
# nothing returns 1 and kills the deploy — which is what happened the first time
# this ran against a container that had not just restarted, so there were no
# startup lines inside the window. A line that only REPORTS must not be able to
# fail the thing it reports on.
docker logs --since 10m "$CONTAINER" 2>&1 | grep -E "(ranking:|fusion:|reranker:)" | tail -3 | sed 's/^/    /' || echo "    (no startup lines in the window — the container did not restart)"

echo "==> smoke: one real search through the endpoint agents call"
start=$(date +%s)
code=$(curl -s -o /tmp/redeploy-smoke.json -w '%{http_code}' -m 60 -X POST "$BASE/mcp" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  ${AUTH_HEADER:+-H "$AUTH_HEADER"} \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"am_search","arguments":{"query":"reachability","limit":3,"snippet_chars":1}}}' || echo 000)
elapsed=$(( $(date +%s) - start ))
printf "    HTTP %s in %ss\n" "$code" "$elapsed"
[ "$code" = "200" ] || { echo "    the endpoint agents call did not answer"; exit 1; }
# HTTP 200 is not a successful search. MCP returns tool FAILURES inside a 200
# JSON-RPC envelope, so a dead embedder answers 200 with an error in the body and
# this script printed "deployed and verified" over it. The body was already
# being saved and never read.
if grep -q '"isError"[[:space:]]*:[[:space:]]*true' /tmp/redeploy-smoke.json ||
   grep -q '"error"[[:space:]]*:[[:space:]]*{' /tmp/redeploy-smoke.json; then
  echo "    the search returned HTTP 200 carrying an MCP error:"
  head -c 400 /tmp/redeploy-smoke.json | sed 's/^/      /'
  exit 1
fi
# The payload is a JSON string INSIDE the envelope, so its quotes arrive escaped.
if ! grep -qE '\\?"count\\?"[[:space:]]*:' /tmp/redeploy-smoke.json; then
  echo "    the search answered 200 with no result payload — this is not a working search:"
  # Bounded, and deliberately short: this body carries real memory text, and a
  # pasted deploy log is a public artifact.
  head -c 200 /tmp/redeploy-smoke.json | sed 's/^/      /'
  exit 1
fi
if [ "$elapsed" -gt 25 ]; then
  echo "    WARNING: ${elapsed}s is beyond what MCP clients have been observed to wait."
  echo "    A search that times out returns nothing, which is worse than a bad ranking."
  exit 1
fi

# The smoke search above just ran through every semantic stage. If it left no
# span, this deploy has no instrument — and an unmeasured deploy is one whose
# claims have to be believed. Asserted on the SEARCH THAT JUST RAN rather than on
# the startup banner, because the banner is only in the window when the container
# actually restarted, and an unchanged image does not restart it. Asserted on the
# trace rather than on the compose file for the same reason the needle check
# above reads the binary: a variable set in a file is a claim about the file.
echo "==> otel: the smoke search must have left a trace"
# --since "$start", NOT a duration: $start is the epoch second captured just
# before the smoke curl above. A window like `--since 120s` asks "did ANY search
# emit a span recently", which a span from a minute-old search satisfies while the
# tracer is dead — docker logs survive a restart, so the stale line is right there.
# That version passed its own mutant for a timing reason rather than a wiring one.
# Polled, not sampled once. The stdout path uses a SimpleSpanProcessor, so there
# is no batch delay — but the tree exporter prints a tree when its ROOT span ends,
# and that is the same instant the HTTP response is written. A single grep the
# millisecond curl returns lost that race and failed a deploy whose tracer was
# demonstrably on.
# 60 half-second attempts, not 20. The first search after a restart runs against a
# cold cross-encoder: measured 10.9s on 2026-08-25, against a 10s poll, and the
# gate failed a deploy whose tracing was demonstrably working. A gate that cries
# wolf on a cold start is one people learn to pass with the skip flag, so the
# bound must exceed the slowest legitimate smoke rather than the typical one.
traced=0
for _ in $(seq 1 60); do
  if docker logs --since "$start" "$CONTAINER" 2>&1 | grep -q "am\.search "; then traced=1; break; fi
  sleep 0.5
done
if [ "$traced" -eq 1 ]; then
  echo "    span tree emitted"
else
  echo "    the smoke search left NO am.search span."
  echo "    AGENTSMEMORY_OTEL_ENDPOINT is unset or the tracer never started, so this"
  echo "    stack cannot tell you which stages ran, which were bypassed, and why."
  exit 1
fi
# ---------------------------------------------------------------------------
# The CLIENT half. Everything above proves the SERVER carries the change; none of
# it says anything about the binary and kit installed on this machine.
#
# This exists for the same reason the rest of the script does. The server once ran
# a 17-hour-old binary through a whole day and nothing noticed; on 2026-08-22 the
# installed CLI was a day-old build, so the Stop hook embedded in it still printed
# a "memories to write" list that had been removed from the source, and `/M` still
# named `mempalace_*` tools that no longer exist. Both were discovered by the
# defect firing, not by a check.
#
# It FAILS rather than warns. A gate whose result is printed and not branched on
# is decoration, and this one has already been ignored once.
echo "==> the installed client kit, against this checkout"
kit_stale=0
if command -v aiagentmemory >/dev/null 2>&1; then
  want_rev="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  bin_path="$(command -v aiagentmemory)"
  # Read the ARTIFACT, not its self-report — the same rule the needle check above
  # follows. Every `go build` inside a checkout stamps vcs.revision into the
  # binary, while main.version is set only by -ldflags in the release workflow.
  # The old check compared `--version` against the short SHA, so every locally
  # built kit reported STALE forever, including one built from this exact commit
  # by the very command the failure message prescribes. A gate whose own remedy
  # cannot satisfy it is a gate people learn to skip.
  # sed, not awk: an unescaped $NF is expanded by the SHELL under `set -u`
  # before awk sees it, and the gate then dies with "NF: unbound variable"
  # instead of reporting staleness — a check that fails for its own reasons.
  have_rev="$(go version -m "$bin_path" 2>/dev/null | sed -n 's/.*vcs\.revision=//p' | head -n1)"
  have_dirty="$(go version -m "$bin_path" 2>/dev/null | sed -n 's/.*vcs\.modified=//p' | head -n1)"
  # Compare TREES, not revisions. A merge commit gives the same tree a new sha,
  # and revision equality reported every kit stale the moment a branch merged —
  # a gate that cries wolf on every merge is one people learn to pass with the
  # skip flag. git rev-parse on an unknown object fails, and the empty result
  # then falls through to the revision comparison below.
  want_tree="$(git rev-parse "${want_rev}^{tree}" 2>/dev/null || echo "")"
  have_tree="$(git rev-parse "${have_rev}^{tree}" 2>/dev/null || echo "")"
  if [ -n "$have_rev" ] && [ -n "$have_tree" ] && [ "$have_tree" = "$want_tree" ] &&
     [ "$have_rev" != "$want_rev" ] && [ "$have_dirty" != "true" ]; then
    echo "    binary  $(printf '%.7s' "$have_rev") (tree identical to $(printf '%.7s' "$want_rev"))"
  elif [ -n "$have_rev" ]; then
    if [ "$have_rev" = "$want_rev" ] && [ "$have_dirty" != "true" ]; then
      echo "    binary  $(printf '%.7s' "$have_rev")"
    elif [ "$have_rev" = "$want_rev" ]; then
      echo "    binary  STALE: built from $(printf '%.7s' "$have_rev") with uncommitted changes"; kit_stale=1
    else
      echo "    binary  STALE: built from $(printf '%.7s' "$have_rev"), checkout is $(printf '%.7s' "$want_rev")"; kit_stale=1
    fi
  else
    # No Go toolchain here, or a binary carrying no VCS stamp: the artifact
    # cannot be read, so say that rather than guess. Not a failure — a gate that
    # blocks with no way to satisfy it is the bug this block just fixed — but it
    # must never look like a pass, because silence is not success.
    have_ver="$(aiagentmemory --version 2>/dev/null | sed -n 's/.* //p')"
    echo "    binary  UNVERIFIED: reports $have_ver; no vcs stamp readable (need go on PATH)"
  fi

  # Byte-compare what the installer would lay down against what is there. The
  # binary embeds these, so a stale binary shows up here too — but a kit that
  # was never re-installed after a fresh binary shows up ONLY here.
  # This list is hand-maintained and has already drifted once: the SubagentStart
  # hook shipped without being added, so the one artifact the kit had just gained
  # was the one artifact this gate could not see. TestRedeployKitCheckCoversEveryInstalledArtifact
  # now fails when a hook, command, or agent definition is added to the kit and
  # not to this list — a gate maintained by intention is the thing this whole
  # script exists to replace.
  cfg="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
  for pair in \
    "commands/M.md:clients/claude-code/commands/M.md" \
    "commands/am.md:clients/claude-code/commands/am.md" \
    "commands/load-skill.md:clients/claude-code/commands/load-skill.md" \
    "agentsmemory-bootstrap.md:clients/claude-code/bootstrap.md" \
    "agentsmemory-stop-hook.sh:clients/claude-code/hooks/agentsmemory-stop-hook.sh" \
    "agentsmemory-verify-hook.sh:clients/claude-code/hooks/agentsmemory-verify-hook.sh" \
    "agentsmemory-session-end-hook.sh:clients/claude-code/hooks/agentsmemory-session-end-hook.sh" \
    "agentsmemory-stats.sh:clients/claude-code/hooks/agentsmemory-stats.sh" \
    "agentsmemory-subagent-start-hook.sh:clients/claude-code/hooks/agentsmemory-subagent-start-hook.sh" \
    "agents/agentsmemory-researcher.md:clients/claude-code/agents/agentsmemory-researcher.md" \
    "agents/agentsmemory-researcher.toml:clients/claude-code/agents/agentsmemory-researcher.toml"; do
    inst="$cfg/${pair%%:*}"; src="${pair##*:}"
    [ -f "$inst" ] || continue           # not installed is not stale
    if ! diff -q "$inst" "$src" >/dev/null 2>&1; then
      echo "    kit     STALE: ${pair%%:*}"
      kit_stale=1
    fi
  done
else
  echo "    (aiagentmemory not on PATH — nothing installed to check)"
fi
if [ "$kit_stale" -ne 0 ]; then
  echo
  echo "    The server is current and the client is not. That gap is invisible until"
  echo "    something embedded in the old kit misbehaves, which is how it was found."
  echo "    Fix:  go build -o \$HOME/.local/bin/aiagentmemory ./clients/claude-code"
  echo "          aiagentmemory install --agent claude --global --local --yes"
  echo "    Skip: REDEPLOY_SKIP_KIT_CHECK=1 scripts/redeploy.sh"
  [ "${REDEPLOY_SKIP_KIT_CHECK:-0}" = "1" ] || exit 1
fi

echo "==> deployed and verified"
