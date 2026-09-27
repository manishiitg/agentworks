// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

const { preloadProductSurface } = vi.hoisted(() => ({
  preloadProductSurface: vi.fn(() => Promise.resolve()),
}))

vi.mock('../products/productSurfacePreload', () => ({ preloadProductSurface }))
vi.mock('../stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    user: { allowed_products: ['work'] },
  }),
}))
vi.mock('../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    productSurface: 'agentworks',
    setProductSurface: () => {},
  }),
}))
vi.mock('../stores/useAppStore', () => ({ useAppStore: { getState: () => ({}) } }))

import { ProductSurfaceSwitcher } from './ProductSurfaceSwitcher'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('ProductSurfaceSwitcher preloading', () => {
  let root: Root | null = null
  let container: HTMLDivElement | null = null

  afterEach(async () => {
    if (root) await act(async () => root!.unmount())
    root = null
    container?.remove()
    container = null
    delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__
    vi.unstubAllGlobals()
    preloadProductSurface.mockClear()
  })

  it('warms only products allowed for the signed-in user', async () => {
    ;(window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = {
      enabledProductSurfaces: ['agentworks', 'work', 'mcp-gateway'],
      gatewayUrl: 'http://127.0.0.1:18161',
    }
    let idleCallback: IdleRequestCallback | null = null
    window.requestIdleCallback = vi.fn((callback: IdleRequestCallback) => {
      idleCallback = callback
      return 1
    })
    window.cancelIdleCallback = vi.fn()

    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    await act(async () => root!.render(<ProductSurfaceSwitcher />))

    expect(idleCallback).not.toBeNull()
    await act(async () => idleCallback!({ didTimeout: false, timeRemaining: () => 50 }))

    expect(preloadProductSurface).toHaveBeenCalledExactlyOnceWith('work')
    expect(preloadProductSurface).not.toHaveBeenCalledWith('mcp-gateway')
  })
})
