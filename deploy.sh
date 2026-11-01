#!/usr/bin/env bash
# Shared deployment entry point. Each server keeps its existing guarded
# implementation; this script only selects the correct one.
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
SERVER="${1:-}"
[[ $# -eq 0 ]] || shift

usage() {
  cat <<'EOF'
Usage: ./deploy.sh <server> [target]

Servers (each one's host, domain and settings live in the private deployments repo: products/<server>/product.env):
  rts, video-studio     the AWS EC2 deployment
  confida               rootless Linux deployment
  citymall              rootless Linux deployment on its own host, reached through a jump host
  sparkquill            rootless Linux deployment
  excellence            rootless Linux deployment (Code only)
  <product>            any named rootless product configured in the private deployments repo
  all-hetzner           excellence, confida and sparkquill in sequence from ONE build (never dominion)
  dominion              legacy host alias for the shared workflow deployment
  check <server|all>    read-only health check of a server (site, certificate, MCP, website callback, release, services, disk)
  report [server]       how each server differs from the standard runtime profile (read-only)
  agent-test <server> [S1,..]  on-request Crew agent tests (happy paths and security) per provider, in the Smoke test Crew
  smoke <server>        post-deploy smoke check: one real tool call per configured provider over public MCP (PLAT-424); every
                        deploy runs it when the private products/<server>/smoke.env exists. Reports only; never fails a deploy
  slotcheck <server> [basic|full]   the slot self-test of a deployed server (read-only; PLAT-478): a real slotted `pwd` per slot in the
                        docs root, a workflow, a Crew and a Code project, the slotctl/slot table/launcher checks, and the
                        secret admission scan (warnings). Exits 1 on any FAIL. Every deploy of a slot host runs it too.
  build [--force]       build a release of the current main of the three repositories on the build host; deploys nothing
  builds                list the builds on the build host (name, age, the three revisions, pinned) and the builds on GitHub
  publish [build]       upload a build (default: the newest) to github.com/manishiitg/agentworks-builds from the build host
  verify [name]         build, vet and test the commit you are on (HEAD) on the build host with the Verify-Test / Verify-Vitest /
                        Verify-Frontend-Build lines of its commit message, like scripts/verify-remote.sh but faster; deploys nothing
  pin|unpin <build>     keep a known-good build from being pruned (old builds are removed after every deploy; only the newest is kept otherwise)
  prune-builds          remove old builds on the build host now (all but the newest, pinned ones and anything younger than 15 minutes)

Build once, deploy everywhere (PLAT-426): rts, excellence, confida, sparkquill and all-hetzner build the release ONCE on the
Hetzner box (deploy/common/build-release.sh -> /srv/_builds/<name>, reused when the three revisions are unchanged), then each
server only copies and activates it after verifying its manifest (architecture, glibc, every file hash).
  --build <name|sha>    deploy that existing build instead of main's head (see `builds`); its three revisions must be
                        ancestors of origin/main of the three repositories
  DEPLOY_BUILD_TRANSPORT=auto|github|stream   how rts gets the build: RTS downloads it from the public builds repo on GitHub
                        (auto: streaming through this machine is the fallback when the release is missing), github (no fallback), stream (old path)
  DEPLOY_SHA_MCP_AGENT_BUILDER_GO / DEPLOY_SHA_MCPAGENT / DEPLOY_SHA_MULTI_LLM_PROVIDER_GO=<40-hex>   build that commit of the repository instead of main's head
                        (it must already be on main); for a release that leaves out work still landing
  DEPLOY_REFRESH_TOOLS=1    refresh provider CLIs and agent-browser from vendors; normal deploys reuse healthy unchanged tools
  DEPLOY_SECURITY_CHECKS=full|basic   how thorough the slot self-test at the end of a deploy of a slot host is (default full): basic = a
                        slotted `pwd` per slot + the refusal checks; full adds a live tmux server on the test slot that no slot
                        command may reach, the Python helpers, and the refusals a workflow chat must get (PLAT-480). Runs on the
                        unassigned TEST slot only. Example: DEPLOY_SECURITY_CHECKS=basic ./deploy.sh confida
  Build host: BUILD_HOST, BUILD_PORT, BUILD_USER, BUILD_SSH_KEY, BUILDS_DIR (/srv/_builds); defaults in the private deployments/deploy.env

EOF
}

reject_extra_arguments() {
  if [[ $# -gt 0 ]]; then
    echo "Server '$SERVER' does not accept additional arguments." >&2
    usage >&2
    exit 2
  fi
}

# The slot self-test level of a deploy (PLAT-480): full by default, DEPLOY_SECURITY_CHECKS=basic to skip the extended checks.
SECURITY_CHECKS_LEVEL="${DEPLOY_SECURITY_CHECKS:-full}"
case "$SECURITY_CHECKS_LEVEL" in
  basic|full) ;;
  *) echo "DEPLOY_SECURITY_CHECKS must be basic or full (got '$SECURITY_CHECKS_LEVEL')" >&2; exit 2 ;;
esac

# Build once, deploy everywhere (PLAT-426): helpers for the build host (see deploy/common/build-once.sh).
# shellcheck disable=SC1091
# Hosts, ports and addresses are private: the deployments repo's deploy.env (PLAT-719). Environment variables override it.
DEPLOY_ENV_FILE="${AGENTWORKS_DEPLOYMENTS_DIR:-$(cd "$REPO_ROOT/.." && pwd)/deployments}/deploy.env"
# shellcheck source=/dev/null
[[ -f "$DEPLOY_ENV_FILE" ]] && source "$DEPLOY_ENV_FILE"
source "$REPO_ROOT/deploy/common/build-once.sh"

# --- AWS EC2 server (settings in the private deployments repo) -----------
# Sends only deployment instructions and secrets; the server clones main of
# all three repositories and builds the release itself.
# The AWS server's profile, stack, CloudFront distribution, public branding and URL are company-specific, so they live in
# the private deployments repo: products/<name>/aws.env plus runtime-config.js, video-studio-agent.service and brand/ there.
# This loads aws.env into the shell and sets AWS_OVERLAY_DIR to the folder holding the rest. Its product.env (the server's
# folders, unit names, account and ports) is read on the server by build-and-activate.sh and deploy/common/activate-release.sh.
load_aws_server_config() {
  local dir; dir="$(deployments_dir)/products/${1:-video-studio}"
  [[ -f "$dir/aws.env" ]] || { echo "The AWS server's settings are missing: $dir/aws.env (private deployments repo; AGENTWORKS_DEPLOYMENTS_DIR)" >&2; exit 1; }
  # shellcheck disable=SC1091
  set -a; . "$dir/aws.env"; set +a
  AWS_OVERLAY_DIR="$dir"
}
deploy_rts() {
  load_aws_server_config "${AWS_PRODUCT_DIR:-video-studio}"
  [[ -f "$AWS_OVERLAY_DIR/runtime-config.js" && -f "$AWS_OVERLAY_DIR/video-studio-agent.service" && -f "$AWS_OVERLAY_DIR/product.env" ]] || { echo "Missing runtime-config.js, video-studio-agent.service or product.env in $AWS_OVERLAY_DIR" >&2; exit 1; }
  local AWS_PROFILE_NAME="${AWS_PROFILE_NAME:?aws.env must set AWS_PROFILE_NAME}"
  local AWS_REGION="${AWS_REGION:-us-west-2}"
  local STACK_NAME="${STACK_NAME:?aws.env must set STACK_NAME}"
  local SSH_KEY_PATH="${SSH_KEY_PATH:-$HOME/.ssh/id_ed25519}"
  local GLOBAL_SECRETS_SECRET_ID="${GLOBAL_SECRETS_SECRET_ID:-video-studio/global-secrets}"
  local RTS_DIR="$REPO_ROOT/deploy/aws-ec2"
  [[ "${DEPLOY_BRANCH:-main}" == main && "${DEPLOY_SOURCE_MODE:-remote-main}" == remote-main ]] || { echo 'Production deployment requires main from all three repositories.' >&2; exit 1; }
  for command in aws git jq rsync ssh; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
  aws_rts() { aws --profile "$AWS_PROFILE_NAME" --region "$AWS_REGION" "$@"; }
  HOST_IP="$(aws_rts cloudformation describe-stacks --stack-name "$STACK_NAME" --query 'Stacks[0].Outputs[?OutputKey==`ElasticIp`].OutputValue | [0]' --output text)"
  JOB="deploy-$(date +%Y%m%d%H%M%S)-$$"
  REMOTE_JOB="/var/lib/video-studio/video-studio/builds/$JOB"
  STAGING="$(mktemp -d)"
  chmod 700 "$STAGING"
  SSH=(ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "$SSH_KEY_PATH" "video-studio@$HOST_IP")
  rts_cleanup() { rm -rf "$STAGING"; "${SSH[@]}" "rm -rf '$REMOTE_JOB'" >/dev/null 2>&1 || true; }
  trap rts_cleanup EXIT
  # The same standard user-namespace profile as every other server, through SSM (this host has no sudo over ssh).
  rts_userns() { echo "==> [rts] userns: $1"; AWS_PROFILE_NAME="$AWS_PROFILE_NAME" AWS_REGION="$AWS_REGION" STACK_NAME="$STACK_NAME" "$RTS_DIR/slots-admin.sh" "$1" || echo "WARN userns $1 via SSM did not run; the host's operator runs deploy/aws-ec2/slots-admin.sh $1"; }
  rts_userns userns
  local prebuilt_name
  # Build once on the Hetzner box (or reuse the build of these revisions); RTS only copies it after verifying the manifest.
  prebuilt_name="$(ensure_prebuilt_build)" || exit 1
  # The manifest's own hash travels separately from the tarball, so RTS can tell a damaged or swapped manifest.
  build_ssh "sha256sum '$BUILDS_DIR/$prebuilt_name/manifest.json'" | awk '{print $1}' > "$STAGING/prebuilt"
  [[ "$(cat "$STAGING/prebuilt")" =~ ^[0-9a-f]{64}$ ]] || { echo "Cannot read the build's manifest hash" >&2; exit 1; }
  aws_rts secretsmanager get-secret-value --secret-id "$GLOBAL_SECRETS_SECRET_ID" --query SecretString --output text \
   | jq -er 'to_entries[] | select(.key | test("^[A-Z0-9_]+$")) | select(.value | type == "string" and length > 0) | if .key == "CLAUDE_CODE_OAUTH_TOKEN" or .key == "CURSOR_API_KEY" then "\(.key)=\(.value)" else "GLOBAL_SECRET_\(.key)=\(.value)" end' > "$STAGING/globals"
  chmod 600 "$STAGING/globals"
  cp "$RTS_DIR/server/bootstrap-build.sh" "$STAGING/bootstrap-build.sh"
  # This server's own branding, public URL, service file and product.env (its layout, read by the shared activation) travel
  # with the deploy.
  mkdir -p "$STAGING/overlay"
  cp "$AWS_OVERLAY_DIR/runtime-config.js" "$AWS_OVERLAY_DIR/video-studio-agent.service" "$AWS_OVERLAY_DIR/product.env" "$STAGING/overlay/"
  [[ ! -d "$AWS_OVERLAY_DIR/brand" ]] || cp -R "$AWS_OVERLAY_DIR/brand" "$STAGING/overlay/brand"
  # Make room first (PLAT-545): a full disk broke the new gateway's database on 2026-10-05. Only runs when space is short,
  # and never removes the live release, the newest one before it, or anything a running process still uses.
  "${SSH[@]}" "python3 - /var/lib/video-studio/video-studio --apply --only-if-free-below-gb 15" < "$REPO_ROOT/deploy/common/prune-releases.py" \
    || echo 'warning: could not make room on RTS before the deploy' >&2
  "${SSH[@]}" "install -d -m 0700 '$REMOTE_JOB'"
  rsync -az -e "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i $SSH_KEY_PATH" "$STAGING/" "video-studio@$HOST_IP:$REMOTE_JOB/"
  echo "Getting build $prebuilt_name onto RTS (verified there against its manifest); nothing is compiled on RTS."
  deliver_build_to_rts "$prebuilt_name" "$REMOTE_JOB" "$(cat "$STAGING/prebuilt")"
  # Bound activation resources while other work keeps running.
  "${SSH[@]}" "systemd-run --user --quiet --wait --pipe --unit='$JOB' --setenv=SECURITY_CHECKS_LEVEL='$SECURITY_CHECKS_LEVEL' -p MemoryMax=${RTS_BUILD_MEMORY_MAX:-3G} -p CPUQuota=200% -p Nice=10 bash '$REMOTE_JOB/bootstrap-build.sh' '$REMOTE_JOB'"
  # The release is live and its services re-exec'd: retire an older per-product profile (refuses while any service still lacks the standard one).
  rts_userns userns-retire
}

# Read-only CloudFront usage for RTS against the always-free tier (1 TB out,
# 10M requests per month). Reported after every RTS deploy; never fails it.
report_rts_cloudfront_usage() {
  load_aws_server_config "${AWS_PRODUCT_DIR:-video-studio}"
  local profile="${AWS_PROFILE_NAME:?aws.env must set AWS_PROFILE_NAME}" dist="${CLOUDFRONT_DISTRIBUTION_ID:?aws.env must set CLOUDFRONT_DISTRIBUTION_ID}"
  local now month_start day_ago
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  month_start=$(date -u +%Y-%m-01T00:00:00Z)
  day_ago=$(date -u -d '-1 day' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v-1d +%Y-%m-%dT%H:%M:%SZ)
  # CloudFront metrics live in us-east-1 with Region=Global.
  cf_metric() {
    aws --profile "$profile" --region us-east-1 cloudwatch get-metric-statistics \
      --namespace AWS/CloudFront --metric-name "$1" \
      --dimensions Name=DistributionId,Value="$dist" Name=Region,Value=Global \
      --start-time "$2" --end-time "$now" --period "$3" --statistics "$4" \
      --query "$([[ "$4" == Average ]] && echo avg || echo sum)(Datapoints[].$4)" --output text 2>/dev/null || echo None
  }
  echo "--- CloudFront usage ($dist) ---"
  python3 - "$(cf_metric Requests "$month_start" 86400 Sum)" "$(cf_metric BytesDownloaded "$month_start" 86400 Sum)" \
    "$(cf_metric Requests "$day_ago" 3600 Sum)" "$(cf_metric 4xxErrorRate "$day_ago" 86400 Average)" \
    "$(cf_metric 5xxErrorRate "$day_ago" 86400 Average)" <<'PY'
import sys
num = lambda v: float(v) if v not in ("None", "", "null") else 0.0
req, byt, req24, e4, e5 = map(num, sys.argv[1:])
gb = byt / 1024**3
print(f"Month to date: {req:,.0f} requests ({req / 1e7 * 100:.2f}% of free), {gb:,.2f} GB out ({gb / 1024 * 100:.2f}% of free)")
print(f"Last 24h: {req24:,.0f} requests, 4xx {e4:.2f}%, 5xx {e5:.2f}%")
if gb > 0.8 * 1024 or req > 0.8e7:
    print("WARNING: CloudFront usage is above 80% of the monthly free tier.")
PY
}

# Company- and customer-specific server configs (product.env, runtime-config.js, branding, nginx, host setup) live in the
# private deployments repository (github.com/runloop-workflows/deployments), checked out next to this repo as
# ../deployments, or at AGENTWORKS_DEPLOYMENTS_DIR. The copies under deploy/rootless-linux/products are the fallback
# until they are removed from this public repository (PLAT-719).
deployments_dir() { printf '%s\n' "${AGENTWORKS_DEPLOYMENTS_DIR:-$(cd "$REPO_ROOT/.." && pwd)/deployments}"; }
product_config_dir() {
  local private_dir; private_dir="$(deployments_dir)/products/$1"
  if [[ -f "$private_dir/product.env" ]]; then printf '%s\n' "$private_dir"; else printf '%s\n' "$REPO_ROOT/deploy/rootless-linux/products/$1"; fi
}

# --- Post-deploy smoke check (PLAT-424) -------------------------------------
# One real AgentWorks tool call per configured company provider, over the server's public MCP, in a dedicated smoke
# Crew. It reports PASS/FAIL per provider and never fails a deploy. The private products/<name>/smoke.env names the
# saved login on this computer (SMOKE_LOGIN, from `agentworks login --name`) and the Crew (SMOKE_WORKSPACE=work:<id>);
# without it nothing runs. DEPLOY_SMOKE=0 skips it for one deploy.
post_deploy_smoke() {
  local product="$1" config
  config="$(deployments_dir)/products/$product/smoke.env"
  [[ "${DEPLOY_SMOKE:-1}" != 0 ]] || { echo "==> [$product] Smoke check skipped (DEPLOY_SMOKE=0)."; return 0; }
  [[ -f "$config" ]] || { echo "==> [$product] Smoke check not set up (no products/$product/smoke.env)."; return 0; }
  (
    # shellcheck disable=SC1090
    source "$config"
    [[ -n "${SMOKE_LOGIN:-}" && -n "${SMOKE_WORKSPACE:-}" ]] || { echo "==> [$product] smoke.env needs SMOKE_LOGIN and SMOKE_WORKSPACE" >&2; exit 0; }
    echo "==> [$product] Smoke check: per provider, a tool call and then a shell turn in the same chat, in $SMOKE_WORKSPACE"
    python3 -I "$REPO_ROOT/scripts/post-deploy-smoke.py" --login "$SMOKE_LOGIN" --workspace-id "$SMOKE_WORKSPACE" ${SMOKE_PROVIDERS:+--providers "$SMOKE_PROVIDERS"} \
      || echo "==> [$product] Smoke check could not run (the deploy itself succeeded)."
  )
  return 0
}

# --- Rootless Linux products (Confida, SparkQuill) -------------------------
# Repeatable redeploy for a fixed-workspace product running as its own isolated
# Linux account on the shared rootless-systemd Hetzner box. Only /srv/$PRODUCT
# and that product's $PRODUCT-* systemd --user units are touched. Nothing is
# built locally: the server clones main of all three repositories fresh and
# runs build-and-activate.sh from that checkout. Product settings live in
# deploy/rootless-linux/products/<product>/product.env (env overrides:
# HOST_IP, SSH_PORT, SSH_KEY_PATH, DEPLOY_BRANCH).
# Runs in a subshell so product.env globals and the cleanup trap stay scoped.
deploy_rootless_product() (
PRODUCT="${1:?Usage: ./deploy.sh <product> (a directory under deploy/rootless-linux/products/)}"

LOCAL_SCRIPT_DIR="$REPO_ROOT/deploy/rootless-linux"
LOCAL_REPO_ROOT="$REPO_ROOT"                                  # mcp-agent-builder-go (local checkout)
LOCAL_WORKSPACE_ROOT="$(cd "$LOCAL_REPO_ROOT/.." && pwd)"    # sibling repos (mcpagent, ...)
PRODUCT_DIR="$(product_config_dir "$PRODUCT")"

test -f "$PRODUCT_DIR/product.env" || { echo "No such product: $PRODUCT_DIR/product.env not found" >&2; exit 1; }
# shellcheck disable=SC1091
source "$PRODUCT_DIR/product.env"
[[ "$PRODUCT" == "$(basename "$PRODUCT_DIR")" ]] || { echo "product.env PRODUCT=$PRODUCT does not match directory $(basename "$PRODUCT_DIR")" >&2; exit 1; }

DEPLOY_BRANCH="${DEPLOY_BRANCH:-main}"
REMOTE_APP="/srv/$PRODUCT"
REMOTE_TOOLS="$REMOTE_APP/tools"
REMOTE_RUNTIME_PATH="$REMOTE_TOOLS/node/bin:$REMOTE_TOOLS/bin:$REMOTE_APP/home/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

git -C "$LOCAL_WORKSPACE_ROOT/mcpagent" rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "Expected sibling checkout: $LOCAL_WORKSPACE_ROOT/mcpagent" >&2; exit 1; }
git -C "$LOCAL_WORKSPACE_ROOT/multi-llm-provider-go" rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "Expected sibling checkout: $LOCAL_WORKSPACE_ROOT/multi-llm-provider-go" >&2; exit 1; }

# The triggering machine no longer builds anything, so it no longer needs
# go/node/npm -- only enough to talk to git remotes and to the server.
for cmd in git scp ssh; do
  command -v "$cmd" >/dev/null || { echo "Missing $cmd" >&2; exit 1; }
done

# Offer only the named key when that file exists (a wrong key then fails with its real error instead of "Too many authentication failures"). When it
# does not exist (a product's default key path on a machine that logs in through the ssh agent) ssh must be free to use the agent's keys.
SSH_IDENTITY=(-i "$SSH_KEY_PATH")
[[ -r "$SSH_KEY_PATH" ]] && SSH_IDENTITY+=(-o IdentitiesOnly=yes)
# SSH_JUMP (product.env): reach a host whose port 22 admits only the jump host. ProxyJump forwards the
# connection; the key stays on this machine (never copy it to the jump host, never use agent forwarding).
SSH_JUMP_OPTS=()
[[ -z "${SSH_JUMP:-}" ]] || SSH_JUMP_OPTS=(-J "$SSH_JUMP")
SSH_OPTS=(-p "$SSH_PORT" "${SSH_IDENTITY[@]}" ${SSH_JUMP_OPTS[@]+"${SSH_JUMP_OPTS[@]}"} -o BatchMode=yes -o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new)
SSH=(ssh "${SSH_OPTS[@]}" "$PRODUCT@$HOST_IP")
SCP=(scp -P "$SSH_PORT" "${SSH_IDENTITY[@]}" ${SSH_JUMP_OPTS[@]+"${SSH_JUMP_OPTS[@]}"} -o BatchMode=yes -o ConnectTimeout=20 -o StrictHostKeyChecking=accept-new)

# HOST_SETUP_SCRIPT (product.env): an idempotent root preparation of a dedicated host (packages, account, units, nginx),
# run through HOST_SETUP_USER's sudo before every deploy. The product's nginx-site.conf travels with it.
if [[ -n "${HOST_SETUP_SCRIPT:-}" ]]; then
  echo "==> [$PRODUCT] Preparing the host as root ($HOST_SETUP_SCRIPT)"
  nginx_site_b64=""
  [[ ! -f "$PRODUCT_DIR/nginx-site.conf" ]] || nginx_site_b64="$(base64 < "$PRODUCT_DIR/nginx-site.conf" | tr -d '\n')"
  ssh "${SSH_OPTS[@]}" "${HOST_SETUP_USER:?HOST_SETUP_SCRIPT needs HOST_SETUP_USER}@$HOST_IP" \
    "sudo -n env NGINX_SITE_B64='$nginx_site_b64' bash -s" < "$([[ -f "$PRODUCT_DIR/$HOST_SETUP_SCRIPT" ]] && echo "$PRODUCT_DIR/$HOST_SETUP_SCRIPT" || echo "$LOCAL_SCRIPT_DIR/$HOST_SETUP_SCRIPT")"
  # Per-user Linux accounts: every account in the user directory gets its own slot, on every deploy (a new account is covered by
  # the next deploy). The deploy's final check fails if any account still has none.
  if [[ "${SLOTS_ENABLED:-false}" == true ]]; then
    echo "==> [$PRODUCT] Giving every account its own Linux slot"
    ssh "${SSH_OPTS[@]}" "$HOST_SETUP_USER@$HOST_IP" "sudo -n env PRODUCT=$PRODUCT SLOT_PREFIX=${SLOT_PREFIX:-slot} SLOT_COUNT=${SLOT_COUNT:-50} bash -s -- ensure" < "$LOCAL_SCRIPT_DIR/../common/provision-slots.sh"
  fi
fi

# Every Linux server gets the same user-namespace exception the sandbox needs (owner, 2026-10-14), scoped to this product's own
# launcher and nothing else, on every deploy: HOST_SETUP_USER (root through sudo) is all it takes, a HOST_SETUP_SCRIPT is optional.
# A server without one is provisioned by its operator with provision-slots.sh userns. The activation then proves the sandbox
# works, or stops the deploy.
if [[ -n "${HOST_SETUP_USER:-}" ]]; then
  echo "==> [$PRODUCT] Allowing the sandbox's user namespaces for this product's launcher only"
  ssh "${SSH_OPTS[@]}" "$HOST_SETUP_USER@$HOST_IP" "sudo -n env PRODUCT=$PRODUCT SLOT_PREFIX=${SLOT_PREFIX:-slot} SLOT_COUNT=${SLOT_COUNT:-50} bash -s -- userns" < "$LOCAL_SCRIPT_DIR/../common/provision-slots.sh"
else
  echo "==> [$PRODUCT] userns: skipped (no HOST_SETUP_USER); the host's operator runs provision-slots.sh userns"
fi

echo "==> [$PRODUCT] Checking deployment configuration"
"${SSH[@]}" "PRODUCT=$PRODUCT EXPECTED_PUBLIC_URL=${EXPECTED_PUBLIC_URL:-} python3 - preflight" < "$LOCAL_SCRIPT_DIR/deployment_checks.py"

# Build once (or reuse the build of these revisions); the server below only copies and activates it.
PREBUILT_NAME="$(ensure_prebuilt_build)"
[[ -n "$PREBUILT_NAME" ]]
PREBUILT_PATH="$BUILDS_DIR/$PREBUILT_NAME"
# PREBUILT_DELIVERY=fetch: a host that cannot read the build host's folder gets its own copy (GitHub, else streamed).
if [[ "${PREBUILT_DELIVERY:-local}" == fetch ]]; then
  PREBUILT_PATH="$REMOTE_APP/prebuilt/$PREBUILT_NAME"
  deliver_build_to_product_host "$PREBUILT_NAME" "$PREBUILT_PATH"
fi

echo "==> [$PRODUCT] Checking for jq on the remote host (required by agent shell scripts)"
if "${SSH[@]}" 'command -v jq' >/dev/null 2>&1; then
  echo "    jq is present."
else
  echo "    WARNING: jq is NOT installed on $HOST_IP. Agent shell scripts that rely on it will fail." >&2
  echo "    Install it once as root: ssh -p $SSH_PORT root@$HOST_IP 'apt-get install -y jq'" >&2
fi

if [[ -n "${PIN_NODE_VERSION:-}" ]]; then
  echo "==> [$PRODUCT] Ensuring pinned Node $PIN_NODE_VERSION is installed"
  test -n "${PIN_NODE_SHA256:-}" || { echo "PIN_NODE_VERSION is set but PIN_NODE_SHA256 is not" >&2; exit 1; }
  "${SSH[@]}" "set -e
    install -d -m 0755 '$REMOTE_TOOLS'
    node_release='node-v$PIN_NODE_VERSION-linux-x64'
    node_dir='$REMOTE_TOOLS/node-v$PIN_NODE_VERSION'
    if [ ! -x \"\$node_dir/bin/node\" ]; then
      node_stage=\$(mktemp -d '$REMOTE_TOOLS/.node-install.XXXXXX')
      trap 'rm -rf \"\$node_stage\"' EXIT
      curl -fsSL \"https://nodejs.org/dist/v$PIN_NODE_VERSION/\$node_release.tar.xz\" -o \"\$node_stage/\$node_release.tar.xz\"
      printf '%s  %s\\n' '$PIN_NODE_SHA256' \"\$node_stage/\$node_release.tar.xz\" | sha256sum -c -
      tar -xJf \"\$node_stage/\$node_release.tar.xz\" -C \"\$node_stage\"
      mv \"\$node_stage/\$node_release\" \"\$node_dir\"
      rm -f \"\$node_stage/\$node_release.tar.xz\"
      rmdir \"\$node_stage\"
      trap - EXIT
    fi
    ln -sfn \"\$node_dir\" '$REMOTE_TOOLS/node'
    export PATH='$REMOTE_RUNTIME_PATH'
    test \"\$(node --version)\" = 'v$PIN_NODE_VERSION'
    npm --version"
fi

# WhatsApp voice notes arrive as Ogg/Opus. The shared speech engine consumes
# PCM WAV, so every rootless product needs an audio converter even when the
# host administrator has not installed the distro ffmpeg package. Keep the
# pinned binary outside releases so it survives normal deploy/prune cycles.
echo "==> [$PRODUCT] Ensuring ffmpeg is available for voice-note transcription"
if "${SSH[@]}" "export PATH='$REMOTE_RUNTIME_PATH'; command -v ffmpeg" >/dev/null 2>&1; then
  echo "    ffmpeg is present."
else
  "${SSH[@]}" "REMOTE_APP='$REMOTE_APP' python3 -" < "$LOCAL_SCRIPT_DIR/install-ffmpeg.py"
  "${SSH[@]}" "export PATH='$REMOTE_RUNTIME_PATH'; ffmpeg -version | head -n 1"
fi
# Browser recordings (.mp4 / .webm) are encoded by agent-browser with this ffmpeg.
if ! "${SSH[@]}" "export PATH='$REMOTE_RUNTIME_PATH'; enc=\$(ffmpeg -hide_banner -encoders 2>/dev/null); echo \"\$enc\" | grep -qw libx264 && echo \"\$enc\" | grep -qw libvpx"; then
  echo "FATAL: [$PRODUCT] ffmpeg lacks libx264 or libvpx; browser recordings cannot encode" >&2
  exit 1
fi

# Browser automation and every advertised coding provider are installation
# dependencies, not something an end user is expected to install over SSH.
# Keep the binaries in stable, service-owned paths outside releases so
# credentials and CLI availability survive release pruning.
#
# Built as a plain variable, not a case statement nested inside $(...) inside
# an outer double-quoted string: bash's paren-matching for a case pattern's
# bare `)` breaks down in exactly that nesting, misreading the case body as
# closing the command substitution early.
# Install the pinned backend Slack CLI in the same persistent tools prefix.
"${SSH[@]}" "bash -s -- '$REMOTE_TOOLS'" < "$LOCAL_REPO_ROOT/agent_go/scripts/install-slack-cli.sh"
# gog (Gmail connector CLI), kept on the latest checksum-verified release.
"${SSH[@]}" "bash -s -- '$REMOTE_TOOLS'" < "$LOCAL_REPO_ROOT/deploy/common/install-gog.sh"
case "${DEPLOY_REFRESH_TOOLS:-0}" in
  0|1) ;;
  *) echo 'DEPLOY_REFRESH_TOOLS must be 0 or 1' >&2; exit 1 ;;
esac
echo "==> [$PRODUCT] Checking server coding CLIs (refresh=${DEPLOY_REFRESH_TOOLS:-0})"
# Ship the installer and state helper before the server's source clone.
cli_helpers="$(mktemp -d)"
cp "$LOCAL_REPO_ROOT/deploy/common/install-coding-clis.sh" "$LOCAL_REPO_ROOT/deploy/common/install-agy.sh" "$LOCAL_REPO_ROOT/deploy/common/coding-cli-state.py" "$cli_helpers/"
cp "$LOCAL_REPO_ROOT/agent_go/internal/codingsdks/coding-sdks.cjs" "$cli_helpers/"
remote_cli_helpers="$REMOTE_APP/tools/.deploy-cli-helpers"
"${SSH[@]}" "install -d -m 0700 '$remote_cli_helpers'"
"${SCP[@]}" "$cli_helpers/install-coding-clis.sh" "$cli_helpers/install-agy.sh" "$cli_helpers/coding-cli-state.py" "$cli_helpers/coding-sdks.cjs" "$PRODUCT@$HOST_IP:$remote_cli_helpers/"
rm -rf "$cli_helpers"
"${SSH[@]}" "set -euo pipefail; export PATH='$REMOTE_RUNTIME_PATH'; bash '$remote_cli_helpers/install-coding-clis.sh' '$REMOTE_TOOLS' '$REMOTE_APP/home' '${AGY_AUTH_MODE:-auto}' '${DEPLOY_REFRESH_TOOLS:-0}' '$remote_cli_helpers/coding-sdks.cjs'; command -v agent-browser >/dev/null; command -v slack >/dev/null"
"${SSH[@]}" "rm -rf '$remote_cli_helpers'"

JOB="$PRODUCT-deploy-$(date +%Y%m%d%H%M%S)-$$"
REMOTE_JOB="$REMOTE_APP/builds/$JOB"
STAGING="$(mktemp -d)"
chmod 700 "$STAGING"
cleanup() { rm -rf "$STAGING"; "${SSH[@]}" "rm -rf '$REMOTE_JOB'" >/dev/null 2>&1 || true; }
trap cleanup EXIT

cp "$LOCAL_SCRIPT_DIR/bootstrap-build.sh" "$STAGING/bootstrap-build.sh"
printf '%s\n' "$PRODUCT" > "$STAGING/product"
# The product's config travels with the job: build-and-activate.sh on the server reads it from here, not from the
# public source checkout (where customer configs no longer live).
tar -C "$PRODUCT_DIR" -czf "$STAGING/product-config.tgz" .
"${SSH[@]}" "install -d -m 0700 '$REMOTE_JOB'"

printf '%s\n' "$PREBUILT_PATH" > "$STAGING/prebuilt"
"${SCP[@]}" "$STAGING/bootstrap-build.sh" "$STAGING/product" "$STAGING/product-config.tgz" "$STAGING/prebuilt" "$PRODUCT@$HOST_IP:$REMOTE_JOB/"
echo "==> [$PRODUCT] Activating prebuilt release $PREBUILT_NAME on $PRODUCT@$HOST_IP (copy and activate, no compile)"
# Activation shares this host with other products, so retain its resource limits.
# DEPLOY_DRAIN_SECONDS is how long the switch-over waits for running agent turns to finish (build-and-activate.sh's
# drain). The owner asked for forced deploys for now (2026-10-03), so it defaults to 0: restart at once. Set
# DEPLOY_DRAIN_SECONDS=300 to wait for turns again.
"${SSH[@]}" "systemd-run --user --quiet --wait --pipe --unit='$JOB' --setenv=DRAIN_TIMEOUT_SECONDS='${DEPLOY_DRAIN_SECONDS:-0}' --setenv=SECURITY_CHECKS_LEVEL='$SECURITY_CHECKS_LEVEL' -p MemoryMax=${ACTIVATE_MEMORY_MAX:-6G} -p CPUQuota=${ACTIVATE_CPU_QUOTA:-300%} -p Nice=10 bash '$REMOTE_JOB/bootstrap-build.sh' '$REMOTE_JOB'"

# The release is live and the services have re-exec'd under the standard profile: only now retire an older per-product profile
# (it refuses while any service process still runs without the standard one).
if [[ -n "${HOST_SETUP_USER:-}" ]]; then
  echo "==> [$PRODUCT] userns-retire: removing an older profile once the services run under the standard one"
  ssh "${SSH_OPTS[@]}" "$HOST_SETUP_USER@$HOST_IP" "sudo -n env PRODUCT=$PRODUCT APP_DIR=${APP_DIR:-} bash -s -- userns-retire" < "$LOCAL_SCRIPT_DIR/../common/provision-slots.sh" || echo "WARN userns-retire did not run; the older profile stays."
else
  echo "==> [$PRODUCT] userns-retire: skipped (no HOST_SETUP_USER)"
fi

echo "==> [$PRODUCT] Verifying"
"${SSH[@]}" "PRODUCT=$PRODUCT EXPECTED_PUBLIC_URL=${EXPECTED_PUBLIC_URL:-} python3 - running" < "$LOCAL_SCRIPT_DIR/deployment_checks.py"
if [[ "${PUBLIC_CHECKS:-true}" == false ]]; then
  echo "==> [$PRODUCT] Public checks skipped (PUBLIC_CHECKS=false: no DNS/HTTPS for $DOMAIN yet)."
  echo "==> [$PRODUCT] Done."
  exit 0
fi
# Not `curl -f`: whether /api/health is reachable without auth depends on the
# gateway's gate -- an app sign-in deployment exempts it (200), a Google
# sign-in gateway (GATEWAY_SSO_ONLY) does not (401). Either is a live,
# correctly routed gateway; only a connection failure or a 5xx means
# something is wrong.
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

echo "==> [$PRODUCT] Done."
)

# --build <name|sha> (or DEPLOY_BUILD): deploy that existing build; --force: rebuild even if these revisions were already built.
DEPLOY_ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --build) [[ $# -ge 2 ]] || { echo "--build needs a build name or sha (see ./deploy.sh builds)" >&2; exit 2; }; DEPLOY_BUILD="$2"; shift 2 ;;
    --build=*) DEPLOY_BUILD="${1#--build=}"; shift ;;
    --force) if [[ "$SERVER" == build ]]; then DEPLOY_FORCE_BUILD=1; shift; else DEPLOY_ARGS+=("$1"); shift; fi ;;
    *) DEPLOY_ARGS+=("$1"); shift ;;
  esac
done
set -- ${DEPLOY_ARGS[@]+"${DEPLOY_ARGS[@]}"}
export DEPLOY_BUILD DEPLOY_FORCE_BUILD

# ./deploy.sh builds: what the build host holds. ./deploy.sh build: make (or reuse) the build of the current main; deploys nothing.
if [[ "$SERVER" == builds ]]; then
  reject_extra_arguments "$@"
  build_ssh "python3 - list '$BUILDS_DIR'" < "$REPO_ROOT/deploy/common/release_manifest.py"
  echo
  echo "On GitHub (github.com/manishiitg/agentworks-builds; a tag is build-<builder8>-<mcpagent8>-<provider8>):"
  bash "$REPO_ROOT/deploy/common/publish-build.sh" --list || echo "(GitHub could not be read)" >&2
  exit 0
fi
if [[ "$SERVER" == verify ]]; then
  [[ $# -le 1 ]] || { echo "Usage: ./deploy.sh verify [name]" >&2; exit 2; }
  verify_change_remote "$@"
  exit $?
fi
if [[ "$SERVER" == publish ]]; then
  [[ $# -le 1 ]] || { echo "Usage: ./deploy.sh publish [build name or sha]" >&2; exit 2; }
  if [[ $# -eq 1 ]]; then
    found="$(find_build "$1")" || { echo "No unique build matches '$1' (see ./deploy.sh builds)" >&2; exit 1; }
  else
    found="$(build_ssh "cd '$BUILDS_DIR' && ls -1t */manifest.json 2>/dev/null | head -n 1 | cut -d/ -f1")"
    [[ -n "$found" ]] || { echo "No builds on the build host (see ./deploy.sh builds)" >&2; exit 1; }
  fi
  tag="$(publish_build_remote "${found%% *}")" || exit 1
  [[ -n "$tag" ]] || { echo "Not published (see the message above)." >&2; exit 1; }
  echo "Published: ${found%% *} -> $tag"
  exit 0
fi
if [[ "$SERVER" == prune-builds ]]; then
  reject_extra_arguments "$@"
  prune_builds_remote
  exit 0
fi
if [[ "$SERVER" == pin || "$SERVER" == unpin ]]; then
  [[ $# -eq 1 ]] || { echo "Usage: ./deploy.sh $SERVER <build name or sha>" >&2; exit 2; }
  found="$(find_build "$1")" || { echo "No unique build matches '$1' (see ./deploy.sh builds)" >&2; exit 1; }
  pinned_name="${found%% *}"
  if [[ "$SERVER" == pin ]]; then
    build_ssh "install -d -m 0755 '$BUILDS_DIR/.pinned' && touch '$BUILDS_DIR/.pinned/$pinned_name'"
  else
    build_ssh "rm -f '$BUILDS_DIR/.pinned/$pinned_name'"
  fi
  echo "$SERVER: $pinned_name"
  exit 0
fi
if [[ "$SERVER" == build ]]; then
  reject_extra_arguments "$@"
  [[ -z "${DEPLOY_BUILD:-}" ]] || { echo "'build' makes a new build; --build selects an existing one for a deploy." >&2; exit 2; }
  name="$(build_release_remote)"
  echo "Build ready: $name (on ${BUILD_HOST:-the build host}:$BUILDS_DIR/$name). Nothing was deployed."
  exit 0
fi

# ./deploy.sh agent-test <server> [scenario ids]: the on-request Crew agent tests in the server's Smoke test Crew
# (scripts/agent-test.py); reports, changes only its own side chats.
if [[ "$SERVER" == agent-test ]]; then
  [[ $# -ge 1 ]] || { echo "Usage: ./deploy.sh agent-test <server> [S1,S2,...]" >&2; exit 2; }
  product="$1"; [[ "$product" == excellence ]] && product=agents; [[ "$product" == rts ]] && product=video-studio
  config="$(deployments_dir)/products/$product/smoke.env"
  [[ -f "$config" ]] || { echo "No products/$product/smoke.env: this server has no Smoke test Crew yet" >&2; exit 1; }
  # shellcheck disable=SC1090
  source "$config"
  exec python3 -I "$REPO_ROOT/scripts/agent-test.py" --login "$SMOKE_LOGIN" --workspace-id "$SMOKE_WORKSPACE" \
    ${SMOKE_WORKSPACE_PORT:+--workspace-port "$SMOKE_WORKSPACE_PORT"} ${2:+--only "$2"}
fi

# ./deploy.sh smoke <server>: the post-deploy smoke check alone (PLAT-424); reports, changes only its own side chats.
if [[ "$SERVER" == smoke ]]; then
  [[ $# -eq 1 ]] || { echo "Usage: ./deploy.sh smoke <server>" >&2; exit 2; }
  product="$1"; [[ "$product" == excellence ]] && product=agents; [[ "$product" == rts ]] && product=video-studio
  post_deploy_smoke "$product"
  exit 0
fi

# ./deploy.sh slotcheck <server>: run the deployed release's slot self-test (deploy/common/slotcheck.sh) as the service
# account. Read-only; changes nothing on the server.
if [[ "$SERVER" == slotcheck ]]; then
  [[ $# -ge 1 && $# -le 2 ]] || { echo "Usage: ./deploy.sh slotcheck <rts|excellence|confida|sparkquill> [basic|full]" >&2; exit 2; }
  check_level="${2:-full}"
  case "$check_level" in basic|full) ;; *) echo "slotcheck level must be basic or full (got '$check_level')" >&2; exit 2 ;; esac
  case "$1" in
    rts|video-studio)
      load_aws_server_config "${AWS_PRODUCT_DIR:-video-studio}"
      HOST_IP="$(aws --profile "${AWS_PROFILE_NAME:?aws.env must set AWS_PROFILE_NAME}" --region "${AWS_REGION:-us-west-2}" cloudformation describe-stacks --stack-name "${STACK_NAME:?aws.env must set STACK_NAME}" --query 'Stacks[0].Outputs[?OutputKey==`ElasticIp`].OutputValue | [0]' --output text)"
      exec ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "${SSH_KEY_PATH:-$HOME/.ssh/id_ed25519}" "video-studio@$HOST_IP" \
        "bash /var/lib/video-studio/video-studio/current/slotcheck.sh --app /var/lib/video-studio/video-studio --docs /data/video-studio/docs --product video-studio --level $check_level"
      ;;
    excellence|confida|sparkquill|dominion)
      product="$1"; [[ "$product" == excellence ]] && product=agents; [[ "$product" == rts ]] && product=video-studio
      (
        # shellcheck disable=SC1090
        source "$(product_config_dir "$product")/product.env"
        identity=(-i "$SSH_KEY_PATH"); [[ -r "$SSH_KEY_PATH" ]] && identity+=(-o IdentitiesOnly=yes)
        exec ssh -p "$SSH_PORT" "${identity[@]}" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "$product@$HOST_IP" \
          "bash /srv/$product/current/slotcheck.sh --app /srv/$product --docs /srv/$product/data/docs --product $product --level $check_level"
      )
      ;;
    *) echo "slotcheck: unknown server $1 (rts, excellence, confida, sparkquill, dominion)" >&2; exit 2 ;;
  esac
  exit $?
fi

# ./deploy.sh check <server|all>: the read-only health check of a server (public site, certificate, compression, MCP,
# the website's MCP callback list, live release, services, disk, deploy config). The server list and the check live in
# the private deployments repository (scripts/server_check.py, servers.json).
if [[ "$SERVER" == check ]]; then
  checker="$(deployments_dir)/scripts/server_check.py"
  [[ -f "$checker" ]] || { echo "check needs the private deployments repository at $(deployments_dir) (or AGENTWORKS_DEPLOYMENTS_DIR)" >&2; exit 2; }
  exec python3 "$checker" "$@"
fi

# ./deploy.sh report [server]: how each server differs from the standard runtime profile. Read-only, deploys nothing.
if [[ "$SERVER" == report ]]; then
  exec "$REPO_ROOT/deploy/common/profile-report-all.sh" "$@"
fi

case "$SERVER" in
  rts|video-studio)
    reject_extra_arguments "$@"
    deploy_rts
    report_rts_cloudfront_usage || echo "CloudFront usage report unavailable (deploy succeeded)." >&2
    prune_builds_remote
    post_deploy_smoke video-studio  # this deployment's private settings folder is products/video-studio
    ;;
  confida|sparkquill|dominion|citymall)
    # Dominion is deployed like Confida since 2026-10-07 (owner: "everything same as excellence/confida").
    # A product on its own host sets SSH_JUMP, HOST_SETUP_SCRIPT and PREBUILT_DELIVERY=fetch in its private product.env.
    reject_extra_arguments "$@"
    deploy_rootless_product "$SERVER"
    prune_builds_remote
    post_deploy_smoke "$SERVER"
    ;;
  excellence)
    # the Hetzner server; its account, units and product
    # folder are named "agents" on the host.
    reject_extra_arguments "$@"
    deploy_rootless_product agents
    prune_builds_remote
    post_deploy_smoke agents
    ;;
  all-hetzner)
    # Excellence, Confida and SparkQuill from ONE build, one after the other; stops at the first failure. Never Dominion.
    reject_extra_arguments "$@"
    DEPLOY_PREBUILT_NAME="$(ensure_prebuilt_build)"
    [[ -n "$DEPLOY_PREBUILT_NAME" ]]
    export DEPLOY_PREBUILT_NAME
    echo "==> all-hetzner: build $DEPLOY_PREBUILT_NAME -> excellence, confida, sparkquill"
    deploy_rootless_product agents
    deploy_rootless_product confida
    deploy_rootless_product sparkquill
    prune_builds_remote  # once, after all three: a chosen older build must still exist for the next product
    post_deploy_smoke agents; post_deploy_smoke confida; post_deploy_smoke sparkquill
    ;;
  -h|--help|help)
    usage
    ;;
  --list)
    printf '%s\n' rts confida sparkquill excellence all-hetzner dominion citymall
    ;;
  "")
    usage >&2
    exit 2
    ;;
  *)
    if [[ "$SERVER" =~ ^[a-z][a-z0-9-]{0,31}$ && -f "$(deployments_dir)/products/$SERVER/product.env" ]]; then
      reject_extra_arguments "$@"
      deploy_rootless_product "$SERVER"
      prune_builds_remote
      post_deploy_smoke "$SERVER"
    else
      echo "Unknown deployment server: $SERVER" >&2
      usage >&2
      exit 2
    fi
    ;;
esac
