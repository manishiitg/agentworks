import { useEffect, useRef } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { workflowUIControl } from '../../services/api'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { usePresentationEvents } from '../presentations/usePresentationEvents'
import { getWorkspaceView, isWorkspaceViewId } from '../../components/workflow/workspaceViews'
import { UI_CONTROL_CONTRACT } from './contract.generated'
import { applyUIAction, workspaceHost, type UIAction, type UISnapshot } from './client'

const kinds = ['workflow.ui-action']
// Lease renewal only (server lease: 6 min). The agent's explicit view
// commands wake the pane over SSE; the right pane never polls the chat.
export const UI_CONTROL_BACKUP_POLL_MS = 5 * 60_000
type Binding = { binding: string; token: string; workspace: string }
// Why a sync ran. A dormant lease (its chat has no live agent session) ignores
// the backup poll and view changes; only SSE wakes, the chat going live, or the
// page becoming visible try to bind again. The server queues an agent's UI
// action and pushes workflow.ui-action over SSE, so no pre-bind is needed.
type SyncReason = 'mount' | 'poll' | 'view' | 'wake' | 'live' | 'visible'

type LivenessState = {
  chatTabs: Record<string, { sessionId?: string | null; isStreaming?: boolean }>
  activeSessionsCache: Array<{ session_id: string }>
}

export function sessionLooksLive(state: LivenessState, session: string): boolean {
  return Object.values(state.chatTabs).some(tab => tab.sessionId === session && tab.isStreaming)
    || state.activeSessionsCache.some(active => active.session_id === session)
}

export type WorkspaceUIControlAdapter = {
  getView: () => string
  openView: (view: string, target?: string) => void
  refreshView?: (view: string, target?: string) => void
  isViewSupported: (view: string) => boolean
  labelForView: (view: string) => string
  actorLabel?: string
  getTarget?: (view: string, host: HTMLElement | undefined) => string | undefined
}

// One lease per mounted, visible chat. No history playback, persisted binding,
// auto product switch, or shared global command queue. Duplicate SSE only wakes
// sync; the authenticated server atomically claims commands for this binding.
export function useWorkspaceUIControl(session: string | undefined, adapter?: WorkspaceUIControlAdapter): void {
  const adapterRef = useRef(adapter)
  adapterRef.current = adapter
  const events = usePresentationEvents(session, kinds)
  // usePresentationEvents yields a new array on every chat event (streaming
  // chunks included); wake only when a new UI action actually arrives.
  const latestAction = events.length ? `${events.length}:${events[events.length - 1].presentationId}` : ''
  const wake = useRef<(() => void) | null>(null)
  useEffect(() => { if (latestAction) wake.current?.() }, [latestAction])
  useEffect(() => { wake.current?.() }, [adapter])
  useEffect(() => {
    if (!session) return
    let binding: Binding | undefined
    let stopped = false
    let busy = false
    let syncQueued = false
    let lastView = ''
    let revision = 0
    let lastVisible = false
    let lastTarget: string | undefined
    let dormant = false
    // A chat that looks live can be a moment ahead of the server (the page sees it streaming
    // before the server tracks the session), so a refused bind retries a few times instead of
    // going dormant with nothing left to wake it. An idle chat still goes dormant at once.
    const LIVE_BIND_RETRY_MS = 1000
    const LIVE_BIND_RETRIES = 4
    let liveRetries = 0
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let queuedReason: SyncReason = 'poll'
    const weakReason = (reason: SyncReason) => reason === 'poll' || reason === 'view'
    const controller = new AbortController()
    const state = (): UISnapshot => {
      const configured = adapterRef.current
      const store = useWorkflowStore.getState()
      const view = configured?.getView() ?? store.workflowWorkspaceView ?? store.lastCanvasView
      const host = binding ? workspaceHost(binding.workspace) : undefined
      const visible = !!host && host.getClientRects().length > 0 && !!host.querySelector('[data-ui-view-mounted]') && document.visibilityState === 'visible'
      const panel = host?.querySelector<HTMLElement>('[data-ui-plan-step]')
      const target = configured?.getTarget?.(view, host)
        ?? (view === 'flow' && panel?.getClientRects().length ? panel.dataset.uiPlanStep : undefined)
      if (lastView !== view || lastVisible !== visible || lastTarget !== target) {
        revision++; lastView = view; lastVisible = visible; lastTarget = target
      }
      return { view, revision, visible, target }
    }
    // Don't send the returned workspace back: identities are server-derived.
    const boundCall = (body: Record<string, unknown>) => workflowUIControl(session, {
      version: UI_CONTROL_CONTRACT.version, binding: binding?.binding, token: binding?.token, ...body,
    })
    const release = async () => {
      const previous = binding
      binding = undefined
      if (previous) await workflowUIControl(session, { version: UI_CONTROL_CONTRACT.version, operation: 'unbind', binding: previous.binding, token: previous.token }).catch(() => {})
    }
    const sync = async (reason: SyncReason = 'wake') => {
      if (stopped) return
      if (!binding && dormant) {
        if (weakReason(reason)) return
        dormant = false
      }
      if (busy) {
        syncQueued = true
        if (!weakReason(reason) || weakReason(queuedReason)) queuedReason = reason
        return
      }
      if (document.visibilityState !== 'visible') { await release(); return }
      busy = true
      try {
        if (!binding) {
          const next = await workflowUIControl(session, { version: UI_CONTROL_CONTRACT.version, operation: 'bind' }) as Binding
          if (!next || typeof next.binding !== 'string' || typeof next.token !== 'string' || typeof next.workspace !== 'string') {
            console.warn('[WorkspaceUIControl] bind invalid_binding_response')
            return
          }
          if (stopped) {
            await workflowUIControl(session, { version: UI_CONTROL_CONTRACT.version, operation: 'unbind', binding: next.binding, token: next.token })
            return
          }
          binding = next
          liveRetries = 0
        }
        if (!workspaceHost(binding.workspace)) {
          console.warn('[WorkspaceUIControl] bind workspace_host_missing')
          await release(); return
        }
        const commands = await boundCall({ operation: 'sync', state: state() }) as UIAction[]
        for (const command of commands) {
          if (stopped) break
          const before = state()
          const result = await applyUIAction(command, binding.workspace, (view, target) => {
            const configured = adapterRef.current
            if (configured) {
              if (configured.isViewSupported(view)) configured.openView(view, target)
            } else if (isWorkspaceViewId(view)) useWorkflowStore.getState().openWorkspaceView(view, target)
          }, state, controller.signal, (view, target) => {
            const configured = adapterRef.current
            if (configured) {
              configured.openView(view, target)
              configured.refreshView?.(view, target)
            } else if (isWorkspaceViewId(view)) {
              const store = useWorkflowStore.getState()
              store.openWorkspaceView(view, target)
              store.refreshWorkspaceView(target)
            }
          })
          if (stopped) break
          if (result.status === 'applied') {
            revision++
            const after = state()
            const configured = adapterRef.current
            const supported = configured?.isViewSupported(after.view) ?? isWorkspaceViewId(after.view)
            if (after.visible && (!before.visible || before.view !== after.view) && supported) {
              const label = configured?.labelForView(after.view) ?? (after.view === 'browser' ? 'Browser' : getWorkspaceView(after.view as Parameters<typeof getWorkspaceView>[0]).label)
              useChatStore.getState().addToast(`${configured?.actorLabel ?? 'Builder'} opened ${label}`, 'info')
            }
          }
          await boundCall({ operation: 'ack', request_id: command.request_id, ...result, state: state() })
        }
      } catch (error) {
        // Never log the request/config object: it includes the binding token.
        const response = (error as { response?: { status?: number; data?: unknown } })?.response
        const code = typeof response?.data === 'string' && /^[a-z_]+\s*$/.test(response.data)
          ? response.data.trim() : 'connection_failed'
        if (!binding && response?.status === 409 && code === 'session_not_active') {
          dormant = true
          if (!stopped && liveRetries < LIVE_BIND_RETRIES && sessionLooksLive(useChatStore.getState(), session)) {
            const delay = LIVE_BIND_RETRY_MS * 2 ** liveRetries
            liveRetries++
            clearTimeout(retryTimer)
            retryTimer = setTimeout(() => { void sync('live') }, delay)
          }
        } else {
          console.warn(`[WorkspaceUIControl] ${binding ? 'sync' : 'bind'} status=${response?.status ?? 'network_error'} code=${code}`)
        }
        // An uncertain outcome is never replayed. A new lease cannot ACK or
        // claim the previous lease's commands; the server expires those.
        await release()
      } finally {
        busy = false
        if (syncQueued && !stopped) {
          syncQueued = false
          const next = queuedReason
          queuedReason = 'poll'
          queueMicrotask(() => { void sync(next) })
        }
      }
    }
    const onVisibility = () => {
      if (document.visibilityState !== 'visible') void release()
      else void sync('visible')
    }
    wake.current = () => { void sync('wake') }
    const unsubscribeViewState = useWorkflowStore.subscribe((current, previous) => {
      if (current.workflowWorkspaceView !== previous.workflowWorkspaceView
        || current.lastCanvasView !== previous.lastCanvasView
        || current.workspaceViewTarget !== previous.workspaceViewTarget) {
        void sync('view')
      }
    })
    // SSE presentation events and local view changes are the wake-up paths.
    // This slow timer only renews the server lease.
    const timer = setInterval(() => { void sync('poll') }, UI_CONTROL_BACKUP_POLL_MS)
    // Bind as soon as the chat goes live.
    const unsubscribeLiveness = useChatStore.subscribe((current, previous) => {
      if (binding) return
      if (sessionLooksLive(current, session) && !sessionLooksLive(previous, session)) void sync('live')
    })
    document.addEventListener('visibilitychange', onVisibility)
    void sync('mount')
    return () => {
      stopped = true
      controller.abort()
      clearInterval(timer)
      clearTimeout(retryTimer)
      unsubscribeViewState()
      unsubscribeLiveness()
      wake.current = null
      document.removeEventListener('visibilitychange', onVisibility)
      void release()
    }
  }, [session])
}
