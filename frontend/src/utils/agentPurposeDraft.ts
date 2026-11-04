import { useChatStore } from '../stores/useChatStore'
import { activateTab } from './activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from './workspacePaneChat'

/** The words the purpose icon puts in a tab's chat box; the person adds one line and sends it, and the agent saves its name, purpose and icon (owner, 4 Nov: sharing needs all three). */
export const AGENT_PURPOSE_PROMPT = "Set this agent's name, purpose and an emoji icon that fits it: "

/** Opens the tab with the purpose request ready in its chat box, cursor at the end (owner, 3 Nov: no form). */
export function fillAgentPurposeDraft(tabId: string) {
  const store = useChatStore.getState()
  const existing = (store.getTabConfig(tabId)?.inputText || '').trim()
  if (!existing.startsWith(AGENT_PURPOSE_PROMPT.trim())) store.setTabConfig(tabId, { inputText: existing ? `${AGENT_PURPOSE_PROMPT}\n\n${existing}` : AGENT_PURPOSE_PROMPT })
  activateTab(tabId)
  window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
}
