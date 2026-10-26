#!/usr/bin/env bash
# Run only after stopping the local app and all workflow writers. No restart.
set -euo pipefail
migration_docs="" migration_state="" migration_agent="" migration_user=""
while (( $# )); do
  case "$1" in
    --workspace-dir) migration_docs="${2:?missing workspace directory}" ;;
    --state-root) migration_state="${2:?missing state root}" ;;
    --agent-binary) migration_agent="${2:?missing agent binary}" ;;
    --local-user-id) migration_user="${2:?missing local user ID}" ;;
    *) echo "Use --workspace-dir DIR --state-root DIR --agent-binary FILE --local-user-id ID; stop the app first." >&2; exit 2 ;;
  esac
  shift 2
done
migration_repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ -d "$migration_docs" && -d "$migration_state" && -x "$migration_agent" && -n "$migration_user" ]]
if [[ "${MULTI_USER_MODE:-false}" == true ]]; then echo "This command is only for the local single-user app." >&2; exit 2; fi
export MULTI_USER_MODE=false DEFAULT_USER_ID="$migration_user" AGENTWORKS_STATE_ROOT="$migration_state" WORKSPACE_DOCS_PATH="$migration_docs"
migration_backup="$migration_state/migration-backups/local-refactor-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$migration_backup"
python3 "$migration_repo/scripts/migrations/agent-email-inboxes.py" "$migration_state/gmail-inbound/email.db" --workspace-root "$migration_docs"
python3 "$migration_repo/scripts/migrations/crew-owner-tabs.py" --workspace-root "$migration_docs" --state-root "$migration_state"
python3 "$migration_repo/scripts/migrations/agentbridge-receipts.py" "$migration_docs/_system/agent_messages/conversations.json"
python3 "$migration_repo/scripts/migrations/retire-agent-server-relay.py" --state-root "$migration_state" --mailbox "$migration_docs/_system/agent_messages/conversations.json"
python3 "$migration_repo/scripts/migrations/fold-crew-role.py" --workspace-root "$migration_docs" --state-root "$migration_state"
python3 "$migration_repo/scripts/remove-autopilot-engine.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/remove-autopilot"
migration_catalogs=()
for migration_catalog in "$migration_repo/agent_go/pkg/platformtools/catalog/"*-actions.json; do
  if [[ -f "$migration_catalog" ]]; then migration_catalogs+=(--catalog "$migration_catalog"); fi
done
python3 "$migration_repo/scripts/group-platform-tool-selections.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/group-platform-tools" "${migration_catalogs[@]}" --native-namespace workspace_advanced
python3 "$migration_repo/scripts/simplify-workflow-settings.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/simplify-workflow-settings"
migration_plan_roots=()
for migration_root in "$migration_docs/Workflow" "$migration_docs/_users/"*/Workflow; do
  if [[ -d "$migration_root" ]]; then migration_plan_roots+=("$migration_root"); fi
done
if (( ${#migration_plan_roots[@]} )); then
  "$migration_agent" server migrate-workflow-agent-steps "${migration_plan_roots[@]}" --prepare-only --apply --server-drained
fi
python3 "$migration_repo/scripts/migrate_workflow_builder_chats.py" --workspace-root "$migration_docs" --default-owner-id "$migration_user" --default-owner-username "Local user" --apply
python3 "$migration_repo/scripts/migrate-project-schedules.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/project-schedules" --single-user --local-user-id "$migration_user"
echo "Local stopped-app migrations complete. The app has not been restarted."
