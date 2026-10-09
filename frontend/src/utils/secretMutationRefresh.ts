import type { PollingEvent } from '../services/api-types'
import { getTypedEventData } from '../generated/event-types'

export const PROJECT_SECRETS_REFRESH_EVENT = 'project-secrets-refresh'

const SECRET_MUTATIONS = new Set([
  'set_workflow_secret',
  'delete_workflow_secret',
  'manage_vault_secret_access',
  'update_project_global_secret_selection',
])

/** A completed project-secret tool call invalidates the right-side list. */
export function projectSecretsNeedRefresh(event: PollingEvent): boolean {
  const result = getTypedEventData(event, 'tool_call_end')
    ?? getTypedEventData(event, 'tool_execution')
  return Boolean(result?.tool_name && SECRET_MUTATIONS.has(result.tool_name))
}
