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
    apk add --no-cache bash >/dev/null 2>&1 || true
    gofmt -l cmd internal | grep -q . && { echo "gofmt dirty"; exit 1; }
    go vet ./... || exit 1
    go test ./... -count=1 >/dev/null 2>&1
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
echo "==> deployed and verified"
