import type { ChatTab } from '../../stores/useChatStore'
import { useChatStore } from '../../stores/useChatStore'
import { isProjectProductId } from './projectProduct'

// Retire only browser projections. Closing a normal tab would cancel its shared
// session; its events and backend conversation must stay intact (PLAT-746-224).
export function retireWorkActivityCopies(tabId: string): void {
  useChatStore.setState(state => {
    const target = state.chatTabs[tabId]
    const projectId = target?.metadata?.agentProfileProjectId
    if (!target?.sessionId || !projectId || target.metadata?.isViewOnly) return state
    const duplicates = Object.values(state.chatTabs).filter(tab =>
      tab.tabId !== tabId && tab.sessionId === target.sessionId && tab.metadata?.isViewOnly === true &&
      belongsToWorkProject(tab, projectId),
    )
    if (!duplicates.length) return state
    const chatTabs = { ...state.chatTabs }
    for (const tab of duplicates) delete chatTabs[tab.tabId]
    return { chatTabs, activeTabId: duplicates.some(tab => tab.tabId === state.activeTabId) ? tabId : state.activeTabId }
  })
}

export type WorkRuntimeSelection = {
  connectionId?: string
  engine: string
  provider?: string
  modelId: string
  reasoningEffort?: string
}

export type ProductEngineSelectionDetail = Partial<WorkRuntimeSelection> & {
  profileId?: string
  tabId?: string
}

export type WorkChatOrder = {
  isMainChat?: boolean
  position?: number
  conversationKey: string
}

/** Render the server's main role and saved order, independent of chat activity. */
export function compareWorkChatOrder(a: WorkChatOrder, b: WorkChatOrder): number {
  const position = (row: WorkChatOrder) => row.position && row.position > 0 ? row.position : Number.MAX_SAFE_INTEGER
  return Number(!!b.isMainChat) - Number(!!a.isMainChat) ||
    position(a) - position(b) ||
    (a.conversationKey === b.conversationKey ? 0 : a.conversationKey < b.conversationKey ? -1 : 1)
}

export function workChatTabOrder(tab: ChatTab): WorkChatOrder {
  return {
    isMainChat: tab.metadata?.agentProfileIsMainChat,
    position: tab.metadata?.agentProfileTabPosition,
    conversationKey: tab.metadata?.agentProfileConversationKey || tab.sessionId || tab.tabId,
  }
}

export function belongsToWorkProject(tab: ChatTab, projectId: string): boolean {
  // Crew and Code project ids are UUIDs, so one check serves both products.
  return Boolean(isProjectProductId(tab.metadata?.agentProfileId) && (
    tab.metadata.agentProfileProjectId === projectId ||
    tab.metadata.agentProfileConversationKey === projectId ||
    tab.metadata.agentProfileConversationKey?.startsWith(`${projectId}:`)
  ))
}

// Code side chats (PLAT-571): extra full Builder chats in one project, like
// running several agents on one repository locally. Each has its own server
// conversation keyed `<projectId>:chat:<id>`; the primary chat (key = projectId)
// keeps every channel, MCP call, schedule, trigger and Pulse message.
// The primary chat and up to twenty side chats per project (Code and Crew). The server enforces the same
// number (externalCrewMaxSideChats in agent_go/cmd/server/external_crew_chats.go); change both together.
export const WORK_SIDE_CHAT_LIMIT = 20

export function workSideChatKey(projectId: string, id: string): string {
  return `${projectId}:chat:${id}`
}

/** The owner's "as a user" chat: the server runs it in Run mode because its id starts with `asuser-` (crew_access.go). */
export const USER_VIEW_CHAT_PREFIX = 'asuser-'
export function workUserViewChatKey(projectId: string, id: string): string {
  return workSideChatKey(projectId, `${USER_VIEW_CHAT_PREFIX}${id}`)
}

export function isWorkSideChatTab(tab: ChatTab | undefined, projectId?: string): boolean {
  const key = tab?.metadata?.agentProfileConversationKey
  if (!tab || !key || tab.metadata?.agentProfileIsMainChat || tab.metadata?.isViewOnly === true || tab.metadata?.agentProfileBuilder === true) return false
  return projectId ? key.startsWith(`${projectId}:chat:`) : /:chat:[^:]+$/.test(key)
}

/**
 * Preparing a project's primary chat reuses its tab, which makes that tab
 * active. When the user had picked another chat of the same project (a side
 * chat from Ctrl+K while in another product), that choice must survive.
 */
export function workTabToKeepActive(
  tabs: Record<string, ChatTab>,
  previousActiveTabId: string | null | undefined,
  projectId: string,
  canonicalTabId: string,
): string | undefined {
  const previous = previousActiveTabId ? tabs[previousActiveTabId] : undefined
  if (!previous || previous.tabId === canonicalTabId || !belongsToWorkProject(previous, projectId)) return undefined
  return previous.tabId
}

export type WorkChatTabShortcut = { kind: 'tab'; index: number } | { kind: 'new' } | { kind: 'adjacent'; direction: -1 | 1 }

/**
 * Code chat tabs (PLAT-571): Cmd/Ctrl+1..5 pick a chat (1 = the primary), the
 * same modifier as Cmd/Ctrl+K (owner 2026-10-07). In a browser tab, Chrome
 * keeps Cmd/Ctrl+1..8 for its own tabs, so Alt+1..5 stays as the fallback.
 * Alt+Shift+T opens a new chat (Cmd/Ctrl+Shift+T reopens a browser tab).
 * Cmd/Ctrl+Left/Right moves between visible chats, including from the composer.
 * The physical key is read, because Option changes the character on a Mac.
 */
export function workChatTabShortcut(event: Pick<KeyboardEvent, 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'code' | 'key'>): WorkChatTabShortcut | null {
  const code = event.code || ''
  const digit = /^Digit([1-5])$/.exec(code)?.[1] ?? (/^[1-5]$/.test(event.key) ? event.key : undefined)
  const command = (event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey
  if (command && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) return { kind: 'adjacent', direction: event.key === 'ArrowLeft' ? -1 : 1 }
  if (command) return digit ? { kind: 'tab', index: Number(digit) - 1 } : null
  if (!event.altKey || event.ctrlKey || event.metaKey) return null
  if (event.shiftKey) return code === 'KeyT' || event.key.toLowerCase() === 't' ? { kind: 'new' } : null
  return digit ? { kind: 'tab', index: Number(digit) - 1 } : null
}

/** Command arrows switch chats from the composer, but remain editing keys elsewhere. */
export function chatTabArrowTargetAllowed(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return true
  if (target.closest('[data-chat-composer]')) return true
  return !target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],.monaco-editor,.cm-editor,.xterm')
}

/** The chat-switch keys that work here: the browser keeps Cmd/Ctrl+1..5. */
export function chatTabSwitchKeys(): { mac: string; other: string } {
  const desktop = typeof window !== 'undefined' && Boolean(window.electronAPI)
  return desktop ? { mac: '⌘1–5', other: 'Ctrl+1–5' } : { mac: '⌥1–5', other: 'Alt+1–5' }
}

/** Find the local projection of the server-owned conversation for this project. */
export function findCanonicalWorkProjectTab(
  tabs: Record<string, ChatTab>,
  projectId: string,
  canonicalSessionId: string,
): ChatTab | undefined {
  return Object.values(tabs).find(tab =>
    belongsToWorkProject(tab, projectId) &&
    tab.metadata?.isViewOnly !== true &&
    tab.sessionId === canonicalSessionId)
}

/** Relaunch the one persistent project conversation when durable context changes. */
export function markWorkProjectRuntimeDirty(projectId: string): void {
  const store = useChatStore.getState()
  for (const tab of Object.values(store.chatTabs)) {
    if (!belongsToWorkProject(tab, projectId)) continue
    store.setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: Boolean(tab.sessionId) })
  }
}

/** Change native coding-agent runtime while retaining the platform conversation. */
export function setWorkProjectRuntimeSelection(
  projectId: string,
  _sourceTabId: string,
  selection: WorkRuntimeSelection,
): void {
  const store = useChatStore.getState()
  for (const tab of Object.values(store.chatTabs)) {
    if (!belongsToWorkProject(tab, projectId) || tab.metadata?.agentProfileChatModelOverride || tab.metadata?.agentProfileConversationKey?.startsWith(`${projectId}:chat:agent-`)) continue
    store.setTabMetadata(tab.tabId, {
      agentProfileEngine: selection.engine,
      agentProfileConnectionID: selection.connectionId,
      agentProfileModelID: selection.modelId,
      agentProfileReasoningEffort: selection.reasoningEffort,
      agentProfileChatModelLabel: undefined,
      agentProfileRuntimeDirty: Boolean(tab.sessionId),
    })
  }
}

/** Apply a model choice emitted by Crew's scoped composer to its project. */
export function applyWorkProjectRuntimeSelection(
  projectId: string,
  fallbackTabId: string | null,
  detail: ProductEngineSelectionDetail | undefined,
): boolean {
  if (!isProjectProductId(detail?.profileId) || !detail.engine || !detail.modelId) return false
  const sourceTabId = detail.tabId || fallbackTabId
  if (!sourceTabId) return false
  const sourceTab = useChatStore.getState().chatTabs[sourceTabId]
  if (!sourceTab || !belongsToWorkProject(sourceTab, projectId)) return false

  setWorkProjectRuntimeSelection(projectId, sourceTabId, {
    engine: detail.engine,
    provider: detail.provider,
    connectionId: detail.connectionId,
    modelId: detail.modelId,
    reasoningEffort: detail.reasoningEffort,
  })
  return true
}
