#!/usr/bin/env bash
# agentsmemory Stop hook — nudge Claude to persist the session into agentsmemory
# memory (the team-shared MCP) before the turn ends: a diary entry, new
# knowledge-graph facts, and any notable decisions as drawers. Mirrors the
# mempalace stop-hook pattern.
#
# It reads the Stop event JSON on stdin, prints a checkpoint to stderr, and exits
# 2 so Claude Code surfaces it as blocking Stop feedback — the turn pauses until
# the session is persisted (or the reminder is acknowledged).
#
# Modes (env AGENTSMEMORY_STOP_HOOK):
#   once (default) — remind on the first Stop of a session, then stay quiet.
#   on             — remind on every Stop, like mempalace.
#   off            — disabled.
#
# It also prints a short recall report from a self-hosted server (AGENTSMEMORY_STATS=off
# to suppress, AGENTSMEMORY_STATS_HOURS to widen the window, AGENTSMEMORY_STATS_URL
# to point elsewhere) — see the bottom of this file for why that belongs here.
#
# `once` is the default because this hook exits 2, which BLOCKS the stop: on every
# turn of a long session that is a lot of interruption for a reminder the agent
# has already acted on. One checkpoint per session is the nudge; repeating it each
# turn is what teaches an agent (and a human) to dismiss it unread.
set -euo pipefail

# Consume stdin so the hook is a clean filter even when nothing reads it.
INPUT="$(cat || true)"

MODE="${AGENTSMEMORY_STOP_HOOK:-once}"
[ "$MODE" = "off" ] && exit 0

# Loop prevention — mirror mempalace's hook: Claude Code sets stop_hook_active=true
# on every Stop *after the first* in a turn. The first genuine Stop has it false
# (we fire); the re-fires caused by our own exit 2 have it true (we let through).
# Net: nudge once after each real stop, no infinite loop. Match on the raw JSON
# with grep rather than parsing — robust to spacing and key ordering.
if printf '%s' "$INPUT" | grep -Eq '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
  exit 0
fi

# In "once" mode, fire only the first time per harness session. The session id is
# parsed from the event JSON without requiring jq, so the hook has no runtime deps.
if [ "$MODE" = "once" ]; then
  SID="$(printf '%s' "$INPUT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  MARKER="${TMPDIR:-/tmp}/agentsmemory-stop-${SID:-nosession}.done"
  if [ -n "${SID:-}" ] && [ -f "$MARKER" ]; then
    exit 0
  fi
  [ -n "${SID:-}" ] && : >"$MARKER" 2>/dev/null || true
fi

# The checkpoint goes to stderr; exit 2 makes Claude Code show it as Stop feedback.
cat >&2 <<'MSG'
agentsmemory checkpoint — persist this session into team memory before stopping:
  1. am_diary_write — an AAAK session summary (what changed, why, open threads).
  2. am_kg_add      — new durable facts as subject -> predicate -> object triples.
  3. am_add_drawer  — notable decisions / code, verbatim, into the right wing + room.
Use the agentsmemory MCP tools (am_ prefix). Skip only if nothing was worth
remembering — and say so. This fires once per session; AGENTSMEMORY_STOP_HOOK=on
reminds every turn, =off disables it.
MSG

# ...and the half a reminder cannot give you: whether the memory is actually
# EARNING its place. A checkpoint that only ever asks for writes trains a team to
# fill a cabinet nobody opens. These lines say how many recalls this session ran,
# how many came back with something, and — most useful of all — what it looked for
# and did not find.
#
# Self-hosted only, and deliberately silent when anything is off: no server, an
# older server without /stats, no curl. A statistics line must never be the reason
# a Stop hook fails.
STATS_URL="${AGENTSMEMORY_STATS_URL:-http://localhost:8080/stats?hours=${AGENTSMEMORY_STATS_HOURS:-2}}"
if [ "${AGENTSMEMORY_STATS:-on}" != "off" ] && command -v curl >/dev/null 2>&1; then
  AUTH=()
  [ -n "${AGENTSMEMORY_LOCAL_TOKEN:-}" ] && AUTH=(-H "Authorization: Bearer ${AGENTSMEMORY_LOCAL_TOKEN}")
  STATS="$(curl -fsS -m 3 "${AUTH[@]}" "$STATS_URL" 2>/dev/null || true)"
  [ -n "$STATS" ] && printf '\n%s' "$STATS" >&2
fi

exit 2
