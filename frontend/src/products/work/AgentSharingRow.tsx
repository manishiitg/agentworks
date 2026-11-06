import { useChatStore } from '../../stores/useChatStore'
import { fillAgentPurposeDraft } from '../../utils/agentPurposeDraft'
import { teamAgentForTab, type TeamTabAgent, type TeamTabState } from './teamApi'

/** This agent's own chat tab in this app (its person's), found by the Team tab's key; undefined when it is not open. */
export function teamAgentChatTab(state: TeamTabState, projectId: string, key: string) {
  return Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && !item.metadata?.isViewOnly
    && teamAgentForTab(state, item.metadata?.agentProfileConversationKey, item.metadata?.agentProfileIsMainChat !== false, item.metadata?.agentProfileTabCanWrite !== false)?.key === key)
}

/** Set purpose (the one purpose flow): fills the agent's own chat with the purpose question. */
export function askAgentPurpose(state: TeamTabState, projectId: string, agent: TeamTabAgent) {
  const tab = teamAgentChatTab(state, projectId, agent.key)
  if (tab) fillAgentPurposeDraft(tab.tabId)
  else useChatStore.getState().addToast(`Open ${agent.name}'s chat, then set its purpose there.`, 'error')
}

/** One agent row's purpose line (owner, 5 Nov, approved mockup "Agent Sharing List"): the server writes it and says
 * whether the viewer may ask the agent's chat to set it. Public on AgentBridge is the Mac online tab's (owner, 6 Nov). */
export function AgentSharingRow({ agent, state, projectId }: { agent: TeamTabAgent; state: TeamTabState; projectId: string }) {
  if (!agent.purpose && !agent.can_set_purpose) return null
  return <p className="ml-0.5 mt-0.5 text-[12.5px] text-muted-foreground">
    {agent.purpose || 'No purpose yet.'}
    {agent.can_set_purpose && <> <button type="button" className="text-primary hover:underline" onClick={() => askAgentPurpose(state, projectId, agent)}>{agent.purpose ? 'Change purpose' : 'Set purpose'}</button></>}
  </p>
}
