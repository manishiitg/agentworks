#!/usr/bin/env bash
set -euo pipefail
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export GOMAXPROCS=3
export GOFLAGS=-p=2
export GOWORK=off
# Synthetic workspace only. The run store is needed for manual program calls;
# this launcher creates no clock schedules and copies no accounts or live data.
export SCHEDULER_ENABLED=true
requirements="$repo/agent_go/pkg/relaypython/requirements-dbos.txt"
digest="$(shasum -a 256 "$requirements" | awk '{print substr($1,1,16)}')"
runtime="$repo/.local/crew-lab-runtime/relay-dbos-$digest"
expected="$(sed -n 's/^dbos==//p' "$requirements")"
if ! "$runtime/bin/python" -I -c 'import dbos, importlib.metadata, sys; assert importlib.metadata.version("dbos") == sys.argv[1]' "$expected" >/dev/null 2>&1; then
  python3 -m venv "$runtime"
  "$runtime/bin/python" -m pip install --disable-pip-version-check -r "$requirements"
fi
export RELAY_DBOS_PYTHON="$runtime/bin/python"
export AGENTWORKS_CLI_DOWNLOAD_DIR="$repo/.local/agentworks-instances/unified-crew/cli-downloads"
mkdir -p "$AGENTWORKS_CLI_DOWNLOAD_DIR"
exec "$repo/scripts/run-local-instance.sh" --instance unified-crew \
 --agent-port 20743 --workspace-port 20744 --gateway-port 20745 \
 --frontend-port 53733 --electron-debug-port 20233 \
 --app-name 'AgentWorks Crew Lab' "$@"
