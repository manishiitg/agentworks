#!/usr/bin/env bash
# Shared Vault lifecycle for rootless Linux and server A. Vault's service stays
# private; only the product's SSO-bound /api/vault/mcp is internet-facing.

vault_build() {
  local repo_root="$1" build_dir="$2"
  # App-only commits must not change Vault bytes through VCS/worktree metadata.
  (cd "$repo_root/mcp-gateway" && GOWORK=off GOOS=linux GOARCH="${BUILD_ARCH:-amd64}" CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$build_dir/bin/agentworks-vault" ./cmd/server)
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
  VAULT_BUILD_DIR="$1"; VAULT_APP_DIR="$2"
  VAULT_PREPARE_SCRIPT="$1/bin/install-vault-service.py"
  VAULT_PREPARE_ARGS=(--app-dir "$2" --docs-dir "$3" --product "$4" --agent-port "$5" --vault-port "$6" --build-dir "$1")
  VAULT_REUSE_PRODUCT=""
  python3 "$VAULT_PREPARE_SCRIPT" "${VAULT_PREPARE_ARGS[@]}"
  if python3 "$VAULT_PREPARE_SCRIPT" "${VAULT_PREPARE_ARGS[@]}" --check-running 2>/dev/null; then
    VAULT_REUSE_PRODUCT="$4"
  fi
}

vault_install() {
  [[ "${VAULT_ENABLED:-false}" == "true" ]] || return 0
  local build_dir="$1" app_dir="$2" product="$3"
  if [[ "${VAULT_REUSE_PRODUCT:-}" == "$product" ]]; then
    echo '[VAULT_DEPLOY] event=bootstrap_skipped reason=unchanged_running_service'
    return 0
  fi
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
  if [[ "${VAULT_REUSE_PRODUCT:-}" == "$product" ]]; then
    # Recheck after the switch; a crash or intervening edit must not be reused.
    if python3 "$VAULT_PREPARE_SCRIPT" "${VAULT_PREPARE_ARGS[@]}" --check-running 2>/dev/null; then
      echo '[VAULT_DEPLOY] event=kept_running reason=unchanged_healthy'
      return 0
    fi
    VAULT_REUSE_PRODUCT=""
    vault_install "$VAULT_BUILD_DIR" "$VAULT_APP_DIR" "$product"
  fi
  systemctl --user daemon-reload
  systemctl --user enable "$product-vault.service"
  systemctl --user restart "$product-vault.service"
  systemctl --user is-active "$product-vault.service"
  # Startup includes opening the audit backend; active alone is insufficient.
  local attempt
  for attempt in {1..30}; do
    if curl --fail --silent --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null; then
      VAULT_RESTART_PRODUCT=""
      if ! python3 "$VAULT_PREPARE_SCRIPT" "${VAULT_PREPARE_ARGS[@]}" --record-running; then
        echo '[VAULT_DEPLOY] event=identity_not_saved next_deploy=restart' >&2
      else
        echo '[VAULT_DEPLOY] event=identity_saved'
      fi
      return 0
    fi
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
