#!/usr/bin/env bash
# Release activation shared by every server deploy (deploy/rootless-linux/build-and-activate.sh and
# deploy/aws-ec2/server/build-and-activate.sh). Source this file on the server, after the release is staged, then call
# activate_release. Each deploy path keeps its own build, upload and transport; from here on every server runs the same
# steps, and what differs between servers is data from the server's product.env.
#
# The caller sets (plain shell variables; the arrays are optional):
#   REPO_ROOT        the source checkout of this revision (the shared build's source/mcp-agent-builder-go)
#   RELEASE_DIR      the staged release folder (APP_ROOT/releases/RELEASE_ID); RELEASE_ID its name
#   APP_ROOT         the app folder: releases/, current, .env, logs/
#   DOCS_ROOT        the workspace documents folder
#   STATE_ROOT       private app state (default APP_ROOT/state)
#   MCP_STATE_DIR    where the agent keeps MCP user config (default STATE_ROOT/mcp)
#   UNIT_PREFIX      systemd --user units are UNIT_PREFIX-agent, -workspace, -gateway (and -vault)
#   SERVICE_ACCOUNT  the Linux account the services run as (default UNIT_PREFIX)
#   AGENT_PORT, WORKSPACE_PORT, VAULT_ENABLED, VAULT_PORT (default WORKSPACE_PORT + 2)
#   RUNTIME_PATH     PATH the services must run with; empty leaves PATH to the unit files
#   EXTRA_ENV[]      KEY=value written to .env and to both services' managed drop-in, then checked in the running processes
#   AGENT_EXTRA_ENV[] the same for the agent only
#   ENV_ONLY_FILE    a file of KEY=value lines written to .env only (secrets, values with spaces); never in argv or drop-ins
#   REMOVE_DEPLOY_DROPINS[], REQUIRED_PERSISTED_ENV_KEYS[], EXPECTED_PUBLIC_URL, PLAYBOOK_SMOKE_PATH, PIN_NODE_VERSION
#   BROWSER_CLI_CHECK_DIR  when set, the sandbox must also run the browser CLI from this folder
#   DRAIN_TIMEOUT_SECONDS  wait for running turns before the restart (default 0: restart at once)
#   DOMAIN, PUBLIC_CHECKS, PUBLIC_HEALTH_STATUS, PUBLIC_CHECK_PATHS[], LOCAL_PROXY_CHECK_URL  the public checks (skipped without DOMAIN)
#   PLATFORM_DOCKER_CHECK  true: fail when the service account has Docker (only a person's own slot may)
#   LIVE_URL         shown in the final messages (default https://DOMAIN)
# deploy/common/slots.sh and deploy/common/vault.sh must be sourced first. The caller's EXIT trap calls activate_recover.

ACTIVATE_STOPPED=0

# Restart the services if the deploy fails while they are stopped, and Vault if it was stopped for its bootstrap.
activate_recover() {
  if declare -F vault_recover >/dev/null; then vault_recover; fi
  if [[ "$ACTIVATE_STOPPED" == 1 ]]; then
    systemctl --user restart "$UNIT_PREFIX-workspace" "$UNIT_PREFIX-agent" "$UNIT_PREFIX-gateway" >/dev/null 2>&1 || true
  fi
}

# Carry the previous release's hashed frontend assets into the new one. A tab opened before the swap still lazy-imports
# chunks by their old hashed names on its next navigation; without these files it fails with "Failed to fetch dynamically
# imported module". Hashed names never collide, so only missing files are copied (-n), mtimes are preserved (-p), and
# anything carried for more than 14 days is dropped so the directory cannot grow forever.
activate_carry_assets() {
  local prev="$1/current/frontend/assets" next="$2/frontend/assets"
  if [[ -d "$prev" && -d "$next" ]]; then
    cp -pn "$prev"/* "$next"/ 2>/dev/null || true
    find "$next" -type f -mtime +14 -delete
  fi
}

activate_wait_for_url() {
  local url="$1" tries=60
  until curl -fsS -o /dev/null --max-time 5 "$url" 2>/dev/null; do
    tries=$((tries - 1))
    [[ "$tries" -gt 0 ]] || { echo "$url did not come up in 2 minutes" >&2; return 1; }
    sleep 2
  done
}

activate_checks() {
  local product="${PRODUCT:-$UNIT_PREFIX}"
  PRODUCT="$product" APP_ROOT="$APP_ROOT" DOCS_ROOT="$DOCS_ROOT" UNIT_PREFIX="$UNIT_PREFIX" \
    SERVICE_ACCOUNT="$SERVICE_ACCOUNT" EXPECTED_PUBLIC_URL="${EXPECTED_PUBLIC_URL:-}" \
    python3 "$REPO_ROOT/deploy/rootless-linux/deployment_checks.py" "$1"
}

activate_release() {
  : "${REPO_ROOT:?}" "${RELEASE_DIR:?}" "${RELEASE_ID:?}" "${APP_ROOT:?}" "${DOCS_ROOT:?}" "${UNIT_PREFIX:?}" "${AGENT_PORT:?}" "${WORKSPACE_PORT:?}"
  STATE_ROOT="${STATE_ROOT:-$APP_ROOT/state}"
  MCP_STATE_DIR="${MCP_STATE_DIR:-$STATE_ROOT/mcp}"
  SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-$UNIT_PREFIX}"
  VAULT_PORT="${VAULT_PORT:-$((WORKSPACE_PORT + 2))}"
  LIVE_URL="${LIVE_URL:-https://${DOMAIN:-}}"
  local env_file="$APP_ROOT/.env" unit_dir="$HOME/.config/systemd/user" runtime_path="${RUNTIME_PATH:-}"
  local agent_dropin="$unit_dir/$UNIT_PREFIX-agent.service.d" workspace_dropin="$unit_dir/$UNIT_PREFIX-workspace.service.d"
  local entry unit pid file obsolete
  echo "==> [$RELEASE_ID] Activating release and restarting services"

  # 1. Settings that must already be stored on the server.
  python3 - "$env_file" "${REQUIRED_PERSISTED_ENV_KEYS[@]:-}" <<'PY'
from pathlib import Path
import sys

required = [key for key in sys.argv[2:] if key]
if required:
    path = Path(sys.argv[1])
    values = {}
    if path.is_file():
        for line in path.read_text().splitlines():
            key, separator, value = line.partition('=')
            if separator:
                values[key] = value.strip().strip('"')
    missing = [key for key in required if not values.get(key)]
    if missing:
        raise SystemExit('Missing required persisted deployment settings: ' + ', '.join(missing))
PY

  # 2. gog (Gmail connector) needs its encrypted file keyring on a headless box; on "auto" it uses the account's
  # gnome-keyring, which has no unlocked default collection, and every Gmail connect fails. Generate the password once and
  # keep it (changing it orphans stored tokens). deployment_checks.py checks it in the env file and the running process.
  if [[ -f "$env_file" ]]; then
    grep -q '^GOG_KEYRING_PASSWORD=' "$env_file" || printf 'GOG_KEYRING_PASSWORD=%s\n' "$(openssl rand -hex 32)" >> "$env_file"
    grep -q '^GOG_KEYRING_BACKEND=' "$env_file" || echo 'GOG_KEYRING_BACKEND=file' >> "$env_file"
    chmod 600 "$env_file"
  fi
  activate_checks preflight

  # 3. The Linux sandbox must really work on this host for this release before it goes live. Shell commands run inside it,
  # and a policy that blocks a path inside a granted one needs a user namespace, which Ubuntu 23.10+ denies by default
  # (kernel.apparmor_restrict_unprivileged_userns=1): a host without it fails every shell command while the other checks pass.
  chmod +x "$RELEASE_DIR"/bin/*
  test -x "$RELEASE_DIR/bin/video-studio-landlock-runner" || { echo "ERROR: the release has no bin/video-studio-landlock-runner." >&2; return 1; }
  [[ -x "$RELEASE_DIR/bin/workspace-security.test" ]] || { echo "ERROR: the release has no bin/workspace-security.test, so the sandbox cannot be proven on this host." >&2; return 1; }
  local sandbox_log
  if ! sandbox_log="$("$RELEASE_DIR/bin/workspace-security.test" -test.run '^TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy$' -test.v 2>&1)"; then
    printf '%s\n' "$sandbox_log" | tail -20 >&2
    echo "ERROR: the Linux sandbox does not work on this host, so shell commands would fail. kernel.apparmor_restrict_unprivileged_userns=$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo n/a)." >&2
    echo "Fix: as root on the host, run: PRODUCT=${PRODUCT:-$UNIT_PREFIX} bash -s -- userns < deploy/common/provision-slots.sh" >&2
    return 1
  fi
  echo "sandbox: proven on this host (mount-namespace fallback test passed)"
  # The browser CLI must start inside the sandbox, not merely be on the host PATH (PLAT-599).
  if [[ -n "${BROWSER_CLI_CHECK_DIR:-}" ]]; then
    AGENTWORKS_BROWSER_CLI_CHECK=1 AGENT_BROWSER_CLI_DIR="$BROWSER_CLI_CHECK_DIR" \
      AGENTWORKS_LANDLOCK_RUNNER="$RELEASE_DIR/bin/video-studio-landlock-runner" \
      "$RELEASE_DIR/bin/workspace-security.test" -test.run '^TestConfiguredBrowserCLIInSandbox$' -test.v
  fi
  # Slot accounts must reach this release's Landlock launcher (releases/ 0711; PLAT-478). No-op without slots.
  slots_release_traversal "$APP_ROOT" "$RELEASE_DIR"
  [[ -d "$APP_ROOT/logs" ]] || install -d -m 0755 "$APP_ROOT/logs"
  ln -sfn "$APP_ROOT/logs" "$RELEASE_DIR/logs"

  # 4. Relays run on a pinned DBOS interpreter outside the releases (reused while its requirements are unchanged). A
  # failure stops the deploy before the switch-over, so a Relay never runs without it.
  source "$REPO_ROOT/deploy/common/relay-dbos.sh"
  local relay_dbos_python
  relay_dbos_python="$(relay_dbos_prepare "$REPO_ROOT" "$APP_ROOT")"
  EXTRA_ENV=("${EXTRA_ENV[@]:-}" "RELAY_DBOS_PYTHON=$relay_dbos_python")

  # 5. Private state, and the one remaining migration: MCP user config moved out of the releases into MCP_STATE_DIR; a
  # copy still in the live release is carried over once and never replaces the durable one.
  install -d -m 0700 "$STATE_ROOT" "$MCP_STATE_DIR"
  for file in "$APP_ROOT"/current/configs/mcp_servers_*_user.json; do
    [[ -f "$file" && ! -e "$MCP_STATE_DIR/$(basename "$file")" ]] || continue
    cp -n "$file" "$MCP_STATE_DIR/$(basename "$file")"
    chmod 0600 "$MCP_STATE_DIR/$(basename "$file")"
  done

  # Per-user accounts: a `tmux` front-end first in PATH sends a slot's sessions to that slot's own tmux server. It lives
  # outside the releases so its path never changes. A host without slots is untouched.
  local slots_shim_dir
  if slots_shim_dir="$(slots_install_shim "$APP_ROOT" "$RELEASE_DIR")" && [[ -n "$slots_shim_dir" && -n "$runtime_path" ]]; then
    runtime_path="$slots_shim_dir:$runtime_path"
  fi

  # 6. Env merge. EnvironmentFile= values win over Environment= for the same variable, so the managed values go into
  # .env. Keys this deploy wrote last time and no longer lists are removed (merge_env.py remembers them in
  # .env.managed-keys); a line added by hand stays.
  if [[ -f "$env_file" ]]; then
    local env_next
    env_next="$(mktemp "$APP_ROOT/.env.runtime.XXXXXX")"
    python3 "$REPO_ROOT/deploy/common/merge_env.py" "$env_file" "$env_next" "$APP_ROOT/.env.managed-keys" "$runtime_path" \
      ${ENV_ONLY_FILE:+--from-file "$ENV_ONLY_FILE"} "${EXTRA_ENV[@]:-}"
    chmod 0600 "$env_next"
    mv "$env_next" "$env_file"
  fi

  # 7. Units: any unit files the release ships, then one managed drop-in per service with the managed environment.
  install -d -m 0755 "$unit_dir" "$agent_dropin" "$workspace_dropin"
  if [[ -d "$RELEASE_DIR/systemd" ]]; then
    for file in "$RELEASE_DIR"/systemd/*.service "$RELEASE_DIR"/systemd/*.timer; do
      [[ -f "$file" ]] && install -m 0644 "$file" "$unit_dir/$(basename "$file")"
    done
    [[ ! -f "$RELEASE_DIR/systemd/$UNIT_PREFIX-logrotate.conf" ]] || install -m 0644 "$RELEASE_DIR/systemd/$UNIT_PREFIX-logrotate.conf" "$STATE_ROOT/logrotate.conf"
  fi
  for obsolete in "${REMOVE_DEPLOY_DROPINS[@]:-}"; do
    [[ -z "$obsolete" ]] || rm -f "$agent_dropin/$obsolete" "$workspace_dropin/$obsolete"
  done
  {
    echo '[Service]'
    [[ -z "$runtime_path" ]] || echo "Environment=PATH=$runtime_path"
    for entry in "${EXTRA_ENV[@]:-}"; do
      [[ -n "$entry" ]] && echo "Environment=$entry"
    done
    echo "Environment=AGENTWORKS_STATE_ROOT=$STATE_ROOT"
  } > "$workspace_dropin/zz-deploy-managed.conf"
  {
    cat "$workspace_dropin/zz-deploy-managed.conf"
    # The agent serves renderer assets from the same packaged frontend as the gateway.
    echo "Environment=STATIC_DIR=$APP_ROOT/current/frontend"
    for entry in "${AGENT_EXTRA_ENV[@]:-}"; do
      [[ -n "$entry" ]] && echo "Environment=$entry"
    done
  } > "$agent_dropin/zz-deploy-managed.conf"

  # Render Vault's service before stopping anything, so a missing audit configuration fails early.
  vault_prepare "$RELEASE_DIR" "$APP_ROOT" "$DOCS_ROOT" "$UNIT_PREFIX" "$AGENT_PORT" "$VAULT_PORT"
  XDG_RUNTIME_DIR="/run/user/$(id -u)"
  export XDG_RUNTIME_DIR

  # 8. Drain: wait up to DRAIN_TIMEOUT_SECONDS for running turns (the agent's /health "drain" block). The default is 0, a
  # restart at once (owner: deploy fast); an agent without the field drains immediately.
  local deadline h idle
  deadline=$(($(date +%s) + ${DRAIN_TIMEOUT_SECONDS:-0}))
  while :; do
    h="$(curl -s --max-time 5 "http://127.0.0.1:$AGENT_PORT/api/health" || true)"
    idle="$(printf '%s' "$h" | python3 -c 'import json,sys
try:
  d=json.load(sys.stdin)
  print("false" if d.get("drain",{}).get("idle") is False else "true")
except Exception:
  print("true")' 2>/dev/null || echo true)"
    [[ "$idle" == "true" ]] && { echo "drain: agent idle, restarting"; break; }
    if [[ $(date +%s) -ge $deadline ]]; then
      echo "drain: still busy after ${DRAIN_TIMEOUT_SECONDS:-0}s; restarting anyway" >&2
      break
    fi
    echo "drain: waiting for in-flight turns"
    sleep 5
  done

  # 9. Switch: stop the old agent, point current at the new release, bootstrap Vault, then start everything on it. If a
  # step fails while the services are stopped, activate_recover (the caller's EXIT trap) restarts them.
  ACTIVATE_STOPPED=1
  systemctl --user stop "$UNIT_PREFIX-agent"
  ln -sfn "$RELEASE_DIR" "$APP_ROOT/current"
  vault_install "$RELEASE_DIR" "$APP_ROOT" "$UNIT_PREFIX"
  systemctl --user daemon-reload
  if [[ -d "$RELEASE_DIR/systemd" ]]; then
    for file in "$RELEASE_DIR"/systemd/*.timer; do
      [[ -f "$file" ]] && systemctl --user enable --now "$(basename "$file")"
    done
  fi
  systemctl --user restart "$UNIT_PREFIX-workspace"
  sleep 2
  vault_start "$UNIT_PREFIX" "$VAULT_PORT"
  systemctl --user restart "$UNIT_PREFIX-agent"
  sleep 2
  systemctl --user restart "$UNIT_PREFIX-gateway"
  ACTIVATE_STOPPED=0
  sleep 2
  systemctl --user is-active "$UNIT_PREFIX-workspace" "$UNIT_PREFIX-agent" "$UNIT_PREFIX-gateway"

  # 10. What the services actually received at exec time. `systemctl show` reports Environment= assignments but not a
  # later override from EnvironmentFile=, which is how a broken deploy could otherwise pass.
  for unit in "$UNIT_PREFIX-agent" "$UNIT_PREFIX-workspace"; do
    pid="$(systemctl --user show "$unit" -p MainPID --value)"
    [[ "$pid" -gt 0 ]] || { echo "$unit has no running process" >&2; return 1; }
    if [[ -n "$runtime_path" ]]; then
      tr '\0' '\n' < "/proc/$pid/environ" | grep -Fx "PATH=$runtime_path" >/dev/null || { echo "$unit did not receive the managed PATH" >&2; return 1; }
    fi
    for entry in "${EXTRA_ENV[@]:-}"; do
      [[ -n "$entry" ]] && { tr '\0' '\n' < "/proc/$pid/environ" | grep -Fx "$entry" >/dev/null || { echo "$unit did not receive $entry" >&2; return 1; }; }
    done
  done
  pid="$(systemctl --user show "$UNIT_PREFIX-agent" -p MainPID --value)"
  for entry in "${AGENT_EXTRA_ENV[@]:-}"; do
    [[ -n "$entry" ]] && { tr '\0' '\n' < "/proc/$pid/environ" | grep -Fx "$entry" >/dev/null || { echo "$UNIT_PREFIX-agent did not receive $entry" >&2; return 1; }; }
  done
  if [[ -n "${PLAYBOOK_SMOKE_PATH:-}" ]]; then
    test -f "/proc/$pid/cwd/playbooks/$PLAYBOOK_SMOKE_PATH"
  fi
  if [[ -n "${PIN_NODE_VERSION:-}" ]]; then
    test "$(node --version)" = "v$PIN_NODE_VERSION"
  fi

  # 11. Health. The agent listens a few seconds after systemd reports it active: retry instead of failing on
  # "connection refused" while the new release is still starting.
  echo "==> [$RELEASE_ID] Verifying"
  activate_checks running
  activate_wait_for_url "http://127.0.0.1:$AGENT_PORT/api/health"
  echo "agent  /api/health: $(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$AGENT_PORT/api/health")"
  activate_wait_for_url "http://127.0.0.1:$WORKSPACE_PORT/health"
  curl -fsS "http://127.0.0.1:$WORKSPACE_PORT/health"; echo
  if [[ -s "$APP_ROOT/logs/agent.log" ]]; then tail -n 5 "$APP_ROOT/logs/agent.log"; fi
  # A local reverse proxy in front of the gateway (a host's own nginx) must answer before the public name exists.
  if [[ -n "${LOCAL_PROXY_CHECK_URL:-}" ]]; then
    activate_wait_for_url "$LOCAL_PROXY_CHECK_URL"
    echo "local proxy $LOCAL_PROXY_CHECK_URL: $(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$LOCAL_PROXY_CHECK_URL")"
  fi
  local public=true public_code=000 path cli_public_code
  if [[ -z "${DOMAIN:-}" || "${PUBLIC_CHECKS:-true}" == false ]]; then
    public=false
    echo "public checks skipped (no DOMAIN, or PUBLIC_CHECKS=false)"
  else
    # Not `curl -f`: whether /api/health answers without sign-in depends on the gateway's gate (GATEWAY_SSO_ONLY). Only a
    # connection failure or a 5xx means the gateway itself is broken.
    public_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://$DOMAIN/api/health")"
    echo "public /api/health: $public_code"
    if [[ -n "${PUBLIC_HEALTH_STATUS:-}" ]]; then
      [[ "$public_code" == "$PUBLIC_HEALTH_STATUS" ]] || { echo "https://$DOMAIN/api/health returned $public_code, expected $PUBLIC_HEALTH_STATUS" >&2; return 1; }
    else
      [[ "$public_code" -lt 500 ]] || { echo "https://$DOMAIN/api/health returned $public_code" >&2; return 1; }
    fi
    for path in "${PUBLIC_CHECK_PATHS[@]:-}"; do
      [[ -z "$path" ]] || curl -fsS -o /dev/null --max-time 10 "https://$DOMAIN$path"
    done
  fi
  # A release that ships the laptop CLI must serve it. A sign-in-gated server returns 401 to anonymous public requests.
  if [[ -f "$RELEASE_DIR/downloads/version.json" ]]; then
    for file in install-agentworks.sh version.json; do
      curl -fsS -o /dev/null --max-time 10 "http://127.0.0.1:$AGENT_PORT/api/downloads/cli/$file"
      if [[ "$public" == false ]]; then
        :
      elif [[ "$public_code" == 401 ]]; then
        cli_public_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://$DOMAIN/api/downloads/cli/$file")"
        [[ "$cli_public_code" == 401 ]] || { echo "https://$DOMAIN/api/downloads/cli/$file returned $cli_public_code, expected 401" >&2; return 1; }
      else
        curl -fsS -o /dev/null --max-time 10 "https://$DOMAIN/api/downloads/cli/$file"
      fi
    done
  fi

  # 12. Prune: keep the live release and any older files still used by running processes.
  rm -f "$RELEASE_DIR/.deploying"
  python3 "$REPO_ROOT/deploy/common/prune-releases.py" "$APP_ROOT" --apply \
    --health-url "http://127.0.0.1:$AGENT_PORT/api/health" \
    --health-url "http://127.0.0.1:$WORKSPACE_PORT/health"

  # 13. Self-tests on the live release. They never roll back; a failure fails the deploy loudly with what to fix.
  # How this server differs from the standard runtime profile (docs/design/deploy_unification.md): a report only.
  python3 "$REPO_ROOT/deploy/common/profile_report.py" --profile "$REPO_ROOT/deploy/common/runtime_profile.json" --name "${PRODUCT:-$UNIT_PREFIX}" \
    --account "$SERVICE_ACCOUNT" --app "$APP_ROOT" --data "$STATE_ROOT" --workspace-port "$WORKSPACE_PORT" || true
  # The slot self-test (PLAT-478), read-only: a real slotted `pwd` per slot in the docs root, a workflow, a Crew and a Code
  # project, plus the config checks.
  if slots_enabled; then
    if ! slots_selfcheck "$APP_ROOT" "$DOCS_ROOT" "$UNIT_PREFIX" "$RELEASE_DIR"; then
      echo "==> Release $RELEASE_ID is live at $LIVE_URL, but the SLOT SELF-TEST FAILED (see FAIL lines above)" >&2
      return 1
    fi
  fi
  # The platform account has no Docker where the server says so (owner, 2026-10-16): AI CLIs and platform terminals run
  # as this account, so a Docker here would be reachable by all of them. Only a person's own slot has Docker.
  if [[ "${PLATFORM_DOCKER_CHECK:-false}" == true ]]; then
    local bad=0
    pid="$(systemctl --user show -p MainPID --value "$UNIT_PREFIX-workspace")"
    if [[ -n "$pid" && "$pid" != 0 ]] && tr '\0' '\n' < "/proc/$pid/environ" | grep '^DOCKER_HOST=' >/dev/null; then
      echo 'FAIL platform-no-docker: the workspace process has DOCKER_HOST set'; bad=1
    fi
    if [[ -S "/run/user/$(id -u)/docker.sock" ]]; then
      echo "FAIL platform-no-docker: /run/user/$(id -u)/docker.sock exists"; bad=1
    fi
    if [[ "$bad" != 0 ]]; then
      echo "==> Release $RELEASE_ID is live at $LIVE_URL, but the platform-no-docker self-test FAILED (see FAIL lines above)" >&2
      return 1
    fi
    echo 'PASS platform-no-docker: no DOCKER_HOST in the workspace process and no platform Docker socket'
  fi
  echo "==> Done. Release $RELEASE_ID is live at $LIVE_URL"
}
