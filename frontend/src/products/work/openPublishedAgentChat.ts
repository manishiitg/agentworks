import { useChatStore } from '../../stores/useChatStore'
import { activateTab } from '../../utils/activateTab'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { hydrateTabEvents } from '../../utils/sessionRestore'
import { belongsToWorkProject } from './workTabs'
import type { PublishedAgentChat } from './CrewPublishedAgents'

/** Reopen the existing private published conversation without starting a turn. */
export async function openPublishedAgentChat(projectId: string, chat: PublishedAgentChat) {
  const identity = captureChatIdentity()
  const state = useChatStore.getState()
  const primary = Object.values(state.chatTabs).find(tab => belongsToWorkProject(tab, projectId) &&
    tab.metadata?.agentProfileConversationKey === projectId && !tab.metadata?.isViewOnly)
  if (!primary) throw new Error('Open this project’s main chat before opening its agent.')
  const existing = Object.values(state.chatTabs).find(tab => belongsToWorkProject(tab, projectId) && tab.sessionId === chat.session_id)
  if (existing) { activateTab(existing.tabId); return }
  const id = await state.createChatTab(chat.name, { ...primary.metadata, agentProfileBuilder: false,
    agentProfileConversationKey: chat.conversation_key, agentProfileConversationId: chat.conversation_id,
    agentProfileEngine: chat.provider, agentProfileModelID: chat.model_id, agentProfileConnectionID: chat.connection_id,
    agentProfileChatProvider: chat.provider, agentProfileChatConnectionID: chat.connection_id,
    agentProfileMCPSelectionInitialized: true,
  }, chat.session_id)
  if (!isChatIdentityCurrent(identity)) return
  const current = useChatStore.getState()
  current.renameTab(id, chat.name)
  current.setTabConfig(id, { selectedServers: primary.config.selectedServers, selectedSkills: primary.config.selectedSkills })
  activateTab(id)
  await hydrateTabEvents(chat.session_id, { workspacePath: primary.metadata?.agentProfileWorkspace, fallbackToChatHistory: true, preferChatHistory: true })
}
