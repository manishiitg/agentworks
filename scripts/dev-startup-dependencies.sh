#!/usr/bin/env bash
# Local launcher setup only; sourcing this file starts nothing.
AGENT_BROWSER_VERSION=0.42.0

ensure_dev_agent_browser() {
    local installed target="$AGENT_BROWSER_VERSION"
    installed="$(agent-browser --version 2>/dev/null | awk '{print $NF}')"
    if [ "${UPDATE_MMX_CLI:-false}" = true ]; then
        target=latest
    elif [ "$installed" = "$target" ]; then
        dev_detail "✅ agent-browser $installed already installed"
        return 0
    fi
    echo "📦 Installing agent-browser@$target..."
    dev_setup npm install -g "agent-browser@$target" || return 1
    command -v agent-browser >/dev/null 2>&1 || return 1
}

dev_binary_digest() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

ensure_dev_mcpbridge() {
    local binary="$1" stamp="$1.stamp" module fingerprint
    module="$(GOWORK=off go list -m -f '{{.Path}} {{.Version}} {{if .Replace}}{{.Replace.Path}} {{.Replace.Version}}{{end}}' github.com/manishiitg/mcpagent)" || return 1
    if [ -x "$binary" ] && [ "${UPDATE_MMX_CLI:-false}" != true ]; then
        fingerprint="$module $(dev_binary_digest "$binary")"
        if [ -r "$stamp" ] && [ "$(cat "$stamp")" = "$fingerprint" ]; then
            dev_detail "✅ Pinned mcpbridge already built"
            return 0
        fi
    fi
    echo "🔨 Building the pinned mcpbridge dependency..."
    GOWORK=off go build -o "$binary" github.com/manishiitg/mcpagent/cmd/mcpbridge || return 1
    fingerprint="$module $(dev_binary_digest "$binary")" || return 1
    printf '%s\n' "$fingerprint" > "$stamp"
}

build_dev_playwright_fixtures() {
    python3 "$SCRIPT_DIR/../scripts/build-playwright-packages.py" "$AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR" || return 1
    local archive
    for archive in agentworks-playwright.tgz agentworks-playwright-python.zip; do
        [ -r "$AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR/$archive" ] || return 1
    done
}

start_deferred_dev_setup() {
    [ "${DEV_FIXTURES_PENDING:-false}" = true ] || return 0
    DEV_FIXTURES_PENDING=false
    dev_log_init || return 1
    (
        trap - EXIT INT TERM
        local ready=false attempt
        for attempt in $(seq 1 180); do
            if curl -fsS "${MCP_AGENT_SERVER_URL%/}/api/health" >/dev/null 2>&1; then ready=true; break; fi
            sleep 1
        done
        if [ "$ready" != true ]; then
            printf '[STARTUP_SETUP] time=%s playwright=failed reason=health_unavailable\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
            exit 1
        fi
        if build_dev_playwright_fixtures; then
            printf '[STARTUP_SETUP] time=%s playwright=completed\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        else
            printf '[STARTUP_SETUP] time=%s playwright=failed\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        fi
    ) >> "$DEV_STARTUP_LOG" 2>&1 < /dev/null &
}
