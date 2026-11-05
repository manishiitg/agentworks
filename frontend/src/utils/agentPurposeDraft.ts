import type { PollingEvent } from '../services/api-types'
import { getTypedEventData } from '../generated/event-types'
import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from './workspacePaneChat'
import { ownerShortName } from '../components/chat/chatTabName'

/** The words the purpose button puts in a tab's chat box; the person adds one line and sends it, and the agent saves its name and purpose (owner, 5 Nov: no icons). */
export const AGENT_PURPOSE_PROMPT = "Set this agent's name and purpose: "

function fillDraft(tabId: string, prompt: string) {
  const store = useChatStore.getState()
  const existing = (store.getTabConfig(tabId)?.inputText || '').trim()
  if (!existing.startsWith(prompt.trim())) store.setTabConfig(tabId, { inputText: existing ? `${prompt}\n\n${existing}` : prompt })
  activateTab(tabId)
  window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
}

/** Opens the tab with the purpose request ready in its chat box, cursor at the end (owner, 3 Nov: no form). */
export function fillAgentPurposeDraft(tabId: string) { fillDraft(tabId, AGENT_PURPOSE_PROMPT) }

/** The Team card's Ask (owner, 4 Nov): Main sets the Team up from chat with team_sync and manage_crew_sync. */
export const TEAM_SETUP_PROMPT = 'Help me set up the Team for this Crew: add or remove people, choose the shared folder and who can edit it, and choose which agents are shared and with whom.'

/** Opens this Crew's Main chat with the Team request in its chat box; false when Main's tab is not open. */
export function fillTeamSetupDraft(projectId: string): boolean {
  const main = Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly)
  if (!main) return false
  fillDraft(main.tabId, TEAM_SETUP_PROMPT)
  return true
}

/** "Ask from my agent" on a teammate's agent you are watching (owner, 5 Nov, "A Team is a shared room"): your own
 * Main opens with the request started; you finish it and send it, and your agent reaches theirs. False when your
 * Main's tab is not open. */
export function fillAskFromMyAgentDraft(projectId: string, ownerName: string, agentName: string, watchedKey?: string): boolean {
  const main = Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly && tab.metadata?.agentProfileTabCanWrite !== false)
  if (!main) return false
  const owner = ownerShortName(ownerName) || 'their'
  const address = teammateAgentAddress(watchedKey)
  fillDraft(main.tabId, `Ask ${owner}'s ${agentName} agent${address ? ` (message it at ${address})` : ''}: `)
  return true
}

/** The exact address the server knows for a watched teammate's agent, so the agent need not search (owner, 5 Nov):
 * workspace:work:<serverProject>/agent/<serverChatKey>. The web tab's key is the server key; a Mac app tab carries it
 * after ":team:" (MacSharedAgents openTeamRemoteAgent). */
export function teammateAgentAddress(watchedKey?: string): string {
  const watched = watchedKey || ''
  // In the Mac app the agent is on the server: its id there is team:<this Crew>:<server chat key>, which this Mac's
  // send_message takes (list_agents names it the same way); a workspace address would not resolve on the Mac.
  if (watched.includes(':team:')) {
    const [localProject, serverKey] = watched.split(':team:')
    return localProject && serverKey ? `team:${localProject}:${serverKey}` : ''
  }
  const project = watched.split(':chat:')[0]
  return project && project !== watched ? `workspace:work:${project}/agent/${watched}` : ''
}

/** A finished set_my_agent_identity call (native or over MCP, whose name carries a prefix), or a saved Crew identity
 * (Main's name and purpose are the Crew's): the agents list is stale. */
export function agentIdentitySaved(event: PollingEvent): boolean {
  if ((getTypedEventData(event, 'product_interaction')?.kind || '').includes('identity')) return true
  const name = (getTypedEventData(event, 'tool_call_end') ?? getTypedEventData(event, 'tool_execution'))?.tool_name || ''
  return name === 'set_my_agent_identity' || name.endsWith('__set_my_agent_identity') || name.endsWith('.set_my_agent_identity')
}
