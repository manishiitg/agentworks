import { useChatStore } from '../../stores/useChatStore'
import { useCrewAgentsStore } from '../../components/chat/AgentCard'
import { openAgentTab } from '../../utils/openAgentTab'

/** Reuse the existing observed teammate tab, never a maker's writable tab or Main draft. */
export function openTeammateAgent(projectId: string, key: string, owner?: string) {
  const tab = Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId
    && item.metadata.agentProfileTabCanWrite === false && (!owner || item.metadata.agentProfileTabOwnerID === owner)
    && (item.metadata.agentProfileConversationKey === key || item.metadata.agentProfileConversationKey?.endsWith(`:team:${key}`)))
  if (tab) { openAgentTab(tab.tabId); return }
  const agents = useCrewAgentsStore.getState()
  const closed = agents.projectId === projectId && agents.closed.find(agent => agent.key === key)
  if (closed) { void closed.open(); return }
  window.dispatchEvent(new CustomEvent('agentworks:open-team-agent', { detail: { projectId, key, owner } }))
}
