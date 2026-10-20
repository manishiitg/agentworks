#!/usr/bin/env bash
# One-off: convert a local (laptop) install to the Crew-only release.
# Run it with the local app stopped. It shows what will change, asks, backs up,
# converts, and removes "code" from the local runtime-config. Then start the app.
# Delete this file together with scripts/migrate-crew-product.py.
#
# Usage: scripts/migrate-local-crew.sh [--docs DIR] [--state DIR] [--yes]
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
docs="$repo/workspace-docs"
state=""
yes=""
while [ $# -gt 0 ]; do
  case "$1" in
    --docs) docs="$2"; shift 2 ;;
    --state) state="$2"; shift 2 ;;
    --yes) yes=1; shift ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done

# Same order as the server: AGENTWORKS_STATE_ROOT, RUNLOOP_USER_DATA_DIR/state,
# then the OS user config folder + AgentWorks/state.
if [ -z "$state" ]; then
  if [ -n "${AGENTWORKS_STATE_ROOT:-}" ]; then
    state="$AGENTWORKS_STATE_ROOT"
  elif [ -n "${RUNLOOP_USER_DATA_DIR:-}" ]; then
    state="$RUNLOOP_USER_DATA_DIR/state"
  elif [ "$(uname -s)" = "Darwin" ]; then
    state="$HOME/Library/Application Support/AgentWorks/state"
  elif [ -n "${APPDATA:-}" ]; then
    state="$APPDATA/AgentWorks/state"
  else
    state="${XDG_CONFIG_HOME:-$HOME/.config}/AgentWorks/state"
  fi
fi

python="$(command -v python3 || command -v python)"
tool="$repo/scripts/migrate-crew-product.py"

if [ ! -d "$docs" ]; then
  echo "No local data folder at $docs. Pass --docs with your app's docs folder." >&2
  exit 1
fi

for port in 18743 18744; do
  if curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$port/"; then
    echo "The local app is still running (port $port). Stop it first, then run this again." >&2
    exit 1
  fi
done

echo "Data:  $docs"
echo "State: $state"
echo
echo "Preview (nothing changes yet):"
"$python" -I "$tool" --docs "$docs" --state "$state"
echo

if [ -z "$yes" ]; then
  read -r -p "Convert now? A backup is made first. [y/N] " answer
  case "$answer" in y|Y|yes|YES) ;; *) echo "Nothing changed."; exit 0 ;; esac
fi

"$python" -I "$tool" --docs "$docs" --state "$state" --apply

config="$repo/frontend/public/runtime-config.js"
if [ -f "$config" ] && grep -q '"code"' "$config"; then
  cp "$config" "$config.before-crew"
  "$python" -I - "$config" <<'PY'
import re, sys
path = sys.argv[1]
text = open(path, encoding="utf-8").read()
text = re.sub(r'"code"\s*,\s*', "", text)
text = re.sub(r',\s*"code"(?=\s*\])', "", text)
open(path, "w", encoding="utf-8").write(text)
PY
  echo "Removed \"code\" from $config (copy kept as runtime-config.js.before-crew)."
fi

echo
echo "Done. Start the local app again. If some chat tabs are missing, log out and back in."
