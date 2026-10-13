#!/usr/bin/env bash
# Print the lifecycle of one chat session from a server's agent log, in time order.
#
#   scripts/server-session-timeline.sh <session_id> <log_dir> [files_back]
#
# Run it on the server (for example through one SSM command). It reads the current agent.log and the most recent rotated
# files (plain or .gz), keeps only the lines that explain whether a turn ran, stalled or ended, and prints them with one
# normalised timestamp. It prints no message text: everything after query=, message=, prompt= or content= on a line is cut.
# The log directory is required (argument 2 or the AGENT_LOG_DIR variable); files_back defaults to 3.
#
# Kept: turn start/end and long-turn heartbeats, tool start/end, delivery of a message into the coding agent (accepted or
# stalled), CLI process watchdog, resume/relaunch, session-not-active replies, errors and cancellations.
set -euo pipefail

session="${1:-}"
logdir="${2:-${AGENT_LOG_DIR:-}}"
back="${3:-3}"
if [[ -z "$session" || -z "$logdir" ]]; then
  echo "usage: $0 <session_id> <log_dir> [files_back]   (or set AGENT_LOG_DIR)" >&2
  exit 2
fi
if [[ ! "$session" =~ ^[A-Za-z0-9._:-]+$ ]]; then
  echo "session id has unexpected characters" >&2
  exit 2
fi

# Newest first: agent.log, then rotated files by modification time.
files=()
while IFS= read -r f; do files+=("$f"); done < <({ ls -1t "$logdir"/agent.log "$logdir"/agent.log-* 2>/dev/null || true; } | head -n "$back")
if [[ ${#files[@]} -eq 0 ]]; then
  echo "no agent.log files in $logdir" >&2
  exit 1
fi

tags='\[STREAM\].*turn (started|ended)|\[TURN\]|\[TOOL\] (START|END|ERROR)|turn ended with .* open|\[QUERY->LIVE\]|\[DELIVERY_STALL\]|CODING_AGENT_DELIVERY|CODING_WATCHDOG|CHAT_HISTORY\]|UI-CONTROL\] session=[^ ]* http_status|AskWithHistory (starting|returned)|\[QUERY\] session|BG AGENT\]|panic|level=(error|warning)|cancel|timed out|timeout exceeded'

for f in "${files[@]}"; do
  if [[ "$f" == *.gz ]]; then
    gzip -dc "$f"
  else
    cat "$f"
  fi
done | grep -F -- "$session" | grep -E -- "$tags" | grep -vE 'Registered custom tool|Custom tool registered' |
  awk '
    {
      line = $0
      ts = ""
      if (match(line, /^[0-9]{4}\/[0-9]{2}\/[0-9]{2} [0-9:]{8}/)) {
        ts = substr(line, 1, 19)
        rest = substr(line, 21)
      } else if (match(line, /time="[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z"/)) {
        t = substr(line, RSTART + 6, 20)
        gsub(/-/, "/", t); sub(/T/, " ", t); sub(/Z$/, "", t)
        ts = substr(t, 1, 19)
        rest = line
        sub(/^time="[^"]*" level=[a-z]+ /, "", rest)
      } else {
        next
      }
      gsub(/\[username=[^]]*\]/, "", rest)
      # Message text can sit in any of these fields and may contain escaped quotes, so cut the rest of the line.
      n = split("query message prompt content", keys, " ")
      for (k = 1; k <= n; k++) {
        if (match(rest, keys[k] "=")) {
          rest = substr(rest, 1, RSTART - 1) keys[k] "=<omitted>"
        }
      }
      gsub(/(file|group|mode|user_id|username|user)="?[^ "]*"?( |$)/, "", rest)
      sub(/^ +/, "", rest)
      sub(/^msg="/, "", rest); sub(/" +/, " ", rest)
      print ts "\t" substr(rest, 1, 200)
    }' | sort -s -t"$(printf '\t')" -k1,1 | awk -F'\t' '!seen[$0]++ { print $1 "  " $2 }'
