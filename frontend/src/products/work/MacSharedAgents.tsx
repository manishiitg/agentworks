import { useChatStore } from '../../stores/useChatStore'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { activateTab } from '../../utils/activateTab'
import type { CrewAsk } from './YourComputerPanel'

// Opening the Access page at a tab (WorkSurface listens), from an agent's tab or the side panel's Access lines.
export type CrewAccessSection = 'mac' | 'publish' | 'team' | 'dashboards' | 'slack'
let pendingAccess: { projectId: string; tab: string; section: CrewAccessSection } | null = null
export function hasPendingCrewAccess(projectId: string) { return pendingAccess?.projectId === projectId }
/** Which Access tab a pending open asks for (Team unless said otherwise). */
export function pendingCrewAccessSection(projectId: string): CrewAccessSection { return pendingAccess?.projectId === projectId ? pendingAccess.section : 'team' }
export function openCrewAccess(projectId: string, tab: string, section: CrewAccessSection = 'team') {
  pendingAccess = { projectId, tab, section }
  window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId, section } }))
}
/** The side panel's Shared folder row: open Access on its Team tab, scrolled to the shared folder's Sync section. */
let pendingSync = ''
export function openCrewSync(projectId: string) {
  pendingSync = projectId
  openCrewAccess(projectId, '')
}
export function takeCrewSync(projectId: string) {
  if (pendingSync !== projectId) return false
  pendingSync = ''
  return true
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
