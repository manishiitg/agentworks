#!/usr/bin/env bash
# Prints how each production server differs from the standard runtime profile (deploy/common/runtime_profile.json,
# docs/design/deploy_unification.md). Read-only: it reads the running processes' environment and a few facts, and changes nothing.
#   deploy/common/profile-report-all.sh            # every server
#   deploy/common/profile-report-all.sh excellence  # one
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="$(python3 -c 'import json,sys;print(json.dumps(json.load(open(sys.argv[1]))))' "$HERE/runtime_profile.json")"
HETZNER=(ssh -p 2299 -o ConnectTimeout=20 -o BatchMode=yes root@116.202.210.102)
# name account app data workspace-port
SERVERS=(
  "excellence agents /srv/agents /srv/agents/state 24001"
  "confida confida /srv/confida /srv/confida/state 22001"
  "sparkquill sparkquill /srv/sparkquill /srv/sparkquill/state 23001"
  "dominion dominion /srv/dominion /srv/dominion/state 21001"
  "rts video-studio /var/lib/video-studio/video-studio /data/video-studio 8080"
)
want="${1:-}"
for line in "${SERVERS[@]}"; do
  read -r name account app data port <<<"$line"
  [[ -z "$want" || "$want" == "$name" ]] || continue
  args=(--profile-json "$PROFILE" --name "$name" --account "$account" --app "$app" --data "$data" --workspace-port "$port")
  if [[ "$name" == rts ]]; then
    # RTS: SSH is deploy-only and IP-restricted; read it through SSM like deploy/aws-ec2/slots-admin.sh.
    quoted="$(printf '%q ' "${args[@]}")"
    remote="python3 - $quoted <<'PYREPORT'
$(cat "$HERE/profile_report.py")
PYREPORT"
    inst="$(aws --profile "${AWS_PROFILE_NAME:-RTS}" --region "${AWS_REGION:-us-west-2}" cloudformation describe-stack-resources --stack-name "${STACK_NAME:-video-studio-prod}" \
      --query "StackResources[?ResourceType=='AWS::EC2::Instance'].PhysicalResourceId" --output text)"
    cid="$(aws --profile "${AWS_PROFILE_NAME:-RTS}" --region "${AWS_REGION:-us-west-2}" ssm send-command --instance-ids "$inst" --document-name AWS-RunShellScript \
      --comment "runtime profile report (read-only)" --parameters "$(jq -cn --arg s "$remote" '{commands: [$s]}')" --query Command.CommandId --output text)"
    for _ in $(seq 1 30); do
      status="$(aws --profile "${AWS_PROFILE_NAME:-RTS}" --region "${AWS_REGION:-us-west-2}" ssm get-command-invocation --command-id "$cid" --instance-id "$inst" --query Status --output text 2>/dev/null)"
      [[ "$status" == Success || "$status" == Failed ]] && break
      sleep 2
    done
    aws --profile "${AWS_PROFILE_NAME:-RTS}" --region "${AWS_REGION:-us-west-2}" ssm get-command-invocation --command-id "$cid" --instance-id "$inst" --query StandardOutputContent --output text
  else
    "${HETZNER[@]}" "python3 - $(printf '%q ' "${args[@]}")" < "$HERE/profile_report.py"
  fi
  echo
done
