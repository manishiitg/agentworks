#!/usr/bin/env bash
# PLAT-746-303: reject a mixed-CPU app, including nested Electron helpers/dylibs,
# then install its real checksum-verified DMG without touching the user's app.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ARCH="${1:?usage: verify-desktop-package.sh arm64|x64}"
case "$ARCH" in arm64) CPU=arm64; PACK=mac-arm64 ;; x64) CPU=x86_64; PACK=mac ;; *) exit 1 ;; esac
APP="$ROOT/desktop/dist/$PACK/AgentWorks.app"
[ -d "$APP" ] || { echo "Missing packaged app: $APP" >&2; exit 1; }
MINIMUM="$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$APP/Contents/Info.plist")"
printf 'Packaged %s app requires macOS %s or later\n' "$ARCH" "$MINIMUM"
while IFS= read -r -d '' binary; do
  if file -b "$binary" | grep -q 'Mach-O'; then
    lipo "$binary" -verify_arch "$CPU"
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
[ -x "$RESOURCES/agentworks-vault" ] || { echo 'The packaged app has no Vault' >&2; exit 1; }
printf 'Vault: %s bytes\n' "$(stat -f %z "$RESOURCES/agentworks-vault")"
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
if otool -L "$RESOURCES/agent-server" | grep -E 'sherpa|onnxruntime'; then
  echo 'Basic server unexpectedly links optional voice libraries' >&2
  exit 1
fi
"$RESOURCES/agent-server" --help
node - "$ROOT" "$RESOURCES" "$(sw_vers -productVersion)" <<'JS'
const { selectPackagedAgent } = require(process.argv[2] + '/desktop/lib/voiceRuntime');
const { spawnSync } = require('node:child_process');
const selected = selectPackagedAgent(process.argv[3], process.argv[4]);
const metadata = require(process.argv[3] + '/voice/minimum-macos.json');
console.log('Optional voice requires macOS ' + metadata.minimumSystemVersion);
console.log('Host server selection: ' + selected.binary);
if (selected.voiceUnavailableReason) console.log(selected.voiceUnavailableReason);
const result = spawnSync(selected.binary, ['--help'], { stdio: 'inherit' });
if (result.error || result.status !== 0) process.exit(1);
JS
printf 'Verified %s packaged app and isolated DMG installation\n' "$ARCH"
