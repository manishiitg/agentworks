import { workspaceSettingsPage } from './workspaceSettingsRoutes'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { currentProductRoute, productRoutePath, type ProductRoute } from './productRoutes'

type ViewReader = () => { id?: string; view?: string; folder?: string; agent?: string }
type HistoryChange = 'projection' | 'navigation' | 'redirect'
const readers = new Map<ProductSurface, Set<ViewReader>>()
let notify: ((change: HistoryChange) => void) | undefined
let replacingSelection = false

/** A synchronous inventory fallback replaces the resource that is no longer available. */
export function replaceProductSelection(update: () => void): void {
  const previous = replacingSelection
  replacingSelection = true
  try { update() } finally { replacingSelection = previous }
}

/** Readers project mounted view state; they do not own a second selection. */
export function registerProductUrlView(surface: ProductSurface, reader: ViewReader): () => void {
  const entries = readers.get(surface) ?? new Set<ViewReader>()
  entries.add(reader); readers.set(surface, entries)
  notify?.('projection')
  return () => { entries.delete(reader); if (!entries.size) readers.delete(surface) }
}
export function productUrlViewChanged(projection = false): void { notify?.(projection ? 'projection' : 'navigation') }

export function readProductNavigation(): ProductRoute {
  const state = useProductSurfaceStore.getState()
  const surface = state.productSurface
  let id: string | undefined
  let view: string | undefined
  if (surface === 'work') id = state.selectedWorkProjectId ?? undefined
  if (surface === 'agentworks') {
    id = useGlobalPresetStore.getState().activePresetIds.workflow ?? undefined
    if (id) view = useWorkflowStore.getState().workflowWorkspaceView ?? undefined
  }
  return { surface, ...(id ? { id } : {}), ...(view ? { view } : {}), ...Object.assign({}, ...[...(readers.get(surface) ?? [])].map(reader => Object.fromEntries(Object.entries(reader()).filter(([, value]) => value !== undefined)))) }
}

/** Batch synchronous projections from one navigation action into one entry. */
export function observeProductHistory(onSelection?: (route: ProductRoute) => void): () => void {
  let lastSelection = productRoutePath(readProductNavigation())
  let timer: ReturnType<typeof setTimeout> | undefined
  let replace = false
  let traversing = false
  // PLAT-746-758: popstate changes the URL before React settles the old view.
  // Cancel its pending write synchronously; pushing that stale selection here
  // would discard Forward. The route boundary installs a fresh observer only
  // after the destination has been admitted and activated.
  const onPop = () => {
    traversing = true
    clearTimeout(timer)
    timer = undefined
  }
  window.addEventListener('popstate', onPop)
  const sameDestination = () => {
    const route = currentProductRoute(), selection = readProductNavigation()
    return route?.surface === selection.surface && route.id === selection.id
  }
  const schedule = (change: HistoryChange, explicit = false) => {
    if (traversing) return
    // Background hydration must not navigate away from Projects or Settings.
    const topLevel = window.location.pathname === '/projects' || Boolean(workspaceSettingsPage())
    if (topLevel && (!explicit || change !== 'navigation')) return
    // Mounting and asynchronous resource hydration settle the current entry.
    // Keep a pending navigation push, and coalesce subsequent view hydration
    // into the same replacement rather than an extra Back step.
    if (change !== 'navigation') {
      if (timer) return
      if (change === 'projection' && !sameDestination()) { lastSelection = productRoutePath(readProductNavigation()); return }
      replace = true
    } else replace = false
    clearTimeout(timer)
    timer = setTimeout(() => {
      timer = undefined
      const replaceEntry = replace
      replace = false
      // Do not take over shared assets, sign-in or consent routes.
      const fromDashboards = window.location.pathname === '/dashboards'
      const fromProjects = window.location.pathname === '/projects'
      const fromSettings = Boolean(workspaceSettingsPage())
      if ((fromProjects || fromSettings) && !topLevel) return
      if (currentProductRoute() === undefined && !fromDashboards && !fromProjects && !fromSettings) return
      const projected = readProductNavigation()
      // A fresh deep link names its view before the mounted surface has
      // applied it; a projection must not rewrite the link to the default.
      if (replaceEntry && currentProductRoute()?.view && projected.view !== currentProductRoute()?.view) { lastSelection = productRoutePath(projected); return }
      if (replaceEntry && currentProductRoute()?.agent && projected.agent !== currentProductRoute()?.agent) { lastSelection = productRoutePath(projected); return }
      onSelection?.(projected)
      const path = productRoutePath(projected)
      if (path === lastSelection && !replaceEntry && !fromDashboards && !fromProjects && !fromSettings) return
      lastSelection = path
      if (path !== window.location.pathname + window.location.search) {
        if (replaceEntry) window.history.replaceState({}, '', path)
        else window.history.pushState({}, '', path)
        // No project selected is the Projects page (/projects), which the route boundary must open itself.
        if (fromDashboards || fromProjects || fromSettings || path === '/projects') window.dispatchEvent(new PopStateEvent('popstate'))
      }
    }, 0)
  }
  const navigate = (change: HistoryChange) => schedule(change, true)
  notify = navigate
  const isWorkflow = () => useProductSurfaceStore.getState().productSurface === 'agentworks'
  const unsubscribers = [
    useProductSurfaceStore.subscribe((state, previous) => {
      if (state.productSurface !== previous.productSurface ||
        (state.productSurface === 'work' && state.selectedWorkProjectId !== previous.selectedWorkProjectId)) schedule(replacingSelection ? 'redirect' : 'navigation')
    }),
    useGlobalPresetStore.subscribe((state, previous) => { if (isWorkflow() && state.activePresetIds.workflow !== previous.activePresetIds.workflow) schedule('navigation') }),
    useWorkflowStore.subscribe((state, previous) => { if (isWorkflow() && state.workflowWorkspaceView !== previous.workflowWorkspaceView) schedule('navigation') }),
  ]
  return () => {
    window.removeEventListener('popstate', onPop)
    clearTimeout(timer)
    if (notify === navigate) notify = undefined
    unsubscribers.forEach(unsubscribe => unsubscribe())
  }
}
