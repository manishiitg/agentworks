import { useChatStore } from '../../stores/useChatStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import type { CrewAsk } from './YourComputerPanel'

// Opening the Access page at a tab (WorkSurface listens), from an agent's tab or the side panel's Access lines.
// Team is its own page; Artifacts is its own page; the remaining tabs make up Online (owner, 11 Nov: Share split into Team and Online).
export type CrewTeamSection = 'people' | 'agents' | 'activity' | 'sync'
export type CrewAccessSection = 'mac' | 'online-agents' | 'publish' | 'team' | 'artifacts' | 'dashboards'
export const crewAccessDestination = (section: CrewAccessSection): CrewAccessSection => section === 'dashboards' ? 'artifacts' : section
let pendingAccess: { projectId: string; tab: string; section: CrewAccessSection; teamSection?: CrewTeamSection } | null = null
export function hasPendingCrewAccess(projectId: string) { return pendingAccess?.projectId === projectId }
/** Which Access tab a pending open asks for (Team unless said otherwise). */
export function pendingCrewAccessSection(projectId: string): CrewAccessSection { return pendingAccess?.projectId === projectId ? pendingAccess.section : 'team' }
export function pendingCrewTeamSection(projectId: string): CrewTeamSection | undefined { return pendingAccess?.projectId === projectId ? pendingAccess.teamSection : undefined }
export function openCrewAccess(projectId: string, tab: string, section: CrewAccessSection = 'team', teamSection?: CrewTeamSection) {
  section = crewAccessDestination(section)
  if (section === 'artifacts') console.info(`[CREW_ARTIFACTS] project=${projectId} action=opened source=crew_navigation`)
  pendingAccess = { projectId, tab, section, teamSection }
  window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId, section, teamSection } }))
}
/** Opens another person's own thread with one of this Mac's shared agents, for the owner to read. */
export async function openCrewAskThread(projectId: string, ask: CrewAsk) {
  const store = useChatStore.getState()
  const tabs = Object.values(store.chatTabs)
  const main = tabs.find(row => row.metadata?.agentProfileProjectId === projectId && row.metadata?.agentProfileIsMainChat && !row.metadata?.isViewOnly)
  if (!main || !ask.session_id) return
  const existing = tabs.find(row => row.sessionId === ask.session_id && row.metadata?.isViewOnly)
  const tabId = existing?.tabId || await store.createChatTab(`${ask.sender}'s chat with ${ask.agent}`, {
    ...main.metadata,
    agentProfileIsMainChat: false,
    agentProfileBuilder: false,
    agentProfileConversationKey: `${projectId}:history:${ask.session_id}`,
    agentProfileConversationId: undefined,
    isViewOnly: true,
    viewOnlyWriter: ask.sender,
    readOnlyRestoredAt: Date.now(),
    userInteractiveContinuation: false,
  }, ask.session_id)
  store.setTabCanSteer(tabId, false)
  await hydrateTabEvents(ask.session_id, { workspacePath: main.metadata?.agentProfileWorkspace, fallbackToChatHistory: true, preferChatHistory: true })
  activateTab(tabId)
}
