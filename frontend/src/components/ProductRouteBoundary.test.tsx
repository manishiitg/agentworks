// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ resolve: vi.fn(), observe: vi.fn(() => vi.fn()), cancel: vi.fn(), authValid: false, checkAuth: vi.fn().mockResolvedValue(undefined) }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: { getState: () => ({ checkAuth: mocks.checkAuth, isAuthenticated: mocks.authValid }) } }))
vi.mock('../utils/resolveProductRoute', () => ({ ProductUnavailable: class extends Error {}, resolveProductRoute: mocks.resolve }))
vi.mock('../utils/workflowNavigation', () => ({ cancelPendingWorkflowNavigation: mocks.cancel }))
vi.mock('../utils/productUrlNavigation', () => ({ observeProductHistory: mocks.observe, readProductNavigation: () => ({ surface: 'bullpen' }) }))
vi.mock('../stores/useChatStore', () => ({ waitForChatStoreHydration: () => Promise.resolve() }))
vi.mock('../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: { getState: () => ({ productSurface: 'bullpen', setProductSurface: vi.fn() }) } }))
import { ProductRouteBoundary } from './ProductRouteBoundary'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

afterEach(() => { vi.clearAllMocks(); mocks.authValid = false; window.history.replaceState({}, '', '/') })
describe('protected deep-link boundary', () => {
  it('opens the default product for safe legacy bookmarks and re-checks expired sessions at the requested URL', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['bullpen', 'code'], defaultProductSurface: 'bullpen' }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const activate = vi.fn()
    mocks.resolve.mockResolvedValue(activate)
    try {
      window.history.replaceState({}, '', '/settings')
      await act(async () => root.render(<ProductRouteBoundary account="reader" allowedProducts={['bullpen', 'code']}>Workspace</ProductRouteBoundary>))
      expect(activate).toHaveBeenCalledOnce()
      expect(host.textContent).toBe('Workspace')
      expect(window.location.pathname).toBe('/bullpen')
      mocks.resolve.mockRejectedValue({ response: { status: 401 } })
      window.history.replaceState({}, '', '/code/projects/example')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(mocks.checkAuth).toHaveBeenCalledOnce()
      expect(window.location.pathname).toBe('/code/projects/example')
      expect(host.textContent).not.toContain('not available')
      mocks.authValid = true
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.textContent).toContain('Could not open this link')
      expect(host.querySelector('button')?.textContent).toBe('Retry')
      const lookups = mocks.resolve.mock.calls.length
      await act(async () => root.render(<ProductRouteBoundary account="reader" allowedProducts={['bullpen', 'code']}>Workspace</ProductRouteBoundary>))
      expect(mocks.resolve).toHaveBeenCalledTimes(lookups)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('does not mount a denied/disabled/missing target or overwrite its URL', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['bullpen', 'code'] }
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const mounted = vi.fn()
    const PrivateView = () => { mounted(); return <div>Private project data</div> }
    try {
      window.history.replaceState({}, '', '/bullpen/connect')
      await act(async () => root.render(<ProductRouteBoundary account="reader" allowedProducts={['code']}><PrivateView /></ProductRouteBoundary>))
      const deniedText = host.querySelector('[role=alert]')?.textContent
      expect(deniedText).toContain('not available')
      expect(mocks.resolve).not.toHaveBeenCalled()
      expect(window.location.pathname).toBe('/bullpen/connect')

      mocks.resolve.mockRejectedValue({ response: { status: 404 } })
      window.history.replaceState({}, '', '/code/projects/missing')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.querySelector('[role=alert]')?.textContent).toBe(deniedText)
      expect(window.location.pathname).toBe('/code/projects/missing')

      window.history.replaceState({}, '', '/sparkquill')
      await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
      expect(host.textContent).toContain('not enabled on this server')
      expect(mounted).not.toHaveBeenCalled()
      expect(mocks.observe).not.toHaveBeenCalled()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
