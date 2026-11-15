import { useChatStore } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'
import { sendToTab } from './agentPurposeDraft'

/** Start in an existing own Builder chat. Main is optional; never overwrite a
 * draft, create a Main, or send through a watched/teammate/Run conversation. */
export function startTeamDashboardHandoff(projectId: string, prompt: string): void {
  const viewer = useAuthStore.getState().user?.id
  const store = useChatStore.getState()
  const candidates = Object.values(store.chatTabs).filter(tab => {
    const m = tab.metadata
    return viewer && m?.agentProfileProjectId === projectId && m.agentProfileBuilder !== false
      && !m.isViewOnly && m.agentProfileTabCanWrite !== false
      && (!m.agentProfileTabOwnerID || m.agentProfileTabOwnerID === viewer)
  }).sort((a,b) => (b.lastAccessedAt || b.createdAt || 0) - (a.lastAccessedAt || a.createdAt || 0))
  const tab = candidates[0]
  if (!tab) throw new Error('Open one of your own agent chats in this Crew, then try Add to Team again.')
  if (store.getTabConfig(tab.tabId)?.inputText?.trim()) throw new Error('Send or clear your draft in this Crew’s agent chat, then try Add to Team again.')
  if (!sendToTab(tab.tabId, prompt)) throw new Error('Reopen your agent chat and try again.')
}
