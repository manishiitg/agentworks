#!/usr/bin/env bash
# Invoked only inside a fresh server-side checkout started by `./deploy.sh <server>`.
#
#   build-and-activate.sh WORKSPACE_ROOT GLOBAL_FILE [--prebuilt BUILD_DIR [--manifest-sha256 HASH]]
#
# Without --prebuilt it compiles on this host (the original path, kept as the fallback). With --prebuilt (PLAT-426) it compiles
# nothing: BUILD_DIR is a release made once by deploy/common/build-release.sh and shipped here as a tarball (without the
# mcpagent and multi-llm-provider-go source and without downloads/, which this host never used); WORKSPACE_ROOT must be its source/
# folder. The manifest (CPU architecture, glibc, sha256 of every file, and the manifest's own hash as announced by the build host)
# is verified before anything is touched. Everything after the compile step is unchanged.
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
REPO_ROOT="$BUILDER_REPO_ROOT"
SCRIPT_DIR="$REPO_ROOT/deploy/aws-ec2"
HYPERFRAMES_VERSION="${HYPERFRAMES_VERSION:-0.8.6}"
AGENTWORKS_PROVIDER="${AGENTWORKS_PROVIDER:-cursor-cli}"
AGENTWORKS_MODEL="${AGENTWORKS_MODEL:-cursor-cli}"
export GOMAXPROCS=2 GOFLAGS=-p=2 NODE_OPTIONS=--max-old-space-size=2048
if [[ -n "$PREBUILT" ]]; then
  [[ "$WORKSPACE_ROOT" == "$PREBUILT/source" ]] || { echo "With --prebuilt, WORKSPACE_ROOT must be $PREBUILT/source" >&2; exit 2; }
  for command in jq rsync python3 ffmpeg ldd; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
  # shellcheck disable=SC1091
  source "$REPO_ROOT/deploy/common/prebuilt.sh"
  # The shipped copy has no mcpagent / multi-llm-provider-go source and no downloads/ (listed in the manifest, absent here by design).
  prebuilt_verify "$REPO_ROOT" "$PREBUILT" --skip-prefix source/mcpagent/ --skip-prefix source/multi-llm-provider-go/ --skip-prefix downloads/ \
    ${MANIFEST_SHA256:+--manifest-sha256 "$MANIFEST_SHA256"}
else
  for command in git go gcc npm jq rsync python3 ffmpeg; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
fi
# This host has one fixed deployment contract: AgentWorks supplies the shared
# application shell and the approved product backends. Fail before
# building or touching the server if either checked-in allowlist drifts.
grep -Fq 'enabledProductSurfaces: ["agentworks", "video-studio", "work", "code", "mcp-gateway", "knowledgebase"]' "$SCRIPT_DIR/server/runtime-config.js" || {
  echo "RTS deployment must expose AgentWorks, Video Studio, Work, Code, Vault, and Brain" >&2
  exit 1
}
grep -Fq 'Environment=AGENT_PRODUCTS=video-studio,work,code,mcp-gateway,knowledgebase' "$SCRIPT_DIR/rootless/video-studio-agent.service" || {
  echo "RTS deployment must load the video-studio, work, code, Vault and Brain product backends" >&2
  exit 1
}
grep -Fq 'Environment=AGENT_BROWSER_CDP_ENABLED=false' "$SCRIPT_DIR/rootless/video-studio-agent.service" || {
  echo "Video Studio server deployment must disable CDP in the agent service" >&2
  exit 1
}
grep -Fq 'Environment=AGENT_BROWSER_CDP_ENABLED=false' "$SCRIPT_DIR/rootless/video-studio-workspace.service" || {
  echo "Video Studio server deployment must disable CDP in the workspace service" >&2
  exit 1
}
grep -Fq 'Environment=DOCKER_HOST=unix:///run/user/%U/docker.sock' "$SCRIPT_DIR/rootless/video-studio-workspace.service" || {
  echo "Video Studio workspace must use the service user's rootless Docker socket" >&2
  exit 1
}
bash "$REPO_ROOT/deploy/common/install-rootless-docker.sh" --check video-studio
grep -Fq 'cdpEnabled: false' "$SCRIPT_DIR/server/runtime-config.js" || {
  echo "Video Studio runtime config must display CDP as disabled" >&2
  exit 1
}

if [[ -n "$PREBUILT" ]]; then
  RELEASE_ID="$(prebuilt_revision "$PREBUILT" mcp-agent-builder-go | cut -c1-7)-$(date +%Y%m%d%H%M%S)"
else
  RELEASE_ID="$(git -C "$REPO_ROOT" rev-parse --short HEAD)-$(date +%Y%m%d%H%M%S)"
fi
REMOTE_APP=/var/lib/video-studio/video-studio
REMOTE_RELEASE="$REMOTE_APP/releases/$RELEASE_ID"
BUILD_DIR="$REMOTE_RELEASE"
mkdir -p "$BUILD_DIR"
touch "$BUILD_DIR/.deploying"
cleanup_build() {
  if declare -F vault_recover >/dev/null; then vault_recover; fi
  rm -f "$GLOBAL_FILE" "$REMOTE_APP/.globals-$RELEASE_ID" "$BUILD_DIR/.deploying"
  if [[ "$(readlink -f "$REMOTE_APP/current")" != "$BUILD_DIR" ]]; then rm -rf "$BUILD_DIR"; fi
}
trap cleanup_build EXIT
VAULT_ENABLED=true
VAULT_PORT="${VAULT_PORT:-8083}"
# All commands below run on this Linux server; there is no artifact upload.
run_local() { if [[ $# == 1 ]]; then bash -c "$1"; else "$@"; fi; }
SSH=(run_local)
mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/frontend" "$BUILD_DIR/configs" "$BUILD_DIR/systemd" "$BUILD_DIR/claude-skills" "$BUILD_DIR/browser" "$BUILD_DIR/playbooks" "$BUILD_DIR/migrations"
# Per-user accounts: slotctl and slottmux (deploy/common/slots.sh, shared with the rootless-linux build).
source "$REPO_ROOT/deploy/common/slots.sh"
source "$REPO_ROOT/deploy/common/vault.sh"
if [[ -n "$PREBUILT" ]]; then
echo "==> [$RELEASE_ID] Copying prebuilt release $(basename "$PREBUILT") (no compile)"
# Plain cp -R, not -a: copied files get today's mtime, as freshly built ones did (the carried-over asset cleanup works by age).
cp -R "$PREBUILT/packages" "$BUILD_DIR/packages"
cp "$PREBUILT/SOURCE_REVISIONS" "$BUILD_DIR/SOURCE_REVISIONS"
# update-coding-clis is installed below from the repository, as before.
prebuilt_copy_bin "$PREBUILT" "$BUILD_DIR/bin" video-studio "agent workspace gateway browser" "update-coding-clis"
cp -R "$PREBUILT/frontend/." "$BUILD_DIR/frontend/"
else
python3 "$REPO_ROOT/scripts/build-playwright-packages.py" "$BUILD_DIR/packages"
# Build exactly the requested checkout while resolving the shared sibling
# modules from the declared workspace root. The checked-in go.work may point
# at a primary checkout instead of this worktree.
DEPLOY_GOWORK="$BUILD_DIR/go.work"
(cd "$BUILD_DIR" && go work init "$BUILDER_REPO_ROOT/agent_go" "$BUILDER_REPO_ROOT/workspace" "$WORKSPACE_ROOT/mcpagent" "$WORKSPACE_ROOT/multi-llm-provider-go")
{
  printf 'mcp-agent-builder-go=%s\n' "$(git -C "$BUILDER_REPO_ROOT" rev-parse HEAD)"
  printf 'mcpagent=%s\n' "$(git -C "$WORKSPACE_ROOT/mcpagent" rev-parse HEAD)"
  printf 'multi-llm-provider-go=%s\n' "$(git -C "$WORKSPACE_ROOT/multi-llm-provider-go" rev-parse HEAD)"
} > "$BUILD_DIR/SOURCE_REVISIONS"

bash "$SCRIPT_DIR/build/build-linux-agent.sh" "$BUILD_DIR" "$BUILDER_REPO_ROOT/agent_go" "$WORKSPACE_ROOT"
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/video-studio-workspace" "$BUILDER_REPO_ROOT/workspace")
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/video-studio-browser" "$BUILDER_REPO_ROOT/workspace/cmd/shared-browser")
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/video-studio-landlock-runner" "$BUILDER_REPO_ROOT/workspace/cmd/landlock-runner")
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$BUILD_DIR/bin/workspace-security.test" "$BUILDER_REPO_ROOT/workspace/security")
slots_build "$WORKSPACE_ROOT" "$DEPLOY_GOWORK" "$BUILDER_REPO_ROOT" "$BUILD_DIR"
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/mcpbridge" ./mcpagent/cmd/mcpbridge)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/bin/video-studio-gateway" "$SCRIPT_DIR/server/auth-gateway.go"
vault_build "$REPO_ROOT" "$BUILD_DIR"
if [[ ! -x "$REPO_ROOT/frontend/node_modules/.bin/tsc" ]]; then
  (cd "$REPO_ROOT/frontend" && npm ci)
fi
(cd "$REPO_ROOT/frontend" && VITE_API_BASE_URL='' VITE_WORKSPACE_API_URL=/api/wp npm run build)
cp -R "$REPO_ROOT/frontend/dist/." "$BUILD_DIR/frontend/"
fi
vault_check_build "$BUILD_DIR"
node "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$BUILD_DIR/frontend"
cp "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$BUILD_DIR/check-release-assets.mjs"
cp "$REPO_ROOT/deploy/common/prune-releases.py" "$BUILD_DIR/prune-releases.py"
cp "$REPO_ROOT/deploy/common/slots.sh" "$BUILD_DIR/slots.sh"
cp "$REPO_ROOT/deploy/common/slotcheck.sh" "$BUILD_DIR/slotcheck.sh"
cp "$REPO_ROOT/deploy/common/admission_scan.py" "$BUILD_DIR/admission_scan.py"
install -m 0755 "$REPO_ROOT/scripts/migrate_workflow_builder_chats.py" "$BUILD_DIR/migrations/migrate_workflow_builder_chats.py"
install -m 0644 "$SCRIPT_DIR/workflow-builder-chat-owners-v1.json" "$BUILD_DIR/migrations/workflow-builder-chat-owners-v1.json"
# frontend's build:report-preview step (part of `npm run build` above) writes
# report-preview.js to agent_go/cmd/server/static/ in the source checkout,
# never into the release. video-studio-agent runs with
# WorkingDirectory=.../current and resolves staticFrontendDir()'s default
# ("./static/") against that cwd, so without this copy preview_report always
# 503s with "Report preview runtime is missing" regardless of how many times
# the frontend gets built.
mkdir -p "$BUILD_DIR/static"
static_source="$REPO_ROOT/agent_go/cmd/server/static"
[[ -z "$PREBUILT" ]] || static_source="$PREBUILT/static"
cp -R "$static_source/." "$BUILD_DIR/static/"
install -m 0644 "$SCRIPT_DIR/server/runtime-config.js" "$BUILD_DIR/frontend/runtime-config.js"
# This deployment's own branding assets (logo, mark, favicon), served at /brand/.
if [[ -d "$SCRIPT_DIR/server/brand" ]]; then
  install -d -m 0755 "$BUILD_DIR/frontend/brand"
  install -m 0644 "$SCRIPT_DIR/server/brand"/* "$BUILD_DIR/frontend/brand/"
fi
# One shared MCP catalog for every deployment, plus this one's differences.
python3 "$REPO_ROOT/deploy/common/build-mcp-catalog.py" "$REPO_ROOT/agent_go/configs/mcp_servers_clean.json" "$BUILD_DIR/configs/mcp_servers_video_studio.json" "$SCRIPT_DIR/server/mcp-servers.override.json"
chmod 0644 "$BUILD_DIR/configs/mcp_servers_video_studio.json"
install -m 0755 "$SCRIPT_DIR/server/chrome-headless-wrapper.sh" "$BUILD_DIR/browser/agentworks-chrome-headless"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-workspace.service" "$BUILD_DIR/systemd/video-studio-workspace.service"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-agent.service" "$BUILD_DIR/systemd/video-studio-agent.service"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-gateway.service" "$BUILD_DIR/systemd/video-studio-gateway.service"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-logrotate.conf" "$BUILD_DIR/systemd/video-studio-logrotate.conf"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-logrotate.service" "$BUILD_DIR/systemd/video-studio-logrotate.service"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-logrotate.timer" "$BUILD_DIR/systemd/video-studio-logrotate.timer"
# Scheduled coding-agent CLI updates: the script ships with the release, the
# timer is installed and enabled at swap time. Releases do not update CLIs
# themselves (only install a missing one in the preflight); the timer does.
install -m 0755 "$SCRIPT_DIR/server/update-coding-clis.sh" "$BUILD_DIR/bin/update-coding-clis"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-cli-update.service" "$BUILD_DIR/systemd/video-studio-cli-update.service"
install -m 0644 "$SCRIPT_DIR/rootless/video-studio-cli-update.timer" "$BUILD_DIR/systemd/video-studio-cli-update.timer"
cp -R "$REPO_ROOT/agent_go/internal/videoproduct/skills/." "$BUILD_DIR/claude-skills/"
cp -R "$REPO_ROOT/playbooks/." "$BUILD_DIR/playbooks/"

# Workflow steps run as slot accounts, which cannot enter the service account's private ~/.cache: activation copies the
# HyperFrames Chrome to <app>/tools/chrome/<version>/ (world-readable) and points AGENT_BROWSER_EXECUTABLE_PATH there
# (PLAT-758; the rootless deploy already uses <app>/tools/chrome).
REMOTE_TOOLS_DIR="/var/lib/video-studio/.local"
"${SSH[@]}" "command -v python3 >/dev/null"
REMOTE_BROWSER_PATH="$("${SSH[@]}" "set -e; export HOME=/var/lib/video-studio; npx --yes 'hyperframes@$HYPERFRAMES_VERSION' browser ensure >/dev/null; npx --yes 'hyperframes@$HYPERFRAMES_VERSION' browser path | tail -n 1")"
case "$REMOTE_BROWSER_PATH" in
  /var/lib/video-studio/.cache/hyperframes/chrome/*/chrome-headless-shell) ;;
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
"${SSH[@]}" bash -s -- "$REMOTE_RELEASE" "$REMOTE_APP" "$REMOTE_TOOLS_DIR" "$RELEASE_ID" "$AGENTWORKS_PROVIDER" "$AGENTWORKS_MODEL" <<'REMOTE_PREFLIGHT'
set -euo pipefail
remote_release="$1"
remote_app="$2"
tools_dir="$3"
release_id="$4"
agentworks_provider="$5"
agentworks_model="$6"
env_file="$remote_app/.env"
global_file="$remote_app/.globals-$release_id"

install -d -m 0755 "$HOME/Downloads" "$remote_app/logs" /data/video-studio/docs/Downloads
ln -sfn "$remote_app/logs" "$remote_release/logs"
test -x "$remote_release/bin/video-studio-landlock-runner"
# Browser recordings (.mp4 / .webm) are encoded by agent-browser with ffmpeg.
encoders="$(ffmpeg -hide_banner -encoders 2>/dev/null)"
grep -qw libx264 <<<"$encoders" && grep -qw libvpx <<<"$encoders" || { echo "ffmpeg lacks libx264 or libvpx; browser recordings cannot encode" >&2; exit 1; }
# This is stronger than checking the host sysctl: it proves that the scoped
# AppArmor exception and the fallback itself both work for this release.
"$remote_release/bin/workspace-security.test" -test.run TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy -test.v
# Qualify the sandbox's CLI, not merely the host PATH (PLAT-599).
AGENTWORKS_BROWSER_CLI_CHECK=1 AGENT_BROWSER_CLI_DIR="$tools_dir/bin" \
  AGENTWORKS_LANDLOCK_RUNNER="$remote_release/bin/video-studio-landlock-runner" \
  "$remote_release/bin/workspace-security.test" -test.run '^TestConfiguredBrowserCLIInSandbox$' -test.v

# The normal release step retains MCP_API_URL, so correct it before that
# idempotent merge instead of trusting an older, Docker-only value.
awk '!/^MCP_API_URL=/' "$env_file" > "$env_file.next"
echo 'MCP_API_URL=http://127.0.0.1:8000' >> "$env_file.next"
# gog's headless file keyring requires a stable encryption password. Generate
# it once, keep it only in the service's mode-0600 env file, and preserve it
# across later releases. Without this, OAuth import waits for a TTY prompt and
# a server-connected Gmail account cannot be registered with gog.
if ! grep -q '^GOG_KEYRING_PASSWORD=' "$env_file.next"; then
  printf 'GOG_KEYRING_PASSWORD=%s\n' "$(openssl rand -hex 32)" >> "$env_file.next"
fi
grep -q '^GOG_KEYRING_BACKEND=' "$env_file.next" || echo 'GOG_KEYRING_BACKEND=file' >> "$env_file.next"
# The AgentWorks LLM is fixed by the deploy, not by whoever last edited the
# box: one provider/model as the default, LLM_CONFIG_LOCKED so the UI shows
# "locked by admin" and the server ignores any other choice, and a published
# list with exactly that one entry so nothing else is offered. Video Studio
# keeps claude-code through its own product profile.
awk '!/^(AGENT_PROVIDER|AGENT_MODEL|LLM_CONFIG_LOCKED|DEFAULT_PUBLISHED_LLMS)=/' "$env_file.next" > "$env_file.next2"
mv "$env_file.next2" "$env_file.next"
{
  echo "AGENT_PROVIDER=$agentworks_provider"
  echo "AGENT_MODEL=$agentworks_model"
  echo "LLM_CONFIG_LOCKED=true"
  printf "DEFAULT_PUBLISHED_LLMS='[{\"id\":\"agentworks-default\",\"name\":\"%s (%s)\",\"provider\":\"%s\",\"model_id\":\"%s\"}]'\n" "$agentworks_provider" "$agentworks_model" "$agentworks_provider" "$agentworks_model"
} >> "$env_file.next"
# Per-user accounts (deploy/common/slots.sh, deploy/common/provision-slots.sh): on a host an administrator has
# provisioned, install the tmux front-end and run in opt-in mode, so a user who holds a slot runs as it
# and everyone else (shared Video Studio and Workflow runs) is unchanged until their folders are slot-aware.
awk '!/^AGENTWORKS_SLOTS=/' "$env_file.next" > "$env_file.next2" && mv "$env_file.next2" "$env_file.next"
if [ -r /etc/agentworks/slots.json ]; then
  . "$remote_release/slots.sh"
  slots_install_shim "$remote_app" "$remote_release" >/dev/null
  echo 'AGENTWORKS_SLOTS=optin' >> "$env_file.next"
fi
# Outside MCP connections may edit Builder plans/code and write Relays (owner 2026-10-05); per-person limits unchanged (PLAT-487).
awk '!/^AGENTWORKS_MCP_BUILDER_ENABLED=/' "$env_file.next" > "$env_file.next2" && mv "$env_file.next2" "$env_file.next"
echo 'AGENTWORKS_MCP_BUILDER_ENABLED=true' >> "$env_file.next"
# New Crews are created at the shared Crew/ root, from the UI too (owner 2026-10-05, PLAT-442).
awk '!/^AGENTWORKS_CREW_SHARED_ROOT=/' "$env_file.next" > "$env_file.next2" && mv "$env_file.next2" "$env_file.next"
echo 'AGENTWORKS_CREW_SHARED_ROOT=on' >> "$env_file.next"
chmod 600 "$env_file.next"
mv "$env_file.next" "$env_file"

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
  # into the same dominion-owned tool prefix as claude. Cursor's installer
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
    *"session limit"*|*"usage limit"*|*"rate limit"*|*"Rate limit"*|*"capacity"*)
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
  *"session limit"*|*"usage limit"*|*"rate limit"*|*"Rate limit"*|*"capacity"*)
    echo "WARNING: AGENTS.md read check skipped, the Claude account is capped right now (${probe_result})." >&2 ;;
  *)
    echo "This claude ($("$tools_dir/bin/claude" --version)) does not read AGENTS.md, so sessions would start without their system prompt: ${probe_result:-no reply}" >&2
    exit 1 ;;
esac
REMOTE_PREFLIGHT
# Carry the previous release's hashed frontend assets into the new one. A tab
# opened before the swap still lazy-imports chunks by their old hashed names on
# its next navigation; without these files it fails with "Failed to fetch
# dynamically imported module" and shows "Something went wrong" (server A,
# 2026-09-03, three deploys in one afternoon). Hashed names never collide, so
# only missing files are copied (-n), mtimes are preserved (-p) and anything
# carried for more than 14 days is dropped so the directory cannot grow forever.
"${SSH[@]}" "set -e; prev='$REMOTE_APP/current/frontend/assets'; next='$REMOTE_RELEASE/frontend/assets'; if [ -d \"\$prev\" ] && [ -d \"\$next\" ]; then cp -pn \"\$prev\"/* \"\$next\"/ 2>/dev/null || true; find \"\$next\" -type f -mtime +14 -delete; fi"
# Production activation is intentionally breaking: swap immediately after a
# complete, verified build. The logical active-session count includes retained
# idle chats and cannot reliably distinguish user work from stale ownership, so
# it must not hold a release indefinitely. Service restart is the explicit
# deployment boundary; clients reconnect to the new runtime.
echo "activation: immediate breaking deploy (logical-session drain disabled)"
# Migrate a legacy release-local overlay before swapping current. Never replace
# an existing durable overlay; only the base catalog is refreshed on startup.
"${SSH[@]}" 'set -e; state="$HOME/.local/state/agentworks/mcp"; old="$HOME/video-studio/current/configs/mcp_servers_video_studio_user.json"; install -d -m 0700 "$state"; if [ -f "$old" ] && [ ! -e "$state/mcp_servers_video_studio_user.json" ]; then cp -n "$old" "$state/mcp_servers_video_studio_user.json"; chmod 600 "$state/mcp_servers_video_studio_user.json"; fi'
# Slot accounts must reach this release's Landlock launcher: releases/ was 0700 on server A and every slotted command failed
# with "fork/exec ...: permission denied" (PLAT-478). releases/ 0711, the release and bin/ o+x. No-op without slots.
slots_release_traversal "$REMOTE_APP" "$BUILD_DIR"
vault_prepare "$BUILD_DIR" "$REMOTE_APP" /data/video-studio/docs video-studio 8000 "$VAULT_PORT"
vault_install "$BUILD_DIR" "$REMOTE_APP" video-studio

"${SSH[@]}" "set -e; stopped=0; trap 'if [ \"\$stopped\" = 1 ]; then systemctl --user restart video-studio-workspace video-studio-agent video-studio-gateway || true; fi' EXIT; browser_dir='$(dirname "$REMOTE_BROWSER_PATH")'; shared_browser_dir=\"$REMOTE_APP/tools/chrome/\$(basename \"\$(dirname \"\$browser_dir\")\")/\$(basename \"\$browser_dir\")\"; install -d -m 0755 '$REMOTE_APP/tools' '$REMOTE_APP/tools/chrome' \"\$(dirname \"\$shared_browser_dir\")\" \"\$shared_browser_dir\"; rsync -a --delete --exclude deb.deps --chmod=D0755,Fa+rX \"\$browser_dir/\" \"\$shared_browser_dir/\"; browser_dir=\"\$shared_browser_dir\"; browser_wrapper=\"\$browser_dir/agentworks-chrome-headless\"; install -m 0755 '$REMOTE_RELEASE/browser/agentworks-chrome-headless' \"\$browser_wrapper\"; env_file='$REMOTE_APP/.env'; global_file='$REMOTE_APP/.globals-$RELEASE_ID'; awk '!/^GLOBAL_SECRET_|^CLAUDE_CODE_OAUTH_TOKEN=|^CURSOR_API_KEY=|^AGENT_BROWSER_EXECUTABLE_PATH=/' \"\$env_file\" > \"\$env_file.next\"; echo \"AGENT_BROWSER_EXECUTABLE_PATH=\$browser_wrapper\" >> \"\$env_file.next\"; grep -q '^MCP_API_URL=' \"\$env_file.next\" || echo 'MCP_API_URL=http://127.0.0.1:8000' >> \"\$env_file.next\"; cat \"\$global_file\" >> \"\$env_file.next\"; chmod 600 \"\$env_file.next\"; mv \"\$env_file.next\" \"\$env_file\"; rm -f \"\$global_file\"; find /data/video-studio/docs/_users -type d -path '*/Chats/Video Studio/projects' -print0 | while IFS= read -r -d '' projects_root; do find \"\$projects_root\" -mindepth 1 -maxdepth 1 -type d -print0 | while IFS= read -r -d '' project; do install -d -m 0755 \"\$project/.claude/skills\"; rsync -a --delete '$REMOTE_RELEASE/claude-skills/' \"\$project/.claude/skills/\"; rm -rf \"\$project/skills/video-studio\"; done; done; install -d -m 0755 \"\$HOME/.config/systemd/user\" '$REMOTE_APP/state'; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-workspace.service' \"\$HOME/.config/systemd/user/video-studio-workspace.service\"; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-agent.service' \"\$HOME/.config/systemd/user/video-studio-agent.service\"; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-gateway.service' \"\$HOME/.config/systemd/user/video-studio-gateway.service\"; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-logrotate.conf' '$REMOTE_APP/state/logrotate.conf'; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-logrotate.service' \"\$HOME/.config/systemd/user/video-studio-logrotate.service\"; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-logrotate.timer' \"\$HOME/.config/systemd/user/video-studio-logrotate.timer\"; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-cli-update.service' \"\$HOME/.config/systemd/user/video-studio-cli-update.service\"; install -m 0644 '$REMOTE_RELEASE/systemd/video-studio-cli-update.timer' \"\$HOME/.config/systemd/user/video-studio-cli-update.timer\"; systemctl --user daemon-reload; systemctl --user enable --now video-studio-logrotate.timer; systemctl --user disable --now video-studio-cli-update.timer || true; stopped=1; systemctl --user stop video-studio-agent; ln -sfn '$REMOTE_RELEASE' '$REMOTE_APP/current'; migration_state='$REMOTE_APP/state/migrations'; migration_marker=\"\$migration_state/workflow-builder-chats-v1.done\"; install -d -m 0700 \"\$migration_state\"; if [ ! -f \"\$migration_marker\" ]; then python3 '$REMOTE_RELEASE/migrations/migrate_workflow_builder_chats.py' --workspace-root /data/video-studio/docs --owner-map '$REMOTE_RELEASE/migrations/workflow-builder-chat-owners-v1.json' --apply; marker_tmp=\$(mktemp \"\$migration_state/.workflow-builder-chats-v1.XXXXXX\"); printf 'release=%s\\ncompleted_at=%s\\n' '$RELEASE_ID' \"\$(date -u +%Y-%m-%dT%H:%M:%SZ)\" > \"\$marker_tmp\"; chmod 0600 \"\$marker_tmp\"; mv \"\$marker_tmp\" \"\$migration_marker\"; fi; secrets_marker=\"\$migration_state/product-secrets-v1.done\"; install -d -m 0700 \"\$migration_state\"; if [ ! -f \"\$secrets_marker\" ]; then ( set -a; . '$REMOTE_APP/.env'; set +a; exec '$REMOTE_RELEASE/bin/video-studio-agent' server migrate-product-secrets --product video-studio --apply ); secrets_tmp=\$(mktemp \"\$migration_state/.product-secrets-v1.XXXXXX\"); printf 'release=%s\\ncompleted_at=%s\\n' '$RELEASE_ID' \"\$(date -u +%Y-%m-%dT%H:%M:%SZ)\" > \"\$secrets_tmp\"; chmod 0600 \"\$secrets_tmp\"; mv \"\$secrets_tmp\" \"\$secrets_marker\"; fi; if ! '$REMOTE_RELEASE/bin/video-studio-agent' server migrate-chat-events --docs-root /data/video-studio/docs --state-root '$REMOTE_APP/state'; then echo 'WARNING: legacy chat import failed; the agent retries it at startup' >&2; fi; systemctl --user restart video-studio-workspace video-studio-agent video-studio-gateway; stopped=0; systemctl --user is-active video-studio-agent video-studio-workspace video-studio-gateway; grep -Fq 'apiBaseUrl: \"\",' '$REMOTE_APP/current/frontend/runtime-config.js'; grep -Fq 'workspaceApiBaseUrl: \"/api/wp\",' '$REMOTE_APP/current/frontend/runtime-config.js'"

vault_start video-studio "$VAULT_PORT"

"${SSH[@]}" "set -e; test -s '$REMOTE_APP/logs/agent.log'; tail -n 5 '$REMOTE_APP/logs/agent.log'"

# Keep the active release and any older files still used by retained sessions.
# No rollback archive is retained after a healthy deployment.
"${SSH[@]}" "set -e; rm -f '$REMOTE_RELEASE/.deploying'; python3 '$REMOTE_RELEASE/prune-releases.py' '$REMOTE_APP' --apply --health-url http://127.0.0.1:8000/api/health --health-url http://127.0.0.1:8080/health"

# The slot self-test (PLAT-478), read-only: on a slot host (the slot table exists), a real slotted `pwd` per slot in the
# docs root, a workflow, a Crew and a Code project, plus the config checks. The release is already live: a failure
# does not roll back, it fails the deploy loudly with FAIL lines that say what to fix.
if slots_enabled; then
  if ! slots_selfcheck "$REMOTE_APP" /data/video-studio/docs video-studio "$REMOTE_RELEASE"; then
    echo "Rootless Video Studio release is live at https://video.realtrainingsys.com, but the SLOT SELF-TEST FAILED (see FAIL lines above)" >&2
    exit 1
  fi
fi

echo "Rootless Video Studio release deployed: https://video.realtrainingsys.com"
