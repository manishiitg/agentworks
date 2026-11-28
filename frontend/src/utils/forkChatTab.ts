import { create } from 'zustand'
import api from '../services/api'
import { useChatStore } from '../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'
import { openAgentTab } from './openAgentTab'
import { openAppProduct } from './appNavigation'
import type { AgentProfileSideChat } from '../services/api-types'


export const chatForkedEvent = 'agentworks:chat-forked'
type ForkedChat = AgentProfileSideChat & { chat_id?: string; project_id?: string; project_name?: string; cross_project?: boolean; engine: string; provider: string; model_id: string; connection_id?: string; reasoning_effort?: string; native_resume: boolean; next: string }
type ForkFeedback = { pending: boolean; error?: string }
// Presentation of the existing request, not a queue or a new admission rule.
const useForkFeedback = create<{ byTab: Record<string, ForkFeedback> }>(() => ({ byTab: {} }))
const feedbackKey = (tabId: string) => `${captureChatIdentity()}:${tabId}`
export function useChatForkFeedback(tabId: string | null) {
  const key = tabId ? feedbackKey(tabId) : ''
  return useForkFeedback(state => state.byTab[key])
}
export function dismissChatForkFeedback(tabId: string) {
  const key = feedbackKey(tabId)
  useForkFeedback.setState(state => {
    const byTab = { ...state.byTab }; delete byTab[key]
    return { byTab }
  })
}

/** Both the slash command and agent card use the server's independent copy. `copy` forks another chat of the same
 * project in this tab's place: a /btw side question kept with "Open as tab" forks its hidden copy (btwSideQuestion.ts).
 * When `options.targetProjectId` names another project, the fork is created there and opened in that project. */
export async function forkChatTab(
  tabId: string,
  copy?: { conversationKey: string; name: string },
  options?: { targetProjectId?: string; name?: string },
): Promise<boolean> {
  const source = useChatStore.getState().chatTabs[tabId]
  if (!source || source.metadata?.agentProfileId !== 'work' || !source.metadata.agentProfileConversationKey || source.metadata.agentProfileTabCanWrite === false || source.metadata.isViewOnly || source.metadata.isScheduledRun || source.metadata.isBotRun) {
    useChatStore.getState().addToast('Open one of your own project chats to fork it.', 'info')
    return false
  }
  const identity = captureChatIdentity()
  const pendingKey = `${identity}:${tabId}`
  if (useForkFeedback.getState().byTab[pendingKey]?.pending) return false
  useForkFeedback.setState(state => ({ byTab: { ...state.byTab, [pendingKey]: { pending: true } } }))
  try {
    const targetProject = options?.targetProjectId?.trim() || ''
    const sourceProject = source.metadata.agentProfileProjectId || ''
    const crossProject = Boolean(targetProject && targetProject !== sourceProject)
    const body: Record<string, string> = copy
      ? { conversation_key: copy.conversationKey, name: copy.name }
      : { conversation_key: source.metadata.agentProfileConversationKey }
    if (options?.name?.trim()) body.name = options.name.trim()
    if (crossProject) body.target_project = targetProject
    const { data: chat } = await api.post<ForkedChat>('/api/agent-profiles/work/conversation/fork', body)
    if (!isChatIdentityCurrent(identity)) return false
    const store = useChatStore.getState()
    if (crossProject || chat.cross_project) {
      const destProject = chat.project_id || targetProject
      const destAgent = chat.chat_id || chat.conversation_key.split(':chat:')[1] || ''
      openAppProduct({ surface: 'work', id: destProject, agent: destAgent || undefined })
      store.addToast(chat.next, 'info')
      return true
    }
    const id = await store.createChatTab(chat.name, {
      ...source.metadata,
      agentProfileIsDefaultAgent: false, agentProfileBuilder: false,
      agentProfileTabPinned: true, agentProfileTabPosition: undefined,
      agentProfileConversationKey: chat.conversation_key, agentProfileConversationId: chat.conversation_id,
      agentProfileEngine: chat.engine, agentProfileModelID: chat.model_id,
      agentProfileConnectionID: chat.connection_id, agentProfileChatConnectionID: chat.connection_id,
      agentProfileChatProvider: chat.provider, agentProfileReasoningEffort: chat.reasoning_effort,
      agentProfileChatModelOverride: true, agentProfileMCPSelectionInitialized: true,
    }, chat.session_id, { activate: false })
    if (!isChatIdentityCurrent(identity)) return false
    store.setTabConfig(id, { selectedServers: source.config.selectedServers, selectedSkills: source.config.selectedSkills })
    window.dispatchEvent(new CustomEvent(chatForkedEvent, { detail: { projectId: source.metadata.agentProfileProjectId, chat: { ...chat, pinned: true } } }))
    openAgentTab(id)
    store.addToast(chat.next, 'info')
    return true
  } catch (cause) {
    if (!isChatIdentityCurrent(identity)) return false
    const detail = (cause as { response?: { data?: { error?: string | { message?: string } } } })?.response?.data?.error
    const message = typeof detail === 'string' ? detail : detail?.message
    const error = message || 'Could not fork this chat. Try again.'
    useForkFeedback.setState(state => ({ byTab: { ...state.byTab, [pendingKey]: { pending: false, error } } }))
    useChatStore.getState().addToast(error, 'error')
    return false
  } finally {
    useForkFeedback.setState(state => {
      if (isChatIdentityCurrent(identity) && state.byTab[pendingKey]?.error) return state
      const byTab = { ...state.byTab }; delete byTab[pendingKey]
      return { byTab }
    })
  }
}
