#!/usr/bin/env bash
# Give the Video Studio / AgentWorks EC2 box its own AWS identity for workflows
# that shell out to the AWS CLI: create (or update) the IAM role + instance
# profile stack and associate the profile with the running instance.
#
# Deliberately NOT part of template.yaml: that stack has
# drifted from the live instance (UserData, KeyName), so any update to it
# would REPLACE the instance -- a change set on 2026-09-03 showed
# "Instance ... Replacement: True". Associating a profile out-of-band is a
# no-interruption operation. Re-runnable: updating the policy is a plain
# stack update, and an already-associated instance is left alone.
set -euo pipefail

AWS_PROFILE_NAME="${AWS_PROFILE_NAME:?set AWS_PROFILE_NAME (private deployments repo: products/video-studio/aws.env)}"
AWS_REGION="${AWS_REGION:-us-west-2}"
STACK_NAME="${STACK_NAME:?set STACK_NAME (private deployments repo: products/video-studio/aws.env)}"
ROLE_STACK_NAME="${ROLE_STACK_NAME:-video-studio-instance-role}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

aws_rts() { aws --profile "$AWS_PROFILE_NAME" --region "$AWS_REGION" "$@"; }

aws_rts cloudformation deploy \
  --stack-name "$ROLE_STACK_NAME" \
  --template-file "$SCRIPT_DIR/instance-role.yaml" \
  --capabilities CAPABILITY_IAM CAPABILITY_NAMED_IAM \
  --no-fail-on-empty-changeset

profile="$(aws_rts cloudformation describe-stacks --stack-name "$ROLE_STACK_NAME" --query 'Stacks[0].Outputs[?OutputKey==`InstanceProfileName`].OutputValue|[0]' --output text)"
instance="$(aws_rts cloudformation describe-stacks --stack-name "$STACK_NAME" --query 'Stacks[0].Outputs[?OutputKey==`InstanceId`].OutputValue|[0]' --output text)"
current="$(aws_rts ec2 describe-iam-instance-profile-associations --filters "Name=instance-id,Values=$instance" --query 'IamInstanceProfileAssociations[?State==`associated`].IamInstanceProfile.Arn|[0]' --output text)"

if [[ "$current" == *"/$profile" ]]; then
  echo "instance $instance already uses $profile"
elif [[ "$current" == "None" || -z "$current" ]]; then
  aws_rts ec2 associate-iam-instance-profile --instance-id "$instance" --iam-instance-profile "Name=$profile" --query 'IamInstanceProfileAssociation.State' --output text
  echo "associated $profile with $instance"
else
  echo "instance $instance already has a different profile ($current); not replacing it" >&2
  exit 1
fi
