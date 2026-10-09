#!/usr/bin/env bash
# Install the CLIs that WORKFLOW shell commands need on the Video Studio /
# AgentWorks box, as root, through SSM.
#
# Why root and why SSM: workflow shells run under Landlock with a strict
# environment (HOME=/tmp, system PATH only) and can only read system roots
# (/usr, /bin, /lib, ...). A tool under the service user's ~/.local is
# invisible to them -- verified 2026-09-03: `aws: not found` inside the
# sandbox while it worked over SSH. So these go to /usr/local, which needs
# root; the box has no sudo and SSH is deploy-only, but the SSM agent is
# running and the instance role carries AmazonSSMManagedInstanceCore
# (instance-role.yaml), so `aws ssm send-command` is the admin channel.
#
# Idempotent: every step checks before installing. Re-run to add tools.
set -euo pipefail

AWS_PROFILE_NAME="${AWS_PROFILE_NAME:-RTS}"
AWS_REGION="${AWS_REGION:-us-west-2}"
STACK_NAME="${STACK_NAME:-video-studio-prod}"
NTN_VERSION_URL="${NTN_VERSION_URL:-https://ntn.dev}"

aws_rts() { aws --profile "$AWS_PROFILE_NAME" --region "$AWS_REGION" "$@"; }
instance="$(aws_rts cloudformation describe-stacks --stack-name "$STACK_NAME" --query 'Stacks[0].Outputs[?OutputKey==`InstanceId`].OutputValue|[0]' --output text)"

status="$(aws_rts ssm describe-instance-information --filters "Key=InstanceIds,Values=$instance" --query 'InstanceInformationList[0].PingStatus' --output text 2>/dev/null || true)"
if [[ "$status" != "Online" ]]; then
  echo "instance $instance is not an SSM managed instance yet (status: ${status:-none}); run attach-instance-role.sh and wait a few minutes" >&2
  exit 1
fi

# The remote script. Keep it plain sh-compatible bash; SSM runs it as root.
read -r -d '' REMOTE <<'REMOTE_SCRIPT' || true
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get install -y -q git unzip curl ca-certificates >/dev/null

# AWS CLI v2, system-wide. Credentials come from the instance role via IMDS.
if ! test -x /usr/local/bin/aws; then
  tmp="$(mktemp -d)"
  curl -fsS "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "$tmp/awscliv2.zip"
  (cd "$tmp" && unzip -q awscliv2.zip && ./aws/install -i /usr/local/aws-cli -b /usr/local/bin >/dev/null)
  rm -rf "$tmp"
fi

# Notion CLI (ntn): real binary under /usr/local/lib/ntn, launcher on PATH.
# The launcher maps the workflow-attached secret SECRET_NOTION_API_TOKEN (a
# global secret named NOTION_API_TOKEN) onto NOTION_API_TOKEN, which the CLI
# reads instead of a keychain; file-based auth, if ever used, goes to a
# writable dir because HOME is /tmp in the sandbox.
if ! test -x /usr/local/lib/ntn/ntn; then
  install -d -m 0755 /usr/local/lib/ntn
  NTN_INSTALL_DIR=/usr/local/lib/ntn bash -c 'curl -fsSL https://ntn.dev | bash' >/dev/null
fi
cat > /usr/local/bin/ntn <<'NTN'
#!/usr/bin/env bash
export NOTION_KEYRING="${NOTION_KEYRING:-0}"
export XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-/tmp/.xdg-config}"
if [[ -z "${NOTION_API_TOKEN:-}" && -n "${SECRET_NOTION_API_TOKEN:-}" ]]; then export NOTION_API_TOKEN="$SECRET_NOTION_API_TOKEN"; fi
exec /usr/local/lib/ntn/ntn "$@"
NTN
chmod 0755 /usr/local/bin/ntn

# surge.sh CLI for workflow publishing (rtslatency publishes rts-daily-ops.surge.sh).
# Installed with the system node so the sandbox sees it; the launcher maps the
# workflow-attached secrets SECRET_SURGE_LOGIN / SECRET_SURGE_TOKEN onto the
# SURGE_LOGIN / SURGE_TOKEN variables the CLI reads, because HOME is /tmp in the
# sandbox and there is no ~/.netrc to log in with.
if ! test -e /usr/lib/node_modules/surge/bin/surge; then
  npm install -g --silent surge >/dev/null
fi
rm -f /usr/local/bin/surge
cat > /usr/local/bin/surge <<'SURGE'
#!/usr/bin/env bash
if [[ -z "${SURGE_LOGIN:-}" && -n "${SECRET_SURGE_LOGIN:-}" ]]; then export SURGE_LOGIN="$SECRET_SURGE_LOGIN"; fi
if [[ -z "${SURGE_TOKEN:-}" && -n "${SECRET_SURGE_TOKEN:-}" ]]; then export SURGE_TOKEN="$SECRET_SURGE_TOKEN"; fi
exec /usr/bin/node /usr/lib/node_modules/surge/bin/surge "$@"
SURGE
chmod 0755 /usr/local/bin/surge

# gog (github.com/openclaw/gogcli) is the Gmail connector's host CLI. Pin the
# release and verify its published checksum so every box gets the same audited
# binary. The application always supplies an explicit account/client or access
# token and an explicit --home; workflow shells therefore cannot accidentally
# send through whichever account an operator last selected.
gog_version="0.39.1"
gog_archive="gogcli_${gog_version}_linux_amd64.tar.gz"
gog_sha256="438efa460b8291f023299ad2ed5610701cad7508db88392039c0891e2175e3b1"
if ! /usr/local/bin/gog --version 2>/dev/null | grep -Fq "$gog_version"; then
  gog_tmp="$(mktemp -d)"
  curl -fsSL "https://github.com/openclaw/gogcli/releases/download/v${gog_version}/${gog_archive}" -o "$gog_tmp/$gog_archive"
  echo "$gog_sha256  $gog_tmp/$gog_archive" | sha256sum -c - >/dev/null
  tar -xzf "$gog_tmp/$gog_archive" -C "$gog_tmp" ./gog
  install -m 0755 "$gog_tmp/gog" /usr/local/bin/gog
  rm -rf "$gog_tmp"
fi

# AWS profile "server A" for workflow shells. The rtslatency workflow was written
# against a named profile on the operator's laptop; on the box the credentials
# are the instance role, so both `default` and `RTS` resolve to it through
# IMDS. The sandbox runs with HOME=/tmp and exports
# AWS_CONFIG_FILE=/usr/local/etc/aws/config (workspace/security/environment.go);
# until that build is deployed, the aws launcher below sets the same variable
# and a copy at /tmp/.aws/config (recreated at boot by tmpfiles) covers boto3.
install -d -m 0755 /usr/local/etc/aws
cat > /usr/local/etc/aws/config <<'AWSCFG'
[default]
region = us-west-2
credential_source = Ec2InstanceMetadata

[profile RTS]
region = us-west-2
credential_source = Ec2InstanceMetadata
AWSCFG
chmod 0644 /usr/local/etc/aws/config
real_aws="$(readlink -f /usr/local/bin/aws 2>/dev/null || true)"
case "$real_aws" in
  /usr/local/aws-cli/*) ;;
  *) real_aws="/usr/local/aws-cli/v2/current/bin/aws" ;;
esac
rm -f /usr/local/bin/aws
cat > /usr/local/bin/aws <<AWSWRAP
#!/usr/bin/env bash
export AWS_CONFIG_FILE="\${AWS_CONFIG_FILE:-/usr/local/etc/aws/config}"
exec "$real_aws" "\$@"
AWSWRAP
chmod 0755 /usr/local/bin/aws
install -d -m 0755 /tmp/.aws && install -m 0644 /usr/local/etc/aws/config /tmp/.aws/config
cat > /etc/tmpfiles.d/agentworks-aws.conf <<'TMPF'
d /tmp/.aws 0755 root root -
C /tmp/.aws/config 0644 root root - /usr/local/etc/aws/config
TMPF

echo "git: $(git --version)"
echo "aws: $(/usr/local/bin/aws --version)"
echo "aws RTS profile: $(/usr/local/bin/aws --profile RTS sts get-caller-identity --query Arn --output text 2>&1 | tail -1)"
echo "ntn: $(/usr/local/bin/ntn --version)"
echo "gog: $(/usr/local/bin/gog --version)"
REMOTE_SCRIPT

# AWS-RunShellScript executes with sh (dash); the script is bash, so ship it
# to a file and run it with bash explicitly.
cmd_id="$(aws_rts ssm send-command --instance-ids "$instance" --document-name AWS-RunShellScript --comment "install workflow system tools" --parameters "$(jq -cn --arg s "$REMOTE" '{commands: ["cat > /tmp/install-system-tools.sh <<'"'"'SSM_EOF'"'"'\n" + $s + "\nSSM_EOF", "bash /tmp/install-system-tools.sh", "rm -f /tmp/install-system-tools.sh"], executionTimeout: ["900"]}')" --query 'Command.CommandId' --output text)"
echo "ssm command $cmd_id sent; waiting"
for _ in $(seq 1 90); do
  st="$(aws_rts ssm get-command-invocation --command-id "$cmd_id" --instance-id "$instance" --query Status --output text 2>/dev/null || echo Pending)"
  case "$st" in
    Success) aws_rts ssm get-command-invocation --command-id "$cmd_id" --instance-id "$instance" --query StandardOutputContent --output text; exit 0 ;;
    Failed|Cancelled|TimedOut) echo "ssm command $st" >&2; aws_rts ssm get-command-invocation --command-id "$cmd_id" --instance-id "$instance" --query '[StandardOutputContent,StandardErrorContent]' --output text | tail -n 40 >&2; exit 1 ;;
  esac
  sleep 10
done
echo "ssm command still running after 15 minutes: $cmd_id" >&2
exit 1
