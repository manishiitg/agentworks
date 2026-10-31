// @vitest-environment happy-dom
import React, { act, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ resolve: vi.fn(), observe: vi.fn(() => vi.fn()), cancel: vi.fn(), authValid: false, checkAuth: vi.fn().mockResolvedValue(undefined) }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: { getState: () => ({ checkAuth: mocks.checkAuth, isAuthenticated: mocks.authValid }) } }))
vi.mock('../services/workspaceState', () => ({ workspaceStateApi: { get: vi.fn().mockResolvedValue({ version: 1, revision: 0, last: null, recent: [], destination: { surface: 'bullpen' }, first_visit: true }) } }))
vi.mock('../utils/restoreWorkspaceChat', () => ({ restoreWorkspaceChat: vi.fn().mockResolvedValue(undefined) }))
vi.mock('../hooks/useWorkspaceReturn', () => ({ installWorkspaceStateFlush: () => () => {}, queueWorkspaceState: vi.fn() }))
vi.mock('../utils/resolveProductRoute', () => ({ ProductUnavailable: class extends Error {}, resolveProductRoute: mocks.resolve }))
vi.mock('../utils/workspaceSettingsNavigation', () => ({ syncWorkspaceSettings: vi.fn() }))
vi.mock('../utils/workflowNavigation', () => ({ cancelPendingWorkflowNavigation: mocks.cancel }))
vi.mock('../utils/productUrlNavigation', () => ({ observeProductHistory: mocks.observe, readProductNavigation: () => ({ surface: 'bullpen' }) }))
vi.mock('../stores/useChatStore', () => ({ waitForChatStoreHydration: () => Promise.resolve() }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: { getState: () => ({ productSurface: 'bullpen', setProductSurface: vi.fn() }) } }))
import { ProductRouteBoundary } from './ProductRouteBoundary'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => { vi.clearAllMocks(); mocks.authValid = false; window.history.replaceState({}, '', '/') })
describe('protected deep-link boundary', () => {
  it('opens the default product for safe legacy bookmarks and re-checks expired sessions at the requested URL', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['bullpen', 'work'], defaultProductSurface: 'bullpen' }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const activate = vi.fn()
    mocks.resolve.mockResolvedValue(activate)
    try {
      window.history.replaceState({}, '', '/settings')
      await act(async () => root.render(<ProductRouteBoundary account="reader" allowedProducts={['bullpen', 'work']}>Workspace</ProductRouteBoundary>))
      expect(activate).toHaveBeenCalledOnce()
      expect(host.textContent).toBe('Workspace')
      expect(window.location.pathname).toBe('/agentbridge')
      mocks.resolve.mockRejectedValue({ response: { status: 401 } })
      window.history.replaceState({}, '', '/crew/projects/example')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(mocks.checkAuth).toHaveBeenCalledOnce()
      expect(window.location.pathname).toBe('/crew/projects/example')
      expect(host.textContent).not.toContain('not available')
      mocks.authValid = true
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.textContent).toContain('Could not open this link')
      expect(host.querySelector('button')?.textContent).toBe('Retry')
      const lookups = mocks.resolve.mock.calls.length
      await act(async () => root.render(<ProductRouteBoundary account="reader" allowedProducts={['bullpen', 'work']}>Workspace</ProductRouteBoundary>))
      expect(mocks.resolve).toHaveBeenCalledTimes(lookups)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('does not mount a denied/disabled/missing target or overwrite its URL', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['bullpen', 'work'] }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const mounted = vi.fn()
    const PrivateView = () => { mounted(); return <div>Private project data</div> }
    try {
      window.history.replaceState({}, '', '/agentbridge/connect')
      await act(async () => root.render(<ProductRouteBoundary account="reader" allowedProducts={['work']}><PrivateView /></ProductRouteBoundary>))
      const deniedText = host.querySelector('[role=alert]')?.textContent
      expect(deniedText).toContain('not available')
      expect(mocks.resolve).not.toHaveBeenCalled()
      expect(window.location.pathname).toBe('/agentbridge/connect')

      mocks.resolve.mockRejectedValue({ response: { status: 404 } })
      window.history.replaceState({}, '', '/crew/projects/missing')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.querySelector('[role=alert]')?.textContent).toBe(deniedText)
      expect(window.location.pathname).toBe('/crew/projects/missing')

      window.history.replaceState({}, '', '/no-such-place')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.querySelector('[role=alert]')?.textContent).toContain('not available')
      expect(host.querySelector('a')?.textContent).toBe('Return to workspace')
      expect(window.location.pathname).toBe('/no-such-place')
      expect(mounted).not.toHaveBeenCalled()

      window.history.replaceState({}, '', '/sparkquill')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.textContent).toContain('not enabled on this server')
      expect(mounted).not.toHaveBeenCalled()
      expect(mocks.observe).not.toHaveBeenCalled()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})

describe('server workspace return', () => {
  it('keeps Home addressable while resolving the saved workspace with normal permissions', async () => {
    const { workspaceStateApi } = await import('../services/workspaceState')
    const { useWorkspaceReturnStore } = await import('../stores/useWorkspaceReturnStore')
    useWorkspaceReturnStore.setState({ account: undefined, ready: false, record: null })
    const saved = { surface: 'work' as const, id: 'last', session_id: 'chat-1' }
    vi.mocked(workspaceStateApi.get).mockResolvedValueOnce({ version: 1, revision: 3, last: saved, recent: [saved] })
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work'] }
    window.history.replaceState({}, '', '/home')
    mocks.resolve.mockResolvedValue(vi.fn())
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<ProductRouteBoundary account="alice">Workspace</ProductRouteBoundary>))
      expect(window.location.pathname).toBe('/home')
      expect(useWorkspaceReturnStore.getState().firstVisit).toBe(false)
      expect(workspaceStateApi.get).toHaveBeenCalled()
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it.each([true, false])('shows first-use welcome only for an empty account (existing work: %s)', async existing => {
    const { workspaceStateApi } = await import('../services/workspaceState')
    const { useWorkspaceReturnStore } = await import('../stores/useWorkspaceReturnStore')
    useWorkspaceReturnStore.setState({ account: undefined, ready: false, record: null })
    vi.mocked(workspaceStateApi.get).mockResolvedValueOnce({ version: 1, revision: 0, last: null, recent: [], destination: existing ? { surface: 'work', id: 'existing', workspace_path: 'Code/existing' } : { surface: 'work' }, first_visit: !existing })
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work'] }
    // PLAT-856-434: the packaged app adds this query on every initial launch.
    window.history.replaceState({}, '', '/?runloop_version=1.2.3')
    mocks.resolve.mockResolvedValue(vi.fn())
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<ProductRouteBoundary account="new-user">Workspace</ProductRouteBoundary>))
      expect(window.location.pathname).toBe(existing ? '/crew/projects/existing' : '/crew')
      expect(useWorkspaceReturnStore.getState().firstVisit).toBe(!existing)
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('restores the server destination and chat at root, preserving an explicit different link', async () => {
    const { workspaceStateApi } = await import('../services/workspaceState')
    const { useWorkspaceReturnStore } = await import('../stores/useWorkspaceReturnStore')
    const { restoreWorkspaceChat } = await import('../utils/restoreWorkspaceChat')
    useWorkspaceReturnStore.setState({ account: undefined, ready: false, record: null, restore: null, firstVisit: false })
    const saved = { surface: 'work' as const, id: 'last', view: 'files', session_id: 'chat-1', layout: { chat_open: true, panel_open: false, split_ratio: 0.4, navigation_mode: 'fixed' as const, focused_pane: 'chat' as const } }
    vi.mocked(workspaceStateApi.get).mockResolvedValueOnce({ version: 1, revision: 3, last: saved, recent: [saved] })
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work', 'bullpen'] }
    window.history.replaceState({}, '', '/')
    mocks.resolve.mockResolvedValue(vi.fn())
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<ProductRouteBoundary account="alice" allowedProducts={['work', 'bullpen']}>Workspace</ProductRouteBoundary>))
      expect(mocks.resolve).toHaveBeenCalledWith(saved, expect.any(AbortSignal))
      expect(restoreWorkspaceChat).toHaveBeenCalledWith(saved, expect.any(AbortSignal))
      expect(window.location.pathname).toBe('/crew/projects/last')
      window.history.replaceState({}, '', '/crew/projects/last?view=dashboard')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(useWorkspaceReturnStore.getState().restore?.view).toBe('dashboard')
      expect(useWorkspaceReturnStore.getState().restore?.layout).toMatchObject({ panel_open: true, focused_pane: 'preview', split_ratio: 0.4 })
      vi.mocked(restoreWorkspaceChat).mockClear()
      window.history.replaceState({}, '', '/bullpen/connect')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(mocks.resolve).toHaveBeenLastCalledWith({ surface: 'bullpen', view: 'setup' }, expect.any(AbortSignal))
      expect(restoreWorkspaceChat).not.toHaveBeenCalled()
      expect(useWorkspaceReturnStore.getState().restore).toBeNull()
      expect(window.location.pathname).toBe('/agentbridge/connect')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('opens the allowed product with a plain note after saved access is removed', async () => {
    const { workspaceStateApi } = await import('../services/workspaceState')
    const { useWorkspaceReturnStore } = await import('../stores/useWorkspaceReturnStore')
    useWorkspaceReturnStore.setState({ account: undefined, ready: false, record: null })
    vi.mocked(workspaceStateApi.get).mockResolvedValueOnce({ version: 1, revision: 3, last: null, recent: [], destination: {surface:'work'}, note: 'Your last workspace is no longer available.' })
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work'] }
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<ProductRouteBoundary account="bob" allowedProducts={['work']}>Workspace</ProductRouteBoundary>))
      expect(window.location.pathname).toBe('/crew')
      expect(useWorkspaceReturnStore.getState().firstVisit).toBe(false)
      expect(useWorkspaceReturnStore.getState().note).toContain('no longer available')
      expect(mocks.resolve).toHaveBeenCalledWith({ surface: 'work' }, expect.any(AbortSignal))
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})


it('opens every account settings deep link without selecting a private workspace or mounting twice', async () => {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['work'] }
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  let mounts = 0
  const Page = () => { useEffect(() => { mounts++ }, []); return <div>Account settings</div> }
  try {
    window.history.replaceState({}, '', '/settings/people')
    await act(async () => root.render(<ProductRouteBoundary account="member" allowedProducts={[]}><Page /></ProductRouteBoundary>))
    for (const path of ['/settings/ai-accounts', '/settings/ai-tools', '/settings/people']) {
      window.history.pushState({}, '', path)
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(window.location.pathname).toBe(path)
      expect(host.textContent).toBe('Account settings')
    }
    expect(mocks.resolve).not.toHaveBeenCalled()
    expect(mounts).toBe(1)
  } finally { await act(async () => root.unmount()); host.remove() }
})
