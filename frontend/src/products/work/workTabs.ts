import type { ChatTab } from '../../stores/useChatStore'
import { useChatStore } from '../../stores/useChatStore'
import { isProjectProductId } from './projectProduct'

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
export const WORK_SIDE_CHAT_LIMIT = 3

export function workSideChatKey(projectId: string, id: string): string {
  return `${projectId}:chat:${id}`
}

export function isWorkSideChatTab(tab: ChatTab | undefined, projectId?: string): boolean {
  const key = tab?.metadata?.agentProfileConversationKey
  if (!tab || !key || tab.metadata?.isViewOnly === true || tab.metadata?.agentProfileBuilder === true) return false
  return projectId ? key.startsWith(`${projectId}:chat:`) : /:chat:[^:]+$/.test(key)
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
    if (!belongsToWorkProject(tab, projectId)) continue
    store.setTabMetadata(tab.tabId, {
      agentProfileEngine: selection.engine,
      agentProfileConnectionID: selection.connectionId,
      agentProfileModelID: selection.modelId,
      agentProfileReasoningEffort: selection.reasoningEffort,
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
