import api from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'
import { openAgentTab } from './openAgentTab'
import type { AgentProfileSideChat } from '../services/api-types'

export const chatForkedEvent = 'agentworks:chat-forked'
type ForkedChat = AgentProfileSideChat & { engine: string; provider: string; model_id: string; connection_id?: string; reasoning_effort?: string; native_resume: boolean; next: string }
const pending = new Set<string>()

/** Both the slash command and agent card use the server's independent copy. */
export async function forkChatTab(tabId: string): Promise<void> {
  const source = useChatStore.getState().chatTabs[tabId]
  if (!source || source.metadata?.agentProfileId !== 'work' || !source.metadata.agentProfileConversationKey || source.metadata.agentProfileTabCanWrite === false || source.metadata.isViewOnly || source.metadata.isScheduledRun || source.metadata.isBotRun) {
    useChatStore.getState().addToast('Open one of your own Crew chats to fork it.', 'info')
    return
  }
  const identity = captureChatIdentity()
  const pendingKey = `${identity}:${tabId}`
  if (pending.has(pendingKey)) return
  pending.add(pendingKey)
  try {
    const { data: chat } = await api.post<ForkedChat>('/api/agent-profiles/work/conversation/fork', { conversation_key: source.metadata.agentProfileConversationKey })
    if (!isChatIdentityCurrent(identity)) return
    const store = useChatStore.getState()
    const id = await store.createChatTab(chat.name, {
      ...source.metadata,
      agentProfileIsMainChat: false, agentProfileBuilder: false,
      agentProfileTabPinned: true, agentProfileTabPosition: undefined,
      agentProfileConversationKey: chat.conversation_key, agentProfileConversationId: chat.conversation_id,
      agentProfileEngine: chat.engine, agentProfileModelID: chat.model_id,
      agentProfileConnectionID: chat.connection_id, agentProfileChatConnectionID: chat.connection_id,
      agentProfileChatProvider: chat.provider, agentProfileReasoningEffort: chat.reasoning_effort,
      agentProfileChatModelOverride: true, agentProfileMCPSelectionInitialized: true,
    }, chat.session_id, { activate: false })
    if (!isChatIdentityCurrent(identity)) return
    store.setTabConfig(id, { selectedServers: source.config.selectedServers, selectedSkills: source.config.selectedSkills })
    window.dispatchEvent(new CustomEvent(chatForkedEvent, { detail: { projectId: source.metadata.agentProfileProjectId, chat: { ...chat, pinned: true } } }))
    openAgentTab(id)
    store.addToast(chat.next, 'info')
  } catch (cause) {
    if (!isChatIdentityCurrent(identity)) return
    const detail = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
    const message = typeof detail === 'string' ? detail : detail?.message
    useChatStore.getState().addToast(message || 'Could not fork this chat. Try again.', 'error')
  } finally { pending.delete(pendingKey) }
}
