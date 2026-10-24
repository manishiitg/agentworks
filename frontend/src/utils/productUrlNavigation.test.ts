// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../stores/useProductSurfaceStore', async () => {
  const { createStore } = await import('zustand/vanilla')
  return { useProductSurfaceStore: createStore(() => ({ productSurface: 'work', selectedWorkProjectId: 'removed' })) }
})
vi.mock('../stores/useAppStore', () => ({ useAppStore: { getState: () => ({ showWorkflowsOverview: false }), subscribe: () => () => {} } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { subscribe: () => () => {} } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { subscribe: () => () => {} } }))

import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { observeProductHistory, productUrlViewChanged, registerProductUrlView, replaceProductSelection } from './productUrlNavigation'

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); window.history.replaceState({}, '', '/') })

it('does not rewrite a fresh deep link to the default view before the surface applies it', () => {
  vi.useFakeTimers()
  window.history.replaceState({}, '', '/crew/projects/example?view=shell')
  // The surface has not applied the linked view yet; it still projects Files.
  let projectedView = 'files'
  const unregister = registerProductUrlView('work', () => ({ id: 'example', view: projectedView }))
  const stop = observeProductHistory()
  try {
    vi.runAllTimers()
    expect(window.location.pathname + window.location.search).toBe('/crew/projects/example?view=shell')
    // A deliberate in-app view switch still updates the URL normally.
    projectedView = 'costs'
    productUrlViewChanged()
    vi.runAllTimers()
    expect(window.location.pathname + window.location.search).toBe('/crew/projects/example?view=costs')
  } finally { stop(); unregister() }
})

it('replaces a removed project with its inventory fallback and still pushes a deliberate project choice', () => {
  vi.useFakeTimers()
  window.history.replaceState({}, '', '/crew/projects/removed')
  const unregister = registerProductUrlView('work', () => ({ view: 'files' }))
  const stop = observeProductHistory()
  const push = vi.spyOn(window.history, 'pushState')
  const replace = vi.spyOn(window.history, 'replaceState')
  try {
    replaceProductSelection(() => useProductSurfaceStore.setState({ selectedWorkProjectId: 'available' }))
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/crew/projects/available')
    expect(replace).toHaveBeenCalledOnce()
    expect(push).not.toHaveBeenCalled()
    useProductSurfaceStore.setState({ selectedWorkProjectId: 'chosen' })
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/crew/projects/chosen')
    expect(push).toHaveBeenCalledOnce()
  } finally { stop(); unregister() }
})


it('keeps the Dashboards deep link until deliberate navigation, including returning to the same Crew', () => {
  vi.useFakeTimers()
  useProductSurfaceStore.setState({ productSurface: 'work', selectedWorkProjectId: 'same' })
  window.history.replaceState({}, '', '/dashboards')
  const popped = vi.fn()
  window.addEventListener('popstate', popped)
  const stop = observeProductHistory()
  try {
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/dashboards')
    productUrlViewChanged()
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/crew/projects/same')
    expect(popped).toHaveBeenCalledOnce()
  } finally { stop(); window.removeEventListener('popstate', popped) }
})


it('keeps Home during background hydration, then opens an explicitly chosen workspace', () => {
  vi.useFakeTimers()
  useProductSurfaceStore.setState({ productSurface: 'work', selectedWorkProjectId: 'same' })
  window.history.replaceState({}, '', '/home')
  const navigated = vi.fn()
  window.addEventListener('product-navigation-changed', navigated)
  const stop = observeProductHistory()
  try {
    replaceProductSelection(() => useProductSurfaceStore.setState({ selectedWorkProjectId: 'available' }))
    productUrlViewChanged(true)
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/home')
    productUrlViewChanged()
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/crew/projects/available')
    expect(navigated).toHaveBeenCalledOnce()
  } finally { stop(); window.removeEventListener('product-navigation-changed', navigated) }
})
