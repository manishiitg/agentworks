#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RUNNER="${AGENTWORKS_RUNNER:-${REPO_ROOT}/agent_go/run_server_with_logging.sh}"

INSTANCE_ID=""
STATE_ROOT=""
AGENT_PORT_VALUE=19743
WORKSPACE_PORT_VALUE=19744
GATEWAY_PORT_VALUE=19745
FRONTEND_PORT_VALUE=52733
ELECTRON_DEBUG_PORT_VALUE=19233
# Isolated development is browser-only unless Electron is explicitly requested.
# This keeps a renderer leak from consuming system-wide memory during ordinary
# frontend/backend work.
BROWSER_ONLY=true
BUILD_FRONTEND=false
DRY_RUN=false
ISOLATE_WORKFLOW_CLI=true
APP_NAME_VALUE="AgentWorks"
FAVICON_URL_VALUE="/logo.svg"

usage() {
    printf '%s\n' \
        "Usage: $0 --instance <id> [options]" \
        "" \
        "Runs a named AgentWorks development instance with isolated runtime state." \
        "" \
        "Options:" \
        "  --instance <id>                 Required lowercase instance name" \
        "  --state-root <absolute-path>    State directory (default: <repo>/.local/agentworks-instances/<id>)" \
        "  --agent-port <port>             Agent API port (default: 19743)" \
        "  --workspace-port <port>         Workspace API port (default: 19744)" \
        "  --gateway-port <port>          Vault port (default: 19745)" \
        "  --frontend-port <port>          Frontend port (default: 52733)" \
        "  --electron-debug-port <port>    Electron CDP port (default: 19233)" \
        "  --app-name <name>               Browser/Electron page title" \
        "  --favicon-url <root-path>       Same-origin favicon path" \
        "  --browser-only                  Serve the frontend without launching Electron (default)" \
        "  --electron                      Explicitly launch the isolated Electron desktop app" \
        "  --build                         Build and preview the frontend" \
        "  --isolate-workflow-cli          Test private per-chat CLI instructions (PLAT-296)" \
        "  --no-isolate-workflow-cli       Explicit rollback to shared workflow CLI instructions" \
        "  --dry-run                       Validate and print the isolation configuration" \
        "  -h, --help                      Show this help"
}

require_value() {
    local option="$1"
    local value="${2:-}"
    if [ -z "$value" ]; then
        echo "Error: $option requires a value" >&2
        usage >&2
        exit 2
    fi
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --instance)
            require_value "$1" "${2:-}"
            INSTANCE_ID="$2"
            shift 2
            ;;
        --state-root)
            require_value "$1" "${2:-}"
            STATE_ROOT="$2"
            shift 2
            ;;
        --agent-port)
            require_value "$1" "${2:-}"
            AGENT_PORT_VALUE="$2"
            shift 2
            ;;
        --workspace-port)
            require_value "$1" "${2:-}"
            WORKSPACE_PORT_VALUE="$2"
            shift 2
            ;;
        --gateway-port)
            require_value "$1" "${2:-}"
            GATEWAY_PORT_VALUE="$2"
            shift 2
            ;;
        --frontend-port)
            require_value "$1" "${2:-}"
            FRONTEND_PORT_VALUE="$2"
            shift 2
            ;;
        --electron-debug-port)
            require_value "$1" "${2:-}"
            ELECTRON_DEBUG_PORT_VALUE="$2"
            shift 2
            ;;
        --app-name)
            require_value "$1" "${2:-}"
            APP_NAME_VALUE="$2"
            shift 2
            ;;
        --favicon-url)
            require_value "$1" "${2:-}"
            FAVICON_URL_VALUE="$2"
            shift 2
            ;;
        --browser-only)
            BROWSER_ONLY=true
            shift
            ;;
        --electron)
            BROWSER_ONLY=false
            shift
            ;;
        --build)
            BUILD_FRONTEND=true
            shift
            ;;
        --isolate-workflow-cli)
            ISOLATE_WORKFLOW_CLI=true
            shift
            ;;
        --no-isolate-workflow-cli)
            ISOLATE_WORKFLOW_CLI=false
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Error: unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ ! "$INSTANCE_ID" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
    echo "Error: --instance must match [a-z0-9][a-z0-9-]*" >&2
    exit 2
fi

if [ "${#APP_NAME_VALUE}" -gt 120 ] || [[ "$APP_NAME_VALUE" == *$'\n'* ]] || [[ "$APP_NAME_VALUE" == *$'\r'* ]]; then
    echo "Error: --app-name must be a single line of at most 120 characters" >&2
    exit 2
fi
if [[ "$FAVICON_URL_VALUE" != /* ]] || [[ "$FAVICON_URL_VALUE" == //* ]] || [[ "$FAVICON_URL_VALUE" == *$'\n'* ]] || [[ "$FAVICON_URL_VALUE" == *$'\r'* ]]; then
    echo "Error: --favicon-url must be a same-origin root-relative path" >&2
    exit 2
fi

if [ ! -x "$RUNNER" ]; then
    echo "Error: AgentWorks runner is not executable: $RUNNER" >&2
    exit 2
fi

validate_port() {
    local label="$1"
    local port="$2"
    if [[ ! "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1024 ] || [ "$port" -gt 65535 ]; then
        echo "Error: $label must be an integer from 1024 to 65535" >&2
        exit 2
    fi
}

validate_port "--agent-port" "$AGENT_PORT_VALUE"
validate_port "--workspace-port" "$WORKSPACE_PORT_VALUE"
validate_port "--gateway-port" "$GATEWAY_PORT_VALUE"
validate_port "--frontend-port" "$FRONTEND_PORT_VALUE"
validate_port "--electron-debug-port" "$ELECTRON_DEBUG_PORT_VALUE"

for other_port in "$AGENT_PORT_VALUE" "$WORKSPACE_PORT_VALUE" "$FRONTEND_PORT_VALUE" "$ELECTRON_DEBUG_PORT_VALUE"; do
    if [ "$GATEWAY_PORT_VALUE" = "$other_port" ]; then
        echo "Error: every instance port must be unique" >&2
        exit 2
    fi
done

if [ "$AGENT_PORT_VALUE" = "$WORKSPACE_PORT_VALUE" ] || \
   [ "$AGENT_PORT_VALUE" = "$FRONTEND_PORT_VALUE" ] || \
   [ "$AGENT_PORT_VALUE" = "$ELECTRON_DEBUG_PORT_VALUE" ] || \
   [ "$WORKSPACE_PORT_VALUE" = "$FRONTEND_PORT_VALUE" ] || \
   [ "$WORKSPACE_PORT_VALUE" = "$ELECTRON_DEBUG_PORT_VALUE" ] || \
   [ "$FRONTEND_PORT_VALUE" = "$ELECTRON_DEBUG_PORT_VALUE" ]; then
    echo "Error: every instance port must be unique" >&2
    exit 2
fi

if [ -z "$STATE_ROOT" ]; then
    STATE_ROOT="${REPO_ROOT}/.local/agentworks-instances/${INSTANCE_ID}"
elif [[ "$STATE_ROOT" != /* ]]; then
    echo "Error: --state-root must be an absolute path" >&2
    exit 2
fi

mkdir -p "$STATE_ROOT"
STATE_ROOT="$(cd "$STATE_ROOT" && pwd)"

case "$STATE_ROOT" in
    /|"$HOME"|"$REPO_ROOT")
        echo "Error: refusing broad state root: $STATE_ROOT" >&2
        exit 2
        ;;
esac

ELECTRON_DATA_DIR="${STATE_ROOT}/electron"
WORKSPACE_DOCS_DIR="${STATE_ROOT}/workspace-docs"
LOG_DIR="${STATE_ROOT}/logs"
CACHE_DIR="${STATE_ROOT}/cache"
STATE_KEY="$(printf '%s' "$STATE_ROOT" | shasum -a 256 | awk '{print substr($1, 1, 10)}')"
# tmux adds its own socket components below TMUX_TMPDIR. Keep this path short
# enough for macOS's Unix-domain socket limit while retaining instance ownership.
TMUX_DIR="/tmp/aw-tmux-${INSTANCE_ID}-${STATE_KEY}"
BIN_DIR="${STATE_ROOT}/bin"
BROWSER_DIR="${STATE_ROOT}/browser"
ENV_FILE="${STATE_ROOT}/environment.env"
RUNTIME_CONFIG_FILE="${STATE_ROOT}/runtime-config.js"
LOCK_DIR="${STATE_ROOT}/instance.lock"

for directory in "$ELECTRON_DATA_DIR" "$WORKSPACE_DOCS_DIR" "$LOG_DIR" "$CACHE_DIR" "$TMUX_DIR" "$BIN_DIR" "$BROWSER_DIR"; do
    mkdir -p "$directory"
done
chmod 700 "$STATE_ROOT" "$ELECTRON_DATA_DIR" "$TMUX_DIR" "$BROWSER_DIR" 2>/dev/null || true

if [ ! -f "${BROWSER_DIR}/config.json" ]; then
    printf '{}\n' > "${BROWSER_DIR}/config.json"
fi

print_configuration() {
    printf '%s\n' \
        "AGENTWORKS_INSTANCE_ID=${INSTANCE_ID}" \
        "AGENTWORKS_STATE_ROOT=${STATE_ROOT}" \
        "CLI_UPDATE_ENABLED=${CLI_UPDATE_ENABLED:-false}" \
        "AGENTWORKS_ISOLATE_WORKFLOW_CLI=${ISOLATE_WORKFLOW_CLI}" \
        "AGENT_PORT=${AGENT_PORT_VALUE}" \
        "WORKSPACE_PORT=${WORKSPACE_PORT_VALUE}" \
        "GATEWAY_PORT=${GATEWAY_PORT_VALUE}" \
        "FRONTEND_PORT=${FRONTEND_PORT_VALUE}" \
        "ELECTRON_REMOTE_DEBUG_PORT=${ELECTRON_DEBUG_PORT_VALUE}" \
        "RUNLOOP_USER_DATA_DIR=${ELECTRON_DATA_DIR}" \
        "WORKSPACE_DOCS_PATH=${WORKSPACE_DOCS_DIR}" \
        "AGENTWORKS_LOG_DIR=${LOG_DIR}" \
        "MCP_CACHE_DIR=${CACHE_DIR}" \
        "TMUX_TMPDIR=${TMUX_DIR}" \
        "AGENTWORKS_BROWSER_SESSION_PREFIX=${INSTANCE_ID}" \
        "AGENT_BROWSER_CONFIG=${BROWSER_DIR}/config.json" \
        "AGENTWORKS_ENV_FILE=${ENV_FILE}" \
        "AGENTWORKS_RUNTIME_CONFIG_PATH=${RUNTIME_CONFIG_FILE}" \
        "AGENTWORKS_APP_NAME=${APP_NAME_VALUE}" \
        "AGENTWORKS_FAVICON_URL=${FAVICON_URL_VALUE}" \
        "BROWSER_ONLY=${BROWSER_ONLY}" \
        "BUILD_FRONTEND=${BUILD_FRONTEND}"
}

if [ "$DRY_RUN" = true ]; then
    print_configuration
    exit 0
fi

for port in "$AGENT_PORT_VALUE" "$WORKSPACE_PORT_VALUE" "$GATEWAY_PORT_VALUE" "$FRONTEND_PORT_VALUE" "$ELECTRON_DEBUG_PORT_VALUE"; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "Error: port $port is already owned by another process; refusing to start" >&2
        exit 1
    fi
done

if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    existing_pid="$(sed -n '1p' "${LOCK_DIR}/pid" 2>/dev/null || true)"
    if [ -n "$existing_pid" ] && kill -0 "$existing_pid" 2>/dev/null; then
        echo "Error: instance ${INSTANCE_ID} is already running as launcher PID ${existing_pid}" >&2
        exit 1
    fi
    rm -f "${LOCK_DIR}/pid" "${LOCK_DIR}/runner.pid"
    rmdir "$LOCK_DIR" 2>/dev/null || {
        echo "Error: stale instance lock could not be reclaimed: $LOCK_DIR" >&2
        exit 1
    }
    mkdir "$LOCK_DIR"
fi
printf '%s\n' "$$" > "${LOCK_DIR}/pid"

RUNNER_PID=""
release_instance_lock() {
    rm -f "${LOCK_DIR}/pid" "${LOCK_DIR}/runner.pid"
    rmdir "$LOCK_DIR" 2>/dev/null || true
}
stop_runner() {
    if [ -n "$RUNNER_PID" ] && kill -0 "$RUNNER_PID" 2>/dev/null; then
        kill -TERM "$RUNNER_PID" 2>/dev/null || true
        wait "$RUNNER_PID" 2>/dev/null || true
    fi
}
handle_signal() {
    stop_runner
    exit 130
}
trap release_instance_lock EXIT
trap handle_signal INT TERM

export AGENTWORKS_INSTANCE_ID="$INSTANCE_ID"
export AGENTWORKS_STATE_ROOT="$STATE_ROOT"
# Test instances opt in explicitly to network CLI installations.
export CLI_UPDATE_ENABLED="${CLI_UPDATE_ENABLED:-false}"
export AGENTWORKS_ISOLATE_WORKFLOW_CLI="$ISOLATE_WORKFLOW_CLI"
export AGENTWORKS_ENV_FILE="$ENV_FILE"
export AGENTWORKS_RUNTIME_CONFIG_PATH="$RUNTIME_CONFIG_FILE"
export AGENTWORKS_APP_NAME="$APP_NAME_VALUE"
export AGENTWORKS_FAVICON_URL="$FAVICON_URL_VALUE"
export AGENTWORKS_LOG_DIR="$LOG_DIR"
export AGENTWORKS_STRICT_PROCESS_OWNERSHIP=true
export AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP=true
export AGENTWORKS_SKIP_GLOBAL_DEPENDENCY_UPDATES=true
export AGENTWORKS_BROWSER_SESSION_PREFIX="$INSTANCE_ID"
export AGENTWORKS_ELECTRON_RSS_LIMIT_MB="${AGENTWORKS_ELECTRON_RSS_LIMIT_MB:-3072}"
export AGENT_BROWSER_CONFIG="${BROWSER_DIR}/config.json"
export AGENT_PORT="$AGENT_PORT_VALUE"
export WORKSPACE_PORT="$WORKSPACE_PORT_VALUE"
export GATEWAY_PORT="$GATEWAY_PORT_VALUE"
export FRONTEND_PORT="$FRONTEND_PORT_VALUE"
export ELECTRON_REMOTE_DEBUG_PORT="$ELECTRON_DEBUG_PORT_VALUE"
export RUNLOOP_USER_DATA_DIR="$ELECTRON_DATA_DIR"
export RUNLOOP_DOCS_DIR="$WORKSPACE_DOCS_DIR"
export WORKSPACE_DOCS_PATH="$WORKSPACE_DOCS_DIR"
export MCP_CACHE_DIR="$CACHE_DIR"
export TMUX_TMPDIR="$TMUX_DIR"
export GOBIN="$BIN_DIR"
export PATH="${BIN_DIR}:${PATH}"

RUNNER_ARGS=(--with-workspace --with-frontend)
if [ "$BROWSER_ONLY" = true ]; then
    RUNNER_ARGS+=(--without-electron)
fi
if [ "$BUILD_FRONTEND" = true ]; then
    RUNNER_ARGS+=(--build)
fi

echo "Starting isolated AgentWorks instance '${INSTANCE_ID}'"
print_configuration

"$RUNNER" "${RUNNER_ARGS[@]}" &
RUNNER_PID=$!
printf '%s\n' "$RUNNER_PID" > "${LOCK_DIR}/runner.pid"

set +e
wait "$RUNNER_PID"
RUNNER_STATUS=$?
set -e
RUNNER_PID=""
exit "$RUNNER_STATUS"
