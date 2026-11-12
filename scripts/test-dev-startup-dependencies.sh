#!/usr/bin/env bash
# PLAT-746-934: a warm launcher must not reinstall/build; pin/binary/update invalidate it.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d "$root/.startup-deps-test.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/bin"
export FIXTURE="$scratch"
export PATH="$scratch/bin:$PATH"
cat > "$scratch/bin/agent-browser" <<'STUB'
#!/bin/bash
printf 'agent-browser %s\n' "$(cat "$FIXTURE/browser-version")"
STUB
cat > "$scratch/bin/npm" <<'STUB'
#!/bin/bash
printf '%s\n' "$*" >> "$FIXTURE/npm-calls"
printf '0.42.0\n' > "$FIXTURE/browser-version"
STUB
cat > "$scratch/bin/go" <<'STUB'
#!/bin/bash
if [ "$1" = list ]; then cat "$FIXTURE/module"; exit; fi
printf 'build\n' >> "$FIXTURE/go-calls"
printf '#!/bin/bash\nexit 0\n' > "$3"
chmod +x "$3"
STUB
chmod +x "$scratch/bin/"*
printf '0.42.0\n' > "$scratch/browser-version"
printf 'github.com/manishiitg/mcpagent v1\n' > "$scratch/module"
source "$root/scripts/dev-startup-dependencies.sh"
dev_detail() { :; }
dev_setup() { "$@"; }
UPDATE_MMX_CLI=false
ensure_dev_agent_browser
[ ! -f "$scratch/npm-calls" ]
printf 'old\n' > "$scratch/browser-version"
ensure_dev_agent_browser
[ "$(wc -l < "$scratch/npm-calls" | tr -d ' ')" = 1 ]
ensure_dev_mcpbridge "$scratch/mcpbridge"
ensure_dev_mcpbridge "$scratch/mcpbridge"
[ "$(wc -l < "$scratch/go-calls" | tr -d ' ')" = 1 ]
printf '# changed\n' >> "$scratch/mcpbridge"
ensure_dev_mcpbridge "$scratch/mcpbridge"
printf 'github.com/manishiitg/mcpagent v2\n' > "$scratch/module"
ensure_dev_mcpbridge "$scratch/mcpbridge"
[ "$(wc -l < "$scratch/go-calls" | tr -d ' ')" = 3 ]
UPDATE_MMX_CLI=true
ensure_dev_agent_browser
ensure_dev_mcpbridge "$scratch/mcpbridge"
grep -q 'agent-browser@latest' "$scratch/npm-calls"
[ "$(wc -l < "$scratch/go-calls" | tr -d ' ')" = 4 ]
echo 'PASS: startup dependency pin/binary/update invalidation (stub tools only)'
# Deferred fixture setup must return before health and build only once ready.
DEV_FIXTURES_PENDING=true
DEV_STARTUP_LOG="$scratch/setup.log"
MCP_AGENT_SERVER_URL=http://example.com
curl() { [ -f "$scratch/healthy" ]; }
sleep() { command sleep 0.05; }
dev_log_init() { :; }
build_dev_playwright_fixtures() { echo built > "$scratch/fixtures-built"; }
start_deferred_dev_setup
worker=$!
[ ! -f "$scratch/fixtures-built" ]
touch "$scratch/healthy"
wait "$worker"
[ -f "$scratch/fixtures-built" ]
grep -q 'playwright=completed' "$DEV_STARTUP_LOG"
echo 'PASS: fixture setup is deferred until health (stub tools only)'

# PLAT-746-935: exercise the actual workspace launch function with a slow
# fake Go build. No real Go build, service, health request or app restart.
eval "$(sed -n '/^start_native_workspace() {/,/^}/p' "$root/agent_go/run_server_with_logging.sh")"
SCRIPT_DIR="$scratch/agent_go"
WORKSPACE_DIR="$scratch"
WORKSPACE_LOG_PATH="$scratch/workspace.log"
WORKSPACE_API_URL=http://example.com
WORKSPACE_PORT=12345
WORKSPACE_DOCS_PATH="$scratch/docs"
WITH_WORKSPACE=true
BACKGROUND_MODE=false
port_in_use() { return 1; }
wait_for_workspace_health() {
    [ -f "$scratch/build-finished" ] || { echo 'health budget began during build' >&2; return 1; }
    touch "$scratch/health-checked"
}
go() {
    [ ! -f "$scratch/health-checked" ] || return 1
    command sleep 0.05
    [ "${FAIL_BUILD:-false}" != true ] || return 1
    printf '#!/bin/bash\nexit 0\n' > "$3"
    chmod +x "$3"
    touch "$scratch/build-finished"
}
start_native_workspace
wait "$WORKSPACE_PID"
[ -f "$scratch/health-checked" ]
rm "$scratch/build-finished" "$scratch/health-checked"
FAIL_BUILD=true
if start_native_workspace; then echo 'failed build must fail launch' >&2; exit 1; fi
[ ! -f "$scratch/health-checked" ]
echo 'PASS: slow workspace build precedes health; failed build never waits for health'
