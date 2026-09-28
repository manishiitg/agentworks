#!/usr/bin/env bash
# Build-and-deploy Dominion (Hetzner) natively on the box itself.
#
# Run this AS the `dominion` user, ON the Dominion server — it clones/pulls
# the three public source repos there and builds with the box's own native
# Go toolchain (linux/amd64), instead of cross-compiling on a laptop and
# scp-ing the result over. That avoids two classes of mistake a manual
# cross-compiled deploy hit on 2026-09-07 (see below).
#
# Usage:
#   deploy-dominion.sh              # clone/pull, build, stage a new release. Does NOT touch `current` or restart anything.
#   deploy-dominion.sh --activate   # also flip `current` to the just-built release and restart the services, with a health-check and automatic rollback.
#   DOMINION_BUILDER_REF=<branch> deploy-dominion.sh --activate # test a builder branch while sibling repositories use main.
#
# Lessons baked in from a bad manual deploy this session:
#   1. agent_go's actual `go build` target is the module root (agent_go/,
#      package main in main.go) — NOT ./cmd/server, which is a *library*
#      package. `go build -o file ./cmd/server` does not error on a
#      non-main package; it silently writes an `ar` archive (.a format)
#      instead of an ELF executable, which then fails at exec() time with
#      a cryptic systemd `status=203/EXEC`. Building the module root, then
#      independently verifying the output is a real executable (`file` +
#      an actual --help invocation) before touching anything live, is not
#      optional.
#   2. frontend/public/runtime-config.js is a hand-maintained, per-deployment
#      file (Dominion's routes through dominion-gateway, sets
#      enabledProductSurfaces, etc.). `npm run build`'s own dist/ output
#      ships a *different*, generic dev-pointing runtime-config.js
#      (api/workspace URLs pointing at a developer's own 127.0.0.1 ports).
#      That generic one must never reach a release — always overwrite it
#      with the current release's copy after building.
#   3. Never flip `current` or restart a service on an unverified binary.
#      A 200 from `file` + `go build` exit 0 is not proof of a working
#      executable — actually running it is.
set -euo pipefail

SRC_ROOT="/srv/dominion/src"
GO_BIN="/srv/dominion/tools/go/bin/go"
RELEASES_ROOT="/srv/dominion/releases"
CURRENT_LINK="/srv/dominion/current"

# This script runs over a plain non-interactive `ssh host 'cmd'` exec, which
# does not source .bashrc/.profile (those only run for interactive/login
# shells) and never loads /srv/dominion/.env's PATH= line either -- that file
# is only read by systemd via EnvironmentFile=. So /srv/dominion/tools/bin
# (where claude/gog/gws/surge/agent-browser all install to) is not on PATH
# here unless this script puts it there itself. 2026-09-09: `command -v
# agent-browser` failed right after a successful install for exactly this
# reason.
export PATH="/srv/dominion/tools/bin:$PATH"

REPO="$SRC_ROOT/mcp-agent-builder-go"
MLP="$SRC_ROOT/multi-llm-provider-go"
MCPAGENT="$SRC_ROOT/mcpagent"

ACTIVATE=0
BUILDER_REF="${DOMINION_BUILDER_REF:-main}"
git check-ref-format --branch "$BUILDER_REF" >/dev/null || { echo "Invalid DOMINION_BUILDER_REF: $BUILDER_REF" >&2; exit 1; }
command -v python3 >/dev/null || { echo 'Missing python3 (required for release cleanup)' >&2; exit 1; }
if [[ "${1:-}" == "--activate" ]]; then
  ACTIVATE=1
fi

if [[ ! -x "$GO_BIN" ]]; then
  echo "FATAL: Go toolchain not found at $GO_BIN — install it first:" >&2
  echo "  curl -fsSL -o /tmp/go.tar.gz https://go.dev/dl/go1.27.1.linux-amd64.tar.gz" >&2
  echo "  tar -C /srv/dominion/tools -xzf /tmp/go.tar.gz" >&2
  exit 1
fi

echo "==> Ensuring agent-browser is installed (preview_report and every browser-automation tool shell out to it by name; nothing checks it's on PATH until it fails at runtime)"
# Dominion's default npm prefix is /usr (root-owned; confirmed 2026-09-08 via
# `npm config get prefix`), which the rootless `dominion` user cannot write to
# -- npm install -g without --prefix fails with EACCES. /srv/dominion/tools is
# the box's established per-user tool prefix (already on PATH via .env, same
# place claude/gog/gws/surge are symlinked from).
if ! npm install --prefix /srv/dominion/tools -g agent-browser@latest 2>&1 | tail -5; then
  echo "FATAL: npm install -g agent-browser@latest failed" >&2
  exit 1
fi
if ! command -v agent-browser >/dev/null 2>&1; then
  echo "FATAL: agent-browser still not on PATH after install — check npm's global bin dir is on PATH for the dominion-agent service user" >&2
  exit 1
fi
echo "    agent-browser: $(agent-browser --version 2>&1)"

mkdir -p "$SRC_ROOT"

echo "==> Syncing source repos (all public, no credentials needed)"
sync_repo() {
  local name="$1" dir="$2" ref="${3:-main}"
  if [[ -d "$dir/.git" ]]; then
    git -C "$dir" fetch origin "$ref"
    git -C "$dir" reset --hard FETCH_HEAD
  else
    git clone --depth 50 "https://github.com/manishiitg/${name}.git" "$dir"
    git -C "$dir" fetch origin "$ref"
    git -C "$dir" reset --hard FETCH_HEAD
  fi
  echo "    $name -> $(git -C "$dir" rev-parse --short HEAD)"
}
sync_repo "coding-agent-loop" "$REPO" "$BUILDER_REF"
sync_repo "multi-llm-provider-go" "$MLP"

# This script's own on-disk copy at /srv/dominion/deploy-dominion.sh is what
# actually runs -- it does NOT auto-update from the repo just synced above.
# 2026-09-09: a real deploy silently ran a copy that predated agent-browser
# provisioning, the static/report-preview fix, and the CDP-disable work by a
# full day, because nothing ever re-copied it after those changes landed.
# Replace the file atomically: Bash may still be reading this run's original
# inode. The next invocation gets the new script without changing this run.
SELF_SOURCE="$REPO/deploy/dedicated-vm/deploy-dominion.sh"
SELF_TARGET="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
if ! cmp -s "$SELF_SOURCE" "$SELF_TARGET"; then
  echo "==> Updating this script's own copy at $SELF_TARGET from the repo just synced (takes effect next run)"
  cp "$SELF_SOURCE" "$SELF_TARGET.next"
  chmod +x "$SELF_TARGET.next"
  mv "$SELF_TARGET.next" "$SELF_TARGET"
fi
sync_repo "mcpagent" "$MCPAGENT"
bash "$REPO/agent_go/scripts/install-slack-cli.sh" /srv/dominion/tools
command -v slack >/dev/null
echo "==> Ensuring gog (Gmail connector CLI) is the latest release"
bash "$REPO/deploy/common/install-gog.sh" /srv/dominion/tools
echo "==> Validating authored Playbook packages before staging"
python3 "$REPO/playbooks/scripts/validate_playbooks.py"

# workspace/ and mcpagent/'s own go.mod carry no `replace` directives (only
# agent_go/go.mod does), so without a go.work tying all three siblings
# together, building them here would resolve multi-llm-provider-go/mcpagent
# from the public Go module proxy instead of the exact commits just synced
# above. Mirrors the RTS server-side build (deploy/aws-ec2/server/build-and-activate.sh).
GOWORK_FILE="$SRC_ROOT/go.work"
rm -f "$GOWORK_FILE" # regenerate fresh each run so it can never point at a stale sibling state
(cd "$SRC_ROOT" && "$GO_BIN" work init "$REPO/agent_go" "$REPO/workspace" "$MCPAGENT" "$MLP")

RELEASE_ID="$(git -C "$REPO" rev-parse --short HEAD)-$(date +%Y%m%d%H%M%S)"
RELEASE_DIR="$RELEASES_ROOT/$RELEASE_ID"
mkdir -p "$RELEASE_DIR/bin" "$RELEASE_DIR/configs" "$RELEASE_DIR/frontend" "$RELEASE_DIR/playbooks"
touch "$RELEASE_DIR/.deploying"
trap 'rm -f "$RELEASE_DIR/.deploying"' EXIT
cp "$REPO/deploy/common/prune-releases.py" "$RELEASE_DIR/prune-releases.py"
ln -sfn /srv/dominion/logs "$RELEASE_DIR/logs"
echo "==> Staging release $RELEASE_ID at $RELEASE_DIR"
cp -a "$REPO/playbooks/." "$RELEASE_DIR/playbooks/"
for category in website-growth finance sales customer-success engineering shopify; do
  test -f "$RELEASE_DIR/playbooks/crew-agents/$category/catalog.json" || {
    echo "FATAL: $category Crew catalog is missing from release" >&2
    exit 1
  }
done

export GOWORK="$GOWORK_FILE"

echo "==> Building dominion-agent (agent_go/ module root — the actual main package)"
(cd "$REPO/agent_go" && "$GO_BIN" build -o "$RELEASE_DIR/bin/dominion-agent" .)

echo "==> Building dominion-workspace"
(cd "$REPO/workspace" && "$GO_BIN" build -o "$RELEASE_DIR/bin/dominion-workspace" .)

echo "==> Building video-studio-landlock-runner (hardcoded filename regardless of product — workspace/security/landlock_policy.go resolves it by this exact name)"
(cd "$REPO/workspace" && "$GO_BIN" build -o "$RELEASE_DIR/bin/video-studio-landlock-runner" ./cmd/landlock-runner)

echo "==> Building mcpbridge"
(cd "$MCPAGENT" && "$GO_BIN" build -o "$RELEASE_DIR/bin/mcpbridge" ./cmd/mcpbridge)

echo "==> Building dominion-gateway (generic cookie-auth gateway, shared source with Video Studio)"
"$GO_BIN" build -o "$RELEASE_DIR/bin/dominion-gateway" "$REPO/deploy/aws-ec2/server/auth-gateway.go"

echo "==> Building AgentWorks CLI matrix for /api/downloads/cli (static builds)"
mkdir -p "$RELEASE_DIR/downloads"
CLI_SHA="$(git -C "$REPO" rev-parse HEAD)"
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
  os="${target%-*}"
  arch="${target#*-}"
  name="agentworks-$os-$arch"
  (cd "$REPO/agent_go" && GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 "$GO_BIN" build -ldflags "-X main.cliVersion=$CLI_SHA" -o "$RELEASE_DIR/downloads/$name" ./cmd/agentworks)
  chmod +x "$RELEASE_DIR/downloads/$name"
  (cd "$RELEASE_DIR/downloads" && sha256sum "$name" > "$name.sha256")
done
cp "$REPO/scripts/install-agentworks-cli.sh" "$RELEASE_DIR/downloads/install-agentworks.sh"
printf '{"version":"%s","release":"%s"}\n' "$CLI_SHA" "$RELEASE_ID" > "$RELEASE_DIR/downloads/version.json"

echo "==> Verifying staged CLI downloads (format per OS + checksums)"
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
  os="${target%-*}"
  name="agentworks-$target"
  filetype="$(file -b "$RELEASE_DIR/downloads/$name")"
  case "$os" in
    darwin) want="Mach-O" ;;
    linux) want="ELF" ;;
  esac
  if [[ "$filetype" != *"$want"* ]]; then
    echo "FATAL: downloads/$name is not a valid $want binary (got: $filetype)" >&2
    exit 1
  fi
  (cd "$RELEASE_DIR/downloads" && sha256sum -c "$name.sha256") || { echo "FATAL: downloads/$name.sha256 does not verify" >&2; exit 1; }
done
bash -n "$RELEASE_DIR/downloads/install-agentworks.sh" || { echo "FATAL: staged install-agentworks.sh has a syntax error" >&2; exit 1; }
python3 -c "import json;d=json.load(open('$RELEASE_DIR/downloads/version.json'));assert len(d.get('version',''))==40,d" || { echo "FATAL: staged version.json is missing the commit sha" >&2; exit 1; }

echo "==> Verifying every binary is a real, runnable executable (not just 'go build exit 0')"
for bin in dominion-agent dominion-workspace video-studio-landlock-runner mcpbridge dominion-gateway; do
  path="$RELEASE_DIR/bin/$bin"
  chmod +x "$path"
  filetype="$(file -b "$path")"
  if [[ "$filetype" != *"ELF"* ]]; then
    echo "FATAL: $bin is not a valid ELF executable (got: $filetype)" >&2
    exit 1
  fi
done
if ! "$RELEASE_DIR/bin/dominion-agent" --help >/dev/null 2>&1; then
  echo "FATAL: dominion-agent built but --help failed to run — do not deploy this" >&2
  exit 1
fi
echo "    all 5 binaries verified as real, runnable ELF executables"

echo "==> Building frontend"
(cd "$REPO/frontend" && npm ci && VITE_API_BASE_URL='' VITE_WORKSPACE_API_URL=/api/wp npm run build)
cp -R "$REPO/frontend/dist/." "$RELEASE_DIR/frontend/"
# Cloudflare can override the gateway's no-cache header for runtime-config.js.
# Give each release a new script URL so browsers reload the product allowlist.
python3 - "$RELEASE_DIR/frontend/index.html" "$RELEASE_ID" <<'PY'
import pathlib, re, sys
path = pathlib.Path(sys.argv[1])
source = path.read_text()
updated, count = re.subn(r'(/runtime-config\.js)(?:\?[^"\s]*)?',
                         lambda match: match.group(1) + '?v=' + sys.argv[2], source)
if count != 1:
    raise SystemExit(f'FATAL: expected one runtime-config.js script in {path}, found {count}')
path.write_text(updated)
PY
node "$REPO/frontend/scripts/check-release-assets.mjs" "$RELEASE_DIR/frontend"

# frontend's build:report-preview step (part of `npm run build` above) writes
# report-preview.js to agent_go/cmd/server/static/ in the SOURCE checkout, not
# into the release. dominion-agent runs with WorkingDirectory=$CURRENT_LINK
# and resolves staticFrontendDir()'s default ("./static/") against that cwd,
# so without this copy preview_report always 503s with "Report preview
# runtime is missing" no matter how many times the frontend gets built.
mkdir -p "$RELEASE_DIR/static"
cp -R "$REPO/agent_go/cmd/server/static/." "$RELEASE_DIR/static/"

echo "==> Restoring the hand-maintained runtime-config.js from the current release (see lesson #2 above)"
if [[ -f "$CURRENT_LINK/frontend/runtime-config.js" ]]; then
  cp "$CURRENT_LINK/frontend/runtime-config.js" "$RELEASE_DIR/frontend/runtime-config.js"
  echo "    restored from $CURRENT_LINK/frontend/runtime-config.js"
else
  echo "WARNING: no existing runtime-config.js found at $CURRENT_LINK — this release ships whatever npm run build produced. Verify it by hand before activating." >&2
fi
if grep -q 'cdpEnabled:' "$RELEASE_DIR/frontend/runtime-config.js"; then
  sed -i -E 's/cdpEnabled:[[:space:]]*(true|false)/cdpEnabled: false/' "$RELEASE_DIR/frontend/runtime-config.js"
else
  sed -i '/window.__APP_RUNTIME_CONFIG__ = {/a\  cdpEnabled: false,' "$RELEASE_DIR/frontend/runtime-config.js"
fi
grep -Fq 'cdpEnabled: false' "$RELEASE_DIR/frontend/runtime-config.js" || {
  echo "FATAL: Dominion runtime config does not display CDP as disabled" >&2
  exit 1
}
python3 - "$RELEASE_DIR/frontend/runtime-config.js" <<'PY'
import json, pathlib, re, sys
path = pathlib.Path(sys.argv[1])
source = path.read_text()
pattern = r'enabledProductSurfaces:\s*(\[[^\]]*\])'
match = re.search(pattern, source)
if not match:
    raise SystemExit('FATAL: Dominion runtime config has no product surface list')
surfaces = json.loads(match.group(1))
if 'dominion' not in surfaces:
    raise SystemExit('FATAL: Dominion runtime config does not include Dominion')
if 'work' not in surfaces:
    surfaces.append('work')
path.write_text(source[:match.start(1)] + json.dumps(surfaces) + source[match.end(1):])
PY
grep -Fq '"work"' "$RELEASE_DIR/frontend/runtime-config.js" || { echo 'FATAL: Crew is absent from Dominion runtime config' >&2; exit 1; }

echo "==> Copying configs/ unchanged from the current release"
if [[ -d "$CURRENT_LINK/configs" ]]; then
  cp -a "$CURRENT_LINK/configs/." "$RELEASE_DIR/configs/"
fi

echo ""
echo "Release staged: $RELEASE_DIR"
echo "Currently active: $(readlink -f "$CURRENT_LINK" 2>/dev/null || echo none)"

if [[ "$ACTIVATE" -ne 1 ]]; then
  # Keep this candidate until the next build/activation; repeated staging
  # must not accumulate an archive of every earlier candidate.
  if [[ -d "$CURRENT_LINK" ]]; then
    rm -f "$RELEASE_DIR/.deploying"
    python3 "$RELEASE_DIR/prune-releases.py" /srv/dominion --apply --keep "$RELEASE_ID" \
      --health-url http://127.0.0.1:21000/api/health --health-url http://127.0.0.1:21001/health
  fi
  echo ""
  echo "Not activated (pass --activate to flip + restart). To activate this exact release later:"
  echo "  ln -sfn $RELEASE_DIR $CURRENT_LINK && systemctl --user restart dominion-agent"
  exit 0
fi

PREVIOUS_RELEASE="$(readlink -f "$CURRENT_LINK" 2>/dev/null || true)"
echo ""
echo "==> Activating: flipping $CURRENT_LINK -> $RELEASE_DIR and restarting dominion-workspace, dominion-agent, dominion-gateway"
python3 - /srv/dominion/.env <<'PY'
import os, pathlib, sys, tempfile
path = pathlib.Path(sys.argv[1])
managed = {'AGENT_PRODUCTS': 'dominion,work', 'AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES': 'work'}
lines = [line for line in path.read_text().splitlines() if line.partition('=')[0] not in managed]
lines += [f'{key}={value}' for key, value in managed.items()]
with tempfile.NamedTemporaryFile(mode='w', dir=path.parent, prefix='.env.next.', delete=False) as output:
    output.write('\n'.join(lines) + '\n')
    staged = output.name
os.chmod(staged, 0o600)
os.replace(staged, path)
PY
ln -sfn "$RELEASE_DIR" "$CURRENT_LINK"
mkdir -p "$HOME/.config/systemd/user/dominion-agent.service.d"
printf '%s\n' '[Service]' 'Environment=AGENTWORKS_MCP_STATE_DIR=/srv/dominion/state/mcp' > "$HOME/.config/systemd/user/dominion-agent.service.d/30-durable-mcp.conf"
printf '%s\n' '[Service]' 'Environment=AGENT_BROWSER_CDP_ENABLED=false' > "$HOME/.config/systemd/user/dominion-agent.service.d/20-disable-cdp.conf"
mkdir -p "$HOME/.config/systemd/user/dominion-workspace.service.d"
printf '%s\n' '[Service]' 'Environment=AGENT_BROWSER_CDP_ENABLED=false' > "$HOME/.config/systemd/user/dominion-workspace.service.d/20-disable-cdp.conf"
# Keep service logs bounded (agent.log and workspace.log grew to hundreds of
# MB with no rotation). Same policy as RTS: rotate at 100M, keep 7, every 10m.
mkdir -p "$HOME/.config/systemd/user" /srv/dominion/state
cat > /srv/dominion/state/logrotate.conf <<'LOGROTATE'
/srv/dominion/logs/*.log {
    size 100M
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    copytruncate
    dateext
    dateformat -%Y%m%d-%H%M%S
}
LOGROTATE
printf '%s\n' '[Unit]' 'Description=Rotate Dominion service logs' '' '[Service]' 'Type=oneshot' \
  'ExecStart=/usr/sbin/logrotate --state /srv/dominion/state/logrotate.status /srv/dominion/state/logrotate.conf' \
  'Nice=19' 'IOSchedulingClass=idle' 'CPUQuota=20%' 'MemoryMax=256M' > "$HOME/.config/systemd/user/dominion-logrotate.service"
printf '%s\n' '[Unit]' 'Description=Keep Dominion service logs bounded' '' '[Timer]' 'OnBootSec=5m' 'OnUnitActiveSec=10m' \
  'AccuracySec=1m' 'Persistent=true' '' '[Install]' 'WantedBy=timers.target' > "$HOME/.config/systemd/user/dominion-logrotate.timer"
systemctl --user daemon-reload
systemctl --user enable --now dominion-logrotate.timer || echo "WARNING: could not enable dominion-logrotate.timer" >&2
# gog (Gmail integration) must use its file keyring on this headless box.
# Left on "auto" it picks the user's gnome-keyring over D-Bus, which has no
# unlocked default collection here, and every Gmail reconnect fails with
# "store token: set token: Object does not exist at path /". Same idempotent
# block as deploy/aws-ec2/server/build-and-activate.sh: generate the
# encryption password once, keep it only in the mode-0600 env file, and
# preserve it across releases (changing it would orphan stored tokens).
DOMINION_ENV_FILE=/srv/dominion/.env
if ! grep -q '^GOG_KEYRING_PASSWORD=' "$DOMINION_ENV_FILE"; then
  printf 'GOG_KEYRING_PASSWORD=%s\n' "$(openssl rand -hex 32)" >> "$DOMINION_ENV_FILE"
fi
grep -q '^GOG_KEYRING_BACKEND=' "$DOMINION_ENV_FILE" || echo 'GOG_KEYRING_BACKEND=file' >> "$DOMINION_ENV_FILE"
chmod 600 "$DOMINION_ENV_FILE"
# Deterministic, fail-closed configuration check (shared with Confida and
# SparkQuill) before anything restarts.
PRODUCT=dominion python3 "$REPO/deploy/rootless-linux/deployment_checks.py" preflight
# dominion-agent depends on dominion-workspace (After=dominion-workspace.service
# in its unit), so restart it first -- and it must actually be restarted here:
# until now this script only ever restarted dominion-agent, so dominion-workspace
# kept running the PREVIOUS release's binary (and the previous env) indefinitely
# after every deploy, CDP-disable drop-in included.
systemctl --user restart dominion-workspace
sleep 2
if ! systemctl --user is-active --quiet dominion-workspace; then
  echo "FATAL: dominion-workspace failed to start on the new release — rolling back to $PREVIOUS_RELEASE" >&2
  if [[ -n "$PREVIOUS_RELEASE" ]]; then
    ln -sfn "$PREVIOUS_RELEASE" "$CURRENT_LINK"
    systemctl --user restart dominion-workspace
    sleep 2
    systemctl --user is-active --quiet dominion-workspace && echo "    rollback successful, dominion-workspace active again" || echo "    ROLLBACK ALSO FAILED — needs manual intervention" >&2
  fi
  exit 1
fi
systemctl --user restart dominion-agent
sleep 3
# The gateway binary is rebuilt into every release but was never restarted
# here, so it kept running the previous release indefinitely — gateway fixes
# (e.g. PAT pass-through for CLI/MCP) staged on disk yet never took effect.
systemctl --user restart dominion-gateway
sleep 2
if ! systemctl --user is-active --quiet dominion-gateway; then
  echo "FATAL: dominion-gateway failed to start on the new release — rolling back to $PREVIOUS_RELEASE" >&2
  if [[ -n "$PREVIOUS_RELEASE" ]]; then
    ln -sfn "$PREVIOUS_RELEASE" "$CURRENT_LINK"
    systemctl --user restart dominion-workspace
    sleep 2
    systemctl --user restart dominion-agent
    sleep 3
    systemctl --user restart dominion-gateway
    sleep 2
    systemctl --user is-active --quiet dominion-gateway && echo "    rollback successful, dominion-gateway active again" || echo "    ROLLBACK ALSO FAILED — needs manual intervention" >&2
  fi
  exit 1
fi

# The agent recovers queued conversation turns and warms caches before it
# listens, which can take well over the few seconds slept above. A single
# probe rolled back a healthy release (2026-09-24); wait up to ~2 minutes.
healthy=false
for _ in $(seq 1 60); do
  if curl -fsS -o /dev/null -w '' http://127.0.0.1:21000/api/health; then healthy=true; break; fi
  sleep 2
done
if $healthy; then
  echo "==> Health check passed. Active release: $(readlink -f "$CURRENT_LINK")"
  # The running agent must actually carry the checked configuration (e.g.
  # gog's file keyring), not just the env file on disk.
  PRODUCT=dominion python3 "$REPO/deploy/rootless-linux/deployment_checks.py" running
else
  echo "FATAL: health check failed after restart — rolling back to $PREVIOUS_RELEASE" >&2
  if [[ -n "$PREVIOUS_RELEASE" ]]; then
    ln -sfn "$PREVIOUS_RELEASE" "$CURRENT_LINK"
    systemctl --user restart dominion-workspace
    sleep 2
    systemctl --user restart dominion-agent
    sleep 3
    systemctl --user restart dominion-gateway
    sleep 2
    curl -fsS -o /dev/null http://127.0.0.1:21000/api/health && echo "    rollback successful, service healthy again" || echo "    ROLLBACK ALSO FAILED — needs manual intervention" >&2
  fi
  exit 1
fi

rm -f "$RELEASE_DIR/.deploying"
python3 "$RELEASE_DIR/prune-releases.py" /srv/dominion --apply \
  --health-url http://127.0.0.1:21000/api/health --health-url http://127.0.0.1:21001/health
