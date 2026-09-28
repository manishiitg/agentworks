// Work product constants. The profile id/version and conversation metadata
// root must match agent_go/internal/workproduct/product.yaml.
import { getApiBaseUrl, getAuthToken } from '../../services/api'

export const WORK_PROFILE_ID = 'work'
export const WORK_PROFILE_VERSION = 3
export const WORK_PROJECTS_ROOT = 'Chats/Work/projects'

export type WorkProductCommand = {
  name: string
  description: string
  icon: string
  prompt: string
}

type AgentProfileResponse = {
  commands?: Array<Record<string, unknown>>
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

// Slash commands Crew ships with itself, declared in its product.yaml.
// A command with no prompt is dropped rather than offered: it would appear in
// the menu and then submit nothing, which reads as the product being broken.
export function parseProductCommands(profile: AgentProfileResponse): WorkProductCommand[] {
  return (profile.commands ?? []).flatMap((command) => {
    const name = asString(command.name)
    const prompt = asString(command.prompt)
    if (!name || !prompt) return []
    return [{
      name,
      description: asString(command.description),
      icon: asString(command.icon) || 'terminal',
      prompt,
    }]
  })
}

export async function loadWorkProductCommands(profileId: string = WORK_PROFILE_ID, profileVersion: number = WORK_PROFILE_VERSION, noun = 'Crew'): Promise<WorkProductCommand[]> {
  const token = getAuthToken()
  const response = await fetch(`${getApiBaseUrl()}/api/agent-profiles/${encodeURIComponent(profileId)}?version=${profileVersion}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  if (!response.ok) throw new Error(`Unable to load ${noun} commands (${response.status})`)
  return parseProductCommands(await response.json() as AgentProfileResponse)
}
