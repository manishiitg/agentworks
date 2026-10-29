#!/usr/bin/env bash
# Activate a verified release produced by the shared build host.
# build-and-activate.sh WORKSPACE_ROOT PRODUCT --prebuilt BUILD_DIR [--stage-only]
# WORKSPACE_ROOT must be BUILD_DIR/source. --stage-only assembles a release in
# DEPLOY_APP_ROOT without changing current or starting services.
set -euo pipefail
source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../common" && pwd)/linux-arch.sh"
WORKSPACE_ROOT="$1"
PRODUCT="$2"
shift 2
PREBUILT=""
STAGE_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prebuilt) PREBUILT="$2"; shift 2 ;;
    --stage-only) STAGE_ONLY=1; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
REPO_ROOT="$WORKSPACE_ROOT/mcp-agent-builder-go"
[[ -n "$PREBUILT" ]] || { echo "A prebuilt release is required; create it with ./deploy.sh build." >&2; exit 2; }
SCRIPT_DIR="$REPO_ROOT/deploy/rootless-linux"
# The product config shipped with the deploy job (PRODUCT_CONFIG_DIR), else the copy in this checkout.
PRODUCT_DIR="${PRODUCT_CONFIG_DIR:-$SCRIPT_DIR/products/$PRODUCT}"
EXPECTED_PRODUCT="$PRODUCT"
REMOTE_APP="/srv/$PRODUCT"
if [[ -n "${DEPLOY_APP_ROOT:-}" ]]; then
  [[ "$STAGE_ONLY" == 1 ]] || { echo "DEPLOY_APP_ROOT is only allowed together with --stage-only" >&2; exit 2; }
  REMOTE_APP="$DEPLOY_APP_ROOT"
fi
[[ "$WORKSPACE_ROOT" == "$PREBUILT/source" ]] || { echo "WORKSPACE_ROOT must be $PREBUILT/source" >&2; exit 2; }
# shellcheck disable=SC1091
source "$REPO_ROOT/deploy/common/prebuilt.sh"
export NODE_OPTIONS=--max-old-space-size=2048
export PATH="$REMOTE_APP/tools/node/bin:$REMOTE_APP/tools/bin:$PATH"

test -f "$PRODUCT_DIR/product.env" || { echo "No such product: $PRODUCT_DIR/product.env" >&2; exit 1; }
# shellcheck disable=SC1091
source "$PRODUCT_DIR/product.env"
[[ "$PRODUCT" == "$EXPECTED_PRODUCT" ]] || { echo "product.env PRODUCT=$PRODUCT does not match the deployed product $EXPECTED_PRODUCT" >&2; exit 1; }

for snippet in "${RUNTIME_CONFIG_REQUIRED_SNIPPETS[@]:-}"; do
  [[ -z "$snippet" ]] || grep -Fq "$snippet" "$PRODUCT_DIR/runtime-config.js" || {
    echo "$PRODUCT runtime config is missing required setting: $snippet" >&2
    exit 1
  }
done

  # Nothing is compiled here: refuse the build before touching anything if it does not match this host or its files differ.
  for command in python3 file sha256sum openssl ldd; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
  prebuilt_verify "$REPO_ROOT" "$PREBUILT"
python3 "$REPO_ROOT/deploy/common/verify-cli-release.py" "$PREBUILT/downloads"
builder_revision="$(prebuilt_revision "$PREBUILT" mcp-agent-builder-go)"
test -n "$builder_revision"
RELEASE_ID="${PRODUCT}-${builder_revision:0:8}-$(date +%Y%m%d%H%M%S)"
REMOTE_RELEASE="$REMOTE_APP/releases/$RELEASE_ID"
BUILD_DIR="$REMOTE_RELEASE"
# Make room first (PLAT-545): only when space is short, and never the live release, the newest one before it, or one in use.
python3 "$REPO_ROOT/deploy/common/prune-releases.py" "$REMOTE_APP" --apply --only-if-free-below-gb 15 \
  || echo 'warning: could not make room before the deploy' >&2
mkdir -p "$BUILD_DIR/bin" "$BUILD_DIR/frontend" "$BUILD_DIR/configs"
touch "$BUILD_DIR/.deploying"
source "$REPO_ROOT/deploy/common/slots.sh"
source "$REPO_ROOT/deploy/common/vault.sh"
# The activation steps every server runs (env merge, units, switch, restart, health, prune, self-tests).
source "$REPO_ROOT/deploy/common/activate-release.sh"
UNIT_PREFIX="$PRODUCT"
cleanup_build() {
  activate_recover
  if [[ "$(readlink -f "$REMOTE_APP/current")" != "$BUILD_DIR" && "${KEEP_STAGED:-0}" != 1 ]]; then rm -rf "$BUILD_DIR"; fi
}
trap cleanup_build EXIT

# version.json: which CLI build this server offers and its compatibility epoch (downloads/cli-release.json, from
# deploy/cli-release.json). The CLI installs a release automatically only when the epoch equals the one it was built with;
# bumping the epoch is how a release is marked breaking, and the person is told why.
write_cli_version_json() {
  python3 - "$BUILD_DIR/downloads" "$builder_revision" "$cli_build" "$RELEASE_ID" <<'PY'
import json, os, sys
downloads, version, build, release = sys.argv[1:5]
out = {"version": version, "cli_build": build, "release": release}
path = os.path.join(downloads, "cli-release.json")
if os.path.exists(path):
    rel = json.load(open(path))
    epoch, reason = rel.get("compat_epoch", ""), rel.get("breaking_reason", "")
    if not isinstance(epoch, str) or not isinstance(reason, str) or len(epoch) >= 64 or len(reason) > 200 or any(ord(c) < 32 for c in epoch + reason):
        sys.exit("deploy/cli-release.json: compat_epoch (under 64 chars) and breaking_reason (under 200) must be single-line strings")
    if epoch and not reason.strip():
        sys.exit("deploy/cli-release.json: a compat_epoch needs a breaking_reason the person can read")
    if epoch:
        out["compat_epoch"], out["breaking_reason"] = epoch, reason.strip()
        out["breaking"] = True  # for CLIs older than the epoch (they read only this flag and then stop auto-installing); newer CLIs ignore it
built = os.path.join(downloads, "cli-built-at.txt")
if os.path.exists(built) and open(built).read().strip().isdigit():
    out["built_at"] = int(open(built).read().strip())  # when the CLI was built: only a hint that a newer CLI is not moved back
with open(os.path.join(downloads, "version.json"), "w") as f:
    f.write(json.dumps(out) + "\n")
PY
}

echo "==> [$RELEASE_ID] Copying prebuilt release $(basename "$PREBUILT") (no compile)"
# Plain cp -R, not -a: copied files get today's mtime, as freshly built ones did. The carried-over asset cleanup below
# deletes files by age, and an old build must not have its assets removed for the age of the build.
cp "$PREBUILT/SOURCE_REVISIONS" "$BUILD_DIR/SOURCE_REVISIONS"
mkdir -p "$BUILD_DIR/source" "$BUILD_DIR/downloads"
cp -R "$PREBUILT/source/." "$BUILD_DIR/source/"
# browser and update-coding-clis are used by the server A host only. workspace-security.test goes to every host: the activation runs it.
prebuilt_copy_bin "$PREBUILT" "$BUILD_DIR/bin" "$PRODUCT" "agent workspace gateway" "browser update-coding-clis"
cp -R "$PREBUILT/downloads/." "$BUILD_DIR/downloads/"
cli_build="$(cat "$BUILD_DIR/downloads/cli-build.txt" 2>/dev/null || true)"
write_cli_version_json
cp -R "$PREBUILT/frontend/." "$BUILD_DIR/frontend/"

vault_check_build "$BUILD_DIR"
cp "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$BUILD_DIR/check-release-assets.mjs"
cp "$REPO_ROOT/deploy/common/prune-releases.py" "$BUILD_DIR/prune-releases.py"
cp "$SCRIPT_DIR/deployment_checks.py" "$BUILD_DIR/deployment_checks.py"
# The slot self-test and the secret admission scan travel with the release (./deploy.sh slotcheck runs current's copy).
cp "$REPO_ROOT/deploy/common/slotcheck.sh" "$BUILD_DIR/slotcheck.sh"
cp "$REPO_ROOT/deploy/common/admission_scan.py" "$BUILD_DIR/admission_scan.py"
# frontend's build:report-preview step (part of `npm run build` above) writes
# report-preview.js to agent_go/cmd/server/static/ in the source checkout,
# never into the release. $PRODUCT-agent, like every other product on this
# pattern, runs with WorkingDirectory=.../current and resolves
# staticFrontendDir()'s default ("./static/") against that cwd, so without
# this copy preview_report always 503s with "Report preview runtime is
# missing" regardless of how many times the frontend gets built.
mkdir -p "$BUILD_DIR/static"
static_source="$PREBUILT/static"
cp -R "$static_source/." "$BUILD_DIR/static/"
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
# A deployment's own Pi providers/models (products/<p>/pi-agent: models.json, settings.json; no keys), staged by the Pi
# adapter into every session when EXTRA_ENV sets PI_CLI_AGENT_TEMPLATE_DIR=<app>/current/configs/pi-agent (PLAT-711).
if [[ -d "$PRODUCT_DIR/pi-agent" ]]; then
  install -d -m 0755 "$BUILD_DIR/configs/pi-agent"
  install -m 0644 "$PRODUCT_DIR"/pi-agent/*.json "$BUILD_DIR/configs/pi-agent/"
fi
python3 "$REPO_ROOT/deploy/common/build-mcp-catalog.py" "$REPO_ROOT/agent_go/configs/mcp_servers_clean.json" "$BUILD_DIR/configs/mcp_servers_$PRODUCT.json" "${mcp_override[@]}"
chmod 0644 "$BUILD_DIR/configs/mcp_servers_$PRODUCT.json"
node "$BUILD_DIR/check-release-assets.mjs" "$BUILD_DIR/frontend"

if [[ "${COPY_PLAYBOOKS:-false}" == "true" ]]; then
  # With --prebuilt, REPO_ROOT is the shared build's source: read-only for the product account, and the playbook tests create temporary folders
  # next to the playbooks (server C's first prebuilt deploy failed with PermissionError there, 2026-10-04). The build validated that source once
  # (build-release.sh); the release's own writable copy is validated below.
  mkdir -p "$BUILD_DIR/playbooks"
  cp -R "$REPO_ROOT/playbooks/." "$BUILD_DIR/playbooks/"
  python3 "$BUILD_DIR/playbooks/scripts/validate_playbooks.py" --skills-root "$REPO_ROOT/agent_go/internal/workproduct/skills"
  if [[ -n "${PLAYBOOK_SMOKE_PATH:-}" ]]; then
    test -f "$BUILD_DIR/playbooks/$PLAYBOOK_SMOKE_PATH"
  fi
fi

# Carry the previous release's hashed frontend assets into the new one (open tabs still load their old chunks).
activate_carry_assets "$REMOTE_APP" "$BUILD_DIR"

if [[ "$STAGE_ONLY" == 1 ]]; then
  rm -f "$BUILD_DIR/.deploying"
  KEEP_STAGED=1
  echo "==> [$RELEASE_ID] Stage only: release assembled in $BUILD_DIR; preflight, current and services untouched."
  exit 0
fi

# Video export uses the same encoder as browser recordings plus FFprobe.
# Provision this core tool during deploy, never in a person's export request.
if ! REMOTE_APP="$REMOTE_APP" python3 "$SCRIPT_DIR/install-ffprobe.py"; then
  echo "Video export is not available on this server yet: FFprobe setup failed. Dashboard playback is available." >&2
fi

# The standard runtime profile (deploy/common/runtime_profile.json, docs/design/deploy_unification.md): the same settings on every
# product, written into .env and both services like EXTRA_ENV (a product's own EXTRA_ENV entry for the same key still wins).
mapfile -t STANDARD_ENV < <(python3 - "$REPO_ROOT/deploy/common/runtime_profile.json" "$REMOTE_APP" "$REMOTE_APP/state" <<'PY'
import json, sys
profile, app, data = sys.argv[1:]
for key, value in json.load(open(profile))["same_everywhere"].items():
    print(f"{key}={value.replace('{app}', app).replace('{data}', data)}")
PY
)
# The managed browser's launcher (chrome-agentworks) goes beside the host's Chrome on every server; without a Chrome the setting is
# dropped rather than pointing the browser at a missing file.
if ! "$SCRIPT_DIR/install-managed-chrome.sh" "$REMOTE_APP" >/dev/null; then
  STANDARD_ENV=("${STANDARD_ENV[@]/AGENT_BROWSER_EXECUTABLE_PATH=*/}")
fi
EXTRA_ENV=("${STANDARD_ENV[@]}" "${EXTRA_ENV[@]:-}")
install -d -m 0700 "$REMOTE_APP/state" "$REMOTE_APP/state/browser-profile"

# This server's layout, read by the shared activation. A rootless product is the account PRODUCT in /srv/PRODUCT.
RELEASE_DIR="$BUILD_DIR"
APP_ROOT="$REMOTE_APP"
DOCS_ROOT="$REMOTE_APP/data/docs"
STATE_ROOT="$REMOTE_APP/state"
SERVICE_ACCOUNT="$PRODUCT"
LIVE_URL="https://$DOMAIN"
RUNTIME_PATH="$REMOTE_APP/tools/node/bin:$REMOTE_APP/tools/bin:$REMOTE_APP/home/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
activate_release
