#!/usr/bin/env bash
# Prints how each production server differs from the standard runtime profile (deploy/common/runtime_profile.json,
# docs/design/deploy_unification.md). Read-only: it reads the running processes' environment and a few facts, and changes nothing.
#   deploy/common/profile-report-all.sh            # every server
#   deploy/common/profile-report-all.sh server B  # one
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="$(python3 -c 'import json,sys;print(json.dumps(json.load(open(sys.argv[1]))))' "$HERE/runtime_profile.json")"
# The build host's address is private: the deployments repo's deploy.env (PLAT-719).
DEPLOYMENTS_DIR="${AGENTWORKS_DEPLOYMENTS_DIR:-$(cd "$HERE/../../.." && pwd)/deployments}"
DEPLOY_ENV_FILE="$DEPLOYMENTS_DIR/deploy.env"
# shellcheck source=/dev/null
[[ -f "$DEPLOY_ENV_FILE" ]] && source "$DEPLOY_ENV_FILE"
HETZNER=(ssh -p "${BUILD_PORT:?set BUILD_PORT in deployments/deploy.env}" -o ConnectTimeout=20 -o BatchMode=yes "${BUILD_USER:-root}@${BUILD_HOST:?set BUILD_HOST in deployments/deploy.env}")
# The server list is private (PLAT-719): profile-report-servers.txt in the deployments repo, one server per line:
#   name account app data workspace-port [ssh|aws]
# ssh: read over the build host's SSH; aws: the AWS EC2 server, read through SSM with its products/video-studio/aws.env.
SERVERS_FILE="$DEPLOYMENTS_DIR/profile-report-servers.txt"
[[ -f "$SERVERS_FILE" ]] || { echo "No server list: $SERVERS_FILE (private deployments repo; AGENTWORKS_DEPLOYMENTS_DIR)" >&2; exit 2; }
SERVERS=()
while IFS= read -r line; do SERVERS+=("$line"); done < <(grep -v -E '^[[:space:]]*(#|$)' "$SERVERS_FILE")
want="${1:-}"
for line in "${SERVERS[@]}"; do
  read -r name account app data port transport <<<"$line"
  [[ -z "$want" || "$want" == "$name" ]] || continue
  args=(--profile-json "$PROFILE" --name "$name" --account "$account" --app "$app" --data "$data" --workspace-port "$port")
  if [[ "${transport:-ssh}" == aws ]]; then
    # The AWS server: SSH is deploy-only and IP-restricted; read it through SSM like deploy/aws-ec2/slots-admin.sh.
    if [[ -z "${AWS_PROFILE_NAME:-}" && -f "$DEPLOYMENTS_DIR/products/${AWS_PRODUCT_DIR:-video-studio}/aws.env" ]]; then
      # shellcheck disable=SC1090
      set -a; . "$DEPLOYMENTS_DIR/products/${AWS_PRODUCT_DIR:-video-studio}/aws.env"; set +a
    fi
    quoted="$(printf '%q ' "${args[@]}")"
    remote="python3 - $quoted <<'PYREPORT'
$(cat "$HERE/profile_report.py")
PYREPORT"
    inst="$(aws --profile "${AWS_PROFILE_NAME:?set AWS_PROFILE_NAME (products/video-studio/aws.env)}" --region "${AWS_REGION:-us-west-2}" cloudformation describe-stack-resources --stack-name "${STACK_NAME:?set STACK_NAME (products/video-studio/aws.env)}" \
      --query "StackResources[?ResourceType=='AWS::EC2::Instance'].PhysicalResourceId" --output text)"
    cid="$(aws --profile "${AWS_PROFILE_NAME:?set AWS_PROFILE_NAME (products/video-studio/aws.env)}" --region "${AWS_REGION:-us-west-2}" ssm send-command --instance-ids "$inst" --document-name AWS-RunShellScript \
      --comment "runtime profile report (read-only)" --parameters "$(jq -cn --arg s "$remote" '{commands: [$s]}')" --query Command.CommandId --output text)"
    for _ in $(seq 1 30); do
      status="$(aws --profile "${AWS_PROFILE_NAME:?set AWS_PROFILE_NAME (products/video-studio/aws.env)}" --region "${AWS_REGION:-us-west-2}" ssm get-command-invocation --command-id "$cid" --instance-id "$inst" --query Status --output text 2>/dev/null)"
      [[ "$status" == Success || "$status" == Failed ]] && break
      sleep 2
    done
    aws --profile "${AWS_PROFILE_NAME:?set AWS_PROFILE_NAME (products/video-studio/aws.env)}" --region "${AWS_REGION:-us-west-2}" ssm get-command-invocation --command-id "$cid" --instance-id "$inst" --query StandardOutputContent --output text
  else
    "${HETZNER[@]}" "python3 - $(printf '%q ' "${args[@]}")" < "$HERE/profile_report.py"
  fi
  echo
done
