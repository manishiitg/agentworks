#!/usr/bin/env bash
# Install the chrome-agentworks launcher beside the host's Chrome so every server starts its managed browser the same way
# (AGENT_BROWSER_EXECUTABLE_PATH=<app>/tools/chrome/current/chrome-agentworks, set by deploy/common/runtime_profile.json).
#
#   install-managed-chrome.sh /srv/<product>
#
# - <app>/tools/chrome/current already exists (SparkQuill/RTS: a pinned Chrome for Testing): the launcher is installed into the
#   directory `current` resolves to, next to that `chrome`.
# - otherwise, a system Chrome (/opt/google/chrome/chrome, the .deb) gets <app>/tools/chrome/system/{chrome -> that, chrome-agentworks}
#   and `current -> system`. The launcher runs the real `chrome` binary, never the /usr/bin/google-chrome shell script, which writes
#   under HOME (denied inside the shell sandbox).
# - no Chrome at all: prints a warning and exits 1 (the caller then leaves AGENT_BROWSER_EXECUTABLE_PATH unset).
# Safe to repeat. Prints the launcher path on success.
set -euo pipefail
app="${1:?usage: install-managed-chrome.sh /srv/<product>}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source_wrapper="$here/chrome-agentworks"
chrome_root="$app/tools/chrome"
current="$chrome_root/current"

if [[ -e "$current/chrome" ]]; then
  target="$(readlink -f "$current")"
elif [[ -x /opt/google/chrome/chrome ]]; then
  target="$chrome_root/system"
  mkdir -p "$target"
  ln -sfn /opt/google/chrome/chrome "$target/chrome"
  ln -sfn system "$current"
else
  echo "install-managed-chrome: no Chrome found ($current/chrome or /opt/google/chrome/chrome); browser stays on its default" >&2
  exit 1
fi

# Replace atomically: a running browser may be executing the old launcher.
tmp="$(mktemp "$target/.chrome-agentworks.XXXXXX")"
install -m 0755 "$source_wrapper" "$tmp"
mv -f "$tmp" "$target/chrome-agentworks"
echo "$current/chrome-agentworks"
