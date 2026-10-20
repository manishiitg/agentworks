import { agentApi } from '../services/api'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { belongsToWorkProject, isWorkSideChatTab } from '../products/work/workTabs'
import { activateTab } from './activateTab'
import { captureChatIdentity, isChatIdentityCurrent } from './chatIdentity'

/** Preserve the permanent chat and the product's existing close rules. */
export function canCloseWorkspaceChatTab(tab: ChatTab, tabs: Record<string, ChatTab>): boolean {
  const meta = tab.metadata
  if (meta?.mode === 'workflow') {
    if (meta.isViewOnly !== true && meta.phaseId === 'workflow-builder') return false
    return (
      Object.values(tabs).filter(
        (item) =>
          item.metadata?.mode === 'workflow' && item.metadata?.presetQueryId === meta.presetQueryId
      ).length > 1
    )
  }
  if (meta?.agentProfileId === 'work' || meta?.agentProfileId === 'work') {
    return meta.isViewOnly === true || isWorkSideChatTab(tab, meta.agentProfileProjectId)
  }
  return false
}

/** Shared by the project tab strip and cross-product sidebar. */
export async function closeWorkspaceChatTab(tabId: string, canonicalTabId?: string): Promise<void> {
  const identity = captureChatIdentity()
  const state = useChatStore.getState()
  const tab = state.chatTabs[tabId]
  if (!tab || !canCloseWorkspaceChatTab(tab, state.chatTabs)) return
  const meta = tab.metadata
  const projectId = meta?.agentProfileProjectId
  const side = isWorkSideChatTab(tab, projectId)
  const fallback =
    canonicalTabId ||
    Object.values(state.chatTabs)
      .filter(
        (item) =>
          item.tabId !== tabId &&
          (projectId
            ? belongsToWorkProject(item, projectId) &&
              item.metadata?.agentProfileId === meta?.agentProfileId
            : item.metadata?.mode === 'workflow' &&
              item.metadata?.presetQueryId === meta?.presetQueryId)
      )
      .sort((a, b) => {
        const primary = (item: ChatTab) =>
          item.metadata?.phaseId === 'workflow-builder' ||
          (!!projectId && item.metadata?.agentProfileConversationKey === projectId)
        return Number(primary(b)) - Number(primary(a)) || a.createdAt - b.createdAt
      })[0]?.tabId
  const stillCurrent = () =>
    isChatIdentityCurrent(identity) &&
    useChatStore.getState().chatTabs[tabId]?.sessionId === tab.sessionId
  // The server decides whether a physical chat can close and releases idle
  // runtime resources. Never stop a running agent as a side effect of Close.
  if (side && meta?.agentProfileConversationKey && meta.agentProfileId) {
    try {
      await agentApi.closeAgentProfileSideChat(meta.agentProfileId, meta.agentProfileConversationKey)
    } catch (cause) {
      if (stillCurrent()) {
        const data = (cause as { response?: { data?: unknown } })?.response?.data
        const reason = typeof data === 'string' ? data : data && typeof data === 'object' && 'error' in data ? data.error : undefined
        useChatStore.getState().addToast(typeof reason === 'string' && reason.trim() ? reason : 'Unable to close this agent. Try again.', 'error')
      }
      return
    }
  }
  if (!stillCurrent()) return
  const selected = useChatStore.getState().activeTabId === tabId
  const surface = useProductSurfaceStore.getState().productSurface
  await useChatStore
    .getState()
    .closeTab(tabId, meta?.mode === 'workflow' && meta.isViewOnly !== true)
  if (
    selected &&
    fallback &&
    isChatIdentityCurrent(identity) &&
    useProductSurfaceStore.getState().productSurface === surface
  )
    activateTab(fallback)
}
