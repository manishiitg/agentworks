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
printf '[]' > "$STAGE/tools.json"
node - "$RESOURCES/bin/mcpbridge" "$STAGE/tools.json" <<'JS'
const { spawn } = require('node:child_process');
const bridge = spawn(process.argv[2], [], { env: { ...process.env,
  MCP_API_URL: 'http://127.0.0.1:1', MCP_API_TOKEN: 'installer-fixture',
  MCP_TOOLS_FILE: process.argv[3], MCP_SESSION_ID: 'installer-fixture',
}, stdio: ['pipe', 'pipe', 'inherit'] });
let output = '', initialized = false, listed = false;
const timer = setTimeout(() => { console.error('Bridge MCP handshake timed out'); bridge.kill(); process.exitCode = 1; }, 10000);
bridge.stdout.on('data', chunk => {
  output += chunk;
  let end;
  while ((end = output.indexOf('\n')) >= 0) {
    const line = output.slice(0, end); output = output.slice(end + 1);
    if (!line.trim()) continue;
    const response = JSON.parse(line);
    if (response.id === 1 && response.result?.serverInfo) initialized = true;
    if (response.id === 2 && Array.isArray(response.result?.tools)) listed = true;
    if (initialized && listed) { clearTimeout(timer); bridge.kill(); }
  }
});
bridge.on('error', error => { clearTimeout(timer); console.error(error); process.exitCode = 1; });
bridge.on('exit', () => { clearTimeout(timer); if (!initialized || !listed) process.exitCode = 1; });
for (const message of [
  { jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'installer-smoke', version: '1' } } },
  { jsonrpc: '2.0', method: 'notifications/initialized' },
  { jsonrpc: '2.0', id: 2, method: 'tools/list', params: {} },
]) bridge.stdin.write(JSON.stringify(message) + '\n');
JS
"$RESOURCES/workspace-server" --help
"$RESOURCES/agent-server" --help
printf 'Verified %s packaged app and isolated DMG installation\n' "$ARCH"
