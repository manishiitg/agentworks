#!/usr/bin/env bash
set -euo pipefail
JOB="$1"
export PATH="$HOME/.local/go/bin:$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"
[[ "$(uname -sm)" == "Linux x86_64" ]]
# Serialize complete deployments, including checkout, activation and pruning.
exec 9>"$HOME/video-studio/deploy.lock"
flock -n 9 || { echo 'Another deployment is already running.' >&2; exit 1; }
trap 'rm -rf "$JOB"' EXIT
# Prebuilt release (PLAT-426): shipped into $JOB/build by `./deploy.sh rts`; nothing to install, clone or compile. The activation
# script that runs is the one from the very revisions the build was made from.
if [[ -f "$JOB/prebuilt" ]]; then
  PREBUILT="$JOB/build"
  test -f "$PREBUILT/manifest.json" || { echo "Prebuilt release is missing from $JOB" >&2; exit 1; }
  bash "$PREBUILT/source/mcp-agent-builder-go/deploy/aws-ec2/server/build-and-activate.sh" "$PREBUILT/source" "$JOB/globals" \
    --prebuilt "$PREBUILT" --manifest-sha256 "$(cat "$JOB/prebuilt")"
  exit $?
fi

if ! command -v go >/dev/null; then
  echo 'Installing the pinned Go toolchain on the server'
  curl --fail --location --silent --show-error https://go.dev/dl/go1.27.1.linux-amd64.tar.gz -o "$JOB/go.tar.gz"
  curl --fail --location --silent --show-error 'https://go.dev/dl/?mode=json&include=all' -o "$JOB/go-releases.json"
  python3 - "$JOB" <<'PY'
import hashlib,json,pathlib,sys
p=pathlib.Path(sys.argv[1])
expected=next(f['sha256'] for v in json.loads((p/'go-releases.json').read_text()) for f in v['files'] if f['filename']=='go1.27.1.linux-amd64.tar.gz')
assert hashlib.sha256((p/'go.tar.gz').read_bytes()).hexdigest()==expected, 'Go checksum mismatch'
PY
  mkdir -p "$HOME/.local"
  tar -xzf "$JOB/go.tar.gz" -C "$HOME/.local"
  rm -f "$JOB/go.tar.gz" "$JOB/go-releases.json"
fi
mkdir -p "$JOB/source"
index=0
for repo in mcp-agent-builder-go mcpagent multi-llm-provider-go; do
  index=$((index+1))
  url="$(sed -n "${index}p" "$JOB/repos")"
  echo "Cloning $repo/main on $(hostname)"
  GIT_TERMINAL_PROMPT=0 git clone --quiet --depth 1 --single-branch --branch main "$url" "$JOB/source/$repo"
done
bash "$JOB/source/mcp-agent-builder-go/deploy/aws-ec2/server/build-and-activate.sh" "$JOB/source" "$JOB/globals"
