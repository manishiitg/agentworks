import { useChatStore } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'
import { sendWorkspacePaneMessageToChat } from './workspacePaneChat'

/** Start in an existing own Builder chat. Main is optional; never overwrite a
 * draft, create a Main, or send through a watched/teammate/Run conversation. */
export async function startTeamDashboardHandoff(projectId: string, prompt: string): Promise<void> {
  const viewer = useAuthStore.getState().user?.id
  const store = useChatStore.getState()
  const candidates = Object.values(store.chatTabs).filter(tab => {
    const m = tab.metadata
    // agentProfileBuilder marks the setup editor, not the person's role.
    // Ordinary own Crew agent tabs deliberately carry false (WorkSurface).
    return viewer && m?.agentProfileProjectId === projectId && m.agentProfileBuilder !== true
      && !m.isViewOnly && !m.isScheduledRun && !m.isBotRun && !m.isExecutionRun && m.agentProfileTabCanWrite !== false
      && (!m.agentProfileTabOwnerID || m.agentProfileTabOwnerID === viewer)
  }).sort((a,b) => (b.lastAccessedAt || b.createdAt || 0) - (a.lastAccessedAt || a.createdAt || 0))
  const tab = candidates[0]
  if (!tab) throw new Error('Open one of your own agent chats in this Crew, then try Add to Team again.')
  // Normal queued delivery starts the requested turn without touching whatever
  // the person is typing, and waits behind an already-running turn.
  await sendWorkspacePaneMessageToChat({ tabId: tab.tabId, message: prompt })
}
