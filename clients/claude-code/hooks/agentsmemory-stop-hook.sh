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
# It serves TWO events. `Stop` is the main agent finishing a turn; `SubagentStop`
# is a subagent finishing for good, and it gets a different message — findings and
# facts, not a session summary. The dispatcher writes the summary. A diary entry
# per subagent is how a journal stops being read: a 16-way fan-out would file
# seventeen accounts of one piece of work, sixteen of them by an agent that saw a
# sliver of it. Same machinery, different text, one file to keep in step.
#
# Modes (env AGENTSMEMORY_STOP_HOOK):
#   once (default) — remind on the first Stop of a session, then stay quiet.
#   on             — remind on every Stop, like mempalace.
#   off            — disabled, both events.
#
# AGENTSMEMORY_SUBAGENT_STOP_HOOK=off disables the subagent half alone, leaving
# the human's checkpoint. Exit 2 costs a subagent one extra turn and a wide
# fan-out pays that once per branch, so the bill has its own switch rather than
# forcing a choice between subagent writes and the session checkpoint.
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

# Which event fired. `grep -o | head -n1` rather than a greedy sed, because the
# SubagentStop payload also carries `last_assistant_message` — arbitrary agent
# prose, after this key — and `sed 's/.*"hook_event_name"...'` matches the LAST
# occurrence, so an agent that happened to quote the key would choose the branch.
# Every pipeline here ends in `|| true`: grep exits 1 on no match and head can
# SIGPIPE its producer, neither of which may kill the hook under set -euo pipefail.
EVENT="$(printf '%s' "$INPUT" | grep -o '"hook_event_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed 's/.*"\([^"]*\)"$/\1/' || true)"
# Default to the SESSION path for anything unrecognised. If a future harness
# renames the event, the human's checkpoint must survive the rename — a branch
# that failed closed would take that away too, on a change nobody announced.
IS_SUBAGENT=0
if [ "${EVENT:-}" = "SubagentStop" ]; then
  IS_SUBAGENT=1
  [ "${AGENTSMEMORY_SUBAGENT_STOP_HOOK:-on}" = "off" ] && exit 0
fi

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
#
# A subagent stop neither READS nor WRITES this marker, and both halves of that
# matter. SubagentStop carries the PARENT session's session_id — observed, not
# assumed; the captured payload is in hooks_test.go — so the marker is one file
# shared by the main session and every subagent under it. Reading it means a
# session that already stopped once silences every subagent afterwards; writing it
# means the first subagent to finish silences the human's own checkpoint for the
# rest of the session. `once` is a statement about how often a HUMAN should be
# interrupted, and a subagent stops exactly once regardless.
if [ "$IS_SUBAGENT" -eq 0 ] && [ "$MODE" = "once" ]; then
  SID="$(printf '%s' "$INPUT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  MARKER="${TMPDIR:-/tmp}/agentsmemory-stop-${SID:-nosession}.done"
  if [ -n "${SID:-}" ] && [ -f "$MARKER" ]; then
    exit 0
  fi
  [ -n "${SID:-}" ] && : >"$MARKER" 2>/dev/null || true
fi

# A subagent is asked for what it FOUND, and then this hook is done: no session
# summary, and no recall report either. That report describes every session on the
# server in a window (see the long note at the bottom), and printing it into each
# branch of a fan-out is the same server-wide number repeated N times at the one
# audience that cannot act on it.
#
# The wing advice is deliberately NOT the advice the SubagentStart hook gives, and
# the difference is the whole reason it is stated. There a guessed wing costs a bad
# recall — confident, on-topic, irrelevant results. Here it costs a WRITE into
# another project's palace, which the protocol names as poisoning it. `wing` is
# optional on am_add_drawer and defaults to the wing this registration was created
# for, so passing none is both the safe answer and the correct one.
if [ "$IS_SUBAGENT" -eq 1 ]; then
  cat >&2 <<'SUBMSG'
agentsmemory — this subagent is stopping. Offer back what it found:
  1. am_add_drawer — the finding or decision, verbatim, into the right room
                     ("decisions", "incidents", …). Not a retelling of the task.
  2. am_kg_add     — anything durable, as subject -> predicate -> object.
Pass no wing unless you were given one: the registration already scopes the
write, and a guessed wing files this work into another project's palace.
Your dispatcher writes the session summary; you write the finding. Nothing worth
another session's time, or dispatched read-only to review? Write nothing and say
so in one line. AGENTSMEMORY_SUBAGENT_STOP_HOOK=off disables this.
SUBMSG
  exit 2
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
# The window is measured from the transcript file the event names rather than a
# fixed number of hours, because a fixed window at the first Stop of a session
# reports mostly the PREVIOUS session's work.
#
# It is NOT this session's recalls, and an earlier version of this comment said
# it was. search_events carries no session identity, so /stats filters by team and
# TIME — narrowing the window cannot separate sessions that overlap in it, and
# concurrent sessions against one local palace are the normal case, not the
# exception. The window bounds the report; it does not attribute it. See ADR-018.
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
  # The report's own first line claims "this session", which the server cannot
  # know. Say what it actually describes, so the numbers can be read at the scope
  # they were measured at instead of at the one the heading implies.
  REPORT="$(printf '%s\n' "$REPORT" | sed 's/^memory, this session:/memory, every session on this server in this window:/' || true)"
  # $(...) strips trailing newlines, so the report needs its last one back —
  # without it whatever the terminal prints next continues the report's last line.
  [ -n "$REPORT" ] && printf '\n%s\n' "$REPORT" >&2
  # The "memories to write" list is NOT printed, and its absence is the fix.
  #
  # It was the most useful thing this hook emitted: the questions a team asked and
  # could not answer are exactly the memories it should have written. But it is a
  # TASK LIST, not a statistic, and the server cannot say whose searches it is
  # built from — so each session was handed every other session's unanswered
  # questions under a heading that read as its own. Following it means filing a
  # memory about a question you never asked, into a wing you never opened, from no
  # evidence you hold. One session noticed and refused; the more diligent the
  # agent, the worse the outcome.
  #
  # It comes back the moment a recall can be attributed to the session that ran
  # it — ADR-018 T2 adds the column and the filter. A list that is right most of
  # the time is worse than none, because "most of the time" is not a property
  # anyone can check at the moment they read it.
fi

exit 2
