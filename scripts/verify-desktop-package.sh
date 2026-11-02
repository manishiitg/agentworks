#!/usr/bin/env bash
# PLAT-746-303: reject a mixed-CPU app, including nested Electron helpers/dylibs,
# then install its real checksum-verified DMG without touching the user's app.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARCH="${1:?usage: verify-desktop-package.sh arm64|x64}"
case "$ARCH" in arm64) CPU=arm64; PACK=mac-arm64 ;; x64) CPU=x86_64; PACK=mac ;; *) exit 1 ;; esac
APP="$ROOT/desktop/dist/$PACK/AgentWorks.app"
[ -d "$APP" ] || { echo "Missing packaged app: $APP" >&2; exit 1; }
while IFS= read -r -d '' binary; do
  if file -b "$binary" | grep -q 'Mach-O'; then
    lipo -verify_arch "$CPU" "$binary"
  fi
done < <(find "$APP" -type f -print0)
STAGE="$(mktemp -d -t agentworks-dmg-smoke)"
trap 'rm -rf "$STAGE"' EXIT
DMGS=("$ROOT"/desktop/dist/*-"$ARCH".dmg)
[ "${#DMGS[@]}" = 1 ] || { echo 'Expected one architecture-specific DMG' >&2; exit 1; }
bash "$ROOT/install.sh" --dmg "${DMGS[0]}" --sha256-file "${DMGS[0]}.sha256" --app-dir "$STAGE/Applications" --bin-dir "$STAGE/bin" --no-open
RESOURCES="$STAGE/Applications/AgentWorks.app/Contents/Resources"
"$STAGE/bin/agentworks" --version
"$RESOURCES/bin/tmux" -V
"$RESOURCES/bin/rsync" --version
"$RESOURCES/bin/mcpbridge" --help
"$RESOURCES/workspace-server" --help
"$RESOURCES/agent-server" --help
printf 'Verified %s packaged app and isolated DMG installation\n' "$ARCH"
