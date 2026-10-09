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

Servers:
  rts, video-studio     video.realtrainingsys.com
  confida               Confida rootless Linux deployment
  citymall              agents.citymall.live (Citymall's own AWS host, reached through the Hetzner jump; Pi on Citymall's gateway)
  sparkquill            SparkQuill rootless Linux deployment
  excellence            agents.excellencetechnologies.in (Code only, rootless Linux)
  all-hetzner           excellence, confida and sparkquill in sequence from ONE build (never dominion)
  dominion              legacy host alias for the shared workflow deployment
  check <server|all>    read-only health check of a server (site, certificate, MCP, website callback, release, services, disk)
  report [server]       how each server differs from the standard runtime profile (read-only)
  slotcheck <server> [basic|full]   the slot self-test of a deployed server (read-only; PLAT-478): a real slotted `pwd` per slot in the
                        docs root, a workflow, a Crew and a Code project, the slotctl/slot table/launcher checks, and the
                        secret admission scan (warnings). Exits 1 on any FAIL. Every deploy of a slot host runs it too.
  build [--force]       build a release of the current main of the three repositories on the build host; deploys nothing
  builds                list the builds on the build host (name, age, the three revisions, pinned) and the builds on GitHub
  publish [build]       upload a build (default: the newest) to github.com/manishiitg/agentworks-builds from the build host
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
  DEPLOY_SECURITY_CHECKS=full|basic   how thorough the slot self-test at the end of a deploy of a slot host is (default full): basic = a
                        slotted `pwd` per slot + the refusal checks; full adds a live tmux server on the test slot that no slot
                        command may reach, the Python helpers, and the refusals a workflow chat must get (PLAT-480). Runs on the
                        unassigned TEST slot only. Example: DEPLOY_SECURITY_CHECKS=basic ./deploy.sh confida
  DEPLOY_BUILD_MODE=server   the original path: the server clones main and compiles itself (fallback)
  Build host: BUILD_HOST (116.202.210.102), BUILD_PORT (2299), BUILD_USER (root), BUILD_SSH_KEY, BUILDS_DIR (/srv/_builds)

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
source "$REPO_ROOT/deploy/common/build-once.sh"

# --- RTS (video.realtrainingsys.com) -------------------------------------
# Sends only deployment instructions and secrets; the server clones main of
# all three repositories and builds the release itself.
deploy_rts() {
  local AWS_PROFILE_NAME="${AWS_PROFILE_NAME:-RTS}"
  local AWS_REGION="${AWS_REGION:-us-west-2}"
  local STACK_NAME="${STACK_NAME:-video-studio-prod}"
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
  local repo url prebuilt_name=""
  if [[ "${DEPLOY_BUILD_MODE:-prebuilt}" == prebuilt ]]; then
    # Build once on the Hetzner box (or reuse the build of these revisions); RTS only copies it after verifying the manifest.
    prebuilt_name="$(ensure_prebuilt_build)" || exit 1
    # The manifest's own hash travels separately from the tarball, so RTS can tell a damaged or swapped manifest.
    build_ssh "sha256sum '$BUILDS_DIR/$prebuilt_name/manifest.json'" | awk '{print $1}' > "$STAGING/prebuilt"
    [[ "$(cat "$STAGING/prebuilt")" =~ ^[0-9a-f]{64}$ ]] || { echo "Cannot read the build's manifest hash" >&2; exit 1; }
  else
    [[ -z "${DEPLOY_BUILD:-}" ]] || { echo "--build needs the prebuilt mode (unset DEPLOY_BUILD_MODE=server)" >&2; exit 1; }
    for repo in mcp-agent-builder-go mcpagent multi-llm-provider-go; do
      url="$(git -C "$REPO_ROOT/../$repo" remote get-url origin)"
      url="${url/git@github.com:/https://github.com/}"
      [[ "$url" =~ ^https://github.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Unsupported repository URL for $repo" >&2; exit 1; }
      printf '%s\n' "$url" >> "$STAGING/repos"
    done
  fi
  aws_rts secretsmanager get-secret-value --secret-id "$GLOBAL_SECRETS_SECRET_ID" --query SecretString --output text \
   | jq -er 'to_entries[] | select(.key | test("^[A-Z0-9_]+$")) | select(.value | type == "string" and length > 0) | if .key == "CLAUDE_CODE_OAUTH_TOKEN" or .key == "CURSOR_API_KEY" then "\(.key)=\(.value)" else "GLOBAL_SECRET_\(.key)=\(.value)" end' > "$STAGING/globals"
  chmod 600 "$STAGING/globals"
  cp "$RTS_DIR/server/bootstrap-build.sh" "$STAGING/bootstrap-build.sh"
  # Make room first (PLAT-545): a full disk broke the new gateway's database on 2026-10-05. Only runs when space is short,
  # and never removes the live release, the newest one before it, or anything a running process still uses.
  "${SSH[@]}" "python3 - /var/lib/video-studio/video-studio --apply --only-if-free-below-gb 15" < "$REPO_ROOT/deploy/common/prune-releases.py" \
    || echo 'warning: could not make room on RTS before the deploy' >&2
  "${SSH[@]}" "install -d -m 0700 '$REMOTE_JOB'"
  rsync -az -e "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i $SSH_KEY_PATH" "$STAGING/" "video-studio@$HOST_IP:$REMOTE_JOB/"
  if [[ -n "$prebuilt_name" ]]; then
    echo "Getting build $prebuilt_name onto RTS (verified there against its manifest); nothing is compiled on RTS."
    deliver_build_to_rts "$prebuilt_name" "$REMOTE_JOB" "$(cat "$STAGING/prebuilt")"
  else
    echo 'Server cloning main from all three repositories and building the release locally.'
  fi
  # Keep builds below ~3/4 of a 4 GB host (RTS_BUILD_MEMORY_MAX overrides, e.g. 6G on a larger instance) and two CPU
  # cores while tests keep running; swap absorbs the rest, so a build is slower, not killed.
  "${SSH[@]}" "systemd-run --user --quiet --wait --pipe --unit='$JOB' --setenv=SECURITY_CHECKS_LEVEL='$SECURITY_CHECKS_LEVEL' -p MemoryMax=${RTS_BUILD_MEMORY_MAX:-3G} -p CPUQuota=200% -p Nice=10 bash '$REMOTE_JOB/bootstrap-build.sh' '$REMOTE_JOB'"
}

# Read-only CloudFront usage for RTS against the always-free tier (1 TB out,
# 10M requests per month). Reported after every RTS deploy; never fails it.
report_rts_cloudfront_usage() {
  local profile="${AWS_PROFILE_NAME:-RTS}" dist="${CLOUDFRONT_DISTRIBUTION_ID:-E1OYOJGT2ZANUB}"
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

test -d "$LOCAL_WORKSPACE_ROOT/mcpagent/.git" || { echo "Expected sibling checkout: $LOCAL_WORKSPACE_ROOT/mcpagent" >&2; exit 1; }
test -d "$LOCAL_WORKSPACE_ROOT/multi-llm-provider-go/.git" || { echo "Expected sibling checkout: $LOCAL_WORKSPACE_ROOT/multi-llm-provider-go" >&2; exit 1; }

# The triggering machine no longer builds anything, so it no longer needs
# go/node/npm -- only enough to talk to git remotes and to the server.
for cmd in git scp ssh; do
  command -v "$cmd" >/dev/null || { echo "Missing $cmd" >&2; exit 1; }
done

# Offer only the named key when that file exists (a wrong key then fails with its real error instead of "Too many authentication failures"). When it
# does not exist (Confida's default ~/.ssh/confida_deploy on a machine that logs in through the ssh agent) ssh must be free to use the agent's keys.
SSH_IDENTITY=(-i "$SSH_KEY_PATH")
[[ -r "$SSH_KEY_PATH" ]] && SSH_IDENTITY+=(-o IdentitiesOnly=yes)
# SSH_JUMP (product.env, e.g. Citymall): reach a host whose port 22 admits only the jump host. ProxyJump forwards the
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
  # Every prepared host also gets the user-namespace exception the sandbox needs (a no-op where AppArmor does not restrict it),
  # scoped to this product's own launcher and nothing else. The activation then proves the sandbox works, or stops the deploy.
  echo "==> [$PRODUCT] Allowing the sandbox's user namespaces for this product's launcher only"
  ssh "${SSH_OPTS[@]}" "$HOST_SETUP_USER@$HOST_IP" "sudo -n env PRODUCT=$PRODUCT bash -s -- userns" < "$LOCAL_SCRIPT_DIR/../common/provision-slots.sh"
fi

echo "==> [$PRODUCT] Checking deployment configuration"
"${SSH[@]}" "PRODUCT=$PRODUCT EXPECTED_PUBLIC_URL=${EXPECTED_PUBLIC_URL:-} python3 - preflight" < "$LOCAL_SCRIPT_DIR/deployment_checks.py"

PREBUILT_NAME=""
if [[ "${DEPLOY_BUILD_MODE:-prebuilt}" == prebuilt ]]; then
  # Build once (or reuse the build of these revisions); the server below only copies and activates it.
  PREBUILT_NAME="$(ensure_prebuilt_build)"
  [[ -n "$PREBUILT_NAME" ]]
  PREBUILT_PATH="$BUILDS_DIR/$PREBUILT_NAME"
  # PREBUILT_DELIVERY=fetch: a host that cannot read the build host's folder gets its own copy (GitHub, else streamed).
  if [[ "${PREBUILT_DELIVERY:-local}" == fetch ]]; then
    PREBUILT_PATH="$REMOTE_APP/prebuilt/$PREBUILT_NAME"
    deliver_build_to_product_host "$PREBUILT_NAME" "$PREBUILT_PATH"
  fi
else
  [[ -z "${DEPLOY_BUILD:-}" ]] || { echo "--build needs the prebuilt mode (unset DEPLOY_BUILD_MODE=server)" >&2; exit 1; }
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
echo "==> [$PRODUCT] Installing/updating all server coding CLIs"
# Ship both helpers because this phase precedes the server's source clone.
cli_helpers="$(mktemp -d)"
cp "$LOCAL_REPO_ROOT/deploy/common/install-coding-clis.sh" "$LOCAL_REPO_ROOT/deploy/common/install-agy.sh" "$cli_helpers/"
remote_cli_helpers="$REMOTE_APP/tools/.deploy-cli-helpers"
"${SSH[@]}" "install -d -m 0700 '$remote_cli_helpers'"
"${SCP[@]}" "$cli_helpers/install-coding-clis.sh" "$cli_helpers/install-agy.sh" "$PRODUCT@$HOST_IP:$remote_cli_helpers/"
rm -rf "$cli_helpers"
"${SSH[@]}" "set -euo pipefail; export PATH='$REMOTE_RUNTIME_PATH'; bash '$remote_cli_helpers/install-coding-clis.sh' '$REMOTE_TOOLS' '$REMOTE_APP/home' '${AGY_AUTH_MODE:-auto}'; npm install -g --prefix '$REMOTE_TOOLS' --allow-scripts=agent-browser agent-browser@latest >/dev/null; command -v agent-browser >/dev/null; command -v slack >/dev/null; agent-browser --version"
"${SSH[@]}" "rm -rf '$remote_cli_helpers'"

JOB="$PRODUCT-deploy-$(date +%Y%m%d%H%M%S)-$$"
REMOTE_JOB="$REMOTE_APP/builds/$JOB"
STAGING="$(mktemp -d)"
chmod 700 "$STAGING"
cleanup() { rm -rf "$STAGING"; "${SSH[@]}" "rm -rf '$REMOTE_JOB'" >/dev/null 2>&1 || true; }
trap cleanup EXIT

cp "$LOCAL_SCRIPT_DIR/bootstrap-build.sh" "$STAGING/bootstrap-build.sh"
printf '%s\n' "$DEPLOY_BRANCH" > "$STAGING/branch"
printf '%s\n' "$PRODUCT" > "$STAGING/product"
# The product's config travels with the job: build-and-activate.sh on the server reads it from here, not from the
# public source checkout (where customer configs no longer live).
tar -C "$PRODUCT_DIR" -czf "$STAGING/product-config.tgz" .
"${SSH[@]}" "install -d -m 0700 '$REMOTE_JOB'"

# All three repos are public; to_https_url (above) gives an anonymous HTTPS URL so a fresh account with no SSH deploy key for
# github.com can still clone (confida hit "Host key verification failed" on the SSH remote form, 2026-09-11).
if [[ -n "$PREBUILT_NAME" ]]; then
  printf '%s\n' "$PREBUILT_PATH" > "$STAGING/prebuilt"
  "${SCP[@]}" "$STAGING/bootstrap-build.sh" "$STAGING/branch" "$STAGING/product" "$STAGING/product-config.tgz" "$STAGING/prebuilt" "$PRODUCT@$HOST_IP:$REMOTE_JOB/"
  echo "==> [$PRODUCT] Activating prebuilt release $PREBUILT_NAME on $PRODUCT@$HOST_IP (copy and activate, no compile)"
else
  echo "==> [$PRODUCT] Resolving git remotes for $DEPLOY_BRANCH (mcp-agent-builder-go, mcpagent, multi-llm-provider-go)"
  {
    to_https_url "$(git -C "$LOCAL_REPO_ROOT" remote get-url origin)"
    to_https_url "$(git -C "$LOCAL_WORKSPACE_ROOT/mcpagent" remote get-url origin)"
    to_https_url "$(git -C "$LOCAL_WORKSPACE_ROOT/multi-llm-provider-go" remote get-url origin)"
  } > "$STAGING/repos"
  "${SCP[@]}" "$STAGING/bootstrap-build.sh" "$STAGING/branch" "$STAGING/product" "$STAGING/product-config.tgz" "$STAGING/repos" "$PRODUCT@$HOST_IP:$REMOTE_JOB/"
  echo "==> [$PRODUCT] Building on $PRODUCT@$HOST_IP: cloning/using $DEPLOY_BRANCH and building natively"
fi
# Throttled below the box's shared core/RAM budget: this box also runs other
# products, each under its own account, and a full go+npm build must not
# starve their live services while it runs.
# DEPLOY_DRAIN_SECONDS is how long the switch-over waits for running agent turns to finish (build-and-activate.sh's
# drain). The owner asked for forced deploys for now (2026-10-03), so it defaults to 0: restart at once. Set
# DEPLOY_DRAIN_SECONDS=300 to wait for turns again.
"${SSH[@]}" "systemd-run --user --quiet --wait --pipe --unit='$JOB' --setenv=DRAIN_TIMEOUT_SECONDS='${DEPLOY_DRAIN_SECONDS:-0}' --setenv=SECURITY_CHECKS_LEVEL='$SECURITY_CHECKS_LEVEL' -p MemoryMax=${ACTIVATE_MEMORY_MAX:-6G} -p CPUQuota=${ACTIVATE_CPU_QUOTA:-300%} -p Nice=10 bash '$REMOTE_JOB/bootstrap-build.sh' '$REMOTE_JOB'"

echo "==> [$PRODUCT] Verifying"
"${SSH[@]}" "PRODUCT=$PRODUCT EXPECTED_PUBLIC_URL=${EXPECTED_PUBLIC_URL:-} python3 - running" < "$LOCAL_SCRIPT_DIR/deployment_checks.py"
if [[ "${PUBLIC_CHECKS:-true}" == false ]]; then
  echo "==> [$PRODUCT] Public checks skipped (PUBLIC_CHECKS=false: no DNS/HTTPS for $DOMAIN yet)."
  echo "==> [$PRODUCT] Done."
  exit 0
fi
# Not `curl -f`: whether /api/health is reachable without auth depends on the
# gateway's own gate model (GATEWAY_DISABLE_PASSWORD_GATE in .env) -- a
# per-user-JWT deployment like confida exempts it (200), a shared-password
# deployment like sparkquill does not (401). Either is a live, correctly
# routed gateway; only a connection failure or a 5xx means something is wrong.
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

# --- Deploy notices in Slack -------------------------------------------------
# A short message when a deploy starts and when it finishes, so people know. The incoming-webhook
# URL is a secret (anyone with it can post to the channel): it is read from DEPLOY_SLACK_WEBHOOK_URL
# or the first line of ~/.config/agentworks/deploy-slack-webhook (mode 600), never from the repo.
# OFF by default (owner, 2026-10-03: "for now make Slack posts silent"); post for one run with
#   DEPLOY_SLACK_NOTIFY=1 ./deploy.sh confida
# With no webhook set nothing is sent either, and a failed post never fails or delays a deploy.
deploy_notify() {
  local url="${DEPLOY_SLACK_WEBHOOK_URL:-}" file="${DEPLOY_SLACK_WEBHOOK_FILE:-$HOME/.config/agentworks/deploy-slack-webhook}"
  [[ -z "$url" && -r "$file" ]] && url="$(head -n1 "$file" | tr -d '[:space:]')"
  [[ -n "$url" ]] || return 0
  local payload
  payload="$(python3 -c 'import json,sys; print(json.dumps({"text": sys.argv[1]}))' "$1" 2>/dev/null)" || return 0
  curl -sS -m 10 -X POST -H 'Content-type: application/json' --data "$payload" "$url" >/dev/null 2>&1 || true
}

deploy_label() {
  case "$SERVER" in
    rts|video-studio) echo "RTS (video.realtrainingsys.com)" ;;
    excellence) echo "Excellence (agents.excellencetechnologies.in)" ;;
    all-hetzner) echo "Excellence, Confida and SparkQuill" ;;
    confida) echo "Confida (confida.agentworkshq.com)" ;;
    citymall) echo "Citymall (agents.citymall.live)" ;;
    dominion) echo "Trader workflow host" ;;
    *) echo "$SERVER" ;;
  esac
}

# The commit a server runs now, read from its current release folder's name (read-only; empty when unknown).
deploy_current_revision() {
  local release=""
  case "$SERVER" in
    rts|video-studio) release="$(ssh -o BatchMode=yes -o ConnectTimeout=15 -i "${SSH_KEY_PATH:-$HOME/.ssh/id_ed25519}" "video-studio@${RTS_HOST_IP:-44.253.29.127}" 'readlink /var/lib/video-studio/video-studio/current' 2>/dev/null)" ;;
    excellence) release="$(ssh -p 2299 -o BatchMode=yes -o ConnectTimeout=15 root@116.202.210.102 'readlink -f /srv/agents/current' 2>/dev/null)" ;;
    confida|sparkquill|dominion) release="$(ssh -p 2299 -o BatchMode=yes -o ConnectTimeout=15 root@116.202.210.102 "readlink -f /srv/$SERVER/current" 2>/dev/null)" ;;
  esac
  release="$(basename "${release:-}")"
  # Release folders are <sha>-<time> (RTS) or <product>-<sha>-<time> (Hetzner).
  local part
  for part in ${release//-/ }; do
    [[ "$part" =~ ^[0-9a-f]{7,40}$ ]] && git -C "$REPO_ROOT" cat-file -e "$part^{commit}" 2>/dev/null && { echo "$part"; return 0; }
  done
}

# What changes for people since the server's current release: one line per change, bookkeeping commits left out.
deploy_changelog() {
  local from="$1" to="$2" lines count
  [[ -n "$from" ]] || return 0
  lines="$(git -C "$REPO_ROOT" log --no-merges --reverse --format='%s' "$from..$to" 2>/dev/null | grep -vE '^(Record |Merge |Use PLAT-[0-9]+ for |PLAT-[0-9]+: ticket)' | awk '!seen[$0]++')"
  count="$(printf '%s\n' "$lines" | grep -c . || true)"
  [[ "$count" -gt 0 ]] || return 0
  printf '\n*What changes* (%s changes since %s):\n' "$count" "${from:0:9}"
  printf '%s\n' "$lines" | head -n 60 | cut -c1-140 | sed 's/^/• /'
  [[ "$count" -le 60 ]] || printf '…and %s more\n' "$((count - 60))"
}

deploy_start_notice() {
  case "${DEPLOY_SLACK_NOTIFY:-0}" in 0|false|no|off) return 0 ;; esac
  [[ -n "$SERVER" && "$SERVER" != "-h" && "$SERVER" != "--help" ]] || return 0
  local head_line target changelog
  git -C "$REPO_ROOT" fetch -q origin main >/dev/null 2>&1 || true
  target="origin/main"
  head_line="$(git -C "$REPO_ROOT" log -1 --format='%h %s' "$target" 2>/dev/null | cut -c1-90)"
  # DEPLOY_CHANGELOG_FROM=<sha> lists changes since that commit instead (for example since an earlier deploy whose
  # notice was too short).
  changelog="$(deploy_changelog "${DEPLOY_CHANGELOG_FROM:-$(deploy_current_revision)}" "$target")"
  DEPLOY_NOTICE_STARTED="$(date +%s)"
  deploy_notify ":rocket: Deploying *$(deploy_label)* now (${head_line:-main}). It restarts in a few minutes; chats reconnect on their own.${changelog}"
  trap 'deploy_finish_notice $?' EXIT
}

deploy_finish_notice() {
  local rc="$1" took=""
  case "${DEPLOY_SLACK_NOTIFY:-0}" in 0|false|no|off) trap - EXIT; return "$rc" ;; esac
  [[ -n "${DEPLOY_NOTICE_STARTED:-}" ]] && took=" in $(( ($(date +%s) - DEPLOY_NOTICE_STARTED) / 60 )) min"
  trap - EXIT
  if [[ "$rc" == "0" ]]; then
    deploy_notify ":white_check_mark: *$(deploy_label)* is deployed${took}. You can carry on."
  else
    deploy_notify ":warning: The *$(deploy_label)* deploy finished${took} with a problem (exit $rc). It may still be on the previous release; the team is checking."
  fi
  return "$rc"
}

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
  echo "Build ready: $name (on ${BUILD_HOST:-116.202.210.102}:$BUILDS_DIR/$name). Nothing was deployed."
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
      HOST_IP="$(aws --profile "${AWS_PROFILE_NAME:-RTS}" --region "${AWS_REGION:-us-west-2}" cloudformation describe-stacks --stack-name "${STACK_NAME:-video-studio-prod}" --query 'Stacks[0].Outputs[?OutputKey==`ElasticIp`].OutputValue | [0]' --output text)"
      exec ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "${SSH_KEY_PATH:-$HOME/.ssh/id_ed25519}" "video-studio@$HOST_IP" \
        "bash /var/lib/video-studio/video-studio/current/slotcheck.sh --app /var/lib/video-studio/video-studio --docs /data/video-studio/docs --product video-studio --level $check_level"
      ;;
    excellence|confida|sparkquill|dominion)
      product="$1"; [[ "$product" == excellence ]] && product=agents
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

deploy_start_notice

case "$SERVER" in
  rts|video-studio)
    reject_extra_arguments "$@"
    deploy_rts
    report_rts_cloudfront_usage || echo "CloudFront usage report unavailable (deploy succeeded)." >&2
    [[ "${DEPLOY_BUILD_MODE:-prebuilt}" != prebuilt ]] || prune_builds_remote
    ;;
  confida|sparkquill|dominion|citymall)
    # Dominion is deployed like Confida since 2026-10-07 (owner: "everything same as excellence/confida").
    # Citymall too, on its own host: products/citymall/product.env (SSH_JUMP, HOST_SETUP_SCRIPT, PREBUILT_DELIVERY=fetch).
    reject_extra_arguments "$@"
    deploy_rootless_product "$SERVER"
    [[ "${DEPLOY_BUILD_MODE:-prebuilt}" != prebuilt ]] || prune_builds_remote
    ;;
  excellence)
    # agents.excellencetechnologies.in; its account, units and product
    # folder are named "agents" on the host.
    reject_extra_arguments "$@"
    deploy_rootless_product agents
    [[ "${DEPLOY_BUILD_MODE:-prebuilt}" != prebuilt ]] || prune_builds_remote
    ;;
  all-hetzner)
    # Excellence, Confida and SparkQuill from ONE build, one after the other; stops at the first failure. Never Dominion.
    reject_extra_arguments "$@"
    [[ "${DEPLOY_BUILD_MODE:-prebuilt}" == prebuilt ]] || { echo "all-hetzner needs the prebuilt mode" >&2; exit 1; }
    DEPLOY_PREBUILT_NAME="$(ensure_prebuilt_build)"
    [[ -n "$DEPLOY_PREBUILT_NAME" ]]
    export DEPLOY_PREBUILT_NAME
    echo "==> all-hetzner: build $DEPLOY_PREBUILT_NAME -> excellence, confida, sparkquill"
    deploy_rootless_product agents
    deploy_rootless_product confida
    deploy_rootless_product sparkquill
    prune_builds_remote  # once, after all three: a chosen older build must still exist for the next product
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
    echo "Unknown deployment server: $SERVER" >&2
    usage >&2
    exit 2
    ;;
esac
