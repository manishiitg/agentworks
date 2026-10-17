import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { deploymentDefaultProductSurface, enabledProductSurfaces, intersectAllowedProductSurfaces } from '../products/productSurfaceConfig'
import { waitForChatStoreHydration } from '../stores/useChatStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { useAuthStore } from '../stores/useAuthStore'
import { isLegacyAppPath, LEGACY_APP_ROOTS, legacyAppRoot, parseProductRoute, productRoutePath } from '../utils/productRoutes'
import { observeProductHistory } from '../utils/productUrlNavigation'
import { cancelPendingWorkflowNavigation } from '../utils/workflowNavigation'
import { ProductUnavailable, resolveProductRoute } from '../utils/resolveProductRoute'

import { workspaceStateApi } from '../services/workspaceState'
import { useWorkspaceReturnStore } from '../stores/useWorkspaceReturnStore'
import { installWorkspaceStateFlush, queueWorkspaceState } from '../hooks/useWorkspaceReturn'
import { restoreWorkspaceChat } from '../utils/restoreWorkspaceChat'

type Status = 'loading' | 'ready' | 'disabled' | 'unavailable' | 'error'
export function ProductRouteBoundary({ children, account, allowedProducts }: { children: ReactNode; account?: string; allowedProducts?: string[] | null }) {
  useEffect(installWorkspaceStateFlush, [])
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [location, setLocation] = useState(() => window.location.pathname + window.location.search)
  const [attempt, setAttempt] = useState(0)
  const permissionKey = allowedProducts == null ? 'unrestricted' : JSON.stringify([...allowedProducts].map(value => value.toLowerCase()).sort())
  const activeResolution = useRef<AbortController | null>(null)
  const [resolution, setResolution] = useState<{ location: string; account?: string; status: Status; attempt: number; permissionKey: string; workspace?: string }>({ location: '', status: 'loading', attempt: 0, permissionKey: '' })
  const status = resolution.location === location && resolution.account === account && resolution.workspace === workspace && resolution.attempt === attempt && resolution.permissionKey === permissionKey ? resolution.status : 'loading'
  useLayoutEffect(() => {
    const onPop = () => {
      activeResolution.current?.abort()
      cancelPendingWorkflowNavigation()
      // Store-driven pushes need no rehydration. A later Back to the initial
      // path can have the same cached URL string, but is still a new intent.
      setAttempt(value => value + 1)
      setLocation(window.location.pathname + window.location.search)
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    activeResolution.current = controller
    const finish = (status: Status) => { if (!controller.signal.aborted) setResolution({ location, account, workspace, status, attempt, permissionKey }) }
    // Old Home bookmarks now return to the person's workspace as well.
    const requested = window.location.pathname === '/home' ? '/' : window.location.pathname + window.location.search
    let route = parseProductRoute(requested)
    const enabled = enabledProductSurfaces()
    const allowed = intersectAllowedProductSurfaces(enabled, allowedProducts)
    if (route === undefined && isLegacyAppPath(requested) && LEGACY_APP_ROOTS.has(legacyAppRoot(requested)) && allowed.length) {
      window.history.replaceState({}, '', '/')
      const preferred = deploymentDefaultProductSurface()
      route = { surface: allowed.includes(preferred) ? preferred : allowed[0] }
    }
    if (route === undefined) { finish('unavailable'); return () => controller.abort() }
    if (route && !enabled.includes(route.surface)) { finish('disabled'); return () => controller.abort() }
    if (!allowed.length || (route && !allowed.includes(route.surface))) { finish('unavailable'); return () => controller.abort() }
    const initialize = async () => {
      let state = useWorkspaceReturnStore.getState()
      if (state.account !== account || state.workspace !== workspace || !state.ready) {
        useWorkspaceReturnStore.setState({ account, workspace, ready: false, record: null, restore: null, firstVisit: false, note: '', saveError: '' })
        const record = await workspaceStateApi.get(controller.signal)
        if (controller.signal.aborted) return
        useWorkspaceReturnStore.setState({ account, workspace, ready: true, record, note: record.note || '' })
        state = useWorkspaceReturnStore.getState()
      }
      let destination = route
      const last = state.record?.last
      const saved = ((!destination && requested === '/') || (destination && last?.surface === destination.surface && last.id === destination.id)) ? last : null
      if (saved && !destination) destination = saved
      const defaultDestination = { surface: allowed.includes(deploymentDefaultProductSurface()) ? deploymentDefaultProductSurface() : allowed[0] }
      if (!destination && requested === '/') {
        // Existing users without a saved return record still enter their work.
        // Only an empty account gets the first-use welcome.
        const inventory = await workspaceStateApi.home(controller.signal)
        if (controller.signal.aborted) return
        const existing = [...inventory.work].filter(item => allowed.includes(item.surface))
          .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))[0]
        destination = existing ? { surface: existing.surface, id: existing.id } : defaultDestination
        useWorkspaceReturnStore.setState({ firstVisit: !existing && !state.note, restore: null })
      } else {
        useWorkspaceReturnStore.setState({ firstVisit: false, restore: null })
      }
      // Root connection parameters belong to App, not workspace restoration.
      if (!destination) { finish('ready'); return }
      if (!allowed.includes(destination.surface)) {
        if (!saved) { finish('unavailable'); return }
        const activate = await resolveProductRoute(defaultDestination, controller.signal)
        if (controller.signal.aborted) return
        activate()
        useWorkspaceReturnStore.setState({ firstVisit: false, restore: null, note: 'Your last workspace is no longer available. Choose another workspace.' })
        window.history.replaceState({}, '', productRoutePath(defaultDestination)); finish('ready'); return
      }
      await waitForChatStoreHydration()
      if (controller.signal.aborted) return
      try {
        const activate = await resolveProductRoute(destination, controller.signal)
        if (controller.signal.aborted) return
        activate()
        if (saved) {
          await restoreWorkspaceChat(saved, controller.signal).catch(() => {
            if (!controller.signal.aborted) useWorkspaceReturnStore.setState({ note: 'Your workspace opened, but its saved chat could not be restored.' })
          })
          if (controller.signal.aborted) return
          const explicitPanel = route?.view && route.view !== saved.view
          const layout = explicitPanel && saved.layout ? { ...saved.layout, panel_open: true, focused_pane: 'preview' as const } : saved.layout
          useWorkspaceReturnStore.setState({ restore: ['agentworks', 'relays', 'work', 'code'].includes(saved.surface) ? { ...saved, view: destination.view, layout } : null })
        }
        window.history.replaceState({}, '', productRoutePath(destination))
        finish('ready')
      } catch (error) {
        const code = (error as { response?: { status?: number } })?.response?.status
        if (saved && (error instanceof ProductUnavailable || code === 403 || code === 404)) {
          const activate = await resolveProductRoute(defaultDestination, controller.signal)
          if (controller.signal.aborted) return
          activate()
          useWorkspaceReturnStore.setState({ firstVisit: false, restore: null, note: 'Your last workspace is no longer available. Choose another workspace.' })
          window.history.replaceState({}, '', productRoutePath(defaultDestination)); finish('ready'); return
        }
        throw error
      }
    }
    void initialize().catch(error => {
      const code = error?.response?.status
      if (controller.signal.aborted) return
      if (code === 401) {
        void useAuthStore.getState().checkAuth({ background: true }).then(() => {
          if (useAuthStore.getState().isAuthenticated) finish('error')
        })
        return
      }
      finish(error instanceof ProductUnavailable || code === 403 || code === 404 ? 'unavailable' : 'error')
    })
    return () => { controller.abort(); cancelPendingWorkflowNavigation() }
  }, [location, account, workspace, permissionKey, attempt])
  useEffect(() => status === 'ready' ? observeProductHistory(route => {
    if (!['agentworks', 'relays', 'work', 'code'].includes(route.surface)) queueWorkspaceState({ surface: route.surface, view: route.view })
  }) : undefined, [status, location, account])
  if (status === 'ready') return <>{children}</>
  const text = status === 'loading' ? 'Opening link…' : status === 'disabled' ? 'This product is not enabled on this server.' : status === 'error' ? 'Could not open this link. Please try again.' : 'This item is not available. You may not have access.'
  return <main className="grid min-h-screen place-items-center p-6"><div className="max-w-md space-y-4 text-center"><p role={status === 'loading' ? 'status' : 'alert'}>{text}</p>{status !== 'loading' && <div className="flex justify-center gap-4"><a className="underline" href="/">Return to workspace</a>{status === 'error' && <button className="underline" onClick={() => { activeResolution.current?.abort(); setAttempt(value => value + 1) }}>Retry</button>}</div>}</div></main>
}
