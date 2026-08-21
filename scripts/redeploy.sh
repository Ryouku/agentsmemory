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
  needles=("ranking: " "chunks_matched" "reranked" "lex-norm")
fi

echo "==> tests must pass before anything is built"
docker run --rm -v "$PWD":/src \
  -v agentsmemory-gocache:/root/.cache/go-build -v agentsmemory-mod:/go/pkg/mod \
  -w /src golang:1.26-alpine sh -c '
    gofmt -l cmd internal | grep -q . && { echo "gofmt dirty"; exit 1; }
    go vet ./... || exit 1
    go test ./... -count=1 >/dev/null 2>&1
  '
echo "    suite green"

echo "==> build"
"${COMPOSE[@]}" build "$SVC" >/dev/null
echo "==> restart"
"${COMPOSE[@]}" up -d "$SVC" >/dev/null

echo "==> wait for health"
for _ in $(seq 1 60); do
  curl -fsS -m 2 http://localhost:8080/healthz >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS -m 5 http://localhost:8080/healthz >/dev/null || { echo "    server did not come back"; exit 1; }

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

echo "==> what the running server resolved"
docker logs --since 2m "$CONTAINER" 2>&1 | grep -E "^.*(ranking:|fusion:|reranker:)" | tail -3 | sed 's/^/    /'

echo "==> smoke: one real search through the endpoint agents call"
start=$(date +%s)
code=$(curl -s -o /tmp/redeploy-smoke.json -w '%{http_code}' -m 60 -X POST http://localhost:8080/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"am_search","arguments":{"query":"reachability","limit":3,"snippet_chars":1}}}' || echo 000)
elapsed=$(( $(date +%s) - start ))
printf "    HTTP %s in %ss\n" "$code" "$elapsed"
[ "$code" = "200" ] || { echo "    the endpoint agents call did not answer"; exit 1; }
if [ "$elapsed" -gt 25 ]; then
  echo "    WARNING: ${elapsed}s is beyond what MCP clients have been observed to wait."
  echo "    A search that times out returns nothing, which is worse than a bad ranking."
  exit 1
fi
echo "==> deployed and verified"
