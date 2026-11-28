import { useChatStore } from '../../stores/useChatStore'
import { setUpAgentPurpose } from '../../utils/agentPurposeDraft'
import { teamAgentForTab, type TeamTabAgent, type TeamTabState } from './teamApi'

/** This agent's own chat tab in this app (its person's), found by the Team tab's key; undefined when it is not open. */
export function teamAgentChatTab(state: TeamTabState, projectId: string, key: string) {
  return Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && !item.metadata?.isViewOnly
    && teamAgentForTab(state, item.metadata?.agentProfileConversationKey, item.metadata?.agentProfileIsDefaultAgent !== false, item.metadata?.agentProfileTabCanWrite !== false)?.key === key)
}

/** Set purpose (the one purpose flow): asks the agent in its own chat to set its name and purpose. */
export function askAgentPurpose(state: TeamTabState, projectId: string, agent: TeamTabAgent) {
  const tab = teamAgentChatTab(state, projectId, agent.key)
  if (tab) setUpAgentPurpose(tab.tabId)
  else useChatStore.getState().addToast(`Open ${agent.name}'s chat, then set its purpose there.`, 'error')
}
