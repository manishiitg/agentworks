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
export AGENTWORKS_DEPLOYMENT_MODE=local MULTI_USER_MODE=false DEFAULT_USER_ID="$migration_user" AGENTWORKS_STATE_ROOT="$migration_state" WORKSPACE_DOCS_PATH="$migration_docs"
migration_backup="$migration_state/migration-backups/local-refactor-$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$migration_backup"
python3 "$migration_repo/scripts/migrations/account-availability.py" --workspace-dir "$migration_docs" --env-file "${AGENTWORKS_ENV_FILE:-$migration_repo/agent_go/.env}" --backup-dir "$migration_backup/account-audiences"
python3 "$migration_repo/scripts/migrations/vault-apps-setting.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/vault-apps-setting" --done-file "$migration_state/migrations/vault-apps-setting.done"
python3 "$migration_repo/scripts/migrations/one-sharing-model.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/one-sharing-model"
python3 "$migration_repo/scripts/migrations/agent-email-inboxes.py" "$migration_state/gmail-inbound/email.db" --workspace-root "$migration_docs"
python3 "$migration_repo/scripts/migrations/crew-owner-tabs.py" --workspace-root "$migration_docs" --state-root "$migration_state"
python3 "$migration_repo/scripts/migrations/agentbridge-receipts.py" "$migration_docs/_system/agent_messages/conversations.json"
python3 "$migration_repo/scripts/migrations/retire-agent-server-relay.py" --state-root "$migration_state" --mailbox "$migration_docs/_system/agent_messages/conversations.json"
python3 "$migration_repo/scripts/migrations/fold-crew-role.py" --workspace-root "$migration_docs" --state-root "$migration_state"
migration_catalogs=()
for migration_catalog in "$migration_repo/agent_go/pkg/platformtools/catalog/"*-actions.json; do
  if [[ -f "$migration_catalog" ]]; then migration_catalogs+=(--catalog "$migration_catalog"); fi
done
python3 "$migration_repo/scripts/group-platform-tool-selections.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/group-platform-tools" "${migration_catalogs[@]}" --native-namespace workspace_advanced --native-namespace workspace_browser --native-namespace human_tools
python3 "$migration_repo/scripts/simplify-workflow-settings.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/simplify-workflow-settings"
python3 "$migration_repo/scripts/migrate_workflow_builder_chats.py" --workspace-root "$migration_docs" --default-owner-id "$migration_user" --default-owner-username "Local user" --apply
python3 "$migration_repo/scripts/migrate-project-schedules.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/project-schedules" --single-user --local-user-id "$migration_user"
python3 "$migration_repo/scripts/migrate-browser-modes.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/browser-modes" --state-root "$migration_state" --deployment-mode local
python3 "$migration_repo/scripts/repair-workflow-builder-tabs.py" --workspace-dir "$migration_docs" --backup-dir "$migration_backup/workflow-builder-tabs"
python3 "$migration_repo/scripts/migrations/split-agent-mailbox.py" "$migration_docs/_system/agent_messages/conversations.json"

echo "Local stopped-app migrations complete. The app has not been restarted."
