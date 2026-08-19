#!/usr/bin/env bash
# agentsmemory SessionEnd hook — the closing read on how memory did this session.
#
# The Stop hook fires when the AGENT finishes a turn, which is the right place for
# the persist checkpoint (it needs the agent to still be running) but the wrong
# place for a summary: at the first Stop the session has barely started. This
# fires when the session actually ends, so the numbers describe the whole of it.
#
# Modes (env AGENTSMEMORY_SESSION_REPORT):
#   on  (default) — print the recall report when the session ends.
#   off           — disabled.
#
# Everything is optional: no server, no curl, no stats endpoint each exit quietly.
# Nothing here is worth interfering with a session shutting down.
set -uo pipefail

INPUT="$(cat || true)"

[ "${AGENTSMEMORY_SESSION_REPORT:-on}" = "off" ] && exit 0
command -v curl >/dev/null 2>&1 || exit 0

# Measure the session from its transcript, so the report covers this session
# rather than an arbitrary window (see the same logic in the Stop hook).
QUERY="hours=${AGENTSMEMORY_STATS_HOURS:-2}"
TRANSCRIPT="$(printf '%s' "$INPUT" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
if [ -n "${TRANSCRIPT:-}" ] && [ -f "$TRANSCRIPT" ]; then
  BORN="$(stat -f %B "$TRANSCRIPT" 2>/dev/null || stat -c %W "$TRANSCRIPT" 2>/dev/null || true)"
  case "${BORN:-0}" in ''|*[!0-9]*|0) BORN="$(stat -f %m "$TRANSCRIPT" 2>/dev/null || stat -c %Y "$TRANSCRIPT" 2>/dev/null || echo 0)" ;; esac
  NOW="$(date +%s)"
  if [ "${BORN:-0}" -gt 0 ] && [ "$NOW" -ge "$BORN" ]; then
    MINUTES=$(( (NOW - BORN) / 60 + 1 ))
    [ "$MINUTES" -gt 1440 ] && MINUTES=1440
    QUERY="minutes=${MINUTES}&label=this%20session"
  fi
fi

BASE="${AGENTSMEMORY_STATS_BASE:-http://localhost:8080}"
if [ -n "${AGENTSMEMORY_LOCAL_TOKEN:-}" ]; then
  STATS="$(curl -fsS -m 3 -H "Authorization: Bearer ${AGENTSMEMORY_LOCAL_TOKEN}" "${BASE}/stats?${QUERY}" 2>/dev/null || true)"
else
  STATS="$(curl -fsS -m 3 "${BASE}/stats?${QUERY}" 2>/dev/null || true)"
fi

# stdout, not stderr: SessionEnd cannot block anything, so there is no feedback
# channel to use — this is a plain closing note.
# $(...) strips trailing newlines; give the report its last one back.
[ -n "$STATS" ] && printf '%s\n' "$STATS"
exit 0
