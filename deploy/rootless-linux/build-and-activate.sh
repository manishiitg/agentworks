#!/usr/bin/env bash
# Invoked only inside a fresh $PRODUCT@<host> checkout by bootstrap-build.sh.
# Everything below runs directly on the target host -- there is no
# cross-compile and no artifact upload, matching
# deploy/aws-ec2/server/build-and-activate.sh's (video-studio's) on-server
# build shape. Confida and SparkQuill share this parameterized version:
# per-product
# facts (ports, provider/model, CLI list, extra env, runtime-config.js,
# an optional mcp-servers.override.json) live under deploy/rootless-linux/products/$PRODUCT/ and
# are read from the checkout on the deployed branch, never from the
# triggering machine.
set -euo pipefail
[[ "$(uname -sm)" == "Linux x86_64" ]] || { echo "Build must run on Linux x86_64" >&2; exit 1; }
WORKSPACE_ROOT="$1"
PRODUCT="$2"
REPO_ROOT="$WORKSPACE_ROOT/mcp-agent-builder-go"
SCRIPT_DIR="$REPO_ROOT/deploy/rootless-linux"
PRODUCT_DIR="$SCRIPT_DIR/products/$PRODUCT"
REMOTE_APP="/srv/$PRODUCT"
export GOMAXPROCS=4 GOFLAGS=-p=4 NODE_OPTIONS=--max-old-space-size=2048
export PATH="$REMOTE_APP/tools/node/bin:$REMOTE_APP/tools/bin:$PATH"

test -f "$PRODUCT_DIR/product.env" || { echo "No such product: $PRODUCT_DIR/product.env" >&2; exit 1; }
# shellcheck disable=SC1091
source "$PRODUCT_DIR/product.env"
[[ "$PRODUCT" == "$(basename "$PRODUCT_DIR")" ]] || { echo "product.env PRODUCT=$PRODUCT does not match directory $(basename "$PRODUCT_DIR")" >&2; exit 1; }

python3 - "$REMOTE_APP/.env" "${REQUIRED_PERSISTED_ENV_KEYS[@]:-}" <<'PY'
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

for snippet in "${RUNTIME_CONFIG_REQUIRED_SNIPPETS[@]:-}"; do
  [[ -z "$snippet" ]] || grep -Fq "$snippet" "$PRODUCT_DIR/runtime-config.js" || {
    echo "$PRODUCT runtime config is missing required setting: $snippet" >&2
    exit 1
  }
done

for command in git go gcc npm python3 file sha256sum openssl; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
# gog (Gmail connector) needs its encrypted file keyring on a headless box;
# on "auto" it uses the service user's gnome-keyring, which has no unlocked
# default collection, and every Gmail connect fails. Generate the password
# once and preserve it (changing it orphans stored tokens). Checked below by
# deployment_checks.py, in the env file and in the running process.
if [[ -f "$REMOTE_APP/.env" ]]; then
  grep -q '^GOG_KEYRING_PASSWORD=' "$REMOTE_APP/.env" || printf 'GOG_KEYRING_PASSWORD=%s\n' "$(openssl rand -hex 32)" >> "$REMOTE_APP/.env"
  grep -q '^GOG_KEYRING_BACKEND=' "$REMOTE_APP/.env" || echo 'GOG_KEYRING_BACKEND=file' >> "$REMOTE_APP/.env"
  chmod 600 "$REMOTE_APP/.env"
fi
PRODUCT="$PRODUCT" EXPECTED_PUBLIC_URL="${EXPECTED_PUBLIC_URL:-}" python3 "$SCRIPT_DIR/deployment_checks.py" preflight

builder_revision="$(git -C "$REPO_ROOT" rev-parse HEAD)"
test -n "$builder_revision"
RELEASE_ID="${PRODUCT}-${builder_revision:0:8}-$(date +%Y%m%d%H%M%S)"
REMOTE_RELEASE="$REMOTE_APP/releases/$RELEASE_ID"
BUILD_DIR="$REMOTE_RELEASE"
MIGRATION_STOPPED_AGENT=0
mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/frontend" "$BUILD_DIR/configs"
touch "$BUILD_DIR/.deploying"
cleanup_build() {
  if [[ "$MIGRATION_STOPPED_AGENT" == 1 ]]; then
    systemctl --user restart "$PRODUCT-agent" >/dev/null 2>&1 || true
  fi
  if [[ "$(readlink -f "$REMOTE_APP/current")" != "$BUILD_DIR" ]]; then rm -rf "$BUILD_DIR"; fi
}
trap cleanup_build EXIT

# Build exactly the requested checkout while resolving the shared sibling
# modules from the declared workspace root, rather than trusting whatever
# checked-in go.work happens to be sitting in the cloned repo.
DEPLOY_GOWORK="$BUILD_DIR/go.work"
(cd "$BUILD_DIR" && go work init "$REPO_ROOT/agent_go" "$REPO_ROOT/workspace" "$WORKSPACE_ROOT/mcpagent" "$WORKSPACE_ROOT/multi-llm-provider-go")
{
  printf 'mcp-agent-builder-go=%s\n' "$builder_revision"
  printf 'mcpagent=%s\n' "$(git -C "$WORKSPACE_ROOT/mcpagent" rev-parse HEAD)"
  printf 'multi-llm-provider-go=%s\n' "$(git -C "$WORKSPACE_ROOT/multi-llm-provider-go" rev-parse HEAD)"
} > "$BUILD_DIR/SOURCE_REVISIONS"

# Keep the exact source this release was built from beside its binaries (no .git, no
# dependency or build folders), so the code running on the host can always be inspected there.
# It lives in the release folder and is pruned with it; SOURCE_REVISIONS names the commits.
echo "==> [$RELEASE_ID] Saving the source this release is built from"
mkdir -p "$BUILD_DIR/source"
for source_repo in mcp-agent-builder-go mcpagent multi-llm-provider-go; do
  source_dir="$WORKSPACE_ROOT/$source_repo"
  [[ "$source_repo" == mcp-agent-builder-go ]] && source_dir="$REPO_ROOT"
  mkdir -p "$BUILD_DIR/source/$source_repo"
  tar -C "$source_dir" --exclude=.git --exclude=node_modules --exclude=dist -cf - . | tar -C "$BUILD_DIR/source/$source_repo" -xf -
done

echo "==> [$RELEASE_ID] Building binaries (native linux/amd64, on $(hostname))"
# Compile the agent with cgo enabled (it links sherpa-onnx for voice/STT) and
# an $ORIGIN/lib rpath, staging the native libraries beside the binary. The
# remaining Go services stay static CGO_ENABLED=0 builds.
bash "$REPO_ROOT/deploy/aws-ec2/build/build-linux-agent.sh" "$BUILD_DIR" "$REPO_ROOT/agent_go" "$WORKSPACE_ROOT"
mv "$BUILD_DIR/bin/video-studio-agent" "$BUILD_DIR/bin/$PRODUCT-agent"
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/$PRODUCT-workspace" "$REPO_ROOT/workspace")
# Literal filename required: workspace/security/landlock_policy.go resolves
# its sandbox launcher by this exact name regardless of which product runs.
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/video-studio-landlock-runner" "$REPO_ROOT/workspace/cmd/landlock-runner")
# Per-user accounts: slotctl and slottmux (deploy/common/slots.sh, shared with the RTS build).
source "$REPO_ROOT/deploy/common/slots.sh"
slots_build "$WORKSPACE_ROOT" "$DEPLOY_GOWORK" "$REPO_ROOT" "$BUILD_DIR"
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/mcpbridge" ./mcpagent/cmd/mcpbridge)
GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/$PRODUCT-gateway" "$REPO_ROOT/deploy/aws-ec2/server/auth-gateway.go"

echo "==> [$RELEASE_ID] Building AgentWorks CLI downloads"
mkdir -p "$BUILD_DIR/downloads"
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
  os="${target%-*}"
  arch="${target#*-}"
  name="agentworks-$target"
  (cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -ldflags "-X main.cliVersion=$builder_revision" -o "$BUILD_DIR/downloads/$name" "$REPO_ROOT/agent_go/cmd/agentworks")
  chmod +x "$BUILD_DIR/downloads/$name"
  (cd "$BUILD_DIR/downloads" && sha256sum "$name" > "$name.sha256" && sha256sum -c "$name.sha256")
done
install -m 0644 "$REPO_ROOT/scripts/install-agentworks-cli.sh" "$BUILD_DIR/downloads/install-agentworks.sh"
printf '{"version":"%s","release":"%s"}\n' "$builder_revision" "$RELEASE_ID" > "$BUILD_DIR/downloads/version.json"
bash -n "$BUILD_DIR/downloads/install-agentworks.sh"
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
  case "$target" in darwin-*) expected=Mach-O ;; linux-*) expected=ELF ;; esac
  actual="$(file -b "$BUILD_DIR/downloads/agentworks-$target")"
  [[ "$actual" == *"$expected"* ]] || { echo "Invalid agentworks-$target binary: $actual" >&2; exit 1; }
done

echo "==> [$RELEASE_ID] Building frontend"
(cd "$REPO_ROOT/frontend" && npm ci)
(cd "$REPO_ROOT/frontend" && VITE_API_BASE_URL='' VITE_WORKSPACE_API_URL=/api/wp npm run build)
cp -R "$REPO_ROOT/frontend/dist/." "$BUILD_DIR/frontend/"
cp "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$BUILD_DIR/check-release-assets.mjs"
cp "$REPO_ROOT/deploy/common/prune-releases.py" "$BUILD_DIR/prune-releases.py"
cp "$SCRIPT_DIR/deployment_checks.py" "$BUILD_DIR/deployment_checks.py"
# frontend's build:report-preview step (part of `npm run build` above) writes
# report-preview.js to agent_go/cmd/server/static/ in the source checkout,
# never into the release. $PRODUCT-agent, like every other product on this
# pattern, runs with WorkingDirectory=.../current and resolves
# staticFrontendDir()'s default ("./static/") against that cwd, so without
# this copy preview_report always 503s with "Report preview runtime is
# missing" regardless of how many times the frontend gets built.
mkdir -p "$BUILD_DIR/static"
cp -R "$REPO_ROOT/agent_go/cmd/server/static/." "$BUILD_DIR/static/"
install -m 0644 "$PRODUCT_DIR/runtime-config.js" "$BUILD_DIR/frontend/runtime-config.js"
# A deployment's own branding assets (logo, mark, favicon), served at /brand/.
if [[ -d "$PRODUCT_DIR/brand" ]]; then
  install -d -m 0755 "$BUILD_DIR/frontend/brand"
  install -m 0644 "$PRODUCT_DIR"/brand/* "$BUILD_DIR/frontend/brand/"
fi
# The page title and favicon in index.html follow runtime-config.js too, so the
# tab, link previews and the first paint carry the deployment's name and icon
# before any script runs (the app applies the same values at start).
python3 - "$BUILD_DIR/frontend/index.html" "$PRODUCT_DIR/runtime-config.js" <<'PY'
import html, re, sys
index_path, config_path = sys.argv[1:3]
config = open(config_path).read()
def setting(key):
    match = re.search(r'^\s*' + key + r':\s*"([^"]*)"', config, re.M)
    return match.group(1).strip() if match else ""
name, favicon = setting("appName"), setting("faviconUrl")
page = open(index_path).read()
if name and "\n" not in name:
    page = re.sub(r"<title>[^<]*</title>", "<title>" + html.escape(name) + "</title>", page, count=1)
if favicon.startswith("/") and not favicon.startswith("//"):
    kind = "image/svg+xml" if favicon.lower().endswith(".svg") else "image/x-icon" if favicon.lower().endswith(".ico") else "image/png"
    page = re.sub(r'<link rel="icon"[^>]*/>', '<link rel="icon" type="' + kind + '" href="' + html.escape(favicon) + '" />', page, count=1)
open(index_path, "w").write(page)
PY
# One shared MCP catalog for every deployment; a product may add an
# mcp-servers.override.json with only what it does differently.
mcp_override=()
[[ -f "$PRODUCT_DIR/mcp-servers.override.json" ]] && mcp_override=("$PRODUCT_DIR/mcp-servers.override.json")
python3 "$REPO_ROOT/deploy/common/build-mcp-catalog.py" "$REPO_ROOT/agent_go/configs/mcp_servers_clean.json" "$BUILD_DIR/configs/mcp_servers_$PRODUCT.json" "${mcp_override[@]}"
chmod 0644 "$BUILD_DIR/configs/mcp_servers_$PRODUCT.json"
node "$BUILD_DIR/check-release-assets.mjs" "$BUILD_DIR/frontend"

if [[ "${COPY_PLAYBOOKS:-false}" == "true" ]]; then
  python3 "$REPO_ROOT/playbooks/scripts/validate_playbooks.py"
  mkdir -p "$BUILD_DIR/playbooks"
  cp -R "$REPO_ROOT/playbooks/." "$BUILD_DIR/playbooks/"
  python3 "$BUILD_DIR/playbooks/scripts/validate_playbooks.py"
  if [[ -n "${PLAYBOOK_SMOKE_PATH:-}" ]]; then
    test -f "$BUILD_DIR/playbooks/$PLAYBOOK_SMOKE_PATH"
  fi
fi

# Carry the previous release's hashed frontend assets into the new one. A tab
# opened before the swap still lazy-imports chunks by their old hashed names
# on its next navigation; without these files it fails with "Failed to fetch
# dynamically imported module". Hashed names never collide, so only missing
# files are copied (-n), mtimes are preserved (-p), and anything carried for
# more than 14 days is dropped so the directory cannot grow forever.
prev="$REMOTE_APP/current/frontend/assets"
next="$BUILD_DIR/frontend/assets"
if [[ -d "$prev" && -d "$next" ]]; then
  cp -pn "$prev"/* "$next"/ 2>/dev/null || true
  find "$next" -type f -mtime +14 -delete
fi

echo "==> [$RELEASE_ID] Activating release and restarting services"
# Check again after the build, before switching current or restarting services.
PRODUCT="$PRODUCT" EXPECTED_PUBLIC_URL="${EXPECTED_PUBLIC_URL:-}" python3 "$SCRIPT_DIR/deployment_checks.py" preflight
chmod +x "$BUILD_DIR"/bin/*
ln -sfn "$REMOTE_APP/logs" "$BUILD_DIR/logs"

# The standard runtime profile (deploy/common/runtime_profile.json, docs/design/deploy_unification.md): the same settings on every
# product, written below into .env and both services like EXTRA_ENV (a product's own EXTRA_ENV entry for the same key still wins).
mapfile -t STANDARD_ENV < <(python3 - "$REPO_ROOT/deploy/common/runtime_profile.json" "$REMOTE_APP" "$REMOTE_APP/state" <<'PY'
import json, sys
profile, app, data = sys.argv[1:]
for key, value in json.load(open(profile))["same_everywhere"].items():
    print(f"{key}={value.replace('{app}', app).replace('{data}', data)}")
PY
)
EXTRA_ENV=("${STANDARD_ENV[@]}" "${EXTRA_ENV[@]:-}")
install -d -m 0700 "$REMOTE_APP/state" "$REMOTE_APP/state/mcp" "$REMOTE_APP/state/browser-profile"

if true; then
  # MCP state lives in state/mcp on every product now (the profile sets AGENTWORKS_MCP_STATE_DIR): carry a release's user config over once.
  mcp_state="$REMOTE_APP/state/mcp"
  install -d -m 0700 "$mcp_state"
  legacy_mcp="$REMOTE_APP/current/configs/mcp_servers_${PRODUCT}_user.json"
  if [[ -f "$legacy_mcp" && ! -e "$mcp_state/mcp_servers_${PRODUCT}_user.json" ]]; then
    cp -n "$legacy_mcp" "$mcp_state/mcp_servers_${PRODUCT}_user.json"
    chmod 0600 "$mcp_state/mcp_servers_${PRODUCT}_user.json"
  fi
fi

runtime_path="$REMOTE_APP/tools/node/bin:$REMOTE_APP/tools/bin:$REMOTE_APP/home/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# Per-user accounts: a `tmux` front-end first in PATH sends a slot's sessions to that slot's own tmux
# server and leaves every other command to the real tmux. It lives outside the releases so its path
# never changes; a host without slots keeps the plain PATH.
if slots_shim_dir="$(slots_install_shim "$REMOTE_APP" "$BUILD_DIR")" && [[ -n "$slots_shim_dir" ]]; then
  runtime_path="$slots_shim_dir:$runtime_path"
fi

# EnvironmentFile= values are applied after Environment= and therefore win
# for duplicate variables -- a systemd drop-in setting PATH looks correct in
# `systemctl show` while the executed process still receives whatever PATH
# is (or is not) in .env. Replace just that one line atomically so the
# managed PATH always wins regardless of what the drop-in also says, and
# preserve every other line in .env untouched (Confida hit this live on
# 2026-09-11).
env_file="$REMOTE_APP/.env"
if [[ -f "$env_file" ]]; then
  env_next="$(mktemp "$REMOTE_APP/.env.runtime.XXXXXX")"
  python3 - "$env_file" "$env_next" "$runtime_path" "${EXTRA_ENV[@]:-}" <<'PY'
from pathlib import Path
import sys

source, destination, runtime_path, *managed_entries = sys.argv[1:]
managed = {"PATH": runtime_path}
for entry in managed_entries:
    if not entry:
        continue
    key, separator, value = entry.partition("=")
    if not separator or not key:
        raise SystemExit("EXTRA_ENV entries must use KEY=value")
    managed[key] = value

output = []
written = set()
for line in Path(source).read_text().splitlines():
    key, separator, _ = line.partition("=")
    if separator and key in managed:
        if key not in written:
            output.append(f"{key}={managed[key]}")
            written.add(key)
        continue
    output.append(line)
for key, value in managed.items():
    if key not in written:
        output.append(f"{key}={value}")
Path(destination).write_text("\n".join(output) + "\n")
PY
  chmod 0600 "$env_next"
  mv "$env_next" "$env_file"
fi

mkdir -p "$HOME/.config/systemd/user/$PRODUCT-agent.service.d" "$HOME/.config/systemd/user/$PRODUCT-workspace.service.d"
for obsolete in "${REMOVE_DEPLOY_DROPINS[@]:-}"; do
  [[ -z "$obsolete" ]] || {
    rm -f "$HOME/.config/systemd/user/$PRODUCT-agent.service.d/$obsolete"
    rm -f "$HOME/.config/systemd/user/$PRODUCT-workspace.service.d/$obsolete"
  }
done
{
  echo '[Service]'
  echo "Environment=PATH=$runtime_path"
  for entry in "${EXTRA_ENV[@]:-}"; do
    [[ -n "$entry" ]] && echo "Environment=$entry"
  done
} > "$HOME/.config/systemd/user/$PRODUCT-agent.service.d/zz-deploy-managed.conf"
cp "$HOME/.config/systemd/user/$PRODUCT-agent.service.d/zz-deploy-managed.conf" \
   "$HOME/.config/systemd/user/$PRODUCT-workspace.service.d/zz-deploy-managed.conf"
echo "Environment=AGENTWORKS_STATE_ROOT=$REMOTE_APP/state" >> "$HOME/.config/systemd/user/$PRODUCT-agent.service.d/zz-deploy-managed.conf"
for entry in "${AGENT_EXTRA_ENV[@]:-}"; do
  [[ -n "$entry" ]] && echo "Environment=$entry" >> "$HOME/.config/systemd/user/$PRODUCT-agent.service.d/zz-deploy-managed.conf"
done

export XDG_RUNTIME_DIR="/run/user/$(id -u)"

# Drain before the restart: restarting while a turn is running hands the user
# a 502 mid-message. Poll the agent's /health "drain" block until it is idle,
# up to DRAIN_TIMEOUT_SECONDS (default 5 min); new turns can still start
# during the wait, so this is best-effort, and an agent without the field
# (older release, or not yet running) drains immediately.
DRAIN_TIMEOUT_SECONDS="${DRAIN_TIMEOUT_SECONDS:-300}"
deadline=$(($(date +%s) + DRAIN_TIMEOUT_SECONDS))
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
    echo "drain: still busy after ${DRAIN_TIMEOUT_SECONDS}s; restarting anyway" >&2
    break
  fi
  echo "drain: waiting for in-flight turns"
  sleep 5
done

# Activate the immutable release before any opted-in migration. The migration
# marker and data live outside releases, while every subsequent service start
# must resolve binaries and assets from this exact candidate.
ln -sfn "$BUILD_DIR" "$REMOTE_APP/current"

# Stop the old agent once for every migration below. Setting the flag first
# means any failure from here on restarts the agent via cleanup_build instead
# of leaving the product down.
MIGRATION_STOPPED_AGENT=1
systemctl --user stop "$PRODUCT-agent"

# One-time migration of legacy Workflow Builder chats, if this product opted
# in. Runs after `current` points at code that understands the new nested
# layout, but before starting the new agent, so no turn can rewrite a
# transcript while it moves. The marker lives outside releases and makes all
# later deployments a no-op.
if [[ "${RUN_WORKFLOW_BUILDER_MIGRATION:-false}" == "true" ]]; then
  migration_state="$REMOTE_APP/state/migrations"
  migration_marker="$migration_state/workflow-builder-chats-v1.done"
  install -d -m 0700 "$migration_state"
  if [[ ! -f "$migration_marker" ]]; then
    echo "==> [$RELEASE_ID] Migrating legacy Workflow Builder chats"
    mkdir -p "$BUILD_DIR/migrations"
    install -m 0755 "$REPO_ROOT/scripts/migrate_workflow_builder_chats.py" "$BUILD_DIR/migrations/migrate_workflow_builder_chats.py"
    install -m 0644 "$PRODUCT_DIR/workflow-builder-chat-owners-v1.json" "$BUILD_DIR/migrations/workflow-builder-chat-owners-v1.json"
    python3 "$BUILD_DIR/migrations/migrate_workflow_builder_chats.py" \
      --workspace-root "$REMOTE_APP/data/docs" \
      --owner-map "$BUILD_DIR/migrations/workflow-builder-chat-owners-v1.json" \
      --apply
    marker_tmp="$(mktemp "$migration_state/.workflow-builder-chats-v1.XXXXXX")"
    printf 'release=%s\ncompleted_at=%s\n' "$RELEASE_ID" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$marker_tmp"
    chmod 0600 "$marker_tmp"
    mv "$marker_tmp" "$migration_marker"
  fi
fi

# One-time migration of retired personal ("yours") secrets into the
# product's secret box, if this product opted in. Same shape as the Workflow
# Builder migration above: runs after `current` points at the new release
# but before the new agent starts, stamping a marker outside releases so
# later deploys are a no-op. The agent binary reads AUTH_SECRET and
# WORKSPACE_DOCS_PATH from the service .env (exported in a subshell so the
# rest of this script keeps its own environment). Personal files are
# archived, never deleted, and only after every secret for that user
# migrated cleanly; anything unreadable is reported and left in place.
if [[ "${RUN_PRODUCT_SECRETS_MIGRATION:-false}" == "true" ]]; then
  migration_state="$REMOTE_APP/state/migrations"
  migration_marker="$migration_state/product-secrets-v1.done"
  install -d -m 0700 "$migration_state"
  if [[ ! -f "$migration_marker" ]]; then
    echo "==> [$RELEASE_ID] Migrating personal secrets into the $PRODUCT box"
    # shellcheck disable=SC1091
    ( set -a; . "$REMOTE_APP/.env"; set +a; exec "$BUILD_DIR/bin/$PRODUCT-agent" server migrate-product-secrets --product "$PRODUCT" --apply )
    marker_tmp="$(mktemp "$migration_state/.product-secrets-v1.XXXXXX")"
    printf 'release=%s\ncompleted_at=%s\n' "$RELEASE_ID" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$marker_tmp"
    chmod 0600 "$marker_tmp"
    mv "$marker_tmp" "$migration_marker"
  fi
fi

# Build the canonical chat log last, so it reads the layout the migrations
# above produced. Marker-backed; a no-op on later deployments. A failure is not
# fatal: the new agent runs the same idempotent import at startup and retries
# on every start until it completes.
if ! "$BUILD_DIR/bin/$PRODUCT-agent" server migrate-chat-events \
  --docs-root "$REMOTE_APP/data/docs" \
  --state-root "$REMOTE_APP/state"; then
  echo "WARNING: [$RELEASE_ID] legacy chat import failed; the agent retries it at startup" >&2
fi

systemctl --user daemon-reload
systemctl --user restart "$PRODUCT-workspace"
sleep 2
systemctl --user restart "$PRODUCT-agent"
MIGRATION_STOPPED_AGENT=0
sleep 2
systemctl --user restart "$PRODUCT-gateway"
sleep 2
systemctl --user is-active "$PRODUCT-workspace" "$PRODUCT-agent" "$PRODUCT-gateway"

# Verify what the services actually received at exec time. `systemctl show`
# reports Environment= assignments but does not expose a later PATH override
# from EnvironmentFile=, which is how a broken deploy can otherwise pass.
for unit in "$PRODUCT-agent" "$PRODUCT-workspace"; do
  pid="$(systemctl --user show "$unit" -p MainPID --value)"
  [[ "$pid" -gt 0 ]]
  tr '\0' '\n' < "/proc/$pid/environ" | grep -Fqx "PATH=$runtime_path"
  for entry in "${EXTRA_ENV[@]:-}"; do
    [[ -n "$entry" ]] && { tr '\0' '\n' < "/proc/$pid/environ" | grep -Fqx "$entry" || { echo "$unit did not receive $entry" >&2; exit 1; }; }
  done
done

agent_pid="$(systemctl --user show "$PRODUCT-agent" -p MainPID --value)"
for entry in "${AGENT_EXTRA_ENV[@]:-}"; do
  [[ -n "$entry" ]] && { tr '\0' '\n' < "/proc/$agent_pid/environ" | grep -Fqx "$entry" || { echo "$PRODUCT-agent did not receive $entry" >&2; exit 1; }; }
done
if [[ -n "${PLAYBOOK_SMOKE_PATH:-}" ]]; then
  test -f "/proc/$agent_pid/cwd/playbooks/$PLAYBOOK_SMOKE_PATH"
fi

if [[ -n "${PIN_NODE_VERSION:-}" ]]; then
  test "$(node --version)" = "v$PIN_NODE_VERSION"
fi

echo "==> [$RELEASE_ID] Verifying"
PRODUCT="$PRODUCT" EXPECTED_PUBLIC_URL="${EXPECTED_PUBLIC_URL:-}" python3 "$BUILD_DIR/deployment_checks.py" running
# The agent listens a few seconds after systemd reports it active (after a drain it can take longer): retry
# instead of failing the deploy on "connection refused" while the new release is still starting.
wait_for_url() {
  local url="$1" tries=60
  until curl -fsS -o /dev/null --max-time 5 "$url" 2>/dev/null; do
    tries=$((tries - 1))
    [[ "$tries" -gt 0 ]] || { echo "$url did not come up in 2 minutes" >&2; return 1; }
    sleep 2
  done
}
wait_for_url "http://127.0.0.1:$AGENT_PORT/api/health"
echo "agent  /api/health: $(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$AGENT_PORT/api/health")"
wait_for_url "http://127.0.0.1:$WORKSPACE_PORT/health"
curl -fsS "http://127.0.0.1:$WORKSPACE_PORT/health"; echo
# Not `curl -f`: whether /api/health is reachable through the gateway without
# auth depends on its gate model (GATEWAY_DISABLE_PASSWORD_GATE in .env) --
# see the matching comment in deploy.sh. Only a connection failure or a 5xx
# means the gateway itself is broken.
public_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://$DOMAIN/api/health")"
echo "public /api/health: $public_code"
if [[ -n "${PUBLIC_HEALTH_STATUS:-}" ]]; then
  [[ "$public_code" == "$PUBLIC_HEALTH_STATUS" ]] || { echo "https://$DOMAIN/api/health returned $public_code, expected $PUBLIC_HEALTH_STATUS" >&2; exit 1; }
else
  [[ "$public_code" -lt 500 ]] || { echo "https://$DOMAIN/api/health returned $public_code" >&2; exit 1; }
fi
for path in "${PUBLIC_CHECK_PATHS[@]:-}"; do
  [[ -z "$path" ]] || curl -fsS -o /dev/null --max-time 10 "https://$DOMAIN$path"
done
for file in install-agentworks.sh version.json; do
  # The agent must serve both assets. A password-gated product returns 401
  # for anonymous public requests to these paths, just like /api/health.
  curl -fsS -o /dev/null --max-time 10 "http://127.0.0.1:$AGENT_PORT/api/downloads/cli/$file"
  if [[ "$public_code" == 401 ]]; then
    cli_public_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "https://$DOMAIN/api/downloads/cli/$file")"
    [[ "$cli_public_code" == 401 ]] || { echo "https://$DOMAIN/api/downloads/cli/$file returned $cli_public_code, expected 401" >&2; exit 1; }
  else
    curl -fsS -o /dev/null --max-time 10 "https://$DOMAIN/api/downloads/cli/$file"
  fi
done

rm -f "$BUILD_DIR/.deploying"
python3 "$BUILD_DIR/prune-releases.py" "$REMOTE_APP" --apply \
  --health-url "http://127.0.0.1:$AGENT_PORT/api/health" \
  --health-url "http://127.0.0.1:$WORKSPACE_PORT/health"

# How this server differs from the standard runtime profile (docs/design/deploy_unification.md). Report only for now: it never fails
# a deploy while the servers are being aligned.
python3 "$REPO_ROOT/deploy/common/profile_report.py" --profile "$REPO_ROOT/deploy/common/runtime_profile.json" --name "$PRODUCT" \
  --account "$PRODUCT" --app "$REMOTE_APP" --data "$REMOTE_APP/state" --workspace-port "$WORKSPACE_PORT" || true

echo "==> Done. Release $RELEASE_ID is live at https://$DOMAIN"
