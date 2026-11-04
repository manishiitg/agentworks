import type { PollingEvent } from '../services/api-types'
import { getTypedEventData } from '../generated/event-types'
import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from './workspacePaneChat'

/** The words the purpose icon puts in a tab's chat box; the person adds one line and sends it, and the agent saves its name, purpose and icon (owner, 4 Nov: sharing needs all three). */
export const AGENT_PURPOSE_PROMPT = "Set this agent's name, purpose and an emoji icon that fits it: "

export const AGENT_ICON_PROMPT = 'Pick an emoji icon that fits this agent and save it: '

function fillDraft(tabId: string, prompt: string) {
  const store = useChatStore.getState()
  const existing = (store.getTabConfig(tabId)?.inputText || '').trim()
  if (!existing.startsWith(prompt.trim())) store.setTabConfig(tabId, { inputText: existing ? `${prompt}\n\n${existing}` : prompt })
  activateTab(tabId)
  window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
}

/** Opens the tab with the purpose request ready in its chat box, cursor at the end (owner, 3 Nov: no form). */
export function fillAgentPurposeDraft(tabId: string) { fillDraft(tabId, AGENT_PURPOSE_PROMPT) }

/** The same for the agent's icon alone. */
export function fillAgentIconDraft(tabId: string) { fillDraft(tabId, AGENT_ICON_PROMPT) }

/** The Team card's Ask (owner, 4 Nov): Main sets the Team up from chat with team_sync and manage_crew_sync. */
export const TEAM_SETUP_PROMPT = 'Help me set up the Team for this Crew: add or remove people, choose the shared folder and who can edit it, and choose which agents are shared and with whom.'

/** Opens this Crew's Main chat with the Team request in its chat box; false when Main's tab is not open. */
export function fillTeamSetupDraft(projectId: string): boolean {
  const main = Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly)
  if (!main) return false
  fillDraft(main.tabId, TEAM_SETUP_PROMPT)
  return true
}

/** A finished set_my_agent_identity call (native or over MCP, whose name carries a prefix), or a saved Crew identity
 * (Main's name, purpose and icon are the Crew's): the agents list is stale. */
export function agentIdentitySaved(event: PollingEvent): boolean {
  if ((getTypedEventData(event, 'product_interaction')?.kind || '').includes('identity')) return true
  const name = (getTypedEventData(event, 'tool_call_end') ?? getTypedEventData(event, 'tool_execution'))?.tool_name || ''
  return name === 'set_my_agent_identity' || name.endsWith('__set_my_agent_identity') || name.endsWith('.set_my_agent_identity')
}
