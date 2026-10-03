import { getApiBaseUrl, getAuthToken } from '../services/api'
import { parseAgentworksProductCommands, type AgentworksProductCommand } from './agentworksProductCommands'

export const AGENTWORKS_PROFILE_ID = 'agentworks'
export const AGENTWORKS_PROFILE_VERSION = 1

const loaded = new Map<string, Promise<AgentworksProductCommand[]>>()

// The workflow chat remounts on every workflow switch; load the product
// commands once per page. A missing profile (404) is a stable answer (no
// commands), so it is cached too; other failures may be retried.
export function loadWorkflowProductCommands(profileId = AGENTWORKS_PROFILE_ID, version = AGENTWORKS_PROFILE_VERSION): Promise<AgentworksProductCommand[]> {
  const key = `${profileId}:${version}`
  let pending = loaded.get(key)
  if (!pending) {
    pending = fetchWorkflowProductCommands(profileId, version).catch(error => {
      loaded.delete(key)
      throw error
    })
    loaded.set(key, pending)
  }
  return pending
}

export function loadAgentworksProductCommands(): Promise<AgentworksProductCommand[]> {
  return loadWorkflowProductCommands()
}

export function resetAgentworksProductCommandsCache(): void {
  loaded.clear()
}

async function fetchWorkflowProductCommands(profileId: string, version: number): Promise<AgentworksProductCommand[]> {
  const token = getAuthToken()
  const response = await fetch(`${getApiBaseUrl()}/api/agent-profiles/${encodeURIComponent(profileId)}?version=${version}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  if (response.status === 404) return []
  if (!response.ok) throw new Error(`Unable to load ${profileId} commands (${response.status})`)
  return parseAgentworksProductCommands(await response.json() as { commands?: Array<Record<string, unknown>> })
}
