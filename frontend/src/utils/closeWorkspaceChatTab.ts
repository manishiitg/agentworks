import { agentApi } from '../services/api'
import { useChatStore, type ChatTab } from '../stores/useChatStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { belongsToWorkProject, isWorkSideChatTab } from '../products/work/workTabs'
import { sessionRuntimeStatus, runtimeHasBackgroundAgents } from './runtimeActivity'
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
  if (meta?.agentProfileId === 'work' || meta?.agentProfileId === 'code') {
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
  const runtime = state.activeSessionsCache?.find((item) => item.session_id === tab.sessionId)
  if (
    side &&
    (tab.isStreaming ||
      tab.hasRunningBgAgents ||
      (runtime &&
        (sessionRuntimeStatus(runtime) === 'busy' || runtimeHasBackgroundAgents(runtime))))
  ) {
    state.addToast('This chat is still working. Stop it first, then close the tab.', 'error')
    return
  }
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
  const ignoreMissing = async (operation: Promise<unknown>) => {
    try {
      await operation
    } catch (cause) {
      if ((cause as { response?: { status?: number } })?.response?.status !== 404) throw cause
    }
  }
  // Idle side chats retain warm runtime resources and a server-owned chat key.
  if (side && tab.sessionId) await ignoreMissing(agentApi.stopSession(tab.sessionId))
  if (!stillCurrent()) return
  if (side && meta?.agentProfileConversationKey && meta.agentProfileId) {
    await ignoreMissing(
      agentApi.closeAgentProfileSideChat(meta.agentProfileId, meta.agentProfileConversationKey)
    )
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
