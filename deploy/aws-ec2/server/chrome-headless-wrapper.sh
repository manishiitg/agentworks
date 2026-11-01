#!/bin/sh
set -eu

wrapper_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
browser="$wrapper_dir/chrome-headless-shell"
test -x "$browser"

# The browser daemon outlives the sandboxed shell command that started it, and
# that command's scratch TMPDIR is deleted when it returns. Give Chromium a
# private, service-owned temporary root that survives between commands.
uid=$(id -u)
browser_tmp="/tmp/aw-browser-$uid"
if ! mkdir -m 700 "$browser_tmp" 2>/dev/null; then
  if [ -L "$browser_tmp" ] || [ ! -d "$browser_tmp" ] || [ "$(stat -c '%u:%a' "$browser_tmp")" != "$uid:700" ]; then
    echo "Unsafe managed browser temporary directory" >&2
    exit 1
  fi
fi
export TMPDIR="$browser_tmp" TMP="$browser_tmp" TEMP="$browser_tmp"

# Multi-process Chromium under the workspace's Landlock policy (same launcher
# as deploy/rootless-linux/chrome-agentworks).
# Landlock's /proc/self grant is bound to the launcher's PID, so a child's own
# /proc entries are unreadable: the zygote (proc_util.cc) and the separate GPU
# process (sandbox_linux.cc) abort on them. --no-zygote and --in-process-gpu
# avoid both without widening /proc. /dev/shm is not granted, so shared memory
# goes to TMPDIR. --single-process is gone: one renderer crash took the whole
# browser down ("CDP response channel closed"). Callers supply --no-sandbox.
exec "$browser" \
  --no-zygote \
  --in-process-gpu \
  --disable-crash-reporter \
  --disable-dev-shm-usage \
  --disable-gpu \
  "$@"
