import { useEffect } from 'react'
import api from '../services/api'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { sendWorkspacePaneMessageToChat } from '../utils/workspacePaneChat'
import type { ChromeExtensionStatus } from '../components/workflow/ChromeExtensionConnection'

const inFlight = new Set<string>()

function eligible(tab: ChatTab | undefined, workspace: string) {
  const metadata = tab?.metadata
  return metadata?.mode === 'multi-agent' && ['code', 'work'].includes(metadata.agentProfileId || '') &&
    metadata.agentProfileWorkspace === workspace && !metadata.agentProfileBuilder &&
    !metadata.isViewOnly && !metadata.isScheduledRun && !metadata.isBotRun
}

/** Observe the active Code or Crew chat even when its Browser pane is closed. */
export async function deliverChromeExtensionChatNotifications(tabId: string, workspace: string) {
  if (inFlight.has(tabId)) return
  const before = useChatStore.getState()
  const tab = before.getTab(tabId)
  if (before.activeTabId !== tabId || !eligible(tab, workspace)) return
  const sessionId = tab?.sessionId
  const profileId = tab?.metadata?.agentProfileId
  inFlight.add(tabId)
  try {
    const { data } = await api.get<ChromeExtensionStatus>('/api/browser/extension', {
      params: { workspace_path: workspace, profile_id: profileId }, skipSessionContext: true,
    })
    const store = useChatStore.getState()
    const current = store.getTab(tabId)
    if (store.activeTabId !== tabId || !eligible(current, workspace) || current?.sessionId !== sessionId || current?.metadata?.agentProfileId !== profileId) return
    const saved = store.getTabConfig(tabId)?.browserExtensionState
    const previous = saved?.workspace === workspace ? saved : undefined
    const connectionId = data.connection_id || previous?.connectionId || ''
    let notificationId = '', message = ''
    if (data.connected && data.connection_id) {
      notificationId = `browser-extension:${data.connection_id}:connected`
      message = '[AUTO-NOTIFICATION] Your browser extension is connected to this project. ' +
        'Call agent_browser status to verify the current connection, then use ordinary agent_browser commands without --cdp; the backend selects the private connection. ' +
        'Tabs stay in the background by default; use active=true only when intentionally bringing a tab forward. Discard old browser refs and take a fresh snapshot before acting. ' +
        (data.tabs > 0 ? 'Shared tabs are available. Continue the requested browser task if there is one.' : 'No tabs exist for this project yet. Create a tab with agent_browser open or tab new to begin; I only need to share a tab manually if I want you to use an already-open page.')
      // A later first share makes a connection that was initially empty usable.
      if (previous?.connected && previous.connectionId === data.connection_id && previous.tabs === 0 && data.tabs > 0) {
        notificationId = `browser-extension:${data.connection_id}:ready`
        message = '[AUTO-NOTIFICATION] A browser tab is now shared with this project. Call agent_browser status, use ordinary commands without --cdp, and take a fresh snapshot. Continue the requested browser task if there is one.'
      }
    } else if (connectionId && (previous?.connected || (previous?.selected && !data.selected))) {
      const restoredWorkspace = previous?.selected && !data.selected
      notificationId = `browser-extension:${connectionId}:${restoredWorkspace ? 'deselected' : 'disconnected'}`
      message = '[AUTO-NOTIFICATION] Your browser extension has disconnected from this project. Discard cached browser refs. ' +
        (data.selected ? 'Pause browser actions and ask me to reconnect. Do not fall back to another browser.' : 'The extension is no longer selected. Call agent_browser status before further browser actions to verify the newly selected browser.')
    }
    if (notificationId && !store.getTabConfig(tabId)?.mcpOAuthNotificationIDs?.includes(notificationId)) await sendWorkspacePaneMessageToChat({ tabId, message, notificationId })
    if (previous && previous.connectionId === connectionId && previous.connected === data.connected &&
      previous.selected === data.selected && previous.tabs === data.tabs) return
    store.setTabConfig(tabId, { browserExtensionState: {
      workspace, connectionId, connected: data.connected, selected: data.selected, tabs: data.tabs,
    } })
  } finally { inFlight.delete(tabId) }
}

export function useChromeExtensionChatNotifications(tabId: string | null | undefined) {
  const metadata = useChatStore(state => tabId ? state.chatTabs[tabId]?.metadata : undefined)
  const activeTabId = useChatStore(state => state.activeTabId)
  const sessionId = useChatStore(state => tabId ? state.chatTabs[tabId]?.sessionId : undefined)
  const workspace = metadata?.agentProfileWorkspace
  const enabled = activeTabId === tabId && metadata?.mode === 'multi-agent' && ['code', 'work'].includes(metadata.agentProfileId || '') &&
    !metadata.agentProfileBuilder && !metadata.isViewOnly && !metadata.isScheduledRun && !metadata.isBotRun
  useEffect(() => {
    if (!enabled || !tabId || !workspace) return
    const refresh = () => { void deliverChromeExtensionChatNotifications(tabId, workspace).catch(error => console.warn('[Browser extension] Could not deliver chat notification', error)) }
    refresh()
    const timer = window.setInterval(refresh, 2500)
    window.addEventListener('focus', refresh)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', refresh) }
  }, [enabled, tabId, workspace, sessionId])
}
