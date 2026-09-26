#!/bin/bash
# Script to run the MCP agent server with logging enabled
# This makes it easier to debug event issues by capturing all output to a log file
# Terminal output is suppressed as requested.

# Get script directory first (needed for both test and server modes)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Capture WORKSPACE_DOCS_PATH from the caller's shell BEFORE any .env sourcing.
# .env files in this project commonly carry the Docker path (/app/workspace-docs),
# which must not leak into native mode — but a deliberate shell export is a
# legitimate override and must win. See docs/bugs/workspace_docs_path_inside_repo.md.
WORKSPACE_DOCS_PATH_FROM_SHELL="${WORKSPACE_DOCS_PATH:-}"

# When this script is launched from a non-login environment, system package
# managers may not be on PATH even though they are available in the user's
# normal terminal. Import the login-shell PATH once, then keep common host
# package locations as a fallback for native workspace shell commands.
import_login_shell_path() {
    local login_shell="${SHELL:-/bin/zsh}"
    local login_path=""
    if [ -x "$login_shell" ]; then
        login_path="$("$login_shell" -ilc 'printf "%s" "$PATH"' 2>/dev/null || true)"
    fi
    if [ -n "$login_path" ]; then
        PATH="$login_path:$PATH"
    fi
    local candidate
    for candidate in "$HOME/.local/bin" "$HOME/go/bin" "$HOME/.cargo/bin" "$HOME/.bun/bin" /opt/homebrew/bin /opt/homebrew/sbin /usr/local/bin /usr/local/sbin; do
        case ":$PATH:" in
            *":$candidate:"*) ;;
            *) PATH="$candidate:$PATH" ;;
        esac
    done
    export PATH
}

import_login_shell_path

TEST_CONNECTIONS=false
BACKGROUND_MODE=false
WITH_WORKSPACE=false
WITH_FRONTEND=false
ONLY_FRONTEND=false
WITH_GATEWAY=false
ONLY_GATEWAY=false
GATEWAY_EXPLICIT=false
UPDATE_MMX_CLI=false
FRONTEND_BUILD_MODE=false
WITHOUT_ELECTRON=false
ENABLE_CHAT_TERMINAL_DEBUGS=false
DEBUG_MEMORY=false
MCP_SERVER_API_TOKEN_ARG=""
MCP_SERVER_API_TOKEN_ARG_SET=false
EXPECT_MCP_SERVER_API_TOKEN_VALUE=false
POSITIONAL_ARGS=()

print_usage() {
    printf '%s\n' 'Usage: ./run_server_with_logging.sh [options]'
    printf '%s\n' ''
    printf '%s\n' 'Default (no composition flags): agent + workspace + frontend + gateway.'
    printf '%s\n' ''
    printf '%s\n' 'Options:'
    printf '%s\n' '  --with-workspace              Start the local workspace service.'
    printf '%s\n' '  --with-frontend               Start the frontend and Electron app.'
    printf '%s\n' '  --only-frontend               Start only the frontend and Electron app.'
    printf '%s\n' '  --with-gateway                Start the local MCP Gateway service.'
    printf '%s\n' '  --only-gateway                Start only the MCP Gateway service.'
    printf '%s\n' '  --build                       Build and serve the frontend (use with --only-frontend).'
    printf '%s\n' '  --without-electron            Do not launch Electron.'
    printf '%s\n' '  --debug-memory                Electron with heap-snapshot port + RSS sampler.'
    printf '%s\n' '  --enable-chat-terminal-debugs Enable the developer terminal/step diagnostics rail.'
    printf '%s\n' '  --background, -b              Run services in the background.'
    printf '%s\n' '  --test-connections, -t [file] Test an MCP config file.'
    printf '%s\n' '  --mcp-api-token <token>       Set the MCP server API token for this run.'
    printf '%s\n' '  --help, -h                    Show this help message.'
}

for arg in "$@"; do
    if [ "$EXPECT_MCP_SERVER_API_TOKEN_VALUE" = true ]; then
        MCP_SERVER_API_TOKEN_ARG="$arg"
        MCP_SERVER_API_TOKEN_ARG_SET=true
        EXPECT_MCP_SERVER_API_TOKEN_VALUE=false
        continue
    fi

    case "$arg" in
        --test-connections|--test-mcp|-t)
            TEST_CONNECTIONS=true
            ;;
        --background|-b)
            BACKGROUND_MODE=true
            ;;
        --with-workspace)
            WITH_WORKSPACE=true
            ;;
        --with-frontend)
            WITH_FRONTEND=true
            ;;
        --only-frontend)
            ONLY_FRONTEND=true
            ;;
        --with-gateway)
            WITH_GATEWAY=true
            GATEWAY_EXPLICIT=true
            ;;
        --only-gateway)
            ONLY_GATEWAY=true
            WITH_GATEWAY=true
            GATEWAY_EXPLICIT=true
            ;;
        --build)
            FRONTEND_BUILD_MODE=true
            ;;
        --without-electron)
            WITHOUT_ELECTRON=true
            ;;
        --debug-memory)
            DEBUG_MEMORY=true
            ;;
        --enable-chat-terminal-debugs)
            ENABLE_CHAT_TERMINAL_DEBUGS=true
            ;;
        --update)
            UPDATE_MMX_CLI=true
            ;;
        --mcp-api-token)
            EXPECT_MCP_SERVER_API_TOKEN_VALUE=true
            ;;
        --mcp-api-token=*)
            MCP_SERVER_API_TOKEN_ARG="${arg#--mcp-api-token=}"
            MCP_SERVER_API_TOKEN_ARG_SET=true
            ;;
        --help|-h)
            print_usage
            exit 0
            ;;
        *)
            POSITIONAL_ARGS+=("$arg")
            ;;
    esac
done

# Default composition: unless the caller picked a stack shape explicitly
# (--with-workspace / --with-frontend / --only-frontend / --with-gateway /
# --only-gateway / --test-connections), run the whole local stack (agent +
# workspace + frontend + gateway). Bare modifiers (--background,
# --without-electron, ...) keep the default.
if [ "$WITH_WORKSPACE" != true ] && [ "$WITH_FRONTEND" != true ] && [ "$ONLY_FRONTEND" != true ] && [ "$WITH_GATEWAY" != true ] && [ "$ONLY_GATEWAY" != true ] && [ "$TEST_CONNECTIONS" != true ]; then
    WITH_WORKSPACE=true
    WITH_FRONTEND=true
    WITH_GATEWAY=true
fi

# Only connection-test mode accepts a positional MCP configuration path. Treat
# anything else as an error: a typo such as --with-frontendclear must not be
# silently ignored and start a confusing partial local stack.
if [ "${#POSITIONAL_ARGS[@]}" -gt 0 ] && [ "$TEST_CONNECTIONS" != true ]; then
    echo "❌ Error: unknown option or argument: ${POSITIONAL_ARGS[0]}"
    echo "   Run ./run_server_with_logging.sh --help to see supported options."
    exit 2
fi

if [ "$EXPECT_MCP_SERVER_API_TOKEN_VALUE" = true ]; then
    echo "❌ Error: --mcp-api-token requires a token value"
    exit 1
fi

if [ "$MCP_SERVER_API_TOKEN_ARG_SET" = true ] && [ -z "$MCP_SERVER_API_TOKEN_ARG" ]; then
    echo "❌ Error: --mcp-api-token requires a non-empty token value"
    exit 1
fi

if [ "$MCP_SERVER_API_TOKEN_ARG_SET" = true ]; then
    export MCP_SERVER_API_TOKEN="$MCP_SERVER_API_TOKEN_ARG"
fi

# Terminal panes, child-agent rails, and execution trees are engineering
# diagnostics. The command-line flag is their sole control; the resolved
# environment is applied after loading .env below.
if [ "$ENABLE_CHAT_TERMINAL_DEBUGS" = true ]; then
    echo "🔬 Chat terminal diagnostics enabled for this run"
fi

# Every server launched by this script owns an isolated tmux socket. Coding
# agent session names are process-global otherwise, and shutdown cleanup from
# one dev/test server can kill sessions belonging to the desktop app or a
# second server. TMUX_TMPDIR is inherited by the Go server and all CLI adapter
# subprocesses, so ordinary tmux commands continue to work without extra flags.
if [ -z "${TMUX_TMPDIR:-}" ]; then
    TMUX_TMPDIR="${TMPDIR:-/tmp}/agentworks-tmux-$$"
    AGENTWORKS_AUTO_TMUX_TMPDIR=true
else
    AGENTWORKS_AUTO_TMUX_TMPDIR=false
fi
mkdir -p "$TMUX_TMPDIR"
chmod 700 "$TMUX_TMPDIR"
export TMUX_TMPDIR

FRONTEND_PORT_EXPLICIT="${FRONTEND_PORT:-}"
LOCALHOST_BASE_URL="${LOCALHOST_BASE_URL:-http://127.0.0.1}"
FRONTEND_HOST="${FRONTEND_HOST:-}"
FRONTEND_BIND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_URL_HOST="${FRONTEND_URL_HOST:-127.0.0.1}"

port_in_use() {
    lsof -nP -iTCP:"$1" -sTCP:LISTEN > /dev/null 2>&1
}

json_escape_runtime_value() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%s' "$value"
}

print_port_status() {
    local port="$1"
    local label="$2"

    if [ -z "$port" ]; then
        return 0
    fi

    if port_in_use "$port"; then
        echo "⚠️  Port $port ($label) is still in use"
        lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | sed 's/^/   /'
    else
        echo "✅ Port $port ($label) is free"
    fi
}

print_stop_target() {
    local label="$1"
    local pid="$2"
    local port="$3"

    if [ -n "$port" ]; then
        echo "🛑 Stopping $label (PID: $pid, port: $port)..."
    else
        echo "🛑 Stopping $label (PID: $pid)..."
    fi
}

kill_process_on_port() {
    local port="$1"
    local label="${2:-process on port $port}"
    local grace_attempts="${3:-50}"

    if [ "${AGENTWORKS_STRICT_PROCESS_OWNERSHIP:-false}" = "true" ]; then
        if port_in_use "$port"; then
            echo "⚠️  Strict process ownership: refusing to kill unrecorded $label on port $port"
        fi
        return 0
    fi

    local pid
    pid="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1)"
    if [ -n "$pid" ]; then
        echo "⚠️  $label still listening on port $port (PID: $pid); killing..."
        kill_process_tree "$pid" "$label" "$grace_attempts"
    fi
}

kill_process_tree() {
    local root_pid="$1"
    local label="${2:-process}"
    local grace_attempts="${3:-20}"

    if [ -z "$root_pid" ] || ! kill -0 "$root_pid" 2>/dev/null; then
        return 0
    fi

    local child_pids
    child_pids="$(pgrep -P "$root_pid" 2>/dev/null || true)"
    for child_pid in $child_pids; do
        kill_process_tree "$child_pid" "$label child"
    done

    kill "$root_pid" 2>/dev/null || true

    local attempt
    for attempt in $(seq 1 "$grace_attempts"); do
        if ! kill -0 "$root_pid" 2>/dev/null; then
            return 0
        fi
        sleep 0.1
    done

    echo "⚠️  $label (PID: $root_pid) did not stop after SIGTERM; forcing stop..."
    kill -9 "$root_pid" 2>/dev/null || true
}

cleanup_coding_agent_tmux_sessions() {
    command -v tmux >/dev/null 2>&1 || return 0

    local sessions
    sessions="$(tmux list-sessions -F '#{session_name}' 2>/dev/null || true)"
    [ -n "$sessions" ] || return 0

    local session
    local count=0
    while IFS= read -r session; do
        case "$session" in
            mlp-claude-code-*|mlp-codex-cli-*|mlp-cursor-cli-*|mlp-agy-cli-*|mlp-pi-cli-*|mlp-muse-*)
                tmux kill-session -t "$session" 2>/dev/null || true
                count=$((count + 1))
                ;;
        esac
    done <<EOF
$sessions
EOF

    if [ "$count" -gt 0 ]; then
        echo "🧹 Cleaned up $count coding-agent tmux session(s) from $TMUX_TMPDIR"
    fi
}

choose_frontend_port() {
    local preferred="${1:-51733}"
    local port="$preferred"

    if [ -n "$FRONTEND_PORT_EXPLICIT" ]; then
        echo "$preferred"
        return 0
    fi

    while port_in_use "$port"; do
        port=$((port + 1))
        if [ "$port" -gt 51999 ]; then
            return 1
        fi
    done

    echo "$port"
}

if [ "$TEST_CONNECTIONS" = true ]; then
    TEST_CONNECTIONS=true
    echo "🔌 Testing MCP Server Connections"
    echo "========================================="
    
    # Change to script directory
    cd "$SCRIPT_DIR" || {
        echo "❌ Error: Failed to change to script directory: $SCRIPT_DIR"
        exit 1
    }
    
    # Source environment variables from .env file if it exists
    if [ -f "../agent_go/.env" ]; then
        echo "🔧 Loading environment variables from ../agent_go/.env..."
        source ../agent_go/.env
    elif [ -f ".env" ]; then
        echo "🔧 Loading environment variables from .env..."
        source .env
    fi
    
    # Get config file path (default or from second argument)
    MCP_CONFIG="${POSITIONAL_ARGS[0]:-configs/mcp_servers_clean.json}"
    
    # Verify main.go exists
    if [ ! -f "main.go" ]; then
        echo "❌ Error: main.go not found in current directory: $(pwd)"
        exit 1
    fi
    
    # Verify go is available
    if ! command -v go &> /dev/null; then
        echo "❌ Error: 'go' command not found. Please install Go."
        exit 1
    fi
    
    # Run the test-all command
    echo "🚀 Running MCP connection tests..."
    go run main.go mcp test-all --config "$MCP_CONFIG" >> "logs/server_debug.log" 2>&1
    exit $?
fi

if [ "$ONLY_GATEWAY" = true ]; then
    echo "🔌 Starting MCP Gateway only — no agent, workspace, or frontend"
    echo "========================================="

    cd "$SCRIPT_DIR" || {
        echo "❌ Error: Failed to change to script directory: $SCRIPT_DIR"
        exit 1
    }

    GATEWAY_DIR="${SCRIPT_DIR}/../mcp-gateway"
    if [ ! -f "${GATEWAY_DIR}/go.mod" ]; then
        echo "❌ Error: gateway module not found: ${GATEWAY_DIR}/go.mod"
        exit 1
    fi
    GATEWAY_PORT="${GATEWAY_PORT:-18745}"
    if port_in_use "$GATEWAY_PORT"; then
        echo "❌ Error: Port $GATEWAY_PORT is already in use."
        echo "   Stop the existing process or set GATEWAY_PORT to another value."
        exit 1
    fi
    LOG_DIR="${AGENTWORKS_LOG_DIR:-logs}"
    mkdir -p "$LOG_DIR"
    GATEWAY_LOG_PATH="${LOG_DIR}/gateway_debug.log"
    export GATEWAY_PORT
    export GATEWAY_PUBLIC_URL="${GATEWAY_PUBLIC_URL:-http://127.0.0.1:${GATEWAY_PORT}}"
    export GATEWAY_HUMAN_TOKEN="${GATEWAY_HUMAN_TOKEN:-local-admin}"
    export GATEWAY_DEMO="${GATEWAY_DEMO:-}"
    export GATEWAY_STATE_DIR="${GATEWAY_STATE_DIR:-${GATEWAY_DIR}/var}"
    export GATEWAY_GRANT_TOOLS="${GATEWAY_GRANT_TOOLS:-resolve-library-id}"

    echo "🚀 Starting MCP Gateway..."
    echo "📝 Gateway log file: $GATEWAY_LOG_PATH"
    echo "🌐 Gateway MCP URL: ${GATEWAY_PUBLIC_URL}/mcp"
    echo "🔑 Gateway admin token: $GATEWAY_HUMAN_TOKEN"
    echo "🚀 Gateway Session Started: $(date)" > "$GATEWAY_LOG_PATH"
    if [ "$BACKGROUND_MODE" = true ]; then
        nohup bash -lc "cd \"$GATEWAY_DIR\" && exec go run ./cmd/server" >> "$GATEWAY_LOG_PATH" 2>&1 &
    else
        (
            cd "$GATEWAY_DIR" || exit 1
            exec go run ./cmd/server
        ) >> "$GATEWAY_LOG_PATH" 2>&1 &
    fi
    GATEWAY_PID=$!
    for _attempt in $(seq 1 60); do
        if curl -fsS "${GATEWAY_PUBLIC_URL}/healthz" >/dev/null 2>&1; then
            echo "✅ MCP Gateway is healthy at: ${GATEWAY_PUBLIC_URL}/mcp"
            break
        fi
        if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
            echo "❌ Error: Gateway exited during startup. Check logs: $GATEWAY_LOG_PATH"
            tail -20 "$GATEWAY_LOG_PATH"
            exit 1
        fi
        sleep 1
    done
    if ! curl -fsS "${GATEWAY_PUBLIC_URL}/healthz" >/dev/null 2>&1; then
        echo "❌ Error: Gateway did not become healthy in time. Check logs: $GATEWAY_LOG_PATH"
        tail -20 "$GATEWAY_LOG_PATH"
        exit 1
    fi

    cleanup_gateway_only() {
        if [ "$BACKGROUND_MODE" != true ]; then
            if [ -n "$GATEWAY_PID" ] && kill -0 "$GATEWAY_PID" 2>/dev/null; then
                print_stop_target "MCP Gateway" "$GATEWAY_PID" "$GATEWAY_PORT"
                kill_process_tree "$GATEWAY_PID" "MCP Gateway"
                wait "$GATEWAY_PID" 2>/dev/null
                print_port_status "$GATEWAY_PORT" "gateway"
            fi
        fi
    }
    trap cleanup_gateway_only EXIT
    trap "exit 130" INT TERM

    if [ "$BACKGROUND_MODE" = true ]; then
        echo ""
        echo "✅ MCP Gateway running in background (PID: $GATEWAY_PID)"
        echo "🛑 To stop: kill $GATEWAY_PID"
        exit 0
    fi
    echo ""
    echo "✅ MCP Gateway running (foreground). Press Ctrl+C to stop."
    while true; do
        if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
            echo "❌ MCP Gateway exited. Check logs: $GATEWAY_LOG_PATH"
            tail -20 "$GATEWAY_LOG_PATH"
            exit 1
        fi
        sleep 1
    done
    exit 0
fi

if [ "$ONLY_FRONTEND" = true ]; then
    if [ "$FRONTEND_BUILD_MODE" = true ]; then
        echo "🎨 Starting frontend only (static build + Electron) — no backend, no workspace"
    else
        echo "🎨 Starting frontend only (Vite + Electron) — no backend, no workspace"
    fi
    echo "========================================="

    cd "$SCRIPT_DIR" || {
        echo "❌ Error: Failed to change to script directory: $SCRIPT_DIR"
        exit 1
    }

    FRONTEND_PORT="$(choose_frontend_port "${FRONTEND_PORT:-51733}")" || {
        echo "❌ Error: No free frontend port available in range 51733-51999"
        exit 1
    }
    if [ -z "$FRONTEND_PORT_EXPLICIT" ] && [ "$FRONTEND_PORT" != "51733" ]; then
        echo "🔎 Frontend port 51733 is busy; using $FRONTEND_PORT"
    fi
    FRONTEND_URL="http://${FRONTEND_URL_HOST}:${FRONTEND_PORT}"
    FRONTEND_DIR="${SCRIPT_DIR}/../frontend"
    DESKTOP_DIR="${SCRIPT_DIR}/../desktop"
    ELECTRON_BIN="${DESKTOP_DIR}/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"
    FRONTEND_RUNTIME_CONFIG_PATH="${AGENTWORKS_RUNTIME_CONFIG_PATH:-${SCRIPT_DIR}/../frontend/public/runtime-config.js}"

    # Fallback chain for AGENT_PORT / WORKSPACE_PORT (when not explicitly set):
    #   1. running backend process (most accurate — survives stale config)
    #   2. runtime-config.js from a previous backend run
    # The process scan reads --port from the actual go binary's argv, so when
    # the user starts the backend on a different port than last time, the
    # frontend won't follow a stale runtime-config.js into a dead port.
    # Several checkouts can run backends at once (P0 gate worktrees, sibling
    # worktrees). Only a backend started from THIS checkout counts; picking the
    # first one in ps order connected the UI to another checkout's workflows.
    detect_checkout_server_port() {
        local pattern="$1" pid cwd
        for pid in $(pgrep -f "$pattern" 2>/dev/null); do
            cwd="$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p')"
            case "$cwd" in
                "$CHECKOUT_ROOT"|"$CHECKOUT_ROOT"/*)
                    ps -o args= -p "$pid" 2>/dev/null | grep -oE -- '--port[[:space:]]+[0-9]+' | awk '{print $2}' | head -1
                    return 0
                    ;;
            esac
        done
    }
    CHECKOUT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
    if [ -z "${AGENT_PORT:-}" ]; then
        detected_agent_port="$(detect_checkout_server_port '/main server')"
        if [ -n "$detected_agent_port" ]; then
            AGENT_PORT="$detected_agent_port"
            echo "🔎 Detected AGENT_PORT=$AGENT_PORT from running backend process"
        fi
    fi
    if [ -z "${WORKSPACE_PORT:-}" ]; then
        detected_workspace_port="$(detect_checkout_server_port '/workspace server')"
        if [ -n "$detected_workspace_port" ]; then
            WORKSPACE_PORT="$detected_workspace_port"
            echo "🔎 Detected WORKSPACE_PORT=$WORKSPACE_PORT from running workspace process"
        fi
    fi

    # Secondary fallback: read ports from runtime-config.js written by a
    # previous backend run. Used when no live backend process matches above.
    if [ -z "${AGENT_PORT:-}" ] && [ -f "$FRONTEND_RUNTIME_CONFIG_PATH" ]; then
        detected_agent_port="$(grep -oE 'apiBaseUrl:[[:space:]]*"http://[^"]+"' "$FRONTEND_RUNTIME_CONFIG_PATH" | grep -oE '[0-9]+"' | tr -d '"' | head -1)"
        if [ -n "$detected_agent_port" ]; then
            AGENT_PORT="$detected_agent_port"
            echo "🔎 Detected AGENT_PORT=$AGENT_PORT from existing runtime-config.js"
        fi
    fi
    if [ -z "${WORKSPACE_PORT:-}" ] && [ -f "$FRONTEND_RUNTIME_CONFIG_PATH" ]; then
        detected_workspace_port="$(grep -oE 'workspaceApiBaseUrl:[[:space:]]*"http://[^"]+"' "$FRONTEND_RUNTIME_CONFIG_PATH" | grep -oE '[0-9]+"' | tr -d '"' | head -1)"
        if [ -n "$detected_workspace_port" ]; then
            WORKSPACE_PORT="$detected_workspace_port"
            echo "🔎 Detected WORKSPACE_PORT=$WORKSPACE_PORT from existing runtime-config.js"
        fi
    fi

    if [ -z "${AGENT_PORT:-}" ]; then
        echo "❌ Error: AGENT_PORT is not set and could not be detected from $FRONTEND_RUNTIME_CONFIG_PATH"
        echo "   Either start the backend first (it writes the runtime config), or pass AGENT_PORT explicitly."
        echo "   Example: AGENT_PORT=18080 ./run_server_with_logging.sh --only-frontend"
        exit 1
    fi

    WORKSPACE_PORT="${WORKSPACE_PORT:-8081}"

    export MCP_AGENT_SERVER_URL="${LOCALHOST_BASE_URL}:${AGENT_PORT}"
    export WORKSPACE_API_URL="${LOCALHOST_BASE_URL}:${WORKSPACE_PORT}"

    # Sanity-check the backend is actually reachable on that port.
    if ! curl -fsS "${MCP_AGENT_SERVER_URL}/api/health" >/dev/null 2>&1; then
        echo "⚠️  Warning: backend at $MCP_AGENT_SERVER_URL did not respond to /api/health."
        echo "   Make sure the backend is running before the frontend tries to call it."
    fi

    echo "🔧 Backend (expected running): $MCP_AGENT_SERVER_URL"
    echo "🔧 Workspace (expected running): $WORKSPACE_API_URL"
    if [ "$FRONTEND_BUILD_MODE" = true ]; then
        echo "🔧 Static frontend URL: $FRONTEND_URL"
    else
        echo "🔧 Vite URL: $FRONTEND_URL"
    fi

    LOG_DIR="${AGENTWORKS_LOG_DIR:-logs}"
    RUNTIME_APP_NAME="$(json_escape_runtime_value "${AGENTWORKS_APP_NAME:-AgentWorks}")"
    RUNTIME_FAVICON_URL="$(json_escape_runtime_value "${AGENTWORKS_FAVICON_URL:-/logo.svg}")"
    # A frontend-only restart must not drop the gateway switcher entry while
    # the gateway (started with the backend) is still up. The backend falls
    # back to a random port in 18100-18199 when the default is busy, so when
    # the default probe misses (and no explicit GATEWAY_PORT pins the port),
    # scan that range for a live gateway instead of dropping the entry.
    ONLY_FRONTEND_GATEWAY_LINE=""
    ONLY_FRONTEND_GATEWAY_PROBE="${GATEWAY_PORT:-18745}"
    if ! curl -fsS --max-time 2 "http://127.0.0.1:${ONLY_FRONTEND_GATEWAY_PROBE}/healthz" >/dev/null 2>&1; then
        if [ -n "${GATEWAY_PORT:-}" ]; then
            ONLY_FRONTEND_GATEWAY_PROBE=""
        else
            ONLY_FRONTEND_GATEWAY_PROBE=""
            for probe_port in {18100..18199}; do
                if curl -fsS --max-time 1 "http://127.0.0.1:${probe_port}/healthz" >/dev/null 2>&1; then
                    ONLY_FRONTEND_GATEWAY_PROBE="$probe_port"
                    break
                fi
            done
        fi
    fi
    if [ -n "$ONLY_FRONTEND_GATEWAY_PROBE" ]; then
        ONLY_FRONTEND_GATEWAY_LINE=",
  gatewayUrl: \"http://127.0.0.1:${ONLY_FRONTEND_GATEWAY_PROBE}\""
    fi
    mkdir -p "$LOG_DIR"
    mkdir -p "$(dirname "$FRONTEND_RUNTIME_CONFIG_PATH")"
    cat > "$FRONTEND_RUNTIME_CONFIG_PATH" <<EOF
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "${MCP_AGENT_SERVER_URL}",
  workspaceApiBaseUrl: "${WORKSPACE_API_URL}",
  cdpEnabled: true,
  appName: "${RUNTIME_APP_NAME}",
  faviconUrl: "${RUNTIME_FAVICON_URL}"${ONLY_FRONTEND_GATEWAY_LINE}
};
EOF
    echo "📝 Frontend runtime config written to: $FRONTEND_RUNTIME_CONFIG_PATH"

    FRONTEND_LOG_PATH="${LOG_DIR}/frontend_debug.log"
    ELECTRON_LOG_PATH="${LOG_DIR}/electron_debug.log"
    > "$FRONTEND_LOG_PATH"
    > "$ELECTRON_LOG_PATH"

    if [ ! -f "${FRONTEND_DIR}/package.json" ]; then
        echo "❌ Error: frontend package.json not found: ${FRONTEND_DIR}/package.json"
        exit 1
    fi
    # Always run `npm install` before serving the frontend. It is idempotent:
    # when node_modules already matches package-lock.json it just does a fast
    # check (~1-2s) and installs nothing; when a pull added a dependency (e.g.
    # @xterm/xterm) it installs only the missing/changed packages. This avoids
    # the Vite "Failed to resolve import" failure after a dependency was added,
    # without ever reinstalling everything.
    # Dev launches also pass --force below. npm can leave Vite's generated
    # dependency metadata internally consistent while a referenced chunk is
    # missing after a pull; forced optimization rebuilds that cache before the
    # browser can request an obsolete chunk.
    echo "📦 Ensuring frontend dependencies (npm install)..."
    (
        cd "$FRONTEND_DIR" || exit 1
        npm install
    ) >> "$FRONTEND_LOG_PATH" 2>&1 || {
        echo "❌ Error: frontend dependency install failed. See $FRONTEND_LOG_PATH"
        exit 1
    }
    if port_in_use "$FRONTEND_PORT"; then
        echo "❌ Error: Port $FRONTEND_PORT is already in use."
        if [ -n "$FRONTEND_PORT_EXPLICIT" ]; then
            echo "   FRONTEND_PORT was explicitly set; choose another value or stop the existing process."
        else
            echo "   Port became busy after selection; retry the command."
        fi
        exit 1
    fi

    if [ "$FRONTEND_BUILD_MODE" = true ]; then
        echo "🔨 Building frontend..."
        (
            cd "$FRONTEND_DIR" || exit 1
            npm run build
        ) >> "$FRONTEND_LOG_PATH" 2>&1 || {
            echo "❌ Error: Frontend build failed. Check logs: $FRONTEND_LOG_PATH"
            tail -30 "$FRONTEND_LOG_PATH"
            exit 1
        }
        echo "🚀 Vite Preview Session Started: $(date)" >> "$FRONTEND_LOG_PATH"
    else
        echo "🚀 Vite Dev Session Started: $(date)" > "$FRONTEND_LOG_PATH"
    fi
    if [ "$BACKGROUND_MODE" = true ]; then
        if [ "$FRONTEND_BUILD_MODE" = true ]; then
            nohup bash -lc "cd \"$FRONTEND_DIR\" && exec npm run preview -- --host \"$FRONTEND_BIND_HOST\" --port \"$FRONTEND_PORT\" --strictPort" >> "$FRONTEND_LOG_PATH" 2>&1 &
        elif [ -n "$FRONTEND_HOST" ]; then
            nohup bash -lc "cd \"$FRONTEND_DIR\" && exec npm run dev -- --host \"$FRONTEND_HOST\" --port \"$FRONTEND_PORT\" --strictPort --force" >> "$FRONTEND_LOG_PATH" 2>&1 &
        else
            nohup bash -lc "cd \"$FRONTEND_DIR\" && exec npm run dev -- --port \"$FRONTEND_PORT\" --strictPort --force" >> "$FRONTEND_LOG_PATH" 2>&1 &
        fi
    else
        (
            cd "$FRONTEND_DIR" || exit 1
            if [ "$FRONTEND_BUILD_MODE" = true ]; then
                exec npm run preview -- --host "$FRONTEND_BIND_HOST" --port "$FRONTEND_PORT" --strictPort
            elif [ -n "$FRONTEND_HOST" ]; then
                exec npm run dev -- --host "$FRONTEND_HOST" --port "$FRONTEND_PORT" --strictPort --force
            else
                exec npm run dev -- --port "$FRONTEND_PORT" --strictPort --force
            fi
        ) >> "$FRONTEND_LOG_PATH" 2>&1 &
    fi
    FRONTEND_PID=$!
    if [ "$FRONTEND_BUILD_MODE" = true ]; then
        echo "✅ Static frontend server started (PID: $FRONTEND_PID) — $FRONTEND_URL"
    else
        echo "✅ Vite dev server started (PID: $FRONTEND_PID) — $FRONTEND_URL"
    fi

    frontend_ready=false
    for attempt in $(seq 1 60); do
        if curl -fsS "$FRONTEND_URL" >/dev/null 2>&1; then
            frontend_ready=true
            break
        fi
        if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
            echo "❌ Error: Frontend server exited during startup. Check logs: $FRONTEND_LOG_PATH"
            tail -20 "$FRONTEND_LOG_PATH"
            exit 1
        fi
        sleep 1
    done
    if [ "$frontend_ready" != true ]; then
        echo "❌ Error: Frontend server did not become ready in time. Check logs: $FRONTEND_LOG_PATH"
        tail -20 "$FRONTEND_LOG_PATH"
        kill_process_tree "$FRONTEND_PID" "frontend server"
        exit 1
    fi

    if [ "$WITHOUT_ELECTRON" != true ] && [ ! -f "${DESKTOP_DIR}/package.json" ]; then
        echo "❌ Error: desktop package.json not found: ${DESKTOP_DIR}/package.json"
        kill_process_tree "$FRONTEND_PID" "frontend server"
        exit 1
    fi
    if [ "$WITHOUT_ELECTRON" != true ] && [ ! -x "$ELECTRON_BIN" ]; then
        echo "❌ Error: Electron binary not found or not executable: $ELECTRON_BIN"
        kill_process_tree "$FRONTEND_PID" "frontend server"
        print_port_status "$FRONTEND_PORT" "frontend"
        exit 1
    fi

    ELECTRON_PID=""
    if [ "$WITHOUT_ELECTRON" = true ]; then
        echo "🌐 Browser-only frontend requested; Electron will not be started"
    else
        echo "🚀 Electron Session Started: $(date)" > "$ELECTRON_LOG_PATH"
        ELECTRON_EXTRA_ARGS=""
        if [ "$DEBUG_MEMORY" = true ]; then
            # Not 9222: a Chrome started with remote debugging (e.g. for browser
            # automation) usually holds it, and Electron then silently has no
            # DevTools endpoint ("Cannot start http server for devtools").
            ELECTRON_DEBUG_PORT="${ELECTRON_DEBUG_PORT:-9333}"
            ELECTRON_EXTRA_ARGS="--remote-debugging-port=${ELECTRON_DEBUG_PORT} --js-flags=--expose-gc"
        fi
        if [ "$BACKGROUND_MODE" = true ]; then
            # shellcheck disable=SC2086
            nohup bash -lc "cd \"$DESKTOP_DIR\" && DEV_URL=\"$FRONTEND_URL\" exec \"$ELECTRON_BIN\" . $ELECTRON_EXTRA_ARGS" >> "$ELECTRON_LOG_PATH" 2>&1 &
        else
            (
                cd "$DESKTOP_DIR" || exit 1
                # shellcheck disable=SC2086
                DEV_URL="$FRONTEND_URL" exec "$ELECTRON_BIN" . $ELECTRON_EXTRA_ARGS
            ) >> "$ELECTRON_LOG_PATH" 2>&1 &
        fi
        ELECTRON_PID=$!
        echo "✅ Electron started (PID: $ELECTRON_PID)"
        if [ "$DEBUG_MEMORY" = true ]; then
            echo "🧠 Memory debug on: heap snapshots at http://127.0.0.1:${ELECTRON_DEBUG_PORT} (open in Chrome → inspect → Memory)"
            echo "🧠 RSS sampler logging to $ELECTRON_LOG_PATH every 30s"
            (
                while kill -0 "$ELECTRON_PID" 2>/dev/null; do
                    # RSS of the whole Electron tree in MB (macOS ps rss is KiB).
                    EPIDS=$(pgrep -f "electron/dist/Electron.app" 2>/dev/null | tr '\n' ',' | sed 's/,$//')
                    if [ -n "$EPIDS" ]; then
                        total_kb=$(ps -o rss= -p "$EPIDS" 2>/dev/null | awk '{s+=$1} END {print s+0}')
                    else
                        total_kb=0
                    fi
                    echo "[MEMDBG] $(date '+%H:%M:%S') electron_tree_rss_mb=$((total_kb / 1024))" >> "$ELECTRON_LOG_PATH"
                    sleep 30
                done
            ) &
        fi
        sleep 2
        if ! kill -0 "$ELECTRON_PID" 2>/dev/null; then
            echo "❌ Error: Electron exited immediately. Check logs: $ELECTRON_LOG_PATH"
            tail -30 "$ELECTRON_LOG_PATH"
            kill_process_tree "$FRONTEND_PID" "frontend server"
            print_port_status "$FRONTEND_PORT" "frontend"
            exit 1
        fi
    fi

    cleanup_frontend_only() {
        if [ "$BACKGROUND_MODE" != true ]; then
            if [ -n "$ELECTRON_PID" ] && kill -0 "$ELECTRON_PID" 2>/dev/null; then
                print_stop_target "Electron" "$ELECTRON_PID"
                kill_process_tree "$ELECTRON_PID" "Electron"
                wait "$ELECTRON_PID" 2>/dev/null
            fi
            if [ -n "$FRONTEND_PID" ] && kill -0 "$FRONTEND_PID" 2>/dev/null; then
                print_stop_target "frontend server" "$FRONTEND_PID" "$FRONTEND_PORT"
                kill_process_tree "$FRONTEND_PID" "frontend server"
                wait "$FRONTEND_PID" 2>/dev/null
                print_port_status "$FRONTEND_PORT" "frontend"
            fi
        fi
    }
    trap cleanup_frontend_only EXIT
    trap "exit 130" INT TERM

    if [ "$BACKGROUND_MODE" = true ]; then
        echo ""
        echo "✅ Frontend services running in background:"
        echo "   - Frontend server (PID: $FRONTEND_PID) — $FRONTEND_URL"
        [ -n "$ELECTRON_PID" ] && echo "   - Electron (PID: $ELECTRON_PID)"
        echo "   Logs: $FRONTEND_LOG_PATH (vite)${ELECTRON_PID:+, $ELECTRON_LOG_PATH (electron)}"
        echo "🛑 To stop: kill $FRONTEND_PID${ELECTRON_PID:+ $ELECTRON_PID}"
        exit 0
    fi

    echo ""
    echo "✅ Frontend services running (foreground):"
    echo "   - Frontend server (PID: $FRONTEND_PID) — $FRONTEND_URL"
    [ -n "$ELECTRON_PID" ] && echo "   - Electron (PID: $ELECTRON_PID)"
    echo "   Backend expected at: $MCP_AGENT_SERVER_URL"
    echo "   Press Ctrl+C to stop."
    echo ""
    while true; do
        if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
            echo "❌ Frontend server exited. Check logs: $FRONTEND_LOG_PATH"
            tail -20 "$FRONTEND_LOG_PATH"
            exit 1
        fi
        if [ -n "$ELECTRON_PID" ] && ! kill -0 "$ELECTRON_PID" 2>/dev/null; then
            echo "❌ Electron exited. Check logs: $ELECTRON_LOG_PATH"
            tail -30 "$ELECTRON_LOG_PATH"
            kill_process_tree "$FRONTEND_PID" "frontend server"
            print_port_status "$FRONTEND_PORT" "frontend"
            exit 1
        fi
        sleep 1
    done
    exit 0
fi

if [ "$BACKGROUND_MODE" = true ]; then
    BACKGROUND_MODE=true
    echo "🚀 Starting MCP Agent Server with Logging (Background Mode)"
else
    echo "🚀 Starting MCP Agent Server with Logging"
fi
echo "========================================="

find_random_free_port_in_range() {
    local start="$1"
    local end="$2"
    local exclude_csv="${3:-}"
    local attempts=50
    local range_size=$((end - start + 1))
    local attempt
    local port

    is_port_excluded() {
        local candidate="$1"
        if [ -z "$exclude_csv" ]; then
            return 1
        fi

        local old_ifs="$IFS"
        IFS=','
        for excluded in $exclude_csv; do
            if [ "$candidate" = "$excluded" ]; then
                IFS="$old_ifs"
                return 0
            fi
        done
        IFS="$old_ifs"
        return 1
    }

    for attempt in $(seq 1 "$attempts"); do
        port=$((start + RANDOM % range_size))
        if ! is_port_excluded "$port" && ! port_in_use "$port"; then
            echo "$port"
            return 0
        fi
    done

    for port in $(seq "$start" "$end"); do
        if ! is_port_excluded "$port" && ! port_in_use "$port"; then
            echo "$port"
            return 0
        fi
    done

    return 1
}

DEFAULT_AGENT_PORT=18743
DEFAULT_WORKSPACE_PORT=18744

choose_default_then_random_port() {
    local preferred="$1"
    local start="$2"
    local end="$3"
    local exclude_csv="${4:-}"

    if [ -z "$exclude_csv" ]; then
        if ! port_in_use "$preferred"; then
            echo "$preferred"
            return 0
        fi
    elif ! is_csv_value "$preferred" "$exclude_csv" && ! port_in_use "$preferred"; then
        echo "$preferred"
        return 0
    fi

    find_random_free_port_in_range "$start" "$end" "$exclude_csv"
}

is_csv_value() {
    local candidate="$1"
    local csv="$2"
    local old_ifs="$IFS"
    IFS=','
    for value in $csv; do
        if [ "$candidate" = "$value" ]; then
            IFS="$old_ifs"
            return 0
        fi
    done
    IFS="$old_ifs"
    return 1
}

# Gateway port first: the agent/workspace random fallback range (18000-19000)
# contains the gateway default, so they must exclude the chosen gateway port.
GATEWAY_PID=""
GATEWAY_LOG_PATH=""
GATEWAY_DIR="${SCRIPT_DIR}/../mcp-gateway"
DEFAULT_GATEWAY_PORT=18745
if [ "$WITH_GATEWAY" = true ]; then
    if [ -n "${GATEWAY_PORT:-}" ]; then
        echo "🔎 Using requested gateway port: $GATEWAY_PORT"
        if port_in_use "$GATEWAY_PORT"; then
            echo "❌ Error: Requested GATEWAY_PORT $GATEWAY_PORT is already in use"
            exit 1
        fi
    else
        echo "🔎 Selecting gateway port: default ${DEFAULT_GATEWAY_PORT}, random fallback in range 18100-18199..."
        GATEWAY_PORT="$(choose_default_then_random_port "$DEFAULT_GATEWAY_PORT" 18100 18199)"
        if [ -z "$GATEWAY_PORT" ]; then
            echo "❌ Error: No free gateway port available in range 18100-18199"
            exit 1
        fi
    fi
else
    GATEWAY_PORT="${GATEWAY_PORT:-18745}"
fi
export GATEWAY_PORT
export GATEWAY_PUBLIC_URL="${GATEWAY_PUBLIC_URL:-http://127.0.0.1:${GATEWAY_PORT}}"
export GATEWAY_HUMAN_TOKEN="${GATEWAY_HUMAN_TOKEN:-local-admin}"
export GATEWAY_DEMO="${GATEWAY_DEMO:-}"
export GATEWAY_STATE_DIR="${GATEWAY_STATE_DIR:-${GATEWAY_DIR}/var}"
export GATEWAY_GRANT_TOOLS="${GATEWAY_GRANT_TOOLS:-resolve-library-id}"

# Kill any orphaned agent server from a previous run on the default port so we can
# reuse it (avoids accumulating background servers on different random ports).
if [ -z "${AGENT_PORT:-}" ] && port_in_use "$DEFAULT_AGENT_PORT"; then
    echo "⚠️  Port $DEFAULT_AGENT_PORT busy — killing orphaned server from previous run..."
    kill_process_on_port "$DEFAULT_AGENT_PORT" "orphaned agent server" 50
    sleep 0.5
fi

if [ -n "${AGENT_PORT:-}" ]; then
    echo "🔎 Using requested agent server port: $AGENT_PORT"
    if port_in_use "$AGENT_PORT"; then
        echo "❌ Error: Requested AGENT_PORT $AGENT_PORT is already in use"
        exit 1
    fi
else
    echo "🔎 Selecting agent server port: default ${DEFAULT_AGENT_PORT}, random fallback in range 18000-19000..."
    AGENT_PORT="$(choose_default_then_random_port "$DEFAULT_AGENT_PORT" 18000 19000 "$GATEWAY_PORT")"
    if [ -z "$AGENT_PORT" ]; then
        echo "❌ Error: No free port available in range 18000-19000"
        exit 1
    fi
fi
export AGENT_PORT
export MCP_AGENT_SERVER_URL="${LOCALHOST_BASE_URL}:${AGENT_PORT}"
# Chat-driven OAuth actions do not have an incoming HTTP request from which to
# infer their callback origin. For local runs the selected agent-server URL is
# the correct callback base. Preserve an explicit PUBLIC_URL for hosted setups.
if [ -z "${PUBLIC_URL:-}" ]; then
    export PUBLIC_URL="$MCP_AGENT_SERVER_URL"
fi
echo "✅ Using agent server port: $AGENT_PORT"

generate_auth_secret() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
        return
    fi
    python3 - <<'PY'
import secrets
print(secrets.token_hex(32))
PY
}

ensure_local_auth_secret() {
    if [ -n "${AUTH_SECRET:-}" ] && [ "$AUTH_SECRET" != "dev-secret-change-in-production" ]; then
        return
    fi

    local target_env_file="${ENV_FILE_PATH:-.env}"
    local generated_secret
    generated_secret="$(generate_auth_secret)"
    if [ -z "$generated_secret" ]; then
        echo "❌ Error: failed to generate AUTH_SECRET"
        exit 1
    fi

    export AUTH_SECRET="$generated_secret"
    mkdir -p "$(dirname "$target_env_file")"

    if [ -f "$target_env_file" ]; then
        local tmp_env_file
        tmp_env_file="$(mktemp)"
        awk -v auth_line="AUTH_SECRET=${AUTH_SECRET}" '
            BEGIN { written = 0 }
            /^AUTH_SECRET=/ {
                if (!written) {
                    print auth_line
                    written = 1
                }
                next
            }
            { print }
            END {
                if (!written) {
                    print auth_line
                }
            }
        ' "$target_env_file" > "$tmp_env_file" && mv "$tmp_env_file" "$target_env_file"
    else
        {
            printf '%s\n' '# Local AgentWorks settings, generated on first launch.'
            printf '%s\n' '# Configure LLM provider credentials in the app, not in this file.'
            printf 'AUTH_SECRET=%s\n' "$AUTH_SECRET"
        } > "$target_env_file"
        LOCAL_ENV_CREATED=true
    fi

    chmod 600 "$target_env_file" 2>/dev/null || true
    echo "🔐 Generated local AUTH_SECRET in $target_env_file"
}

# Source an env file and export every assignment to child processes. The server,
# workspace, Electron, and provider CLIs all need the same resolved instance
# environment, including a persisted AUTH_SECRET on the second launch.
source_exported_env_file() {
    local env_file="$1"
    local restore_allexport=true
    case "$-" in
        *a*) restore_allexport=false ;;
    esac
    set -a
    source "$env_file"
    if [ "$restore_allexport" = true ]; then
        set +a
    fi
}

# Source environment variables from an explicit instance file when configured;
# otherwise preserve the historical repo-local discovery behavior.
ENV_FILE_PATH=""
LOCAL_ENV_CREATED=false
if [ -n "${AGENTWORKS_ENV_FILE:-}" ]; then
    ENV_FILE_PATH="$AGENTWORKS_ENV_FILE"
    if [ -f "$ENV_FILE_PATH" ]; then
        echo "🔧 Loading environment variables from isolated instance file: $ENV_FILE_PATH"
        source_exported_env_file "$ENV_FILE_PATH"
    else
        echo "🔧 Isolated instance environment will be initialized at: $ENV_FILE_PATH"
    fi
elif [ -f "../agent_go/.env" ]; then
    echo "🔧 Loading environment variables from ../agent_go/.env..."
    ENV_FILE_PATH="../agent_go/.env"
    source_exported_env_file ../agent_go/.env
    echo "✅ Environment variables loaded (including Langfuse configuration)"
elif [ -f ".env" ]; then
    echo "🔧 Loading environment variables from .env..."
    ENV_FILE_PATH=".env"
    source_exported_env_file .env
    echo "✅ Environment variables loaded (including Langfuse configuration)"
else
    echo "ℹ️  First local run: creating a minimal .env with a secure AUTH_SECRET."
fi

# Keep managed browser cookies and logins across local server restarts. Workflow
# isolation is added beneath this root by the browser launcher.
export AGENT_BROWSER_SHARED_PROFILE="${AGENT_BROWSER_SHARED_PROFILE:-$HOME/.agentworks/browser-profile}"

# Runtime diagnostics are controlled only by the explicit command-line switch.
# Pin both variables to 0 during ordinary product runs so a stale local .env
# setting cannot expose the child/step terminal rail unexpectedly. A plain
# unset is not enough: the server's godotenv.Load re-sets unset variables
# from .env at boot, while an explicitly set 0 is preserved (and reads off).
if [ "$ENABLE_CHAT_TERMINAL_DEBUGS" = true ]; then
    export AGENTWORKS_RUNTIME_DEBUG=1
    export VITE_RUNTIME_DEBUG=1
else
    export AGENTWORKS_RUNTIME_DEBUG=0
    export VITE_RUNTIME_DEBUG=0
fi

ensure_local_auth_secret

if [ "$LOCAL_ENV_CREATED" = true ]; then
    echo ""
    echo "✨ Local setup is ready."
    echo "   Your AUTH_SECRET was generated and saved to ${ENV_FILE_PATH:-.env}."
    echo "   To use an LLM, open the app and connect a provider in LLM Configuration."
    echo "   Do not copy env.example or add placeholder API keys."
    echo ""
fi

if [ "$MCP_SERVER_API_TOKEN_ARG_SET" = true ]; then
    export MCP_SERVER_API_TOKEN="$MCP_SERVER_API_TOKEN_ARG"
fi
if [ -n "${MCP_SERVER_API_TOKEN:-}" ]; then
    echo "🔐 MCP server API token override is configured for this server process"
fi

# Browser session limits (explicit export so child process inherits them)
export MAX_BROWSER_SESSIONS_PER_AGENT=1
export MAX_BROWSER_SESSIONS_PER_WORKFLOW=4
export MAX_BROWSER_SESSIONS_GLOBAL=8

# Set environment variables for the server
export LOG_LEVEL="debug"
# Use LOG_PATH for the shell script to redirect output
LOG_DIR="${AGENTWORKS_LOG_DIR:-logs}"
LOG_PATH="${LOG_DIR}/server_debug.log"
# Claude Code tmux panes are written here only when startup/prompt detection
# times out. They are useful for support, but may contain user content.
export CLAUDE_CODE_TMUX_DIAGNOSTICS_DIR="${LOG_DIR}/claude-tmux"
# Shared tmux-backed providers (Codex, Cursor, and Pi) write failure panes
# here. Like Claude diagnostics, these files can contain user content.
export TMUX_DIAGNOSTICS_DIR="${LOG_DIR}/tmux"
# Unset LOG_FILE to ensure the Go application logs to stdout (avoiding duplicates)
unset LOG_FILE

# Set MCP_GENERATED_DIR to point to agent_go/generated/
# This ensures code generation happens in the correct location
# (SCRIPT_DIR already set above for test-connections mode)
export MCP_GENERATED_DIR="${MCP_GENERATED_DIR:-${SCRIPT_DIR}/generated}"
echo "🔧 Set MCP_GENERATED_DIR to: $MCP_GENERATED_DIR"

# WORKSPACE_DOCS_PATH: absolute path to workspace-docs as seen by the workspace server.
# When workspace runs in Docker (default), this is /app/workspace-docs.
# Only override for desktop/native deployments where workspace runs on the host.
# export WORKSPACE_DOCS_PATH="/app/workspace-docs"  # default, no need to set

WORKSPACE_PID=""
WORKSPACE_LOG_PATH=""
WORKSPACE_DIR="${SCRIPT_DIR}/../workspace"
FRONTEND_RUNTIME_CONFIG_PATH="${AGENTWORKS_RUNTIME_CONFIG_PATH:-${SCRIPT_DIR}/../frontend/public/runtime-config.js}"

FRONTEND_PID=""
ELECTRON_PID=""
ELECTRON_MEMORY_MONITOR_PID=""
FRONTEND_LOG_PATH=""
ELECTRON_LOG_PATH=""
FRONTEND_DIR="${SCRIPT_DIR}/../frontend"
DESKTOP_DIR="${SCRIPT_DIR}/../desktop"
ELECTRON_BIN="${DESKTOP_DIR}/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron"
FRONTEND_PORT="$(choose_frontend_port "${FRONTEND_PORT:-51733}")" || {
    echo "❌ Error: No free frontend port available in range 51733-51999"
    exit 1
}
if [ -z "$FRONTEND_PORT_EXPLICIT" ] && [ "$FRONTEND_PORT" != "51733" ]; then
    echo "🔎 Frontend port 51733 is busy; using $FRONTEND_PORT"
fi
FRONTEND_URL="http://${FRONTEND_URL_HOST}:${FRONTEND_PORT}"

# Device approval needs the current React app. The API server's static bundle
# may be older during local development, so use Vite when it belongs to this
# agent server. A frontend started by this script comes up after the API.
if [ -z "${AGENTWORKS_CLI_BROWSER_URL:-}" ]; then
    if [ "$WITH_FRONTEND" = true ]; then
        export AGENTWORKS_CLI_BROWSER_URL="$FRONTEND_URL"
    else
        CLI_BROWSER_CANDIDATE="http://${FRONTEND_URL_HOST}:${FRONTEND_PORT_EXPLICIT:-51733}"
        if curl -fsS --max-time 2 "$CLI_BROWSER_CANDIDATE/runtime-config.js" 2>/dev/null | grep -Fq "apiBaseUrl: \"$MCP_AGENT_SERVER_URL\""; then
            export AGENTWORKS_CLI_BROWSER_URL="$CLI_BROWSER_CANDIDATE"
        fi
    fi
fi
if [ -n "${AGENTWORKS_CLI_BROWSER_URL:-}" ]; then
    echo "🔗 Local CLI browser approval: $AGENTWORKS_CLI_BROWSER_URL"
fi

# Change to script directory to ensure relative paths work correctly
cd "$SCRIPT_DIR" || {
    echo "❌ Error: Failed to change to script directory: $SCRIPT_DIR"
    exit 1
}
echo "📁 Working directory: $(pwd)"

# Use repo go.work so the server uses local multi-llm-provider-go (Azure streaming fix, etc.)
if [ -f "${SCRIPT_DIR}/../go.work" ]; then
    export GOWORK="${SCRIPT_DIR}/../go.work"
    echo "🔧 GOWORK=$GOWORK (using local multi-llm-provider-go)"
fi

if [ "$WITH_WORKSPACE" = true ]; then
    # Kill any orphaned workspace server from a previous run on the default port.
    if [ -z "${WORKSPACE_PORT:-}" ] && port_in_use "$DEFAULT_WORKSPACE_PORT"; then
        echo "⚠️  Port $DEFAULT_WORKSPACE_PORT busy — killing orphaned workspace server from previous run..."
        kill_process_on_port "$DEFAULT_WORKSPACE_PORT" "orphaned workspace server" 50
        sleep 0.5
    fi

    if [ -n "${WORKSPACE_PORT:-}" ]; then
        echo "🔎 Using requested workspace server port: $WORKSPACE_PORT"
        if port_in_use "$WORKSPACE_PORT"; then
            echo "❌ Error: Requested WORKSPACE_PORT $WORKSPACE_PORT is already in use"
            exit 1
        fi
    else
        echo "🔎 Selecting workspace server port: default ${DEFAULT_WORKSPACE_PORT}, random fallback in range 18000-19000..."
        WORKSPACE_PORT="$(choose_default_then_random_port "$DEFAULT_WORKSPACE_PORT" 18000 19000 "$AGENT_PORT,$GATEWAY_PORT")"
        if [ -z "$WORKSPACE_PORT" ]; then
            echo "❌ Error: No free workspace port available in range 18000-19000"
            exit 1
        fi
    fi
else
    WORKSPACE_PORT="${WORKSPACE_PORT:-8081}"
fi
export WORKSPACE_PORT

if [ "$WITH_WORKSPACE" = true ]; then
    if [ ! -f "${WORKSPACE_DIR}/main.go" ]; then
        echo "❌ Error: workspace main.go not found: ${WORKSPACE_DIR}/main.go"
        exit 1
    fi

    # Workspace docs path resolution (docs/bugs/workspace_docs_path_inside_repo.md, Option C):
    #   1. A shell-exported WORKSPACE_DOCS_PATH wins (captured before .env sourcing,
    #      so Docker paths like /app/workspace-docs in .env still can't leak in).
    #   2. Otherwise an existing non-empty repo-local workspace-docs/ keeps working
    #      (no surprise migration for current setups).
    #   3. Otherwise new installs default OUTSIDE the repo, so agents can't traverse
    #      from the workspace into project source files.
    REPO_WORKSPACE_DOCS="${SCRIPT_DIR}/../workspace-docs"
    if [ -n "$WORKSPACE_DOCS_PATH_FROM_SHELL" ]; then
        WORKSPACE_DOCS_PATH="$WORKSPACE_DOCS_PATH_FROM_SHELL"
        echo "🔧 WORKSPACE_DOCS_PATH from shell environment: $WORKSPACE_DOCS_PATH"
    elif [ -d "$REPO_WORKSPACE_DOCS" ] && [ -n "$(ls -A "$REPO_WORKSPACE_DOCS" 2>/dev/null)" ]; then
        WORKSPACE_DOCS_PATH="$REPO_WORKSPACE_DOCS"
    else
        WORKSPACE_DOCS_PATH="${HOME}/Documents/mcp-agent-workspace"
        echo "🔧 New install: defaulting workspace docs outside the repo: $WORKSPACE_DOCS_PATH"
    fi
    mkdir -p "$WORKSPACE_DOCS_PATH"
    WORKSPACE_DOCS_PATH="$(cd "$WORKSPACE_DOCS_PATH" && pwd)"
    export WORKSPACE_DOCS_PATH
    export WORKSPACE_API_URL="${LOCALHOST_BASE_URL}:${WORKSPACE_PORT}"
    if [ -z "${WORKSPACE_API_TOKEN:-}" ]; then
        WORKSPACE_API_TOKEN="$(/usr/bin/openssl rand -hex 32 2>/dev/null || uuidgen | tr -d '-')"
    fi
    export WORKSPACE_API_TOKEN

    export NATIVE_WORKSPACE="true"
    echo "🧩 Native workspace start enabled"
    echo "🔧 WORKSPACE_API_URL=$WORKSPACE_API_URL"
    echo "🔧 WORKSPACE_DOCS_PATH=$WORKSPACE_DOCS_PATH"
fi

write_frontend_runtime_config() {
    local runtime_app_name
    local runtime_favicon_url
    local gateway_config_line
    runtime_app_name="$(json_escape_runtime_value "${AGENTWORKS_APP_NAME:-AgentWorks}")"
    runtime_favicon_url="$(json_escape_runtime_value "${AGENTWORKS_FAVICON_URL:-/logo.svg}")"
    gateway_config_line=""
    if [ "$WITH_GATEWAY" = true ]; then
        gateway_config_line=",
  gatewayUrl: \"$(json_escape_runtime_value "$GATEWAY_PUBLIC_URL")\""
    fi
    mkdir -p "$(dirname "$FRONTEND_RUNTIME_CONFIG_PATH")"
    cat > "$FRONTEND_RUNTIME_CONFIG_PATH" <<EOF
window.__APP_RUNTIME_CONFIG__ = {
  apiBaseUrl: "${MCP_AGENT_SERVER_URL}",
  workspaceApiBaseUrl: "${WORKSPACE_API_URL:-${LOCALHOST_BASE_URL}:${WORKSPACE_PORT}}",
  cdpEnabled: true,
  appName: "${runtime_app_name}",
  faviconUrl: "${runtime_favicon_url}"${gateway_config_line}
};
EOF
    echo "📝 Frontend runtime config written to: $FRONTEND_RUNTIME_CONFIG_PATH"
}

write_frontend_runtime_config

# Explicitly set single-user mode (local JWT is still required for API routes)
export MULTI_USER_MODE="false"

# Enable local mode (enables CDP browser connection and other local-only features)
export LOCAL_MODE="true"

# Log all agent prompts (system prompt + user message) to logs/agent_prompts/
export LOG_AGENT_PROMPTS="true"

# Enable split execution learning feature (separates learning reading from execution)
export SPLIT_EXECUTION_LEARNING="true"

# Final server-side safety backstop for a tool that never returns. Durable
# sub-agents return an execution ID immediately; this mainly protects direct
# shell/custom calls without imposing a shorter workflow deadline.
export TOOL_EXECUTION_TIMEOUT="90m"

# Shared coding-CLI MCP client / mcpbridge HTTP safety backstop. Claude and
# Codex consume this through their supported client controls; Cursor and Pi
# currently rely on request cancellation plus the bridge's HTTP backstop.
export CODING_AGENT_MCP_TOOL_TIMEOUT="90m"

# Set MCP cache TTL to 7 days (10080 minutes)
export MCP_CACHE_TTL_MINUTES="10080"

# Set MCP cache directory to ensure consistent path across restarts
export MCP_CACHE_DIR="${MCP_CACHE_DIR:-${SCRIPT_DIR}/cache}"
echo "🔧 Set MCP_CACHE_DIR to: $MCP_CACHE_DIR"

# Context summarization configuration
export ENABLE_CONTEXT_SUMMARIZATION="true"
export SUMMARIZE_ON_TOKEN_THRESHOLD="true"
export TOKEN_THRESHOLD_PERCENT="0.7"  # 70% threshold (default: 0.7 = 70%)
export SUMMARIZE_ON_FIXED_TOKEN_THRESHOLD="true"  # Enable fixed token threshold
export FIXED_TOKEN_THRESHOLD="200000"  # Trigger summarization at 200k tokens (default: 200000)
export SUMMARY_KEEP_LAST_MESSAGES="4"  # Keep last 4 messages when summarizing (roughly 2 turns)

# Context editing configuration (compacts large tool outputs)
# Note: Higher thresholds preserve cached tokens for cost efficiency
export ENABLE_CONTEXT_EDITING="false"  # Enable context editing (default: false)
export CONTEXT_EDITING_THRESHOLD="10000"  # Compact outputs larger than 10k tokens (default: 10000)
export CONTEXT_EDITING_TURN_THRESHOLD="20"  # Compact outputs older than 20 turns (default: 20)

# Context offloading configuration (offloads large tool outputs to filesystem)
# Tool outputs larger than this threshold are saved to file and replaced with a reference
export LARGE_OUTPUT_THRESHOLD="50000"  # Offload outputs larger than 50k tokens (default: 10000)

# Set main LLM configuration (uses Bedrock with AWS credentials from environment)
# Note: Frontend Published LLMs override this for actual agent execution
export DEEP_SEARCH_MAIN_LLM_PROVIDER="bedrock"
export DEEP_SEARCH_MAIN_LLM_MODEL="global.anthropic.claude-sonnet-4-5-20250929-v1:0"
export DEEP_SEARCH_MAIN_LLM_TEMPERATURE="0.0"
export DEEP_SEARCH_MAIN_LLM_MAX_TOKENS="40000"

# Set agent provider environment variable (used by server.go for internal operations)
# Note: Actual agent execution uses Published LLMs from frontend with their own API keys
export AGENT_PROVIDER="${AGENT_PROVIDER:-azure}"
export AGENT_MODEL="${AGENT_MODEL:-gpt-5.2}"


# Claude Code bridge safety: restrict tool usage to execute_shell_command and get_api_spec
# for server-launched sessions. Callers can still override by pre-setting the env var.
export MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING="${MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING:-true}"

# Available models for each provider (optional - set in .env to customize; unset = empty lists, users add custom models)
# Removed hardcoded restrictions - use .env or leave unset for maximum flexibility
# BEDROCK_AVAILABLE_MODELS, OPENROUTER_AVAILABLE_MODELS, OPENAI_AVAILABLE_MODELS, AZURE_AVAILABLE_MODELS

# Supported LLM providers (optional - unset = all 6 providers shown: openrouter, bedrock, openai, vertex, anthropic, azure)
# Removed default restriction to azure only
# SUPPORTED_LLM_PROVIDERS

# Obsidian configuration removed - now using workspace tools

# Create logs directory if it doesn't exist
mkdir -p "$LOG_DIR"

# Truncate the log files to start fresh
echo "📝 Truncating log files for clean start..."
> "$LOG_PATH"
echo "✅ Server log file truncated: $LOG_PATH"
> "${LOG_DIR}/llm_debug.log"
echo "✅ LLM log file truncated: ${LOG_DIR}/llm_debug.log"
if [ "$WITH_WORKSPACE" = true ]; then
    WORKSPACE_LOG_PATH="${LOG_DIR}/workspace_debug.log"
    > "$WORKSPACE_LOG_PATH"
    echo "✅ Workspace log file truncated: $WORKSPACE_LOG_PATH"
fi
if [ "$WITH_FRONTEND" = true ]; then
    FRONTEND_LOG_PATH="${LOG_DIR}/frontend_debug.log"
    ELECTRON_LOG_PATH="${LOG_DIR}/electron_debug.log"
    > "$FRONTEND_LOG_PATH"
    > "$ELECTRON_LOG_PATH"
    echo "✅ Frontend log file truncated: $FRONTEND_LOG_PATH"
    echo "✅ Electron log file truncated: $ELECTRON_LOG_PATH"
fi
if [ "$WITH_GATEWAY" = true ]; then
    GATEWAY_LOG_PATH="${LOG_DIR}/gateway_debug.log"
    > "$GATEWAY_LOG_PATH"
    echo "✅ Gateway log file truncated: $GATEWAY_LOG_PATH"
fi

# Workflow Builder chats historically lived directly under conversation/<date>.
# A local installation has one authenticated owner ("default"), so claim those
# files for that owner before startup. The migration is idempotent and only
# scans the legacy date folders; production deployment runs the same script
# separately with an explicit multi-user owner map.
run_local_builder_chat_migration() {
    local migration_script="${SCRIPT_DIR}/../scripts/migrate_workflow_builder_chats.py"
    local repo_workspace_docs="${SCRIPT_DIR}/../workspace-docs"
    local migration_workspace_root=""

    if [ -n "$WORKSPACE_DOCS_PATH_FROM_SHELL" ]; then
        migration_workspace_root="$WORKSPACE_DOCS_PATH_FROM_SHELL"
    elif [ -d "$repo_workspace_docs" ] && [ -n "$(ls -A "$repo_workspace_docs" 2>/dev/null)" ]; then
        migration_workspace_root="$repo_workspace_docs"
    elif [ -n "${WORKSPACE_DOCS_PATH:-}" ] && [ -d "$WORKSPACE_DOCS_PATH" ]; then
        migration_workspace_root="$WORKSPACE_DOCS_PATH"
    fi

    if [ -z "$migration_workspace_root" ] || [ ! -d "$migration_workspace_root/Workflow" ]; then
        echo "ℹ️  No local legacy Workflow Builder chats to migrate"
        return 0
    fi
    migration_workspace_root="$(cd "$migration_workspace_root" && pwd)"
    echo "🔄 Checking local Workflow Builder chat migration..."
    if ! python3 "$migration_script" \
        --workspace-root "$migration_workspace_root" \
        --default-owner-id "${DEFAULT_USER_ID:-default}" \
        --default-owner-username "Local user" \
        --apply >> "$LOG_PATH" 2>&1; then
        echo "❌ Local Workflow Builder chat migration failed. Check: $LOG_PATH"
        tail -20 "$LOG_PATH"
        return 1
    fi
    echo "✅ Local Workflow Builder chat migration complete"
}

run_local_builder_chat_migration || exit 1

# Log rotation cap (used by background daemon)
LOG_ROTATE_LINES=500000

# Clean up agent prompt logs to start fresh
echo "🧹 Cleaning ${LOG_DIR}/agent_prompts..."
if [ -d "${LOG_DIR}/agent_prompts" ]; then
    rm -rf "${LOG_DIR}/agent_prompts"/*
    echo "✅ ${LOG_DIR}/agent_prompts cleaned"
else
    mkdir -p "${LOG_DIR}/agent_prompts"
    echo "✅ ${LOG_DIR}/agent_prompts created"
fi

# Clean up tool_output_folder to start fresh
echo "🧹 Cleaning tool_output_folder..."
if [ -d "tool_output_folder" ]; then
    rm -rf tool_output_folder/*
    echo "✅ tool_output_folder cleaned (all files and subdirectories removed)"
else
    mkdir -p tool_output_folder
    echo "✅ tool_output_folder created (was missing)"
fi

# Clean up generated/agents directory to start fresh
echo "🧹 Cleaning generated/agents..."
if [ -d "generated/agents" ]; then
    rm -rf generated/agents/*
    echo "✅ generated/agents cleaned (all files and subdirectories removed)"
else
    mkdir -p generated/agents
    echo "✅ generated/agents created (was missing)"
fi

# Add timestamp header to log file
echo "🚀 MCP Agent Server Session Started: $(date)" > "$LOG_PATH"
echo "=========================================" >> "$LOG_PATH"
echo "Configuration:" >> "$LOG_PATH"
echo "- Split Execution Learning: $SPLIT_EXECUTION_LEARNING" >> "$LOG_PATH"
echo "- Tool Execution Timeout: $TOOL_EXECUTION_TIMEOUT" >> "$LOG_PATH"
echo "- MCP Cache TTL: $MCP_CACHE_TTL_MINUTES minutes (7 days)" >> "$LOG_PATH"
echo "- Agent Provider: $AGENT_PROVIDER" >> "$LOG_PATH"
echo "- Agent Model: $AGENT_MODEL" >> "$LOG_PATH"
echo "- Main LLM Provider: $DEEP_SEARCH_MAIN_LLM_PROVIDER" >> "$LOG_PATH"
echo "- Main LLM Model: $DEEP_SEARCH_MAIN_LLM_MODEL" >> "$LOG_PATH"
echo "- Main LLM Temperature: $DEEP_SEARCH_MAIN_LLM_TEMPERATURE" >> "$LOG_PATH"
echo "- Available Bedrock Models: $BEDROCK_AVAILABLE_MODELS" >> "$LOG_PATH"
echo "- Available OpenRouter Models: $OPENROUTER_AVAILABLE_MODELS" >> "$LOG_PATH"
echo "- Available OpenAI Models: $OPENAI_AVAILABLE_MODELS" >> "$LOG_PATH"
echo "- Available Azure Models: $AZURE_AVAILABLE_MODELS" >> "$LOG_PATH"
echo "- Workspace tools: Enabled" >> "$LOG_PATH"
echo "- Context Summarization: $ENABLE_CONTEXT_SUMMARIZATION" >> "$LOG_PATH"
echo "- Token Threshold: $TOKEN_THRESHOLD_PERCENT (70%) | Fixed: ${FIXED_TOKEN_THRESHOLD} tokens" >> "$LOG_PATH"
echo "- Keep Last Messages: $SUMMARY_KEEP_LAST_MESSAGES" >> "$LOG_PATH"
echo "- Context Editing: $ENABLE_CONTEXT_EDITING (Threshold: ${CONTEXT_EDITING_THRESHOLD} tokens, Age: ${CONTEXT_EDITING_TURN_THRESHOLD} turns)" >> "$LOG_PATH"
echo "- Large Output Threshold: ${LARGE_OUTPUT_THRESHOLD} tokens" >> "$LOG_PATH"
echo "- Agent API URL: $MCP_AGENT_SERVER_URL" >> "$LOG_PATH"
if [ "$WITH_WORKSPACE" = true ]; then
    echo "- Native Workspace: Enabled (${WORKSPACE_API_URL}, docs=${WORKSPACE_DOCS_PATH})" >> "$LOG_PATH"
fi
echo "=========================================" >> "$LOG_PATH"
echo "" >> "$LOG_PATH"

# Start background log rotation: keep only last 500000 lines every 30 seconds
rotate_log_file() {
    local file_path="$1"
    if [ -f "$file_path" ]; then
        lines=$(wc -l < "$file_path" 2>/dev/null)
        if [ "$lines" -gt "$LOG_ROTATE_LINES" ]; then
            # Keep the existing inode: the Go server has stdout/stderr open to
            # this file. BSD sed -i replaces the file, leaving the live process
            # writing to an unlinked inode while the visible log goes stale.
            local rotate_tmp="${file_path}.rotate.$$"
            if tail -n "$LOG_ROTATE_LINES" "$file_path" > "$rotate_tmp"; then
                cat "$rotate_tmp" > "$file_path"
            fi
            rm -f "$rotate_tmp"
        fi
    fi
}

log_rotate_daemon() {
    while true; do
        sleep 30
        rotate_log_file "$LOG_PATH"
        if [ "$WITH_WORKSPACE" = true ] && [ -n "$WORKSPACE_LOG_PATH" ]; then
            rotate_log_file "$WORKSPACE_LOG_PATH"
        fi
        if [ "$WITH_FRONTEND" = true ]; then
            [ -n "$FRONTEND_LOG_PATH" ] && rotate_log_file "$FRONTEND_LOG_PATH"
            [ -n "$ELECTRON_LOG_PATH" ] && rotate_log_file "$ELECTRON_LOG_PATH"
        fi
        if [ "$WITH_GATEWAY" = true ] && [ -n "$GATEWAY_LOG_PATH" ]; then
            rotate_log_file "$GATEWAY_LOG_PATH"
        fi
    done
}
log_rotate_daemon &
LOG_ROTATE_PID=$!

stop_native_workspace() {
    if [ -n "$WORKSPACE_PID" ] && kill -0 "$WORKSPACE_PID" 2>/dev/null; then
        print_stop_target "native workspace server" "$WORKSPACE_PID" "$WORKSPACE_PORT"
        kill_process_tree "$WORKSPACE_PID" "native workspace server"
        wait "$WORKSPACE_PID" 2>/dev/null
        print_port_status "$WORKSPACE_PORT" "workspace"
    fi
    # Fallback: go run exits on SIGINT before our cleanup runs, leaving the compiled
    # binary orphaned (re-parented to PID 1). Kill anything still on the port.
    if [ -n "$WORKSPACE_PORT" ]; then
        kill_process_on_port "$WORKSPACE_PORT" "orphaned workspace server" 50
        print_port_status "$WORKSPACE_PORT" "workspace"
    fi
}

stop_mcp_gateway() {
    if [ -n "$GATEWAY_PID" ] && kill -0 "$GATEWAY_PID" 2>/dev/null; then
        print_stop_target "MCP Gateway" "$GATEWAY_PID" "$GATEWAY_PORT"
        kill_process_tree "$GATEWAY_PID" "MCP Gateway"
        wait "$GATEWAY_PID" 2>/dev/null
        print_port_status "$GATEWAY_PORT" "gateway"
    fi
    # Same go-run orphan fallback as the workspace server.
    if [ -n "$GATEWAY_PORT" ]; then
        kill_process_on_port "$GATEWAY_PORT" "orphaned gateway server" 50
        print_port_status "$GATEWAY_PORT" "gateway"
    fi
}

process_tree_rss_kb() {
    local root_pid="$1"
    local rss_kb
    local total_kb
    local child_pid
    local child_total_kb

    if [ -z "$root_pid" ] || ! kill -0 "$root_pid" 2>/dev/null; then
        echo 0
        return 0
    fi

    rss_kb="$(ps -o rss= -p "$root_pid" 2>/dev/null | tr -d '[:space:]')"
    total_kb="${rss_kb:-0}"
    for child_pid in $(pgrep -P "$root_pid" 2>/dev/null || true); do
        child_total_kb="$(process_tree_rss_kb "$child_pid")"
        total_kb=$((total_kb + child_total_kb))
    done
    echo "$total_kb"
}

stop_electron_memory_monitor() {
    if [ -n "$ELECTRON_MEMORY_MONITOR_PID" ] && kill -0 "$ELECTRON_MEMORY_MONITOR_PID" 2>/dev/null; then
        kill "$ELECTRON_MEMORY_MONITOR_PID" 2>/dev/null || true
        wait "$ELECTRON_MEMORY_MONITOR_PID" 2>/dev/null || true
    fi
    ELECTRON_MEMORY_MONITOR_PID=""
}

stop_isolated_electron_helpers() {
    local user_data_dir="${RUNLOOP_USER_DATA_DIR:-}"
    local process_line
    local candidate_pid
    local candidate_command

    [ -n "$user_data_dir" ] || return 0

    # Chromium helpers can briefly re-parent while the Electron main process is
    # exiting. Resolve only helpers carrying this instance's exact profile path;
    # never touch Electron/Codex processes owned by another app or worktree.
    while IFS= read -r process_line; do
        candidate_pid="${process_line%% *}"
        candidate_command="${process_line#* }"
        case "$candidate_command" in
            *Electron*"--user-data-dir=${user_data_dir}"*)
                if [ "$candidate_pid" != "$$" ] && kill -0 "$candidate_pid" 2>/dev/null; then
                    echo "🧹 Stopping orphaned isolated Electron helper (PID: $candidate_pid)"
                    kill_process_tree "$candidate_pid" "isolated Electron helper" 10
                fi
                ;;
        esac
    done < <(ps -axo pid=,command= | sed 's/^[[:space:]]*//')
}

start_electron_memory_monitor() {
    local limit_mb="${AGENTWORKS_ELECTRON_RSS_LIMIT_MB:-0}"
    if ! [[ "$limit_mb" =~ ^[0-9]+$ ]] || [ "$limit_mb" -eq 0 ]; then
        return 0
    fi

    local electron_root_pid="$ELECTRON_PID"
    local limit_kb=$((limit_mb * 1024))
    (
        while kill -0 "$electron_root_pid" 2>/dev/null; do
            sleep 1
            local rss_kb
            rss_kb="$(process_tree_rss_kb "$electron_root_pid")"
            if [ "$rss_kb" -gt "$limit_kb" ]; then
                local rss_mb=$((rss_kb / 1024))
                local message="Electron memory watchdog: isolated process tree reached ${rss_mb} MB (limit ${limit_mb} MB); terminating it to protect the system"
                echo "⚠️  $message" | tee -a "$ELECTRON_LOG_PATH" >&2
                kill_process_tree "$electron_root_pid" "Electron memory limit" 10
                exit 0
            fi
        done
    ) &
    ELECTRON_MEMORY_MONITOR_PID=$!
    echo "🛡️  Electron memory watchdog active (tree RSS limit: ${limit_mb} MB, PID: $ELECTRON_MEMORY_MONITOR_PID)"
}

stop_electron() {
    stop_electron_memory_monitor
    if [ -n "$ELECTRON_PID" ] && kill -0 "$ELECTRON_PID" 2>/dev/null; then
        print_stop_target "Electron" "$ELECTRON_PID"
        kill_process_tree "$ELECTRON_PID" "Electron"
        wait "$ELECTRON_PID" 2>/dev/null
    fi
    stop_isolated_electron_helpers
}

stop_frontend_dev() {
    if [ -n "$FRONTEND_PID" ] && kill -0 "$FRONTEND_PID" 2>/dev/null; then
        print_stop_target "frontend server" "$FRONTEND_PID" "$FRONTEND_PORT"
        kill_process_tree "$FRONTEND_PID" "frontend server"
        wait "$FRONTEND_PID" 2>/dev/null
        print_port_status "$FRONTEND_PORT" "frontend"
    fi
}

stop_agent_server() {
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        print_stop_target "agent server" "$SERVER_PID" "$AGENT_PORT"
        # Give the Go server enough time to run SIGTERM cleanup. In particular,
        # Claude Code experimental runs live in detached tmux sessions, and a
        # fast SIGKILL can leave them orphaned.
        kill_process_tree "$SERVER_PID" "agent server" 200
        wait "$SERVER_PID" 2>/dev/null
        print_port_status "$AGENT_PORT" "agent"
    fi
    # Fallback: go run exits on SIGINT before our cleanup runs, leaving the compiled
    # binary orphaned (re-parented to PID 1, invisible via pgrep -P SERVER_PID).
    # Kill anything still listening on the agent port.
    if [ -n "$AGENT_PORT" ]; then
        kill_process_on_port "$AGENT_PORT" "orphaned agent server" 50
        print_port_status "$AGENT_PORT" "agent"
    fi
    cleanup_coding_agent_tmux_sessions
}

cleanup_on_exit() {
    kill "$LOG_ROTATE_PID" 2>/dev/null
    wait "$LOG_ROTATE_PID" 2>/dev/null
    if [ "$BACKGROUND_MODE" != true ]; then
        stop_electron
        stop_frontend_dev
        stop_agent_server
        stop_native_workspace
        if [ "$WITH_GATEWAY" = true ]; then
            stop_mcp_gateway
        fi
    fi
    if [ "$AGENTWORKS_AUTO_TMUX_TMPDIR" = true ]; then
        rm -rf "$TMUX_TMPDIR"
    fi
}

trap cleanup_on_exit EXIT
trap "echo ''; echo '🛑 Shutting down (Ctrl+C)...'; exit 130" INT TERM
echo "🔄 Log rotation started (keeping last $LOG_ROTATE_LINES lines, PID: $LOG_ROTATE_PID)"

wait_for_workspace_health() {
    local health_url="${WORKSPACE_API_URL%/}/health"
    local attempt
    for attempt in $(seq 1 90); do
        if curl -fsS "$health_url" >/dev/null 2>&1; then
            echo "✅ Native workspace is healthy at: $health_url"
            return 0
        fi
        if ! kill -0 "$WORKSPACE_PID" 2>/dev/null; then
            echo "❌ Error: Native workspace exited during startup. Check logs: $WORKSPACE_LOG_PATH"
            tail -20 "$WORKSPACE_LOG_PATH"
            return 1
        fi
        sleep 1
    done

    echo "❌ Error: Native workspace did not become healthy in time. Check logs: $WORKSPACE_LOG_PATH"
    tail -20 "$WORKSPACE_LOG_PATH"
    return 1
}

start_native_workspace() {
    if [ "$WITH_WORKSPACE" != true ]; then
        return 0
    fi

    if port_in_use "$WORKSPACE_PORT"; then
        echo "❌ Error: Port $WORKSPACE_PORT is already in use."
        echo "   Stop the existing process or set WORKSPACE_PORT to another value."
        return 1
    fi

    echo "🚀 Starting native workspace server..."
    echo "📝 Workspace log file: $WORKSPACE_LOG_PATH"
    echo "🌐 Workspace API URL: $WORKSPACE_API_URL"

    echo "🚀 Native Workspace Session Started: $(date)" > "$WORKSPACE_LOG_PATH"
    echo "=========================================" >> "$WORKSPACE_LOG_PATH"
    echo "- Port: $WORKSPACE_PORT" >> "$WORKSPACE_LOG_PATH"
    echo "- Docs Path: $WORKSPACE_DOCS_PATH" >> "$WORKSPACE_LOG_PATH"
    echo "- Native Workspace: ${NATIVE_WORKSPACE:-}" >> "$WORKSPACE_LOG_PATH"
    echo "- PATH: $PATH" >> "$WORKSPACE_LOG_PATH"
    echo "=========================================" >> "$WORKSPACE_LOG_PATH"
    echo "" >> "$WORKSPACE_LOG_PATH"

    # Bind to localhost as defense in depth. Process-execution routes also
    # require the server-only WORKSPACE_API_TOKEN generated above.
    WORKSPACE_BIND_HOST="${WORKSPACE_BIND_HOST:-127.0.0.1}"
    if [ "$BACKGROUND_MODE" = true ]; then
        nohup bash -lc "cd \"$WORKSPACE_DIR\" && exec go run . server --debug --host \"$WORKSPACE_BIND_HOST\" --port \"$WORKSPACE_PORT\" --docs-dir \"$WORKSPACE_DOCS_PATH\"" >> "$WORKSPACE_LOG_PATH" 2>&1 &
    else
        (
            cd "$WORKSPACE_DIR" || exit 1
            exec go run . server --debug --host "$WORKSPACE_BIND_HOST" --port "$WORKSPACE_PORT" --docs-dir "$WORKSPACE_DOCS_PATH"
        ) >> "$WORKSPACE_LOG_PATH" 2>&1 &
    fi

    WORKSPACE_PID=$!
    echo "✅ Native workspace process started (PID: $WORKSPACE_PID)"
    wait_for_workspace_health
}

wait_for_gateway_health() {
    local health_url="${GATEWAY_PUBLIC_URL%/}/healthz"
    local attempt
    for attempt in $(seq 1 90); do
        if curl -fsS "$health_url" >/dev/null 2>&1; then
            echo "✅ MCP Gateway is healthy at: ${GATEWAY_PUBLIC_URL}/mcp"
            return 0
        fi
        if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
            echo "❌ Error: MCP Gateway exited during startup. Check logs: $GATEWAY_LOG_PATH"
            tail -20 "$GATEWAY_LOG_PATH"
            return 1
        fi
        sleep 1
    done

    echo "❌ Error: MCP Gateway did not become healthy in time. Check logs: $GATEWAY_LOG_PATH"
    tail -20 "$GATEWAY_LOG_PATH"
    return 1
}

start_mcp_gateway() {
    if [ "$WITH_GATEWAY" != true ]; then
        return 0
    fi

    if [ ! -f "${GATEWAY_DIR}/go.mod" ]; then
        echo "❌ Error: gateway module not found: ${GATEWAY_DIR}/go.mod"
        return 1
    fi

    if port_in_use "$GATEWAY_PORT"; then
        echo "❌ Error: Port $GATEWAY_PORT is already in use."
        echo "   Stop the existing process or set GATEWAY_PORT to another value."
        return 1
    fi

    echo "🚀 Starting MCP Gateway..."
    echo "📝 Gateway log file: $GATEWAY_LOG_PATH"
    echo "🌐 Gateway MCP URL: ${GATEWAY_PUBLIC_URL}/mcp"
    echo "🔑 Gateway admin token: $GATEWAY_HUMAN_TOKEN"

    echo "🚀 Gateway Session Started: $(date)" > "$GATEWAY_LOG_PATH"
    echo "=========================================" >> "$GATEWAY_LOG_PATH"
    echo "- Port: $GATEWAY_PORT" >> "$GATEWAY_LOG_PATH"
    echo "- Public URL: $GATEWAY_PUBLIC_URL" >> "$GATEWAY_LOG_PATH"
    echo "=========================================" >> "$GATEWAY_LOG_PATH"
    echo "" >> "$GATEWAY_LOG_PATH"

    if [ "$BACKGROUND_MODE" = true ]; then
        # shellcheck disable=SC2086
        nohup bash -lc "cd \"$GATEWAY_DIR\" && exec go run ./cmd/server" >> "$GATEWAY_LOG_PATH" 2>&1 &
    else
        (
            cd "$GATEWAY_DIR" || exit 1
            exec go run ./cmd/server
        ) >> "$GATEWAY_LOG_PATH" 2>&1 &
    fi

    GATEWAY_PID=$!
    echo "✅ MCP Gateway process started (PID: $GATEWAY_PID)"
    wait_for_gateway_health
}

wait_for_frontend_health() {
    local health_url="$FRONTEND_URL"
    local attempt
    for attempt in $(seq 1 60); do
        if curl -fsS "$health_url" >/dev/null 2>&1; then
            echo "✅ Frontend server is ready at: $health_url"
            return 0
        fi
        if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
            echo "❌ Error: Frontend server exited during startup. Check logs: $FRONTEND_LOG_PATH"
            tail -20 "$FRONTEND_LOG_PATH"
            return 1
        fi
        sleep 1
    done

    echo "❌ Error: Frontend server did not become ready in time. Check logs: $FRONTEND_LOG_PATH"
    tail -20 "$FRONTEND_LOG_PATH"
    return 1
}

start_frontend_dev() {
    if [ "$WITH_FRONTEND" != true ]; then
        return 0
    fi

    if [ ! -f "${FRONTEND_DIR}/package.json" ]; then
        echo "❌ Error: frontend package.json not found: ${FRONTEND_DIR}/package.json"
        return 1
    fi

    # Match the frontend-only path: a newly created worktree may not have all
    # lockfile dependencies even when another worktree is already bootstrapped.
    echo "📦 Ensuring frontend dependencies (npm install)..."
    (
        cd "$FRONTEND_DIR" || exit 1
        npm install
    ) >> "$FRONTEND_LOG_PATH" 2>&1 || {
        echo "❌ Error: frontend dependency install failed. Check logs: $FRONTEND_LOG_PATH"
        tail -30 "$FRONTEND_LOG_PATH"
        return 1
    }

    if port_in_use "$FRONTEND_PORT"; then
        echo "❌ Error: Port $FRONTEND_PORT is already in use."
        if [ -n "$FRONTEND_PORT_EXPLICIT" ]; then
            echo "   FRONTEND_PORT was explicitly set; choose another value or stop the existing process."
        else
            echo "   Port became busy after selection; retry the command."
        fi
        return 1
    fi

    if [ "$FRONTEND_BUILD_MODE" = true ]; then
        echo "🚀 Starting static frontend server..."
    else
        echo "🚀 Starting Vite dev server..."
    fi
    echo "📝 Frontend log file: $FRONTEND_LOG_PATH"
    echo "🌐 Frontend URL: $FRONTEND_URL"

    if [ "$FRONTEND_BUILD_MODE" = true ]; then
        echo "🔨 Building frontend..."
        echo "🔨 Frontend Build Session Started: $(date)" > "$FRONTEND_LOG_PATH"
        (
            cd "$FRONTEND_DIR" || exit 1
            npm run build
        ) >> "$FRONTEND_LOG_PATH" 2>&1 || {
            echo "❌ Error: Frontend build failed. Check logs: $FRONTEND_LOG_PATH"
            tail -30 "$FRONTEND_LOG_PATH"
            return 1
        }
        echo "🚀 Vite Preview Session Started: $(date)" >> "$FRONTEND_LOG_PATH"
    else
        echo "🚀 Vite Dev Session Started: $(date)" > "$FRONTEND_LOG_PATH"
    fi
    echo "=========================================" >> "$FRONTEND_LOG_PATH"
    echo "- Port: $FRONTEND_PORT" >> "$FRONTEND_LOG_PATH"
    echo "- URL: $FRONTEND_URL" >> "$FRONTEND_LOG_PATH"
    echo "=========================================" >> "$FRONTEND_LOG_PATH"

    if [ "$BACKGROUND_MODE" = true ]; then
        if [ "$FRONTEND_BUILD_MODE" = true ]; then
            nohup bash -lc "cd \"$FRONTEND_DIR\" && exec npm run preview -- --host \"$FRONTEND_BIND_HOST\" --port \"$FRONTEND_PORT\" --strictPort" >> "$FRONTEND_LOG_PATH" 2>&1 &
        elif [ -n "$FRONTEND_HOST" ]; then
            nohup bash -lc "cd \"$FRONTEND_DIR\" && exec npm run dev -- --host \"$FRONTEND_HOST\" --port \"$FRONTEND_PORT\" --strictPort --force" >> "$FRONTEND_LOG_PATH" 2>&1 &
        else
            nohup bash -lc "cd \"$FRONTEND_DIR\" && exec npm run dev -- --port \"$FRONTEND_PORT\" --strictPort --force" >> "$FRONTEND_LOG_PATH" 2>&1 &
        fi
    else
        (
            cd "$FRONTEND_DIR" || exit 1
            if [ "$FRONTEND_BUILD_MODE" = true ]; then
                exec npm run preview -- --host "$FRONTEND_BIND_HOST" --port "$FRONTEND_PORT" --strictPort
            elif [ -n "$FRONTEND_HOST" ]; then
                exec npm run dev -- --host "$FRONTEND_HOST" --port "$FRONTEND_PORT" --strictPort --force
            else
                exec npm run dev -- --port "$FRONTEND_PORT" --strictPort --force
            fi
        ) >> "$FRONTEND_LOG_PATH" 2>&1 &
    fi

    FRONTEND_PID=$!
    echo "✅ Frontend server process started (PID: $FRONTEND_PID)"
    wait_for_frontend_health
}

wait_for_agent_health() {
    local url="${MCP_AGENT_SERVER_URL%/}/api/health"
    local attempt
    for attempt in $(seq 1 180); do
        if curl -fsS "$url" >/dev/null 2>&1; then
            echo "✅ Agent server is healthy at: $url"
            return 0
        fi
        if [ -n "$SERVER_PID" ] && ! kill -0 "$SERVER_PID" 2>/dev/null; then
            echo "❌ Error: Agent server exited during startup. Check logs: $LOG_PATH"
            tail -30 "$LOG_PATH"
            return 1
        fi
        sleep 1
    done

    echo "❌ Error: Agent server did not become healthy in time. Check logs: $LOG_PATH"
    tail -30 "$LOG_PATH"
    return 1
}

start_electron() {
    if [ "$WITH_FRONTEND" != true ]; then
        return 0
    fi
    if [ "$WITHOUT_ELECTRON" = true ]; then
        echo "🌐 Browser-only frontend requested; Electron will not be started"
        return 0
    fi

    if [ ! -f "${DESKTOP_DIR}/package.json" ]; then
        echo "❌ Error: desktop package.json not found: ${DESKTOP_DIR}/package.json"
        return 1
    fi
    if [ ! -x "$ELECTRON_BIN" ]; then
        echo "📦 Ensuring desktop dependencies (npm install)..."
        (
            cd "$DESKTOP_DIR" || exit 1
            npm install
        ) >> "$ELECTRON_LOG_PATH" 2>&1 || {
            echo "❌ Error: desktop dependency install failed. Check logs: $ELECTRON_LOG_PATH"
            tail -30 "$ELECTRON_LOG_PATH"
            return 1
        }
    fi
    if [ ! -x "$ELECTRON_BIN" ]; then
        echo "❌ Error: Electron binary not found or not executable: $ELECTRON_BIN"
        return 1
    fi

    local dev_url="$FRONTEND_URL"
    echo "🚀 Starting Electron (DEV_URL=$dev_url)..."
    echo "📝 Electron log file: $ELECTRON_LOG_PATH"

    echo "🚀 Electron Session Started: $(date)" > "$ELECTRON_LOG_PATH"
    echo "=========================================" >> "$ELECTRON_LOG_PATH"
    echo "- DEV_URL: $dev_url" >> "$ELECTRON_LOG_PATH"
    echo "=========================================" >> "$ELECTRON_LOG_PATH"

    if [ "$BACKGROUND_MODE" = true ]; then
        nohup bash -lc "cd \"$DESKTOP_DIR\" && DEV_URL=\"$dev_url\" exec \"$ELECTRON_BIN\" ." >> "$ELECTRON_LOG_PATH" 2>&1 &
    else
        (
            cd "$DESKTOP_DIR" || exit 1
            DEV_URL="$dev_url" exec "$ELECTRON_BIN" .
        ) >> "$ELECTRON_LOG_PATH" 2>&1 &
    fi

    ELECTRON_PID=$!
    echo "✅ Electron process started (PID: $ELECTRON_PID)"
    sleep 2
    if ! kill -0 "$ELECTRON_PID" 2>/dev/null; then
        echo "❌ Error: Electron exited immediately. Check logs: $ELECTRON_LOG_PATH"
        tail -30 "$ELECTRON_LOG_PATH"
        return 1
    fi
    start_electron_memory_monitor
}

# Start the server with enhanced logging and structured output LLM
echo "🚀 Starting MCP Agent Server with enhanced logging..."
echo "📝 Log file: $LOG_PATH"
echo "🔀 Split Execution Learning: $SPLIT_EXECUTION_LEARNING"
echo "⏱️  Tool Timeout: $TOOL_EXECUTION_TIMEOUT"
echo "💾 MCP Cache TTL: $MCP_CACHE_TTL_MINUTES minutes (7 days)"
echo "📁 Workspace Tools: Enabled"
echo "🌐 Agent API URL: $MCP_AGENT_SERVER_URL"
echo "📝 Context Summarization: $ENABLE_CONTEXT_SUMMARIZATION (Threshold: $TOKEN_THRESHOLD_PERCENT = 70%, Fixed: ${FIXED_TOKEN_THRESHOLD} tokens, Keep: $SUMMARY_KEEP_LAST_MESSAGES msgs)"
echo "✂️  Context Editing: $ENABLE_CONTEXT_EDITING (Threshold: ${CONTEXT_EDITING_THRESHOLD} tokens, Age: ${CONTEXT_EDITING_TURN_THRESHOLD} turns)"
echo "📦 Large Output Threshold: ${LARGE_OUTPUT_THRESHOLD} tokens"
echo "📊 Debug level: $LOG_LEVEL"

# Verify main.go exists before attempting to run
if [ ! -f "main.go" ]; then
    echo "❌ Error: main.go not found in current directory: $(pwd)"
    exit 1
fi

# Verify go is available
if ! command -v go &> /dev/null; then
    echo "❌ Error: 'go' command not found. Please install Go."
    exit 1
fi

update_mmx_cli_if_requested() {
    if [ "$UPDATE_MMX_CLI" != true ]; then
        return 0
    fi

    if ! command -v npm &> /dev/null; then
        echo "❌ Error: --update requested but 'npm' command not found. Please install Node.js/npm."
        return 1
    fi

    echo "📦 Updating mmx-cli to latest because --update was provided..."
    npm install -g mmx-cli@latest 2>&1 | tail -5
    local npm_status=${PIPESTATUS[0]}
    if [ "$npm_status" -ne 0 ]; then
        echo "❌ Error: failed to update mmx-cli"
        return "$npm_status"
    fi

    if command -v mmx &> /dev/null; then
        echo "✅ mmx-cli updated: $(mmx --version 2>/dev/null || echo 'version unknown')"
    else
        echo "⚠️  mmx-cli update completed but 'mmx' was not found on PATH"
    fi
}

update_mmx_cli_if_requested || exit 1

ensure_tmux_for_claude_code() {
    if command -v tmux &> /dev/null; then
        local version major
        version="$(tmux -V 2>/dev/null || true)"
        major="$(printf '%s\n' "$version" | sed -E 's/^tmux ([0-9]+).*/\1/')"
        if [ "$major" -ge 3 ] 2>/dev/null; then
            echo "✅ Claude Code experimental runtime dependency available: $version"
            return 0
        fi
        echo "⚠️  Claude Code experimental runtime dependency ${version:-unknown} found, but version 3.x or newer is required."
    fi

    echo "📦 Installing/upgrading Claude Code experimental runtime dependency..."
    if [[ "$(uname -s)" == "Darwin" ]] && command -v brew &> /dev/null; then
        brew upgrade tmux || brew install tmux
    elif command -v apt-get &> /dev/null; then
        if [ "$(id -u)" -eq 0 ]; then
            apt-get update && apt-get install -y --no-install-recommends tmux
        elif command -v sudo &> /dev/null; then
            sudo apt-get update && sudo apt-get install -y --no-install-recommends tmux
        fi
    fi

    if command -v tmux &> /dev/null; then
        version="$(tmux -V 2>/dev/null || true)"
        major="$(printf '%s\n' "$version" | sed -E 's/^tmux ([0-9]+).*/\1/')"
        if [ "$major" -ge 3 ] 2>/dev/null; then
            echo "✅ Claude Code experimental runtime dependency installed: $version"
        else
            echo "⚠️  Claude Code experimental runtime dependency ${version:-unknown} is still below 3.x. Claude Code provider will fail until tmux is upgraded."
        fi
    else
        echo "⚠️  Claude Code experimental runtime dependency is still missing. Claude Code provider will fail until tmux is installed."
    fi
}

if [ "${AGENTWORKS_SKIP_GLOBAL_DEPENDENCY_UPDATES:-false}" = "true" ]; then
    echo "🔒 Isolated instance: skipping global tmux and agent-browser updates"
    if ! command -v tmux >/dev/null 2>&1; then
        echo "❌ Error: tmux is required and global dependency updates are disabled"
        exit 1
    fi
    if ! command -v agent-browser >/dev/null 2>&1; then
        echo "❌ Error: agent-browser is required and global dependency updates are disabled"
        exit 1
    fi
else
    ensure_tmux_for_claude_code

    # Keep the historical developer-runner behavior for the normal instance.
    # preview_report and every browser-automation tool shell out to
    # `agent-browser` by name with no PATH check of their own — an install
    # failure here must not be reported as success, or the first real symptom
    # is an opaque "exit code 127: agent-browser: not found" deep inside a
    # tool call.
    echo "📦 Updating agent-browser to latest..."
    if ! npm install -g agent-browser@latest 2>&1 | tail -5; then
        echo "❌ Error: npm install -g agent-browser@latest failed"
        exit 1
    fi
    if ! command -v agent-browser >/dev/null 2>&1; then
        echo "❌ Error: agent-browser still not on PATH after install"
        exit 1
    fi
    echo "✅ agent-browser updated: $(agent-browser --version 2>&1)"
fi

# Serve the same private Playwright fixtures as server releases. Build these
# from source on every local start so archive contents match this checkout.
# An explicit override belongs to the caller and must not be overwritten.
if [ -z "${AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR:-}" ]; then
    export AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR="$SCRIPT_DIR/packages"
    echo "📦 Building AgentWorks Playwright fixture packages..."
    if ! python3 "$SCRIPT_DIR/../scripts/build-playwright-packages.py" "$AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR"; then
        echo "❌ Failed to build Playwright fixture packages; check Python 3 and package sources"
        exit 1
    fi
fi
for fixture_archive in agentworks-playwright.tgz agentworks-playwright-python.zip; do
    if [ ! -r "$AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR/$fixture_archive" ]; then
        echo "❌ Missing Playwright fixture archive: $AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR/$fixture_archive"
        exit 1
    fi
done
export AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR

# Build mcpbridge binary (required for CLI provider MCP bridge)
# Install from local source to pick up latest fixes (e.g., virtual tool scoping)
echo "🔨 Building mcpbridge binary from local source..."
(cd "${SCRIPT_DIR}/../../mcpagent" && go install ./cmd/mcpbridge/) 2>&1
if [ $? -eq 0 ]; then
    echo "✅ mcpbridge binary installed from local source: $(which mcpbridge || echo ~/go/bin/mcpbridge)"
else
    # Fallback to published module if local build fails
    echo "⚠️  Local build failed, falling back to published release..."
    GOWORK=off go install github.com/manishiitg/mcpagent/cmd/mcpbridge@latest 2>&1
    if [ $? -eq 0 ]; then
        echo "✅ mcpbridge binary installed from published release: $(which mcpbridge || echo ~/go/bin/mcpbridge)"
    else
        echo "⚠️  Failed to install mcpbridge (CLI provider MCP bridge will not work)"
    fi
fi

if [ "$WITH_WORKSPACE" = true ]; then
    start_native_workspace || exit 1
fi

if [ "$WITH_GATEWAY" = true ]; then
    # The gateway is auxiliary: an explicit flag fails the run, but a
    # default-on failure only warns so one sidecar can't break the stack.
    if ! start_mcp_gateway; then
        if [ "$GATEWAY_EXPLICIT" = true ]; then
            exit 1
        fi
        echo "⚠️  Continuing without the MCP Gateway (pass --with-gateway to require it)."
        WITH_GATEWAY=false
        write_frontend_runtime_config
    fi
fi

# Kill all leftover agent-browser daemon processes from previous runs.
# These accumulate as zombies when Chrome crashes — the daemon stays alive with a
# dead CDP connection. We kill them all at startup so the new server gets a clean slate.
if [ "${AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP:-false}" = "true" ]; then
    echo "🔒 Isolated instance: skipping global agent-browser process and state cleanup"
else
ZOMBIE_COUNT=$(pgrep -f 'agent-browser-darwin-arm64' 2>/dev/null | wc -l | tr -d ' ')
if [ "$ZOMBIE_COUNT" -gt 0 ]; then
    echo "🧹 Killing $ZOMBIE_COUNT leftover agent-browser daemon(s) from previous run..."
    pkill -9 -f 'agent-browser-darwin-arm64' 2>/dev/null || true
    echo "✅ agent-browser daemons cleared"
else
    echo "✅ No leftover agent-browser daemons"
fi

# Kill only orphaned agent-browser-owned Chrome processes. Do not match plain
# "Google Chrome for Testing": users may run their own Chrome-for-Testing outside
# Runloop, and startup must not close those windows.
ORPHAN_AGENT_CHROME_PIDS=""
while IFS= read -r chrome_pid; do
    [ -n "$chrome_pid" ] || continue
    chrome_ppid="$(ps -p "$chrome_pid" -o ppid= 2>/dev/null | tr -d '[:space:]')"
    if [ "$chrome_ppid" = "1" ]; then
        ORPHAN_AGENT_CHROME_PIDS="${ORPHAN_AGENT_CHROME_PIDS}${chrome_pid}
"
    fi
done < <(pgrep -f 'agent-browser-chrome-' 2>/dev/null || true)
CHROME_COUNT=$(printf "%s" "$ORPHAN_AGENT_CHROME_PIDS" | sed '/^[[:space:]]*$/d' | wc -l | tr -d ' ')
if [ "$CHROME_COUNT" -gt 0 ]; then
    echo "🧹 Killing $CHROME_COUNT orphaned agent-browser Chrome process(es)..."
    while IFS= read -r chrome_pid; do
        [ -n "$chrome_pid" ] || continue
        kill -9 "$chrome_pid" 2>/dev/null || true
    done <<EOF
$ORPHAN_AGENT_CHROME_PIDS
EOF
    echo "✅ Orphaned agent-browser Chrome processes cleared"
else
    echo "✅ No orphaned agent-browser Chrome processes"
fi

# Clean up stale agent-browser runtime state (dead PID/socket files)
# Prevents "CDP response channel closed" errors from leftover state.
for ab_dir in "$HOME/.agent-browser" "/tmp/.agent-browser"; do
    if [ -d "$ab_dir" ]; then
        for pidfile in "$ab_dir"/*.pid; do
            [ -f "$pidfile" ] || continue
            ab_pid=$(cat "$pidfile" 2>/dev/null | tr -d '[:space:]')
            if [ -n "$ab_pid" ] && ! kill -0 "$ab_pid" 2>/dev/null; then
                base="${pidfile%.pid}"
                echo "🧹 Cleaning stale agent-browser state: PID $ab_pid ($(basename "$pidfile"))"
                rm -f "$pidfile" "${base}.sock" "${base}.stream" "${base}.engine" "${base}.version"
            fi
        done
    fi
done
fi

# PLAT-072: stamp the platform revision into the binary.
#
# Findings record what they observed but not what they observed it against, so a
# fixed problem is indistinguishable from a live one and staleness becomes a
# memory test. Go's own VCS stamping cannot supply this here: a go.work at
# /Users/mipl/ai-work puts the build in workspace mode, which disables -buildvcs
# silently (even -buildvcs=true produces no vcs.* settings), so the revision has
# to be injected explicitly.
#
# An empty value is a legitimate "unknown" and is handled as such by the reader;
# it must never be treated as "old".
PLATFORM_REVISION="$(git -C "$(dirname "$0")/.." rev-parse --short=12 HEAD 2>/dev/null || echo "")"
if [ -n "$PLATFORM_REVISION" ] && [ -n "$(git -C "$(dirname "$0")/.." status --porcelain 2>/dev/null)" ]; then
    PLATFORM_REVISION="${PLATFORM_REVISION}+dirty"
fi
GO_LDFLAGS="-X github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow.injectedPlatformVersion=${PLATFORM_REVISION}"

# go run places the agent binary in a temporary build directory, so it cannot
# discover release-style downloads beside its executable. Package the native
# CLI for this machine and point the development server at that directory.
if [ -z "${AGENTWORKS_CLI_DOWNLOAD_DIR:-}" ]; then
    export AGENTWORKS_CLI_DOWNLOAD_DIR="${SCRIPT_DIR}/bin/cli-downloads"
    bash "${SCRIPT_DIR}/../scripts/package-agentworks-cli-local.sh" "$AGENTWORKS_CLI_DOWNLOAD_DIR" || exit 1
fi

if [ -n "$PLATFORM_REVISION" ]; then
    echo "🔖 Platform revision: $PLATFORM_REVISION"
else
    echo "🔖 Platform revision: unknown (not a git checkout) — findings will record no version"
fi

# Run the server with all the enhanced configuration
echo "🚀 Starting server with 'go run'..."

if [ "$BACKGROUND_MODE" = true ]; then
    # Background mode: run in background and capture PID
    echo "🔄 Starting server in background mode..."
    nohup go run -ldflags "$GO_LDFLAGS" main.go server \
        --port "$AGENT_PORT" \
        --log-level debug \
        --debug \
        --provider "$DEEP_SEARCH_MAIN_LLM_PROVIDER" \
        --model "$DEEP_SEARCH_MAIN_LLM_MODEL" \
        --temperature "$DEEP_SEARCH_MAIN_LLM_TEMPERATURE" \
        --max-turns 500 \
        --mcp-config "configs/mcp_servers_clean.json" >> "$LOG_PATH" 2>&1 &
    
    SERVER_PID=$!
    echo "✅ Server started in background (PID: $SERVER_PID)"
    echo "📝 Logs are being written to: $LOG_PATH"
    echo "🌐 Agent API URL: $MCP_AGENT_SERVER_URL"

    if [ "$WITH_FRONTEND" = true ]; then
        wait_for_agent_health || { stop_native_workspace; exit 1; }
        start_frontend_dev || { stop_native_workspace; exit 1; }
        start_electron || { stop_frontend_dev; stop_native_workspace; exit 1; }
    else
        sleep 3
        if ! kill -0 $SERVER_PID 2>/dev/null; then
            echo "❌ Error: Server process died immediately. Check logs: $LOG_PATH"
            tail -20 "$LOG_PATH"
            if [ "$WITH_WORKSPACE" = true ]; then
                stop_native_workspace
            fi
            exit 1
        fi
        if port_in_use "$AGENT_PORT"; then
            echo "✅ Server is running and listening on port $AGENT_PORT"
        else
            echo "⚠️  Warning: Server process is running but not listening on port $AGENT_PORT yet"
            echo "   Check logs: $LOG_PATH"
        fi
    fi

    echo "🛑 To stop the server, run: kill $SERVER_PID"
    if [ "$WITH_WORKSPACE" = true ]; then
        echo "✅ Native workspace is running in background (PID: $WORKSPACE_PID)"
        echo "📝 Workspace logs are being written to: $WORKSPACE_LOG_PATH"
        echo "🌐 Workspace health: ${WORKSPACE_API_URL%/}/health"
    fi
    if [ "$WITH_FRONTEND" = true ]; then
        echo "✅ Vite dev server is running in background (PID: $FRONTEND_PID)"
        echo "📝 Frontend logs: $FRONTEND_LOG_PATH"
        if [ -n "$ELECTRON_PID" ]; then
            echo "✅ Electron is running in background (PID: $ELECTRON_PID)"
            echo "📝 Electron logs: $ELECTRON_LOG_PATH"
        fi
        echo "🛑 To stop all, run: kill $SERVER_PID $FRONTEND_PID${ELECTRON_PID:+ $ELECTRON_PID}${WORKSPACE_PID:+ $WORKSPACE_PID}"
    fi
elif [ "$WITH_FRONTEND" = true ]; then
    # Foreground + frontend: detach server so we can start frontend after it's healthy,
    # then tail the server log so the user still sees server output.
    echo "🔄 Starting server in foreground mode (with frontend)..."
    echo "   Agent API URL: $MCP_AGENT_SERVER_URL"
    nohup go run -ldflags "$GO_LDFLAGS" main.go server \
        --port "$AGENT_PORT" \
        --log-level debug \
        --debug \
        --provider "$DEEP_SEARCH_MAIN_LLM_PROVIDER" \
        --model "$DEEP_SEARCH_MAIN_LLM_MODEL" \
        --temperature "$DEEP_SEARCH_MAIN_LLM_TEMPERATURE" \
        --max-turns 500 \
        --mcp-config "configs/mcp_servers_clean.json" >> "$LOG_PATH" 2>&1 &

    SERVER_PID=$!
    wait_for_agent_health || exit 1
    start_frontend_dev || exit 1
    start_electron || exit 1

    echo ""
    echo "✅ All services running:"
    echo "   - Agent server (PID: $SERVER_PID) — $MCP_AGENT_SERVER_URL"
    echo "   - Vite (PID: $FRONTEND_PID) — http://127.0.0.1:${FRONTEND_PORT}"
    [ -n "$ELECTRON_PID" ] && echo "   - Electron (PID: $ELECTRON_PID)"
    echo "   Logs: $LOG_PATH (server), $FRONTEND_LOG_PATH (vite)${ELECTRON_PID:+, $ELECTRON_LOG_PATH (electron)}"
    echo "   Press Ctrl+C to stop all."
    echo ""
    wait "$SERVER_PID"
else
    # Foreground mode: run server in background so the INT trap fires immediately
    # on Ctrl+C. Without &, bash cannot process the trap until the foreground
    # command returns, causing Ctrl+C to appear stuck with no output.
    echo "🔄 Starting server in foreground mode..."
    echo "   (Press Ctrl+C to stop)"
    echo "   Agent API URL: $MCP_AGENT_SERVER_URL"
    echo ""

    go run -ldflags "$GO_LDFLAGS" main.go server \
        --port "$AGENT_PORT" \
        --log-level debug \
        --debug \
        --provider "$DEEP_SEARCH_MAIN_LLM_PROVIDER" \
        --model "$DEEP_SEARCH_MAIN_LLM_MODEL" \
        --temperature "$DEEP_SEARCH_MAIN_LLM_TEMPERATURE" \
        --max-turns 500 \
        --mcp-config "configs/mcp_servers_clean.json" >> "$LOG_PATH" 2>&1 &

    SERVER_PID=$!
    wait "$SERVER_PID"
    EXIT_CODE=$?
    if [ $EXIT_CODE -ne 0 ] && [ $EXIT_CODE -ne 130 ]; then
        echo ""
        echo "❌ Error: Server exited with code $EXIT_CODE"
        echo "📝 Check logs for details: $LOG_PATH"
        if [ -f "$LOG_PATH" ]; then
            echo ""
            echo "Last 20 lines of log file:"
            tail -20 "$LOG_PATH"
        fi
        exit $EXIT_CODE
    fi
fi
