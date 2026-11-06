import { useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { fillAgentPurposeDraft } from '../../utils/agentPurposeDraft'
import { teamAgentForTab, type TeamTabAgent, type TeamTabState } from './teamApi'

/** This agent's own chat tab in this app (its person's), found by the Team tab's key; undefined when it is not open. */
export function teamAgentChatTab(state: TeamTabState, projectId: string, key: string) {
  return Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && !item.metadata?.isViewOnly
    && teamAgentForTab(state, item.metadata?.agentProfileConversationKey, item.metadata?.agentProfileIsMainChat !== false, item.metadata?.agentProfileTabCanWrite !== false)?.key === key)
}

/** One agent row's purpose line (owner, 5 Nov, approved mockup "Agent Sharing List"): the server writes it and says
 * whether the viewer may ask the agent's chat to set it. Public on AgentBridge is the Mac online tab's (owner, 6 Nov). */
export function AgentSharingRow({ agent, state, projectId }: { agent: TeamTabAgent; state: TeamTabState; projectId: string }) {
  const askPurpose = () => {
    const tab = teamAgentChatTab(state, projectId, agent.key)
    if (tab) fillAgentPurposeDraft(tab.tabId)
    else useChatStore.getState().addToast(`Open ${agent.name}'s chat, then set its purpose there.`, 'error')
  }
  if (!agent.purpose && !agent.can_set_purpose) return null
  return <p className="ml-0.5 mt-0.5 text-[12.5px] text-muted-foreground">
    {agent.purpose || 'No purpose yet.'}
    {agent.can_set_purpose && <> <button type="button" className="text-primary hover:underline" onClick={askPurpose}>{agent.purpose ? 'Change purpose' : 'Set purpose'}</button></>}
  </p>
}

/** One line anyone's coding app pastes to reach this Crew's agents through AgentWorks (it works from any computer). */
export function CopySetupLine({ crewName }: { crewName: string }) {
  const [copied, setCopied] = useState(false)
  const [fallback, setFallback] = useState('')
  const line = `Connect to AgentWorks, then ask the ${JSON.stringify(crewName)} Crew's agents to help me.`
  const copy = async () => {
    try { await navigator.clipboard.writeText(line); setCopied(true); window.setTimeout(() => setCopied(false), 1500) }
    catch { setFallback(line) }
  }
  return <div className="flex flex-wrap items-center gap-2 border-t border-border pt-2 text-xs">
    <button type="button" className="rounded-full border border-border px-2.5 py-0.5 hover:bg-muted" onClick={() => void copy()}>{copied ? 'Copied' : 'Copy setup line'}</button>
    <span className="text-muted-foreground">for your coding app to reach these agents</span>
    {fallback && <p className="w-full text-muted-foreground">Copy this into your coding app: {fallback}</p>}
  </div>
}
