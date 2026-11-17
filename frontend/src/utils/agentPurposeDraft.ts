import type { PollingEvent } from '../services/api-types'
import { getTypedEventData } from '../generated/event-types'
import { useChatStore } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'
import { activateTab } from './activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT } from './workspacePaneChat'
import { ownerShortName } from '../components/chat/chatTabName'

/** The words the purpose button puts in a tab's chat box; the person adds one line and sends it, and the agent saves its name and purpose (owner, 5 Nov: no icons). */
export const AGENT_PURPOSE_PROMPT = "Set your name and purpose (sessions action=identity). If I add nothing here, suggest them from your work in this chat and save them: "

function fillDraft(tabId: string, prompt: string) {
  const store = useChatStore.getState()
  const existing = store.getTabConfig(tabId)?.inputText || ''
  // Other actions intentionally arm autoRun. A draft must disarm it even when
  // the same starter is already present, or ChatInput may submit it on open.
  const inputText = existing.trim().startsWith(prompt.trim()) ? existing : existing.trim() ? `${prompt}\n\n${existing}` : prompt
  store.setTabConfig(tabId, { inputText, autoRun: false })
  activateTab(tabId)
  window.setTimeout(() => window.dispatchEvent(new CustomEvent(CHAT_FOCUS_COMPOSER_EVENT)), 50)
}

/** The words the Instructions button puts in a tab's chat box: the person writes the agent's own standing rules below and
 * sends them; the agent saves them itself (#3041, same as the purpose: no form). */
export const AGENT_INSTRUCTIONS_PROMPT = "Save your own standing instructions (set_my_agent_identity instructions), exactly as I write them below. If I write nothing, show me your current ones and ask what to change: "

/** Opens the tab with the instructions request ready in its chat box. */
export function fillAgentInstructionsDraft(tabId: string) { fillDraft(tabId, AGENT_INSTRUCTIONS_PROMPT) }

/** Opens the tab with the purpose request ready in its chat box, cursor at the end (owner, 3 Nov: no form). */
export function fillAgentPurposeDraft(tabId: string) { fillDraft(tabId, AGENT_PURPOSE_PROMPT) }

/** A settings explanation is only a draft in this exact own writable agent, never a fallback agent. */
export function fillOwnTabDraft(tabId:string,prompt:string):boolean {
 if(!prompt.trim()||!ownWritableTabs().some(tab=>tab.tabId===tabId))return false
 fillDraft(tabId,prompt);return true
}

/** The viewer's own Main tab in this Crew, when it is open here. */
function ownMainTab(projectId: string) {
  return Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly && tab.metadata?.agentProfileTabCanWrite !== false)
}

/** Each Access/Team card's Ask (owner, 6 Nov): sends the server's message (the /tab reply's asks, or an agent's
 * permissions_prompt) to the viewer's own Main, which looks first and changes only when the person says so. False when
 * their Main's tab is not open. */
export function sendToMain(projectId: string, prompt: string): boolean {
  const main = ownMainTab(projectId)
  if (!main || !prompt.trim()) return false
  useChatStore.getState().setTabConfig(main.tabId, { inputText: prompt, autoRun: true })
  activateTab(main.tabId)
  return true
}

/** An agent card's Automations… (owner, 6 Nov): sends the server's message to that agent's own chat, the same way the
 * Team cards' Ask buttons send theirs. */
export function sendToTab(tabId: string, prompt: string): boolean {
  if (!prompt.trim() || !useChatStore.getState().chatTabs[tabId]) return false
  useChatStore.getState().setTabConfig(tabId, { inputText: prompt, autoRun: true })
  activateTab(tabId)
  return true
}

/** The viewer's own Main opens with the words in its chat box, not sent (the Share tree's Ask, written by the server).
 * False when their Main's tab is not open. */
export function fillMainDraft(projectId: string, prompt: string): boolean {
  const main = ownMainTab(projectId) || ownWritableTabs(projectId)[0]
  if (!main) return false
  fillDraft(main.tabId, prompt)
  return true
}

/** The person's own writable agent tabs in this Crew, most recently used first (never a teammate's or watched tab). */
function ownWritableTabs(projectId?: string) {
  const viewer = useAuthStore.getState().user?.id
  if (!viewer) return []
  return Object.values(useChatStore.getState().chatTabs).filter(tab => {
    const meta = tab.metadata
    // agentProfileBuilder marks the setup editor; ordinary own chats carry false (WorkSurface).
    return Boolean(meta?.agentProfileProjectId) && (projectId ? meta?.agentProfileProjectId === projectId : meta?.agentProfileId === 'work') && !meta?.isViewOnly && meta?.agentProfileTabCanWrite !== false && meta?.agentProfileBuilder !== true
      && !meta?.isScheduledRun && !meta?.isBotRun && !meta?.isExecutionRun
      && (!meta?.agentProfileTabOwnerID || meta.agentProfileTabOwnerID === viewer)
  }).sort((a,b) => (b.lastAccessedAt || b.createdAt || 0) - (a.lastAccessedAt || a.createdAt || 0))
}

/** Global personal settings reuse an existing own chat; never send or create one. */
export function fillOwnAgentDraft(prompt: string): boolean {
  if (!prompt.trim()) return false
  const own = ownWritableTabs()
  const active = useChatStore.getState().activeTabId
  const target = own.find(tab => tab.tabId === active) || own[0]
  if (!target) return false
  fillDraft(target.tabId, prompt)
  return true
}

/** Use the existing agent-mediated Ask until a dedicated recipient chat exists.
 * Main is optional: without an open Main, reuse the person's most recently
 * accessed writable agent in this Crew. Never use a teammate's or watched tab.
 * Opening only drafts the exact recipient; sending remains the person's choice. */
export function fillAskFromMyAgentDraft(projectId: string, ownerName: string, agentName: string, address?: string, question = ''): boolean {
  const own = ownWritableTabs(projectId)
  const sender = own.find(tab => tab.metadata?.agentProfileIsMainChat) || own[0]
  if (!sender) return false
  const owner = ownerShortName(ownerName) || 'their'
  const recipient = ownerName === 'you' ? `your ${agentName}` : `${owner}'s ${agentName}`
  fillDraft(sender.tabId, `Ask ${recipient} agent${address ? ` (use messages action=send, ask=true, target=${address}; not a direct send)` : ''}: ${question}`)
  return true
}

/** A finished set_my_agent_identity call (native or over MCP, whose name carries a prefix), or a saved Crew identity
 * (Main's name and purpose are the Crew's): the agents list is stale. */
export function agentIdentitySaved(event: PollingEvent): boolean {
  if ((getTypedEventData(event, 'product_interaction')?.kind || '').includes('identity')) return true
  const name = (getTypedEventData(event, 'tool_call_end') ?? getTypedEventData(event, 'tool_execution'))?.tool_name || ''
  return name === 'set_my_agent_identity' || name.endsWith('__set_my_agent_identity') || name.endsWith('.set_my_agent_identity')
}
