#!/usr/bin/env bash
# Shared Vault lifecycle for rootless Linux and RTS. Vault's service stays
# private; only the product's SSO-bound /api/vault/mcp is internet-facing.

vault_build() {
  local repo_root="$1" build_dir="$2"
  (cd "$repo_root/mcp-gateway" && GOWORK=off GOOS=linux GOARCH="${BUILD_ARCH:-amd64}" CGO_ENABLED=0 go build -o "$build_dir/bin/agentworks-vault" ./cmd/server)
  install -m 0755 "$repo_root/deploy/common/install-vault-service.py" "$build_dir/bin/install-vault-service.py"
}

# Check the staged assets before creating persistent state or stopping services.
vault_check_build() {
  [[ "${VAULT_ENABLED:-false}" == "true" ]] || return 0
  [[ -x "$1/bin/agentworks-vault" && -x "$1/bin/install-vault-service.py" ]] || {
    echo "Vault is enabled but this release lacks its binary or installer; create a new shared build." >&2
    return 1
  }
}

# Render before stopping services so missing audit configuration fails early.
vault_prepare() {
  [[ "${VAULT_ENABLED:-false}" == "true" ]] || return 0
  python3 "$1/bin/install-vault-service.py" --app-dir "$2" --docs-dir "$3" --product "$4" --agent-port "$5" --vault-port "$6"
}

vault_install() {
  [[ "${VAULT_ENABLED:-false}" == "true" ]] || return 0
  local build_dir="$1" app_dir="$2" product="$3"
  # The storage owner is the only configuration writer, including bootstrap.
  VAULT_RESTART_PRODUCT="$product"
  systemctl --user stop "$product-vault.service" 2>/dev/null || true
  (
    set -a
    . "$app_dir/state/vault/service.env"
    set +a
    GATEWAY_BOOTSTRAP_ONLY=1 "$build_dir/bin/agentworks-vault"
  )
}

vault_start() {
  [[ "${VAULT_ENABLED:-false}" == "true" ]] || return 0
  local product="$1" port="$2"
  systemctl --user daemon-reload
  systemctl --user enable "$product-vault.service"
  systemctl --user restart "$product-vault.service"
  systemctl --user is-active "$product-vault.service"
  # Startup includes opening the audit backend; active alone is insufficient.
  local attempt
  for attempt in {1..30}; do
    if curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; then VAULT_RESTART_PRODUCT=""; return 0; fi
    sleep 1
  done
  echo "Vault failed to start. Check $product-vault.service and audit configuration." >&2
  return 1
}

# Called by the deployment EXIT trap if bootstrap or release activation fails.
# Restart from the current release so an interrupted update does not leave an
# existing Vault service stopped. Preserve the original deployment failure.
vault_recover() {
  if [[ -n "${VAULT_RESTART_PRODUCT:-}" ]]; then
    systemctl --user daemon-reload >/dev/null 2>&1 || true
    systemctl --user restart "$VAULT_RESTART_PRODUCT-vault.service" >/dev/null 2>&1 || true
  fi
}
