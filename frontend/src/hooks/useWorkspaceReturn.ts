import { useEffect, useRef } from 'react'
import { saveWorkspaceStateOnExit, workspaceStateApi, type WorkspaceDestination, type WorkspaceLayout } from '../services/workspaceState'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import type { ProductSurface } from '../products/productSurfaceConfig'

// One writer per page, serialized: a slow save cannot arrive after a newer one.
let pending: { account: string; workspace?: string; destination: WorkspaceDestination } | null = null
let timer: ReturnType<typeof setTimeout> | undefined
let writing = false
export async function flushWorkspaceState() {
  if (writing) return
  const current = useWorkspaceReturnStore.getState()
  if (!pending || !current.ready || !current.record || !current.account) return
  if (pending.account !== current.account || pending.workspace !== current.workspace || (current.workspace !== undefined && current.workspace !== useWorkspaceConnectionStore.getState().activeWorkspaceId)) { pending = null; return }
  const destination = pending.destination; pending = null; writing = true
  const account = current.account
  const workspace = current.workspace
  try {
    const record = await workspaceStateApi.save({ ...current.record, last: destination })
    if (useWorkspaceReturnStore.getState().account === account && useWorkspaceReturnStore.getState().workspace === workspace) useWorkspaceReturnStore.setState({ record, saveError: '' })
  } catch (error) {
    if (useWorkspaceReturnStore.getState().account !== account || useWorkspaceReturnStore.getState().workspace !== workspace) return
    if ((error as { response?: { status?: number } }).response?.status === 409) {
      const record = await workspaceStateApi.get().catch(() => null)
      if (record && useWorkspaceReturnStore.getState().account === account && useWorkspaceReturnStore.getState().workspace === workspace) useWorkspaceReturnStore.setState({ record })
    }
    if (useWorkspaceReturnStore.getState().account !== account || useWorkspaceReturnStore.getState().workspace !== workspace) return
    useWorkspaceReturnStore.setState({ saveError: 'Your latest workspace could not be saved. It will retry when you continue working.' })
  } finally { writing = false; if (pending) void flushWorkspaceState() }
}

export function queueWorkspaceState(destination: WorkspaceDestination) {
  const state = useWorkspaceReturnStore.getState()
  if (!state.ready || state.homeOpen || state.restore || state.account !== useAuthStore.getState().user?.id || (state.workspace !== undefined && state.workspace !== useWorkspaceConnectionStore.getState().activeWorkspaceId)) return
  pending = { account: state.account!, workspace: state.workspace, destination }
  clearTimeout(timer)
  timer = setTimeout(() => { void flushWorkspaceState() }, 700)
}

/** Product surfaces keep their existing layout owners; this adapter restores
 * once, then publishes settled selection/visibility to the server writer. */
export function useWorkspaceReturn(
  surface: ProductSurface, id: string | undefined, view: string | undefined,
  layout: WorkspaceLayout, applyLayout: (layout: WorkspaceLayout) => void,
) {
  const session = useChatStore(state => state.activeTabId ? state.chatTabs[state.activeTabId]?.sessionId : undefined)
  const viewMode = useChatStore(state => state.activeTabId ? state.chatTabs[state.activeTabId]?.viewMode : undefined)
  const name = useChatStore(state => state.activeTabId ? state.chatTabs[state.activeTabId]?.name : undefined)
  const account = useAuthStore(state => state.user?.id)
  const restore = useWorkspaceReturnStore(state => state.restore)
  const navigationMode = useWorkspaceReturnStore(state => state.navigationMode)
  const ready = useWorkspaceReturnStore(state => state.ready)
  const applyRef = useRef(applyLayout); applyRef.current = applyLayout
  useEffect(() => {
    if (restore?.surface !== surface || restore.id !== id) return
    let cancelled = false
    // Apply after the surface's own mount effects so default pane activation
    // cannot overwrite the person's restored visibility in the same commit.
    queueMicrotask(() => {
      if (cancelled || useWorkspaceReturnStore.getState().restore !== restore) return
      if (restore.layout) applyRef.current(restore.layout)
      useWorkspaceReturnStore.setState({ restore: null })
    })
    return () => { cancelled = true }
  }, [restore, surface, id])
  useEffect(() => {
    if (restore || !ready || !id || useProductSurfaceStore.getState().productSurface !== surface) return
    const state = useChatStore.getState()
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    const belongs = surface === 'agentworks' || surface === 'relays'
      ? tab?.metadata?.presetQueryId === id
      : tab?.metadata?.agentProfileId === surface && tab?.metadata?.agentProfileProjectId === id
    queueWorkspaceState({ surface, id, view, layout: { ...layout, navigation_mode: navigationMode },
      ...(belongs && tab?.sessionId ? { session_id: tab.sessionId, chat: { name: tab.name, read_only: tab.metadata?.isViewOnly === true, conversation_key: tab.metadata?.agentProfileConversationKey, execution_run: tab.metadata?.isExecutionRun, scheduled_run: tab.metadata?.isScheduledRun, scheduled_job_name: tab.metadata?.scheduledJobName, bot_platform: tab.metadata?.botPlatform, view_mode: tab.viewMode, workshop_mode: tab.metadata?.workshopMode } } : {}),
    })
  }, [surface, id, view, layout.chat_open, layout.panel_open, layout.split_ratio, layout.navigation_mode, layout.preview_device, layout.focused_pane, layout.mobile_pane_width, navigationMode, session, name, viewMode, ready, restore, account])
}

export function installWorkspaceStateFlush() {
  const onHide = () => { if (document.visibilityState === 'hidden') void flushWorkspaceState() }
  const onExit = () => {
    const state = useWorkspaceReturnStore.getState()
    if (!writing && pending?.account === state.account && pending?.workspace === state.workspace && (state.workspace === undefined || state.workspace === useWorkspaceConnectionStore.getState().activeWorkspaceId) && state.record) {
      const destination = pending.destination; pending = null; clearTimeout(timer)
      void saveWorkspaceStateOnExit({ ...state.record, last: destination })
    }
  }
  document.addEventListener('visibilitychange', onHide)
  window.addEventListener('pagehide', onExit)
  return () => { document.removeEventListener('visibilitychange', onHide); window.removeEventListener('pagehide', onExit) }
}
