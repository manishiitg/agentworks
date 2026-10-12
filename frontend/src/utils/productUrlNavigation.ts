import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAppStore } from '../stores/useAppStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { currentProductRoute, productRoutePath, type ProductRoute } from './productRoutes'

type ViewReader = () => { id?: string; view?: string; folder?: string }
const readers = new Map<ProductSurface, Set<ViewReader>>()
let notify: ((registration?: boolean) => void) | undefined

/** Readers project mounted view state; they do not own a second selection. */
export function registerProductUrlView(surface: ProductSurface, reader: ViewReader): () => void {
  const entries = readers.get(surface) ?? new Set<ViewReader>()
  entries.add(reader); readers.set(surface, entries)
  notify?.(true)
  return () => { entries.delete(reader); if (!entries.size) readers.delete(surface) }
}
export function productUrlViewChanged(): void { notify?.() }

export function readProductNavigation(): ProductRoute {
  const state = useProductSurfaceStore.getState()
  const surface = state.productSurface
  let id: string | undefined
  let view: string | undefined
  let overview: boolean | undefined
  if (surface === 'work') id = state.selectedWorkProjectId ?? undefined
  if (surface === 'code') id = state.selectedCodeProjectId ?? undefined
  if (surface === 'video-studio') id = state.lastVideoProjectId ?? undefined
  if (surface === 'agentworks' || surface === 'relays') {
    overview = useAppStore.getState().showWorkflowsOverview
    if (!overview) {
      id = useGlobalPresetStore.getState().activePresetIds.workflow ?? undefined
      if (id) view = useWorkflowStore.getState().workflowWorkspaceView ?? undefined
    }
  }
  return { surface, ...(id ? { id } : {}), ...(view ? { view } : {}), ...(overview && surface === 'agentworks' ? { overview: true } : {}), ...Object.assign({}, ...[...(readers.get(surface) ?? [])].map(reader => Object.fromEntries(Object.entries(reader()).filter(([, value]) => value !== undefined)))) }
}

/** Batch synchronous projections from one navigation action into one entry. */
export function observeProductHistory(): () => void {
  let lastSelection = productRoutePath(readProductNavigation())
  let timer: ReturnType<typeof setTimeout> | undefined
  const schedule = (registration = false) => {
    // Lazy mounting/hydration is a projection, not a new navigation intent.
    // Learn its actual view without pushing an entry (especially on popstate).
    if (registration) {
      if (!timer) lastSelection = productRoutePath(readProductNavigation())
      return
    }
    clearTimeout(timer)
    timer = setTimeout(() => {
      timer = undefined
      // Do not take over shared assets, sign-in or consent routes.
      if (currentProductRoute() === undefined) return
      const path = productRoutePath(readProductNavigation())
      if (path === lastSelection) return
      lastSelection = path
      if (path !== window.location.pathname + window.location.search) window.history.pushState({}, '', path)
    }, 0)
  }
  notify = schedule
  const isWorkflow = () => ['agentworks', 'relays'].includes(useProductSurfaceStore.getState().productSurface) && !useAppStore.getState().showWorkflowsOverview
  const unsubscribers = [
    useProductSurfaceStore.subscribe((state, previous) => {
      if (state.productSurface !== previous.productSurface ||
        (state.productSurface === 'work' && state.selectedWorkProjectId !== previous.selectedWorkProjectId) ||
        (state.productSurface === 'code' && state.selectedCodeProjectId !== previous.selectedCodeProjectId) ||
        (state.productSurface === 'video-studio' && state.lastVideoProjectId !== previous.lastVideoProjectId)) schedule()
    }),
    useAppStore.subscribe((state, previous) => { if (state.showWorkflowsOverview !== previous.showWorkflowsOverview && ['agentworks', 'relays'].includes(useProductSurfaceStore.getState().productSurface)) schedule() }),
    useGlobalPresetStore.subscribe((state, previous) => { if (isWorkflow() && state.activePresetIds.workflow !== previous.activePresetIds.workflow) schedule() }),
    useWorkflowStore.subscribe((state, previous) => { if (isWorkflow() && state.workflowWorkspaceView !== previous.workflowWorkspaceView) schedule() }),
  ]
  return () => {
    clearTimeout(timer)
    if (notify === schedule) notify = undefined
    unsubscribers.forEach(unsubscribe => unsubscribe())
  }
}
