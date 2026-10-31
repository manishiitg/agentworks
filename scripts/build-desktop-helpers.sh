#!/usr/bin/env bash
# Run on the target Mac architecture, in CI or for a local package test.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/desktop/resources}"
mkdir -p "$OUT/bin"
OUT="$(cd "$OUT" && pwd)"
VERSION="${AGENTWORKS_APP_VERSION:-$(node -p 'require(process.argv[1]).version' "$ROOT/desktop/package.json")}"
BUILD="$(git -C "$ROOT" rev-parse --short=12 HEAD)"
EPOCH="$(node -p 'require(process.argv[1]).compat_epoch' "$ROOT/deploy/cli-release.json")"
(
  cd "$ROOT/agent_go"
  CGO_ENABLED=0 go build -ldflags "-s -w -X main.cliVersion=$VERSION -X main.cliBuild=$BUILD -X main.cliCompatEpoch=$EPOCH" -o "$OUT/bin/agentworks" ./cmd/agentworks
  CGO_ENABLED=0 go build -o "$OUT/bin/mcpbridge" github.com/manishiitg/mcpagent/cmd/mcpbridge
)
python3 "$ROOT/scripts/bundle-desktop-tmux.py" "$OUT"
cp "$ROOT/install.sh" "$OUT/install.sh"
"$OUT/bin/agentworks" --version
