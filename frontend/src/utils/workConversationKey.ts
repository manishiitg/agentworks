import type { ChatTab } from '../stores/useChatStore'
import type { ProductRoute } from './productRoutes'

/** The conversation key a Crew deep link names. The link's agent is the key after the Crew's id: "agent-x" for a side
 * chat (`<crew>:chat:agent-x`), "schedule:<id>" for a message schedule's own chat (`<crew>:schedule:<id>`), "main" for Main. */
export function workConversationKey(projectId: string, agent?: string | null): string {
  if (!agent || agent === 'main') return projectId
  if (agent.startsWith('team:')) return `${projectId}:${agent}`
  return agent.startsWith('schedule:') ? `${projectId}:${agent}` : `${projectId}:chat:${agent}`
}

/** The deep-link agent for a listed conversation key: the part after the Crew's id, so the link keeps its full identity. */
export function workConversationAgent(key: string): string | undefined {
  if (key.includes(':team:')) return key.slice(key.indexOf(':team:') + 1)
  return key.split(':chat:')[1] ?? (key.includes(':schedule:') ? key.slice(key.indexOf(':schedule:') + 1) : undefined)
}

/** An opening hint belongs to the destination project, never the previously
 * mounted project's URL during a cross-project switch. Selection stays server-owned. */
export function workOpeningAgentKey(profileId: string, projectId: string, route: ProductRoute | null | undefined): string | undefined {
  if (route?.surface !== profileId || route.id !== projectId || !route.agent || route.agent === 'main') return undefined
  return workConversationKey(projectId, route.agent)
}

/** A runtime can have both a personal and a Team projection. Only the admitted
 * conversation identity can reuse a tab; a shared session ID is not an alias. */
export function matchesWorkConversation(tab: Pick<ChatTab, 'metadata'>, profileId: string, projectId: string, key: string): boolean {
  return !!key && tab.metadata?.isViewOnly !== true &&
    tab.metadata?.agentProfileId === profileId &&
    tab.metadata?.agentProfileProjectId === projectId &&
    tab.metadata?.agentProfileConversationKey === key
}
