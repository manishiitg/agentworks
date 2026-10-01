#!/usr/bin/env bash
# Per-user Linux accounts on the RTS EC2 host, run from an administrator's laptop. The box has no sudo and
# SSH is deploy-only, so the root-only provisioning script (deploy/common/provision-slots.sh, shared with the
# rootless-linux hosts) goes through SSM Run Command, like install-system-tools.sh.
#
#   deploy/aws-ec2/slots-admin.sh init                      # accounts, slotctl, sudo rule, the slot table
#   deploy/aws-ec2/slots-admin.sh assign <user-id> [slotNN] # give an existing user a slot
#   deploy/aws-ec2/slots-admin.sh adduser <email> [role] [products]
#   deploy/aws-ec2/slots-admin.sh release <user-id> | status
#
# `init` needs a release built from this layout already on the host (it installs that release's slotctl).
# After the first `init`, redeploy once (or restart the services) so the build installs the tmux front-end and
# switches the services to AGENTWORKS_SLOTS=optin.
set -euo pipefail

AWS_PROFILE_NAME="${AWS_PROFILE_NAME:-RTS}"
AWS_REGION="${AWS_REGION:-us-west-2}"
STACK_NAME="${STACK_NAME:-video-studio-prod}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ $# -ge 1 ]] || { sed -n 2,13p "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 2; }

aws_rts() { aws --profile "$AWS_PROFILE_NAME" --region "$AWS_REGION" "$@"; }
instance="$(aws_rts cloudformation describe-stacks --stack-name "$STACK_NAME" --query 'Stacks[0].Outputs[?OutputKey==`InstanceId`].OutputValue|[0]' --output text)"
status="$(aws_rts ssm describe-instance-information --filters "Key=InstanceIds,Values=$instance" --query 'InstanceInformationList[0].PingStatus' --output text 2>/dev/null || true)"
[[ "$status" == "Online" ]] || { echo "instance $instance is not an SSM managed instance (status: ${status:-none})" >&2; exit 1; }

args=""
for a in "$@"; do args+=" $(printf '%q' "$a")"; done
script_b64="$(base64 < "$HERE/../common/provision-slots.sh" | tr -d '\n')"
remote="echo $script_b64 | base64 -d > /tmp/provision-slots.sh && PRODUCT=video-studio APP_DIR=/var/lib/video-studio/video-studio DOCS=/data/video-studio/docs SERVICE_HOME=/var/lib/video-studio bash /tmp/provision-slots.sh$args; rc=\$?; rm -f /tmp/provision-slots.sh; exit \$rc"
cmd_id="$(aws_rts ssm send-command --instance-ids "$instance" --document-name AWS-RunShellScript --comment "slots: $1" --parameters "$(jq -cn --arg s "$remote" '{commands: [$s]}')" --query Command.CommandId --output text)"
echo "ssm command $cmd_id sent; waiting"
for _ in $(seq 1 90); do
  st="$(aws_rts ssm get-command-invocation --command-id "$cmd_id" --instance-id "$instance" --query Status --output text 2>/dev/null || echo Pending)"
  case "$st" in
    Success) aws_rts ssm get-command-invocation --command-id "$cmd_id" --instance-id "$instance" --query StandardOutputContent --output text; exit 0 ;;
    Failed|Cancelled|TimedOut)
      echo "ssm command $st" >&2
      aws_rts ssm get-command-invocation --command-id "$cmd_id" --instance-id "$instance" --query '[StandardOutputContent,StandardErrorContent]' --output text >&2
      exit 1 ;;
  esac
  sleep 5
done
echo "ssm command still running: $cmd_id" >&2
exit 1
