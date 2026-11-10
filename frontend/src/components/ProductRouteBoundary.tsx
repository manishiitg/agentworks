import { markViewSwitchAdmitted, markViewSwitchStart } from '../utils/chatDeliveryTelemetry'
import { workspaceSettingsPage } from '../utils/workspaceSettingsRoutes'
import { syncWorkspaceSettings } from '../utils/workspaceSettingsNavigation'
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { enabledProductSurfaces, intersectAllowedProductSurfaces } from '../products/productSurfaceConfig'
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
import { workConversationKey } from '../utils/workConversationKey'

type Status = 'loading' | 'ready' | 'disabled' | 'unavailable' | 'error'
export function ProductRouteBoundary({ children, account, allowedProducts }: { children: ReactNode; account?: string; allowedProducts?: string[] | null }) {
  useEffect(installWorkspaceStateFlush, [])
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [location, setLocation] = useState(() => window.location.pathname + window.location.search)
  const [attempt, setAttempt] = useState(0)
  const permissionKey = allowedProducts == null ? 'unrestricted' : JSON.stringify([...allowedProducts].map(value => value.toLowerCase()).sort())
  const activeResolution = useRef<AbortController | null>(null)
  const [resolution, setResolution] = useState<{ location: string; account?: string; status: Status; attempt: number; permissionKey: string; workspace?: string; message?: string }>({ location: '', status: 'loading', attempt: 0, permissionKey: '' })
  const status = resolution.location === location && resolution.account === account && resolution.workspace === workspace && resolution.attempt === attempt && resolution.permissionKey === permissionKey ? resolution.status : 'loading'
  useLayoutEffect(() => {
    const onPop = () => {
      activeResolution.current?.abort()
      cancelPendingWorkflowNavigation()
      // Store-driven pushes need no rehydration. A later Back to the initial
      // path can have the same cached URL string, but is still a new intent.
      setAttempt(value => value + 1)
      const next = window.location.pathname + window.location.search
      const nextRoute = parseProductRoute(next)
      if (nextRoute?.surface === 'work' && nextRoute.agent && nextRoute.id) markViewSwitchStart(workConversationKey(nextRoute.id, nextRoute.agent))
      const settings = workspaceSettingsPage()
      syncWorkspaceSettings(settings)
      setLocation(next)
      if (settings) setResolution({ location: next, account, workspace, status: 'ready', attempt: attempt + 1, permissionKey })
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [account, workspace, attempt, permissionKey])
  useEffect(() => {
    const controller = new AbortController()
    activeResolution.current = controller
    const finish = (status: Status, message?: string) => { if (!controller.signal.aborted) setResolution({ location, account, workspace, status, attempt, permissionKey, message }) }
    const settings = workspaceSettingsPage()
    if (settings) {
      syncWorkspaceSettings(settings)
      useWorkspaceReturnStore.setState({ firstVisit: false, restore: null })
      finish('ready')
      return () => controller.abort()
    }
    const homeRequested = window.location.pathname === '/home'
    // Resolve the permitted workspace normally, while Home remains the page.
    // Root query parameters (desktop cache version, connection callbacks) do
    // not select a product. Keep them in the URL for App, but resolve the root.
    let requested = homeRequested || window.location.pathname === '/' ? '/' : window.location.pathname + window.location.search
    // This library has no selected product or new product grant. Its ordinary
    // authenticated API decides which publications the person can discover.
    if (window.location.pathname === '/dashboards') {
      useWorkspaceReturnStore.setState({ firstVisit: false, restore: null })
      void waitForChatStoreHydration().then(() => finish('ready'))
      return () => controller.abort()
    }
    let route = parseProductRoute(requested)
    const enabled = enabledProductSurfaces()
    const allowed = intersectAllowedProductSurfaces(enabled, allowedProducts)
    if (route === undefined && isLegacyAppPath(requested) && LEGACY_APP_ROOTS.has(legacyAppRoot(requested)) && allowed.length) {
      window.history.replaceState({}, '', '/')
      requested = '/'
      route = null
    }
    if (route === undefined) { finish('unavailable'); return () => controller.abort() }
    if (route && !enabled.includes(route.surface)) { finish('disabled'); return () => controller.abort() }
    if (!allowed.length || (route && !allowed.includes(route.surface))) { finish('unavailable'); return () => controller.abort() }
    const initialize = async () => {
      let state = useWorkspaceReturnStore.getState()
      if (state.account !== account || state.workspace !== workspace || !state.ready || requested === '/') {
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
      const defaultDestination = state.record?.destination
      if (!destination && requested === '/') {
        // Entry is resolved server-side using only the permitted inventory.
        // No attention feed or client-selected fallback decides where to land.
        destination = defaultDestination ?? null
        useWorkspaceReturnStore.setState({ firstVisit: state.record?.first_visit === true, restore: null })
      } else {
        useWorkspaceReturnStore.setState({ firstVisit: false, restore: null })
      }
      // Root connection parameters belong to App, not workspace restoration.
      if (!destination) { finish('unavailable'); return }
      if (!allowed.includes(destination.surface)) {
        if (!saved || !defaultDestination) { finish('unavailable'); return }
        const activate = await resolveProductRoute(defaultDestination, controller.signal)
        if (controller.signal.aborted) return
        activate()
        useWorkspaceReturnStore.setState({ firstVisit: false, restore: null, note: 'Your last workspace is no longer available. Choose another workspace.' })
        if (!homeRequested) window.history.replaceState({}, '', productRoutePath(defaultDestination)); finish('ready'); return
      }
      await waitForChatStoreHydration()
      if (controller.signal.aborted) return
      try {
        const activate = await resolveProductRoute(destination, controller.signal)
        if (controller.signal.aborted) return
        activate()
        markViewSwitchAdmitted()
        if (saved && !destination.agent) {
          await restoreWorkspaceChat(saved, controller.signal).catch(() => {
            if (!controller.signal.aborted) useWorkspaceReturnStore.setState({ note: 'Your workspace opened, but its saved chat could not be restored.' })
          })
          if (controller.signal.aborted) return
          const explicitPanel = route?.view && route.view !== saved.view
          const layout = explicitPanel && saved.layout ? { ...saved.layout, panel_open: true, focused_pane: 'preview' as const } : saved.layout
          useWorkspaceReturnStore.setState({ restore: ['agentworks', 'work', 'code'].includes(saved.surface) ? { ...saved, view: destination.view, layout } : null })
        }
        if (!homeRequested) window.history.replaceState({}, '', productRoutePath(destination))
        finish('ready')
      } catch (error) {
        const code = (error as { response?: { status?: number } })?.response?.status
        if (saved && defaultDestination && (error instanceof ProductUnavailable || code === 403 || code === 404)) {
          const activate = await resolveProductRoute(defaultDestination, controller.signal)
          if (controller.signal.aborted) return
          activate()
          useWorkspaceReturnStore.setState({ firstVisit: false, restore: null, note: 'Your last workspace is no longer available. Choose another workspace.' })
          if (!homeRequested) window.history.replaceState({}, '', productRoutePath(defaultDestination)); finish('ready'); return
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
      finish(error instanceof ProductUnavailable || code === 403 || code === 404 ? 'unavailable' : 'error', error instanceof ProductUnavailable ? error.message : undefined)
    })
    return () => { controller.abort(); cancelPendingWorkflowNavigation() }
  }, [location, account, workspace, permissionKey, attempt])
  const sameProduct = parseProductRoute(resolution.location)?.surface === parseProductRoute(location)?.surface
  const openingLink = status === 'loading' && resolution.status === 'ready' && resolution.account === account && resolution.workspace === workspace && resolution.permissionKey === permissionKey && sameProduct
  const [loadingAge, setLoadingAge] = useState<'brief' | 'notice' | 'expired'>('brief')
  useEffect(() => {
    setLoadingAge('brief')
    if (!openingLink) return
    const notice = window.setTimeout(() => setLoadingAge('notice'), 2000)
    const expiry = window.setTimeout(() => setLoadingAge('expired'), 10000)
    return () => { window.clearTimeout(notice); window.clearTimeout(expiry) }
  }, [openingLink, location, attempt])
  useEffect(() => status === 'ready' ? observeProductHistory(route => {
    if (!['agentworks', 'work', 'code'].includes(route.surface)) queueWorkspaceState({ surface: route.surface, view: route.view })
  }) : undefined, [status, location, account])
  // Keep the admitted workspace mounted while another link is checked, so a
  // Crew switch does not tear down the chat and flash a full-page loader.
  // Account, connection and permission changes still remove it immediately.
  if (status === 'ready' || openingLink && loadingAge !== 'expired') return <>
    <div className={openingLink ? 'contents pointer-events-none' : 'contents'} aria-busy={openingLink} inert={openingLink}>{children}</div>
    {openingLink && loadingAge === 'notice' && <p role="status" className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2 rounded-lg border border-border bg-background px-3 py-2 text-xs text-muted-foreground shadow-sm">Opening link…</p>}
  </>
  const text = status === 'loading' ? 'Opening link…' : status === 'disabled' ? 'This product is not enabled on this server.' : status === 'error' ? 'Could not open this link. Please try again.' : resolution.message || 'This item is not available. You may not have access.'
  return <main className="grid min-h-screen place-items-center p-6"><div className="max-w-md space-y-4 text-center"><p role={status === 'loading' ? 'status' : 'alert'}>{text}</p>{status !== 'loading' && <div className="flex justify-center gap-4"><a className="underline" href="/">Return to workspace</a>{status === 'error' && <button className="underline" onClick={() => { activeResolution.current?.abort(); setAttempt(value => value + 1) }}>Retry</button>}</div>}</div></main>
}
