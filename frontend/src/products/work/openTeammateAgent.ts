import { openAppProduct } from '../../utils/appNavigation'
import { productRoutePath } from '../../utils/productRoutes'
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

/** A server-admitted Team Main is an ordinary chat in the Team's online Crew. */
export function openTeamAgentHome(home: string, connection?: string, activeConnection?: string) {
 const route={surface:'work' as const,id:home,agent:'main'}
 if(connection && connection!==activeConnection) return window.electronAPI?.openDesktopConnection?.(connection,productRoutePath(route))
 openAppProduct(route)
}
