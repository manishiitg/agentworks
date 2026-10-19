import type { AgentProfileMainChatChange } from '../../services/api-types'
import { useChatStore } from '../../stores/useChatStore'
import { belongsToWorkProject } from './workTabs'

export const MAIN_CHAT_CHANGED = 'agentworks:main-chat-changed'

export function canMakeMainChat(key: string | undefined, projectId: string): boolean {
  if (!key?.startsWith(`${projectId}:chat:`)) return false
  const id = key.slice(`${projectId}:chat:`.length)
  return Boolean(id && !id.includes(':') && !id.startsWith('fn.') && !id.startsWith('agent-'))
}

// Only role metadata changes. Chat IDs, sessions, drafts, history and model
// selections retain their owners; legacy root metadata adopts its migrated ID.
export function applyMainChatChange(projectId: string, result: AgentProfileMainChatChange): void {
  const store = useChatStore.getState()
  let activeTabId = store.activeTabId
  const chatTabs = { ...store.chatTabs }
  for (const tab of Object.values(store.chatTabs)) {
    if (!belongsToWorkProject(tab, projectId) || tab.metadata?.isViewOnly) continue
    const isMain = tab.sessionId === result.main.session_id
    const binding = isMain ? result.main : tab.sessionId === result.previous_main.session_id ? result.previous_main : undefined
    chatTabs[tab.tabId] = { ...tab, metadata: {
      ...tab.metadata, agentProfileIsMainChat: isMain,
      ...(binding ? { agentProfileConversationKey: binding.conversation_key, agentProfileConversationId: binding.conversation_id } : {}),
    } }
    if (isMain) activeTabId = tab.tabId
  }
  useChatStore.setState({ chatTabs, activeTabId })
}
