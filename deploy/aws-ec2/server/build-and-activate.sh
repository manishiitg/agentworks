#!/usr/bin/env bash
# Activate a shared prebuilt release; no compiler runs on the target host.
# build-and-activate.sh WORKSPACE_ROOT GLOBAL_FILE --prebuilt BUILD_DIR [--manifest-sha256 HASH]
# WORKSPACE_ROOT must be BUILD_DIR/source. The manifest, files and host
# compatibility are verified before assembling the product release.
set -euo pipefail
[[ "$(uname -sm)" == "Linux x86_64" ]] || { echo "Build must run on Linux x86_64" >&2; exit 1; }
WORKSPACE_ROOT="$1"
GLOBAL_FILE="$2"
shift 2
PREBUILT=""
MANIFEST_SHA256=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prebuilt) PREBUILT="$2"; shift 2 ;;
    --manifest-sha256) MANIFEST_SHA256="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
BUILDER_REPO_ROOT="$WORKSPACE_ROOT/mcp-agent-builder-go"
[[ -n "$PREBUILT" ]] || { echo "A prebuilt release is required; create it with ./deploy.sh build." >&2; exit 2; }
REPO_ROOT="$BUILDER_REPO_ROOT"
SCRIPT_DIR="$REPO_ROOT/deploy/aws-ec2"
# This server's branding, public URL, agent service file and product.env come from the private deployments repo, shipped
# next to the globals file as overlay/ (deploy.sh). The copies in this repo are neutral placeholders and are never deployed.
OVERLAY_DIR="$(dirname "$GLOBAL_FILE")/overlay"
[[ -f "$OVERLAY_DIR/runtime-config.js" && -f "$OVERLAY_DIR/video-studio-agent.service" && -f "$OVERLAY_DIR/product.env" ]] || { echo "Missing the private overlay (runtime-config.js, video-studio-agent.service, product.env) in $OVERLAY_DIR" >&2; exit 1; }
# The server's layout (app, docs and state folders, unit names, account, ports) is data in its product.env, read here and
# by the shared activation (deploy/common/activate-release.sh), the same as every other server.
# shellcheck disable=SC1091
source "$OVERLAY_DIR/product.env"
for setting in APP_ROOT DOCS_ROOT MCP_STATE_DIR TOOLS_DIR UNIT_PREFIX SERVICE_ACCOUNT AGENT_PORT WORKSPACE_PORT VAULT_PORT; do
  [[ -n "${!setting:-}" ]] || { echo "The server's product.env must set $setting" >&2; exit 1; }
done
[[ "$(id -un)" == "$SERVICE_ACCOUNT" ]] || { echo "This deploy must run as $SERVICE_ACCOUNT, not $(id -un)" >&2; exit 1; }
RUNTIME_CONFIG="$OVERLAY_DIR/runtime-config.js"
AGENT_SERVICE_FILE="$OVERLAY_DIR/video-studio-agent.service"
BRAND_DIR="$OVERLAY_DIR/brand"
LIVE_URL="$(sed -n 's/^Environment=PUBLIC_URL=//p' "$AGENT_SERVICE_FILE" | head -n 1)"
HYPERFRAMES_VERSION="${HYPERFRAMES_VERSION:-0.8.6}"
AGENTWORKS_PROVIDER="${AGENTWORKS_PROVIDER:-cursor-cli}"
AGENTWORKS_MODEL="${AGENTWORKS_MODEL:-cursor-cli}"
export NODE_OPTIONS=--max-old-space-size=2048
  [[ "$WORKSPACE_ROOT" == "$PREBUILT/source" ]] || { echo "With --prebuilt, WORKSPACE_ROOT must be $PREBUILT/source" >&2; exit 2; }
  for command in jq rsync python3 ffmpeg ldd; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
  # shellcheck disable=SC1091
  source "$REPO_ROOT/deploy/common/prebuilt.sh"
  # The shipped copy has no mcpagent / multi-llm-provider-go source and no downloads/ (listed in the manifest, absent here by design).
  prebuilt_verify "$REPO_ROOT" "$PREBUILT" --skip-prefix source/mcpagent/ --skip-prefix source/multi-llm-provider-go/ --skip-prefix downloads/ \
    ${MANIFEST_SHA256:+--manifest-sha256 "$MANIFEST_SHA256"}
# This host has one fixed deployment contract: AgentWorks supplies the shared
# application shell and the approved product backends. Fail before
# building or touching the server if a checked-in allowlist drifts.
# The product switcher (client enabledProductSurfaces) and the loaded backends
# (AGENT_PRODUCTS) must name the same products; "agentworks" (Goals, the shell) may
# be on either list or both, because the server rejects Goal pages' saved state when it
# is missing from AGENT_PRODUCTS. A mismatch hides a product that is running, or offers
# one that is not.
client_products="$(sed -n 's/.*enabledProductSurfaces: *\[\(.*\)\].*/\1/p' "$RUNTIME_CONFIG" | head -n 1 | tr -d ' "' | tr ',' '\n' | grep -v '^agentworks$' | sort -u)"
server_products="$(sed -n 's/^Environment=AGENT_PRODUCTS=//p' "$AGENT_SERVICE_FILE" | head -n 1 | tr -d ' ' | tr ',' '\n' | grep -v '^$' | grep -v '^agentworks$' | sort -u)"
[[ -n "$client_products" && -n "$server_products" ]] || { echo "Cannot read enabledProductSurfaces from the runtime config or AGENT_PRODUCTS from the agent service" >&2; exit 1; }
only_client="$(comm -23 <(printf '%s\n' "$client_products") <(printf '%s\n' "$server_products") | paste -sd, -)"
only_server="$(comm -13 <(printf '%s\n' "$client_products") <(printf '%s\n' "$server_products") | paste -sd, -)"
if [[ -n "$only_client" || -n "$only_server" ]]; then
  echo "Product lists disagree: in the client switcher only: ${only_client:-none}; loaded as backends only: ${only_server:-none}" >&2
  exit 1
fi
grep -Fq 'Environment=AGENT_BROWSER_CDP_ENABLED=false' "$AGENT_SERVICE_FILE" || {
  echo "Video Studio server deployment must disable CDP in the agent service" >&2
  exit 1
}
grep -Fq 'Environment=AGENT_BROWSER_CDP_ENABLED=false' "$SCRIPT_DIR/rootless/video-studio-workspace.service" || {
  echo "Video Studio server deployment must disable CDP in the workspace service" >&2
  exit 1
}
grep -Fq 'DOCKER_HOST' "$SCRIPT_DIR/rootless/video-studio-workspace.service" && {
  echo "Video Studio workspace must not be given a Docker socket (only a person's own slot has Docker)" >&2
  exit 1
}
grep -Fq 'cdpEnabled: false' "$RUNTIME_CONFIG" || {
  echo "Video Studio runtime config must display CDP as disabled" >&2
  exit 1
}
grep -Fq 'apiBaseUrl: "",' "$RUNTIME_CONFIG" && grep -Fq 'workspaceApiBaseUrl: "/api/wp",' "$RUNTIME_CONFIG" || {
  echo "Video Studio runtime config must use the same-origin API paths (apiBaseUrl \"\", workspaceApiBaseUrl /api/wp)" >&2
  exit 1
}

RELEASE_ID="$(prebuilt_revision "$PREBUILT" mcp-agent-builder-go | cut -c1-7)-$(date +%Y%m%d%H%M%S)"
REMOTE_APP="$APP_ROOT"
REMOTE_RELEASE="$REMOTE_APP/releases/$RELEASE_ID"
BUILD_DIR="$REMOTE_RELEASE"
mkdir -p "$BUILD_DIR"
touch "$BUILD_DIR/.deploying"
# Per-user accounts: slotctl and slottmux (deploy/common/slots.sh, shared with the rootless-linux build).
source "$REPO_ROOT/deploy/common/slots.sh"
source "$REPO_ROOT/deploy/common/vault.sh"
# The activation steps every server runs (env merge, units, switch, restart, health, prune, self-tests).
source "$REPO_ROOT/deploy/common/activate-release.sh"
cleanup_build() {
  activate_recover
  rm -f "$GLOBAL_FILE" "$REMOTE_APP/.globals-$RELEASE_ID" "$BUILD_DIR/.deploying"
  if [[ "$(readlink -f "$REMOTE_APP/current")" != "$BUILD_DIR" ]]; then rm -rf "$BUILD_DIR"; fi
}
trap cleanup_build EXIT
VAULT_ENABLED="${VAULT_ENABLED:-true}"
# The service account here runs AI CLIs and platform terminals, so it must not have Docker (only a person's own slot may).
PLATFORM_DOCKER_CHECK="${PLATFORM_DOCKER_CHECK:-true}"
# All commands below run on this Linux server; there is no artifact upload.
run_local() { if [[ $# == 1 ]]; then bash -c "$1"; else "$@"; fi; }
SSH=(run_local)
mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/frontend" "$BUILD_DIR/configs" "$BUILD_DIR/systemd" "$BUILD_DIR/browser" "$BUILD_DIR/playbooks"
echo "==> [$RELEASE_ID] Copying prebuilt release $(basename "$PREBUILT") (no compile)"
# Plain cp -R, not -a: copied files get today's mtime, as freshly built ones did (the carried-over asset cleanup works by age).
cp -R "$PREBUILT/packages" "$BUILD_DIR/packages"
cp "$PREBUILT/SOURCE_REVISIONS" "$BUILD_DIR/SOURCE_REVISIONS"
# update-coding-clis is installed below from the repository, as before.
prebuilt_copy_bin "$PREBUILT" "$BUILD_DIR/bin" video-studio "agent workspace gateway browser" "update-coding-clis"
cp -R "$PREBUILT/frontend/." "$BUILD_DIR/frontend/"

vault_check_build "$BUILD_DIR"
node "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$BUILD_DIR/frontend"
cp "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$BUILD_DIR/check-release-assets.mjs"
cp "$REPO_ROOT/deploy/common/prune-releases.py" "$BUILD_DIR/prune-releases.py"
cp "$REPO_ROOT/deploy/common/slots.sh" "$BUILD_DIR/slots.sh"
cp "$REPO_ROOT/deploy/common/slotcheck.sh" "$BUILD_DIR/slotcheck.sh"
cp "$REPO_ROOT/deploy/common/admission_scan.py" "$BUILD_DIR/admission_scan.py"
# frontend's build:report-preview step (part of `npm run build` above) writes
# report-preview.js to agent_go/cmd/server/static/ in the source checkout,
# never into the release. video-studio-agent runs with
# WorkingDirectory=.../current and resolves staticFrontendDir()'s default
# ("./static/") against that cwd, so without this copy preview_report always
# 503s with "Report preview runtime is missing" regardless of how many times
# the frontend gets built.
mkdir -p "$BUILD_DIR/static"
static_source="$PREBUILT/static"
cp -R "$static_source/." "$BUILD_DIR/static/"
install -m 0644 "$RUNTIME_CONFIG" "$BUILD_DIR/frontend/runtime-config.js"
# This deployment's own branding assets (logo, mark, favicon), served at /brand/.
if [[ -d "$BRAND_DIR" ]]; then
  install -d -m 0755 "$BUILD_DIR/frontend/brand"
  install -m 0644 "$BRAND_DIR"/* "$BUILD_DIR/frontend/brand/"
fi
# One shared MCP catalog for every deployment, plus this one's differences.
python3 "$REPO_ROOT/deploy/common/build-mcp-catalog.py" "$REPO_ROOT/agent_go/configs/mcp_servers_clean.json" "$BUILD_DIR/configs/mcp_servers_video_studio.json" "$SCRIPT_DIR/server/mcp-servers.override.json"
chmod 0644 "$BUILD_DIR/configs/mcp_servers_video_studio.json"
install -m 0755 "$SCRIPT_DIR/server/chrome-headless-wrapper.sh" "$BUILD_DIR/browser/agentworks-chrome-headless"
# The unit files this host runs; the shared activation installs every unit in systemd/ and enables every timer there.
for unit in workspace.service gateway.service logrotate.conf logrotate.service logrotate.timer; do
  install -m 0644 "$SCRIPT_DIR/rootless/video-studio-$unit" "$BUILD_DIR/systemd/$UNIT_PREFIX-$unit"
done
install -m 0644 "$AGENT_SERVICE_FILE" "$BUILD_DIR/systemd/$UNIT_PREFIX-agent.service"
install -m 0755 "$SCRIPT_DIR/server/update-coding-clis.sh" "$BUILD_DIR/bin/update-coding-clis"
cp -R "$REPO_ROOT/playbooks/." "$BUILD_DIR/playbooks/"

# Workflow steps run as slot accounts, which cannot enter the service account's private ~/.cache: activation copies the
# HyperFrames Chrome to <app>/tools/chrome/<version>/ (world-readable) and points AGENT_BROWSER_EXECUTABLE_PATH there
# (PLAT-758; the rootless deploy already uses <app>/tools/chrome).
REMOTE_TOOLS_DIR="$TOOLS_DIR"
# Core dashboard exports need FFprobe in the same managed command folder that
# workspace shells already receive through AGENT_BROWSER_CLI_DIR. A missing
# optional video tool must not stop the application release.
if ! REMOTE_APP="$REMOTE_APP" python3 "$REPO_ROOT/deploy/rootless-linux/install-ffprobe.py" --bin-dir "$REMOTE_TOOLS_DIR/bin"; then
  echo "Video export is not available on this server yet: FFprobe setup failed. Dashboard playback is available." >&2
fi
"${SSH[@]}" "command -v python3 >/dev/null"
REMOTE_BROWSER_PATH="$("${SSH[@]}" "set -e; npx --yes 'hyperframes@$HYPERFRAMES_VERSION' browser ensure >/dev/null; npx --yes 'hyperframes@$HYPERFRAMES_VERSION' browser path | tail -n 1")"
case "$REMOTE_BROWSER_PATH" in
  "$HOME"/.cache/hyperframes/chrome/*/chrome-headless-shell) ;;
  *) echo "Unexpected HyperFrames browser path: $REMOTE_BROWSER_PATH" >&2; exit 1 ;;
esac
"${SSH[@]}" "test -x '$REMOTE_BROWSER_PATH'"
# agent-browser is a mandatory runtime dependency (preview_report and every
# browser-automation tool shell out to it by name with no PATH check of their
# own), but until now this only asserted it was already on PATH -- if it was
# ever missing, e.g. on a fresh host that had not yet run repair-bootstrap.sh,
# the whole deploy failed with no path to self-heal. Install it into the same
# tool prefix as claude/cursor-agent below, then assert.
"${SSH[@]}" "bash -s -- '$REMOTE_TOOLS_DIR'" < "$REPO_ROOT/agent_go/scripts/install-slack-cli.sh"
# gog (Gmail connector CLI): latest checksum-verified release in the same
# tool prefix, the same "always latest" policy as agent-browser. The one-time
# root copy from install-system-tools.sh stays as a fallback.
"${SSH[@]}" "bash -s -- '$REMOTE_TOOLS_DIR'" < "$REPO_ROOT/deploy/common/install-gog.sh"
export PATH="$REMOTE_TOOLS_DIR/bin:$PATH"
# Teaching needs CDP URL discovery and stable target IDs (agent-browser 0.38.2).
# Upgrade only this service account's tool prefix; leave system tools intact.
if ! python3 - <<'BROWSER_VERSION'
import re, subprocess, sys
try:
    output = subprocess.check_output(["agent-browser", "--version"], text=True)
    match = re.search(r"agent-browser (\d+)\.(\d+)\.(\d+)", output)
    sys.exit(0 if match and tuple(map(int, match.groups())) >= (0, 38, 2) else 1)
except (OSError, subprocess.CalledProcessError):
    sys.exit(1)
BROWSER_VERSION
then
  npm install -g --allow-scripts=agent-browser --prefix "$REMOTE_TOOLS_DIR" agent-browser@0.38.2
fi
agent-browser --version
bash "$REPO_ROOT/deploy/common/install-coding-clis.sh" "$REMOTE_TOOLS_DIR" "$HOME"
"${SSH[@]}" "mkdir -p '$REMOTE_RELEASE'; touch '$REMOTE_RELEASE/.deploying'"
"${SSH[@]}" "node '$REMOTE_RELEASE/check-release-assets.mjs' '$REMOTE_RELEASE/frontend'"
install -m 0600 "$GLOBAL_FILE" "$REMOTE_APP/.globals-$RELEASE_ID"
# Host tools and provider checks for this server, before anything is switched. The sandbox, Relay runtime and env steps
# are part of the shared activation below.
"${SSH[@]}" bash -s -- "$REMOTE_APP" "$REMOTE_TOOLS_DIR" "$RELEASE_ID" "$AGENTWORKS_PROVIDER" "$DOCS_ROOT" <<'REMOTE_PREFLIGHT'
set -euo pipefail
remote_app="$1"
tools_dir="$2"
release_id="$3"
agentworks_provider="$4"
docs_root="$5"
global_file="$remote_app/.globals-$release_id"

install -d -m 0755 "$HOME/Downloads" "$docs_root/Downloads"
# Browser recordings (.mp4 / .webm) are encoded by agent-browser with ffmpeg.
encoders="$(ffmpeg -hide_banner -encoders 2>/dev/null)"
grep -qw libx264 <<<"$encoders" && grep -qw libvpx <<<"$encoders" || { echo "ffmpeg lacks libx264 or libvpx; browser recordings cannot encode" >&2; exit 1; }

printf 'prefix=%s\n' "$tools_dir" > "$HOME/.npmrc"
# The shared installer above updates and launch-checks all six CLIs.
# One managed copy. The services resolve claude from PATH, so the managed one must be
# the one that wins, and no system copy should exist: an old /usr/bin/claude ignores
# AGENTS.md (where the session prompt is carried) and ran silently for hours on
# 2026-09-25. Remove strays with: sudo npm uninstall -g @anthropic-ai/claude-code
resolved_claude="$(PATH="$tools_dir/bin:/usr/local/bin:/usr/bin:/bin" command -v claude)"
if test "$resolved_claude" != "$tools_dir/bin/claude"; then
  echo "claude resolves to $resolved_claude, not the managed $tools_dir/bin/claude" >&2
  exit 1
fi
for stale in /usr/bin/claude /usr/local/bin/claude /usr/bin/pi /usr/local/bin/pi; do
  if test -e "$stale"; then
    echo "WARNING: system copy $stale exists; it is a silent fallback with an old version. Remove it (sudo npm uninstall -g @anthropic-ai/claude-code @earendil-works/pi-coding-agent)." >&2
  fi
done
# CLIs that WORKFLOW shells need (aws, ntn, git) are NOT installed here: the
# sandbox those shells run in cannot read this tool prefix (strict env,
# HOME=/tmp, system read roots only). They are installed system-wide as root
# through SSM by install-system-tools.sh, once per box.
PATH="$tools_dir/bin:/usr/local/bin:/usr/bin:/bin"
export PATH
test "$(HOME="$HOME" npm config get prefix)" = "$tools_dir"
if [[ "$agentworks_provider" == "cursor-cli" ]]; then
  # The AgentWorks LLM shells out to cursor-agent (through tmux); install it
  # into the same tool prefix as claude. Cursor's installer
  # writes to $HOME/.local/bin, which is $tools_dir/bin here.
  cursor_key="$(sed -n 's/^CURSOR_API_KEY=//p' "$global_file" | head -n 1)"
  if [[ -z "$cursor_key" ]]; then
    echo "CURSOR_API_KEY is missing from the global secret; AgentWorks is configured for cursor-cli and would fail on every turn." >&2
    exit 1
  fi
  # A soft round trip: cursor-agent's non-interactive flags are less stable
  # than claude's, so a failure here is reported, not fatal. XDG_CONFIG_HOME
  # matches the agent unit ($HOME/.config is root-owned here) and --trust
  # answers the workspace-trust prompt a headless run cannot.
  install -d -m 0700 "$HOME/.local/state/xdg-config"
  if ! XDG_CONFIG_HOME="$HOME/.local/state/xdg-config" CURSOR_API_KEY="$cursor_key" timeout 90 cursor-agent -p --trust "say OK" >/dev/null 2>"$HOME/.cursor-preflight.err"; then
    echo "WARNING: cursor-agent round trip did not succeed: $(head -c 300 "$HOME/.cursor-preflight.err")" >&2
  fi
  rm -f "$HOME/.cursor-preflight.err"
fi
token="$(sed -n 's/^CLAUDE_CODE_OAUTH_TOKEN=//p' "$global_file" | head -n 1)"
test -n "$token"
# The token must be real: `claude auth status` says "logged in" for any
# non-empty string, so only a round trip proves it, and a dead token would
# deploy an agent that fails on every user's first turn. A capped account is
# a different thing -- "session limit" / "rate limit" means the token is valid
# and the account is merely busy, which a deploy does not make worse and which
# the live agent is already subject to -- so that answer is a warning, not a
# failure (2026-09-03: a release was blocked for 90 minutes by exactly this).
preflight_reply="$(CLAUDE_CODE_OAUTH_TOKEN="$token" claude -p --output-format json <<< 'say OK' || true)"
if ! printf '%s' "$preflight_reply" | jq -e '.is_error == false' >/dev/null 2>&1; then
  preflight_result="$(printf '%s' "$preflight_reply" | jq -r '.result // empty' 2>/dev/null || true)"
  case "$preflight_result" in
    *"session limit"*|*"usage limit"*|*"spend limit"*|*"rate limit"*|*"Rate limit"*|*"capacity"*)
      echo "WARNING: Claude token is valid but the account is capped right now (${preflight_result}); continuing." >&2 ;;
    *)
      echo "Claude token validation failed: ${preflight_result:-no JSON reply from claude -p}" >&2
      exit 1 ;;
  esac
fi
# The session prompt reaches Claude through AGENTS.md (instruction-only projection), so
# prove THIS claude reads it: a Claude that ignores AGENTS.md would run every session
# without its system prompt, with no error. A capped account skips the check with a warning.
probe_dir="$(mktemp -d)"
printf 'The project codeword is WALRUS-7. Report it if asked.\n' > "$probe_dir/AGENTS.md"
probe_reply="$(cd "$probe_dir" && CLAUDE_CODE_OAUTH_TOKEN="$token" claude -p --output-format json 'What is the project codeword from your project instructions? If none, say NONE.' </dev/null || true)"
rm -rf "$probe_dir"
probe_result="$(printf '%s' "$probe_reply" | jq -r '.result // empty' 2>/dev/null || true)"
case "$probe_result" in
  *WALRUS-7*) ;;
  *"session limit"*|*"usage limit"*|*"spend limit"*|*"rate limit"*|*"Rate limit"*|*"capacity"*)
    echo "WARNING: AGENTS.md read check skipped, the Claude account is capped right now (${probe_result})." >&2 ;;
  *)
    echo "This claude ($("$tools_dir/bin/claude" --version)) does not read AGENTS.md, so sessions would start without their system prompt: ${probe_result:-no reply}" >&2
    exit 1 ;;
esac
REMOTE_PREFLIGHT
# Carry the previous release's hashed frontend assets into the new one (open tabs still load their old chunks).
activate_carry_assets "$REMOTE_APP" "$REMOTE_RELEASE"
# Production activation is intentionally breaking: swap immediately after a complete, verified build (no drain unless
# DRAIN_TIMEOUT_SECONDS is set). Service restart is the deployment boundary; clients reconnect to the new runtime.

# Workflow steps run as slot accounts, which cannot enter the service account's private ~/.cache: copy the HyperFrames
# Chrome to <app>/tools/chrome/<version>/ (world-readable) and point AGENT_BROWSER_EXECUTABLE_PATH at its wrapper (PLAT-758).
browser_dir="$(dirname "$REMOTE_BROWSER_PATH")"
shared_browser_dir="$REMOTE_APP/tools/chrome/$(basename "$(dirname "$browser_dir")")/$(basename "$browser_dir")"
install -d -m 0755 "$REMOTE_APP/tools" "$REMOTE_APP/tools/chrome" "$(dirname "$shared_browser_dir")" "$shared_browser_dir"
rsync -a --delete --exclude deb.deps --chmod=D0755,Fa+rX "$browser_dir/" "$shared_browser_dir/"
browser_wrapper="$shared_browser_dir/agentworks-chrome-headless"
install -m 0755 "$REMOTE_RELEASE/browser/agentworks-chrome-headless" "$browser_wrapper"

# What this server's deploy writes into .env, the same way EXTRA_ENV does on every server (a product.env EXTRA_ENV entry
# for the same key wins). The AgentWorks LLM is fixed by the deploy, not by whoever last edited the box: one
# provider/model, LLM_CONFIG_LOCKED so the UI shows "locked by admin", and a published list with exactly that one entry.
# New Crews are created at the shared Crew/ root (owner 2026-10-05, PLAT-442).
server_env=(
  "MCP_API_URL=http://127.0.0.1:$AGENT_PORT"
  "AGENT_BROWSER_EXECUTABLE_PATH=$browser_wrapper"
  "AGENT_PROVIDER=$AGENTWORKS_PROVIDER"
  "AGENT_MODEL=$AGENTWORKS_MODEL"
  "LLM_CONFIG_LOCKED=true"
  "AGENTWORKS_CREW_SHARED_ROOT=on"
)
# Per-user accounts (deploy/common/provision-slots.sh): on a host an administrator has provisioned, run in opt-in mode, so
# a user who holds a slot runs as it and everyone else is unchanged until their folders are slot-aware.
[[ ! -r /etc/agentworks/slots.json ]] || server_env+=("AGENTWORKS_SLOTS=optin")
EXTRA_ENV=("${server_env[@]}" "${EXTRA_ENV[@]:-}")
# Secrets (the global secrets: Claude and Cursor tokens, GLOBAL_SECRET_*) and the published model list (its value has
# spaces) go into .env only, never into a command line or a unit drop-in.
printf "DEFAULT_PUBLISHED_LLMS='[{\"id\":\"agentworks-default\",\"name\":\"%s (%s)\",\"provider\":\"%s\",\"model_id\":\"%s\"}]'\n" \
  "$AGENTWORKS_PROVIDER" "$AGENTWORKS_MODEL" "$AGENTWORKS_PROVIDER" "$AGENTWORKS_MODEL" >> "$REMOTE_APP/.globals-$RELEASE_ID"
ENV_ONLY_FILE="$REMOTE_APP/.globals-$RELEASE_ID"
RELEASE_DIR="$REMOTE_RELEASE"
BROWSER_CLI_CHECK_DIR="$REMOTE_TOOLS_DIR/bin"
activate_release
