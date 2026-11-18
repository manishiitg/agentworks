import type { PollingEvent } from '../services/api-types'
import { getTypedEventData } from '../generated/event-types'
import { useChatStore } from '../stores/useChatStore'
import { useAuthStore } from '../stores/useAuthStore'
import { activateTab } from './activateTab'
import { CHAT_FOCUS_COMPOSER_EVENT, sendWorkspacePaneMessageToChat } from './workspacePaneChat'
import { ownerShortName } from '../components/chat/chatTabName'

/** The purpose button's request, sent at once: the agent picks its own name and purpose from its work and saves them; the person corrects
 * it afterwards (owner, 17 Nov: "we should ask ai to setup"). */
export const AGENT_PURPOSE_PROMPT = "Set up your name and purpose (sessions action=identity): pick a short name and a one-line purpose that fit your work in this chat, save them, then tell me what you chose so I can change it."

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

/** The Instructions button's request, sent at once: the agent proposes its own standing rules from this chat and its current ones,
 * and saves them only after the person agrees, because they change how it behaves (#3041; owner, 17 Nov). */
export const AGENT_INSTRUCTIONS_PROMPT = "Set up your own standing instructions (set_my_agent_identity instructions). Look at your current ones and at how I work with you in this chat, propose a short set of rules for this agent only, and ask me to confirm or change them before you save."

/** Sends the setup request to the agent's own chat as a normal queued message (it waits behind a running turn and never
 * touches what the person is typing); no form (owner, 3 Nov). */
function sendSetup(tabId: string, prompt: string) {
  void sendWorkspacePaneMessageToChat({ tabId, message: prompt }).catch(error => console.warn('[AgentSetup] could not send the setup request', error))
}

/** Asks the agent to propose its own instructions. */
export function setUpAgentInstructions(tabId: string) { sendSetup(tabId, AGENT_INSTRUCTIONS_PROMPT) }

/** Asks the agent to set its own name and purpose. */
export function setUpAgentPurpose(tabId: string) { sendSetup(tabId, AGENT_PURPOSE_PROMPT) }

/** A settings explanation is only a draft in this exact own writable agent, never a fallback agent. */
export function fillOwnTabDraft(tabId:string,prompt:string):boolean {
 if(!prompt.trim()||!ownWritableTabs().some(tab=>tab.tabId===tabId))return false
 fillDraft(tabId,prompt);return true
}

/** The viewer's own Main tab in this Crew, when it is open here. */
function ownMainTab(projectId: string) {
  return Object.values(useChatStore.getState().chatTabs).find(tab => tab.metadata?.agentProfileProjectId === projectId && tab.metadata?.agentProfileIsMainChat && !tab.metadata?.isViewOnly && tab.metadata?.agentProfileTabCanWrite !== false)
}

/** Each Access/Team card's Ask, the Team kit's Apply among them: sends the server's message to the chat the person has
 * open in this Crew (owner, 18 Nov, replacing "always their Main" of 6 Nov), through the one right-pane send, so it waits
 * behind a running turn and never touches what they are typing. When the open chat is not one of their own agents here,
 * it uses their Main, then their most recent own agent. False when none of their own agents is open in this Crew. */
export function sendToOpenChat(projectId: string, prompt: string): boolean {
  if (!prompt.trim()) return false
  const own = ownWritableTabs(projectId)
  const active = useChatStore.getState().activeTabId
  const target = own.find(tab => tab.tabId === active) || ownMainTab(projectId) || own[0]
  if (!target) return false
  void sendWorkspacePaneMessageToChat({ tabId: target.tabId, message: prompt }).catch(error => console.warn('[TeamAsk] could not send to the open chat', error))
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
