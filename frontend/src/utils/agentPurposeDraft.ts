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

/** A finished set_my_agent_identity call (native or over MCP, whose name carries a prefix): the agents list is stale. */
export function agentIdentitySaved(event: PollingEvent): boolean {
  const name = (getTypedEventData(event, 'tool_call_end') ?? getTypedEventData(event, 'tool_execution'))?.tool_name || ''
  return name === 'set_my_agent_identity' || name.endsWith('__set_my_agent_identity') || name.endsWith('.set_my_agent_identity')
}
