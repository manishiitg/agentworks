import type { PollingEvent } from '../services/api-types'
import { getTypedEventData } from '../generated/event-types'
import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from './workspacePaneChat'
import { ownerShortName } from '../components/chat/chatTabName'

/** The words the purpose button puts in a tab's chat box; the person adds one line and sends it, and the agent saves its name and purpose (owner, 5 Nov: no icons). */
export const AGENT_PURPOSE_PROMPT = "Set your name and purpose (sessions action=identity). If I add nothing here, suggest them from your work in this chat and save them: "

function fillDraft(tabId: string, prompt: string) {
  const store = useChatStore.getState()
  const existing = (store.getTabConfig(tabId)?.inputText || '').trim()
  if (!existing.startsWith(prompt.trim())) store.setTabConfig(tabId, { inputText: existing ? `${prompt}\n\n${existing}` : prompt })
  activateTab(tabId)
  window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
}

/** Opens the tab with the purpose request ready in its chat box, cursor at the end (owner, 3 Nov: no form). */
export function fillAgentPurposeDraft(tabId: string) { fillDraft(tabId, AGENT_PURPOSE_PROMPT) }

/** Each Access/Team card's Ask (owner, 5 Nov): the viewer's own Main sets it up from chat (team_sync, manage_crew_sync);
 * the question says what the person wants, so the agent proceeds. */
export const ACCESS_ASK_PROMPTS = {
  team: "Help me set up who's in this Crew's Team.",
  shares: 'Help me set up what this Crew\'s Team shares.',
  agents: 'Help me choose which of my agents are in this Crew\'s Team.',
  lent: 'Help me share my apps and secrets with this Crew\'s Team.',
  sync: 'Help me check this Crew\'s Team sync and fix any clashes.',
  mac: 'Help me set up Mac online for this Crew.',
  publish: 'Help me publish this Crew to the server.',
} as const
export type AccessAskCard = keyof typeof ACCESS_ASK_PROMPTS

/** Opens this Crew's Main chat with the Team request in its chat box; false when Main's tab is not open. */
export function fillAccessAskDraft(projectId: string, card: AccessAskCard): boolean {
  const main = Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly && tab.metadata?.agentProfileTabCanWrite !== false)
  if (!main) return false
  fillDraft(main.tabId, ACCESS_ASK_PROMPTS[card])
  return true
}

/** "Ask from my agent" on a teammate's agent you are watching (owner, 5 Nov, "A Team is a shared room"): your own
 * Main opens with the request started; you finish it and send it, and your agent reaches theirs at the address the
 * server gave it (the Team agents list). False when your Main's tab is not open. */
export function fillAskFromMyAgentDraft(projectId: string, ownerName: string, agentName: string, address?: string): boolean {
  const main = Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly && tab.metadata?.agentProfileTabCanWrite !== false)
  if (!main) return false
  const owner = ownerShortName(ownerName) || 'their'
  fillDraft(main.tabId, `Ask ${owner}'s ${agentName} agent${address ? ` (message it at ${address})` : ''}: `)
  return true
}

/** A finished set_my_agent_identity call (native or over MCP, whose name carries a prefix), or a saved Crew identity
 * (Main's name and purpose are the Crew's): the agents list is stale. */
export function agentIdentitySaved(event: PollingEvent): boolean {
  if ((getTypedEventData(event, 'product_interaction')?.kind || '').includes('identity')) return true
  const name = (getTypedEventData(event, 'tool_call_end') ?? getTypedEventData(event, 'tool_execution'))?.tool_name || ''
  return name === 'set_my_agent_identity' || name.endsWith('__set_my_agent_identity') || name.endsWith('.set_my_agent_identity')
}
