#!/usr/bin/env bash
# Install Vault and initialize its project database before starting the service.
# Existing services must be stopped first: the database permits one writer.
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_DIR="${VAULT_INSTALL_DIR:-$HOME/.local/bin}"
STATE_DIR="${GATEWAY_STATE_DIR:-$HOME/.local/share/agentworks/vault}"
WORKSPACE_DIR="${GATEWAY_WORKSPACE_DIR:-}"
SOURCE_BINARY=""
usage() {
  echo 'Usage: install-vault.sh --workspace-dir PATH [--state-dir PATH] [--install-dir PATH] [--binary PATH]'
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --workspace-dir|--state-dir|--install-dir|--binary)
      [[ $# -ge 2 && -n "$2" ]] || { usage >&2; exit 2; }
      case "$1" in
        --workspace-dir) WORKSPACE_DIR="$2" ;;
        --state-dir) STATE_DIR="$2" ;;
        --install-dir) INSTALL_DIR="$2" ;;
        --binary) SOURCE_BINARY="$2" ;;
      esac
      shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done
[[ -n "$WORKSPACE_DIR" ]] || { echo 'A Vault project folder is required.' >&2; usage >&2; exit 2; }
mkdir -p "$INSTALL_DIR"
STAGE="$(mktemp "$INSTALL_DIR/.vault-install.XXXXXX")"
trap 'rm -f "$STAGE"' EXIT
if [[ -n "$SOURCE_BINARY" ]]; then
  [[ -x "$SOURCE_BINARY" ]] || { echo 'The supplied Vault binary is not executable.' >&2; exit 1; }
  cp "$SOURCE_BINARY" "$STAGE"
else
  command -v go >/dev/null || { echo 'Go is required when no --binary is supplied.' >&2; exit 1; }
  (cd "$REPO_ROOT/mcp-gateway" && go build -o "$STAGE" ./cmd/server)
fi
chmod 700 "$STAGE"
# Reuse the server's validated, transactional bootstrap rather than editing SQL.
# No connectors, tool grants, or secret grants are seeded by installation.
GATEWAY_STATE_DIR="$STATE_DIR" GATEWAY_WORKSPACE_DIR="$WORKSPACE_DIR" \
  GATEWAY_BOOTSTRAP_ONLY=1 "$STAGE"
mv "$STAGE" "$INSTALL_DIR/agentworks-vault"
echo "Installed $INSTALL_DIR/agentworks-vault; Platform group initialized."
echo 'Start the service with the same GATEWAY_STATE_DIR and GATEWAY_WORKSPACE_DIR.'
