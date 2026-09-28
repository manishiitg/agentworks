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
  sparkquill            SparkQuill rootless Linux deployment
  excellence            agents.excellencetechnologies.in (Code only, rootless Linux)
  dominion              trader.tectonicmarkets.com (isolated Hetzner deployment)

dominion optionally takes --activate (stage-only otherwise):
  ./deploy.sh dominion              # clone/pull, build, stage a release
  ./deploy.sh dominion --activate   # also flip `current` and restart services
EOF
}

reject_extra_arguments() {
  if [[ $# -gt 0 ]]; then
    echo "Server '$SERVER' does not accept additional arguments." >&2
    usage >&2
    exit 2
  fi
}

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
  local repo url
  for repo in mcp-agent-builder-go mcpagent multi-llm-provider-go; do
    url="$(git -C "$REPO_ROOT/../$repo" remote get-url origin)"
    url="${url/git@github.com:/https://github.com/}"
    [[ "$url" =~ ^https://github.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Unsupported repository URL for $repo" >&2; exit 1; }
    printf '%s\n' "$url" >> "$STAGING/repos"
  done
  aws_rts secretsmanager get-secret-value --secret-id "$GLOBAL_SECRETS_SECRET_ID" --query SecretString --output text \
   | jq -er 'to_entries[] | select(.key | test("^[A-Z0-9_]+$")) | select(.value | type == "string" and length > 0) | if .key == "CLAUDE_CODE_OAUTH_TOKEN" or .key == "CURSOR_API_KEY" then "\(.key)=\(.value)" else "GLOBAL_SECRET_\(.key)=\(.value)" end' > "$STAGING/globals"
  chmod 600 "$STAGING/globals"
  cp "$RTS_DIR/server/bootstrap-build.sh" "$STAGING/bootstrap-build.sh"
  "${SSH[@]}" "install -d -m 0700 '$REMOTE_JOB'"
  rsync -az -e "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i $SSH_KEY_PATH" "$STAGING/" "video-studio@$HOST_IP:$REMOTE_JOB/"
  echo 'Server cloning main from all three repositories and building the release locally.'
  # Keep builds below half the host's RAM and two CPU cores while tests keep running.
  "${SSH[@]}" "systemd-run --user --quiet --wait --pipe --unit='$JOB' -p MemoryMax=6G -p CPUQuota=200% -p Nice=10 bash '$REMOTE_JOB/bootstrap-build.sh' '$REMOTE_JOB'"
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
PRODUCT_DIR="$LOCAL_SCRIPT_DIR/products/$PRODUCT"

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

SSH_OPTS=(-p "$SSH_PORT" -i "$SSH_KEY_PATH" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new)
SSH=(ssh "${SSH_OPTS[@]}" "$PRODUCT@$HOST_IP")
SCP=(scp -P "$SSH_PORT" -i "$SSH_KEY_PATH" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new)

echo "==> [$PRODUCT] Checking deployment configuration"
"${SSH[@]}" "PRODUCT=$PRODUCT EXPECTED_PUBLIC_URL=${EXPECTED_PUBLIC_URL:-} python3 - preflight" < "$LOCAL_SCRIPT_DIR/deployment_checks.py"

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
if [[ "${#CLI_TOOLS[@]}" -gt 0 ]]; then
  CODEX_CLI_NPM_VERSION="${CODEX_CLI_NPM_VERSION:-latest}"
  [[ "$CODEX_CLI_NPM_VERSION" == latest || "$CODEX_CLI_NPM_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Invalid CODEX_CLI_NPM_VERSION" >&2; exit 1; }
  cli_install_cmd() {
    case "$1" in
      claude) printf "npm install -g --prefix '%s' @anthropic-ai/claude-code@latest >/dev/null" "$REMOTE_TOOLS" ;;
      codex)  printf "npm install -g --prefix '%s' '@openai/codex@%s' --include=optional >/dev/null" "$REMOTE_TOOLS" "$CODEX_CLI_NPM_VERSION" ;;
      pi)     printf "npm install -g --prefix '%s' @earendil-works/pi-coding-agent@latest >/dev/null" "$REMOTE_TOOLS" ;;
      cursor) printf "HOME='%s/home' curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 https://cursor.com/install | HOME='%s/home' bash" "$REMOTE_APP" "$REMOTE_APP" ;;
      muse)   printf "HOME='%s/home' MUSE_INSTALL_DIR='%s/home/.local/bin' MUSE_NO_MODIFY_PATH=1 bash -c \"curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 https://dev.meta.ai/install.sh | bash\"" "$REMOTE_APP" "$REMOTE_APP" ;;
      *) echo "Unknown CLI_TOOLS entry: $1" >&2; exit 1 ;;
    esac
  }
  cli_bin_name() { [[ "$1" == cursor ]] && echo cursor-agent || echo "$1"; }

  install_lines=""
  check_lines=""
  for cli in "${CLI_TOOLS[@]}"; do
    install_lines+="$(cli_install_cmd "$cli")"$'\n'
    check_lines+="command -v '$(cli_bin_name "$cli")' >/dev/null"$'\n'
    if [[ "$cli" == codex ]]; then check_lines+="codex --version"$'\n'; fi
  done

  echo "==> [$PRODUCT] Installing server CLI dependencies (agent-browser, ${CLI_TOOLS[*]})"
  "${SSH[@]}" "set -euo pipefail
    install -d -m 0755 '$REMOTE_TOOLS' '$REMOTE_APP/home/.local/bin'
    export PATH='$REMOTE_RUNTIME_PATH'
    npm install -g --prefix '$REMOTE_TOOLS' --allow-scripts=agent-browser agent-browser@latest >/dev/null
    $install_lines
    export PATH='$REMOTE_TOOLS/bin':\"\$PATH\"
    export PATH='$REMOTE_APP/home/.local/bin':\"\$PATH\"
    command -v agent-browser >/dev/null
    command -v slack >/dev/null
    $check_lines
    agent-browser --version"
fi

JOB="$PRODUCT-deploy-$(date +%Y%m%d%H%M%S)-$$"
REMOTE_JOB="$REMOTE_APP/builds/$JOB"
STAGING="$(mktemp -d)"
chmod 700 "$STAGING"
cleanup() { rm -rf "$STAGING"; "${SSH[@]}" "rm -rf '$REMOTE_JOB'" >/dev/null 2>&1 || true; }
trap cleanup EXIT

cp "$LOCAL_SCRIPT_DIR/bootstrap-build.sh" "$STAGING/bootstrap-build.sh"
printf '%s\n' "$DEPLOY_BRANCH" > "$STAGING/branch"
printf '%s\n' "$PRODUCT" > "$STAGING/product"
"${SSH[@]}" "install -d -m 0700 '$REMOTE_JOB'"

# All three repos are public; use an anonymous HTTPS URL so a fresh account
# with no SSH deploy key for github.com can still clone (confida hit "Host
# key verification failed" on the SSH remote form, 2026-09-11).
to_https_url() {
  local url="$1"
  url="${url/ssh:\/\/git@github.com\//https://github.com/}"
  url="${url/git@github.com:/https://github.com/}"
  url="${url%.git}"
  [[ "$url" =~ ^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "Unsupported repository URL: $1" >&2; exit 1; }
  printf '%s\n' "$url"
}

echo "==> [$PRODUCT] Resolving git remotes for $DEPLOY_BRANCH (mcp-agent-builder-go, mcpagent, multi-llm-provider-go)"
{
  to_https_url "$(git -C "$LOCAL_REPO_ROOT" remote get-url origin)"
  to_https_url "$(git -C "$LOCAL_WORKSPACE_ROOT/mcpagent" remote get-url origin)"
  to_https_url "$(git -C "$LOCAL_WORKSPACE_ROOT/multi-llm-provider-go" remote get-url origin)"
} > "$STAGING/repos"
"${SCP[@]}" "$STAGING/bootstrap-build.sh" "$STAGING/branch" "$STAGING/product" "$STAGING/repos" "$PRODUCT@$HOST_IP:$REMOTE_JOB/"

echo "==> [$PRODUCT] Building on $PRODUCT@$HOST_IP: cloning/using $DEPLOY_BRANCH and building natively"
# Throttled below the box's shared core/RAM budget: this box also runs other
# products, each under its own account, and a full go+npm build must not
# starve their live services while it runs.
"${SSH[@]}" "systemd-run --user --quiet --wait --pipe --unit='$JOB' -p MemoryMax=6G -p CPUQuota=300% -p Nice=10 bash '$REMOTE_JOB/bootstrap-build.sh' '$REMOTE_JOB'"

echo "==> [$PRODUCT] Verifying"
"${SSH[@]}" "PRODUCT=$PRODUCT EXPECTED_PUBLIC_URL=${EXPECTED_PUBLIC_URL:-} python3 - running" < "$LOCAL_SCRIPT_DIR/deployment_checks.py"
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

case "$SERVER" in
  rts|video-studio)
    reject_extra_arguments "$@"
    deploy_rts
    report_rts_cloudfront_usage || echo "CloudFront usage report unavailable (deploy succeeded)." >&2
    ;;
  confida|sparkquill)
    reject_extra_arguments "$@"
    deploy_rootless_product "$SERVER"
    ;;
  excellence)
    # agents.excellencetechnologies.in; its account, units and product
    # folder are named "agents" on the host.
    reject_extra_arguments "$@"
    deploy_rootless_product agents
    ;;
  dominion)
    ACTIVATE_FLAG=""
    if [[ $# -gt 0 ]]; then
      if [[ $# -gt 1 || "$1" != "--activate" ]]; then
        echo "Server 'dominion' only accepts an optional --activate flag." >&2
        usage >&2
        exit 2
      fi
      ACTIVATE_FLAG="--activate"
    fi
    # deploy-dominion.sh runs natively ON the box. Upload the selected
    # checkout's guarded script first: an older remote copy might not know
    # how to fetch the requested branch and could otherwise stage main.
    DOMINION_HOST="${DOMINION_HOST:-116.202.210.102}"
    DOMINION_PORT="${DOMINION_PORT:-2299}"
    DOMINION_USER="${DOMINION_USER:-dominion}"
    DOMINION_REMOTE_SCRIPT="${DOMINION_REMOTE_SCRIPT:-/srv/dominion/deploy-dominion.sh}"
    SSH_ARGS=(-p "$DOMINION_PORT" -o BatchMode=yes -o StrictHostKeyChecking=accept-new)
    SCP_ARGS=(-P "$DOMINION_PORT" -o BatchMode=yes -o StrictHostKeyChecking=accept-new)
    if [[ -n "${DOMINION_SSH_KEY:-}" ]]; then
      SSH_ARGS+=(-i "$DOMINION_SSH_KEY")
      SCP_ARGS+=(-i "$DOMINION_SSH_KEY")
    fi
    printf -v REMOTE_SCRIPT_Q '%q' "$DOMINION_REMOTE_SCRIPT"
    LOCAL_DOMINION_SCRIPT="$REPO_ROOT/deploy/dedicated-vm/deploy-dominion.sh"
    scp "${SCP_ARGS[@]}" "$LOCAL_DOMINION_SCRIPT" "$DOMINION_USER@$DOMINION_HOST:$DOMINION_REMOTE_SCRIPT.next"
    ssh "${SSH_ARGS[@]}" "$DOMINION_USER@$DOMINION_HOST" "chmod +x ${REMOTE_SCRIPT_Q}.next && mv ${REMOTE_SCRIPT_Q}.next $REMOTE_SCRIPT_Q"
    REMOTE_CMD=(bash "$DOMINION_REMOTE_SCRIPT")
    if [[ -n "${DOMINION_BUILDER_REF:-}" ]]; then
      git check-ref-format --branch "$DOMINION_BUILDER_REF" >/dev/null || { echo "Invalid DOMINION_BUILDER_REF: $DOMINION_BUILDER_REF" >&2; exit 2; }
      printf -v BUILDER_REF_Q '%q' "$DOMINION_BUILDER_REF"
      REMOTE_CMD=(env "DOMINION_BUILDER_REF=$BUILDER_REF_Q" bash "$DOMINION_REMOTE_SCRIPT")
    fi
    [[ -z "$ACTIVATE_FLAG" ]] || REMOTE_CMD+=("$ACTIVATE_FLAG")
    exec ssh "${SSH_ARGS[@]}" "$DOMINION_USER@$DOMINION_HOST" "${REMOTE_CMD[@]}"
    ;;
  -h|--help|help)
    usage
    ;;
  --list)
    printf '%s\n' rts confida sparkquill excellence dominion
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
