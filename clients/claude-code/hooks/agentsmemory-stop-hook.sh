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
# The window is THIS SESSION, measured from the transcript file the event names,
# not a fixed number of hours. A fixed window at the first Stop of a session
# reports mostly the PREVIOUS session's work — the numbers looked plausible and
# described the wrong thing, which is worse than no numbers.
STATS_QUERY="hours=${AGENTSMEMORY_STATS_HOURS:-2}"
TRANSCRIPT="$(printf '%s' "$INPUT" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
if [ -n "${TRANSCRIPT:-}" ] && [ -f "$TRANSCRIPT" ]; then
  # Birth time where the filesystem records it (macOS %B, APFS), modification
  # time everywhere else — either bounds the session closely enough, and a bad
  # value simply falls back to the fixed window below.
  BORN="$(stat -f %B "$TRANSCRIPT" 2>/dev/null || stat -c %W "$TRANSCRIPT" 2>/dev/null || true)"
  case "${BORN:-0}" in ''|*[!0-9]*|0) BORN="$(stat -f %m "$TRANSCRIPT" 2>/dev/null || stat -c %Y "$TRANSCRIPT" 2>/dev/null || echo 0)" ;; esac
  NOW="$(date +%s)"
  if [ "${BORN:-0}" -gt 0 ] && [ "$NOW" -ge "$BORN" ]; then
    MINUTES=$(( (NOW - BORN) / 60 + 1 ))
    [ "$MINUTES" -gt 1440 ] && MINUTES=1440
    STATS_QUERY="minutes=${MINUTES}&label=this%20session"
  fi
fi

STATS_URL="${AGENTSMEMORY_STATS_URL:-http://localhost:8080/stats?${STATS_QUERY}}"
if [ "${AGENTSMEMORY_STATS:-on}" != "off" ] && command -v curl >/dev/null 2>&1; then
  # No arrays: macOS ships bash 3.2, where expanding an EMPTY array under `set -u`
  # aborts the script ("AUTH[@]: unbound variable"). Two explicit calls are longer
  # and cannot break the hook on the one platform most of these installs run on.
  if [ -n "${AGENTSMEMORY_LOCAL_TOKEN:-}" ]; then
    STATS="$(curl -fsS -m 3 -H "Authorization: Bearer ${AGENTSMEMORY_LOCAL_TOKEN}" "$STATS_URL" 2>/dev/null || true)"
  else
    STATS="$(curl -fsS -m 3 "$STATS_URL" 2>/dev/null || true)"
  fi
  # The server marks grouped write-me suggestions with a stable "  write: "
  # prefix (palace.RecallStats.SuggestionLines — that prefix is a contract with
  # this grep). They are split out of the report here and re-rendered below as
  # their own section, because a suggestion buried in a statistics table is a
  # statistic, while the same line under a "memories to write" heading is a task.
  # No arrays (bash 3.2), and every pipeline ends in `|| true`: grep exits 1 on
  # no match and `head` can SIGPIPE its producer — neither may kill the hook
  # under set -euo pipefail.
  REPORT="$(printf '%s\n' "$STATS" | grep -v '^  write: ' || true)"
  TODO="$(printf '%s\n' "$STATS" | grep '^  write: ' | head -n 3 | sed 's/^  write: /  /' || true)"
  # $(...) strips trailing newlines, so the report needs its last one back —
  # without it whatever the terminal prints next continues the report's last line.
  [ -n "$REPORT" ] && printf '\n%s\n' "$REPORT" >&2
  # memories to write — the recall flywheel's actionable half. Each line is a
  # cluster of this session's searches that found NOTHING, already collapsed
  # across paraphrasings and counted server-side; the wing in brackets says
  # where the memory belongs. Silent when there is nothing to say, capped at
  # three lines so the nudge stays a nudge — answering them is exactly the
  # am_add_drawer step the checkpoint above asks for.
  if [ -n "$TODO" ]; then
    printf '\nmemories to write — searched, found nothing (am_add_drawer each into its wing):\n%s\n' "$TODO" >&2
  fi
fi

exit 2
