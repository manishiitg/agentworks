#!/usr/bin/env bash
# Run on the target Mac architecture, in CI or for a local package test.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/desktop/resources}"
mkdir -p "$OUT/bin" "$OUT/lib"
OUT="$(cd "$OUT" && pwd)"
VERSION="${AGENTWORKS_APP_VERSION:-$(node -p 'require(process.argv[1]).version' "$ROOT/desktop/package.json")}"
BUILD="$(git -C "$ROOT" rev-parse --short=12 HEAD)"
EPOCH="$(node -p 'require(process.argv[1]).compat_epoch' "$ROOT/deploy/cli-release.json")"
(
  cd "$ROOT/agent_go"
  CGO_ENABLED=0 go build -ldflags "-s -w -X main.cliVersion=$VERSION -X main.cliBuild=$BUILD -X main.cliCompatEpoch=$EPOCH" -o "$OUT/bin/agentworks" ./cmd/agentworks
  CGO_ENABLED=0 go build -o "$OUT/bin/mcpbridge" github.com/manishiitg/mcpagent/cmd/mcpbridge
)
bash "$ROOT/scripts/build-desktop-tmux.sh" "$OUT"
# The browser-control tool agents call by name (servers install the same version). The package carries a standalone
# native binary per platform (no Node needed to run it), so the Mac app ships the one for this build's CPU. Pinned by
# the registry's integrity hash; scripts never run.
AGENT_BROWSER_VERSION=0.38.2
AGENT_BROWSER_SHA512='b/7hvo2RrqocvybjB9kwpX12kGkxkFvtdJ4XPDoott5TLK2n3aPX+lYg7ALd+2YvPi2LrTKGzQYu/rxZC2Y7ZA=='
case "$(uname -m)" in arm64) AB_PLATFORM=darwin-arm64 ;; x86_64) AB_PLATFORM=darwin-x64 ;; *) echo "Unsupported CPU for agent-browser: $(uname -m)" >&2; exit 1 ;; esac
AB_STAGE="$(mktemp -d -t agentworks-agent-browser)"
(
  cd "$AB_STAGE"
  npm pack "agent-browser@$AGENT_BROWSER_VERSION" --ignore-scripts >/dev/null
  GOT="$(openssl dgst -sha512 -binary "agent-browser-$AGENT_BROWSER_VERSION.tgz" | base64 | tr -d '\n')"
  [ "$GOT" = "$AGENT_BROWSER_SHA512" ] || { echo "agent-browser package checksum mismatch" >&2; exit 1; }
  tar -xzf "agent-browser-$AGENT_BROWSER_VERSION.tgz" package/LICENSE "package/bin/agent-browser-$AB_PLATFORM"
)
install -m 755 "$AB_STAGE/package/bin/agent-browser-$AB_PLATFORM" "$OUT/bin/agent-browser"
mkdir -p "$OUT/licenses"
cp "$AB_STAGE/package/LICENSE" "$OUT/licenses/agent-browser-LICENSE"
rm -rf "$AB_STAGE"
cp "$ROOT/install.sh" "$OUT/install.sh"
"$OUT/bin/agentworks" --version
"$OUT/bin/agent-browser" --version
