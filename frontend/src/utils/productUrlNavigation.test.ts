// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('../stores/useProductSurfaceStore', async () => {
  const { createStore } = await import('zustand/vanilla')
  return { useProductSurfaceStore: createStore(() => ({ productSurface: 'code', selectedCodeProjectId: 'removed' })) }
})
vi.mock('../stores/useAppStore', () => ({ useAppStore: { getState: () => ({ showWorkflowsOverview: false }), subscribe: () => () => {} } }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: { subscribe: () => () => {} } }))
vi.mock('../stores/useWorkflowStore', () => ({ useWorkflowStore: { subscribe: () => () => {} } }))

import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { observeProductHistory, productUrlViewChanged, registerProductUrlView, replaceProductSelection } from './productUrlNavigation'

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); window.history.replaceState({}, '', '/') })

it('does not rewrite a fresh deep link to the default view before the surface applies it', () => {
  vi.useFakeTimers()
  window.history.replaceState({}, '', '/code/projects/example?view=shell')
  // The surface has not applied the linked view yet; it still projects Files.
  let projectedView = 'files'
  const unregister = registerProductUrlView('code', () => ({ id: 'example', view: projectedView }))
  const stop = observeProductHistory()
  try {
    vi.runAllTimers()
    expect(window.location.pathname + window.location.search).toBe('/code/projects/example?view=shell')
    // A deliberate in-app view switch still updates the URL normally.
    projectedView = 'costs'
    productUrlViewChanged()
    vi.runAllTimers()
    expect(window.location.pathname + window.location.search).toBe('/code/projects/example?view=costs')
  } finally { stop(); unregister() }
})

it('replaces a removed project with its inventory fallback and still pushes a deliberate project choice', () => {
  vi.useFakeTimers()
  window.history.replaceState({}, '', '/code/projects/removed')
  const unregister = registerProductUrlView('code', () => ({ view: 'files' }))
  const stop = observeProductHistory()
  const push = vi.spyOn(window.history, 'pushState')
  const replace = vi.spyOn(window.history, 'replaceState')
  try {
    replaceProductSelection(() => useProductSurfaceStore.setState({ selectedCodeProjectId: 'available' }))
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/code/projects/available')
    expect(replace).toHaveBeenCalledOnce()
    expect(push).not.toHaveBeenCalled()
    useProductSurfaceStore.setState({ selectedCodeProjectId: 'chosen' })
    vi.runAllTimers()
    expect(window.location.pathname).toBe('/code/projects/chosen')
    expect(push).toHaveBeenCalledOnce()
  } finally { stop(); unregister() }
})
