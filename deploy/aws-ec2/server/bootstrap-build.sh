#!/usr/bin/env bash
set -euo pipefail
JOB="$1"
export PATH="$HOME/.local/go/bin:$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"
[[ "$(uname -sm)" == "Linux x86_64" ]]
# Serialize complete deployments, including checkout, activation and pruning.
exec 9>"$HOME/video-studio/deploy.lock"
flock -n 9 || { echo 'Another deployment is already running.' >&2; exit 1; }
trap 'rm -rf "$JOB"' EXIT
# Prebuilt release (PLAT-426): shipped into $JOB/build by `./deploy.sh <server>`; nothing to install, clone or compile. The activation
# script that runs is the one from the very revisions the build was made from.
[[ -f "$JOB/prebuilt" ]] || { echo "A prebuilt release job is required." >&2; exit 2; }
  PREBUILT="$JOB/build"
  test -f "$PREBUILT/manifest.json" || { echo "Prebuilt release is missing from $JOB" >&2; exit 1; }
  bash "$PREBUILT/source/mcp-agent-builder-go/deploy/aws-ec2/server/build-and-activate.sh" "$PREBUILT/source" "$JOB/globals" \
    --prebuilt "$PREBUILT" --manifest-sha256 "$(cat "$JOB/prebuilt")"
  exit $?
