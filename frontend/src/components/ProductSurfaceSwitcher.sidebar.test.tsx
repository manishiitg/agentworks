// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const auth = vi.hoisted(() => ({ allowed: null as string[] | null }))
const app = vi.hoisted(() => ({ setModeCategory: vi.fn(), setShowWorkflowsOverview: vi.fn(), setShowSchedulesOverview: vi.fn(), setAdminPage: vi.fn(), setActivityWorkflowPath: vi.fn() }))
vi.mock('../stores/useAuthStore', () => ({ useAuthStore: (selector: (s: unknown) => unknown) => selector({ user: { allowed_products: auth.allowed } }) }))
vi.mock('../stores/useLLMStore', () => ({ useLLMStore: { getState: () => ({ setShowLLMModal: vi.fn() }) } }))
vi.mock('../stores/useAppStore', () => ({ useAppStore: { getState: () => app } }))
import { ProductSurfaceSwitcher } from './ProductSurfaceSwitcher'
import { ProductTopBar } from './workspace/ProductTopBar'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let host: HTMLDivElement
let root: Root
beforeEach(() => {
  auth.allowed = null
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18163' }
  useProductSurfaceStore.setState({ productSurface: 'work' })
  host = document.createElement('div'); document.body.append(host); root = createRoot(host)
})
afterEach(async () => { await act(async () => root.unmount()); host.remove(); delete window.__APP_RUNTIME_CONFIG__; vi.clearAllMocks() })
afterAll(() => vi.unstubAllGlobals())
const render = () => act(async () => root.render(<ProductTopBar sidebar><ProductSurfaceSwitcher /></ProductTopBar>))
describe('collapsible product navigation', () => {
  it('preserves deployment title and favicon when entering Vault', async () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { appName: 'Confida', faviconUrl: '/brand/icon.svg' })
    const icon = document.createElement('link'); icon.rel = 'icon'; document.head.append(icon)
    try {
      await render()
      await act(async () => useProductSurfaceStore.setState({ productSurface: 'mcp-gateway' }))
      expect(document.title).toBe('Confida')
      expect(icon.getAttribute('href')).toBe('/brand/icon.svg')
      await act(async () => useProductSurfaceStore.setState({ productSurface: 'work' }))
      expect(document.title).toBe('Confida')
      expect(icon.getAttribute('href')).toBe('/brand/icon.svg')
    } finally { icon.remove() }
  })
  it('shows the current icon and reveals permitted products on hover', async () => {
    await render()
    expect(host.querySelectorAll('[role="group"][aria-label="Products"] button').length).toBe(1)
    const trigger = host.querySelector<HTMLButtonElement>('button')!
    expect(trigger.getAttribute('aria-label')).toBe('Switch product: Crew')
    const group = host.querySelector('[role="group"]')!
    await act(async () => group.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })))
    expect(Array.from(host.querySelectorAll('[role="menuitem"]')).map(b => b.getAttribute('aria-label'))).toEqual(['Goals', 'Crew', 'Code', 'Vault'])
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Code"]')!.click())
    expect(useProductSurfaceStore.getState().productSurface).toBe('code')
    expect(host.querySelector('[role="menu"]')).toBeNull()
    await act(async () => trigger.click())
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Goals"]')!.click())
    expect(app.setShowSchedulesOverview).toHaveBeenCalledWith(false)
    expect(app.setAdminPage).toHaveBeenCalledWith(null)
    expect(app.setModeCategory).toHaveBeenCalledWith('workflow')
    expect(app.setShowWorkflowsOverview).toHaveBeenCalledWith(false)
  })
  it('hides the flyout on mouse leave and supports click and Escape', async () => {
    await render()
    const group = host.querySelector('[role="group"]')!
    await act(async () => group.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })))
    expect(host.querySelector('[role="menu"]')).not.toBeNull()
    await act(async () => group.dispatchEvent(new MouseEvent('mouseout', { bubbles: true, relatedTarget: document.body })))
    expect(host.querySelector('[role="menu"]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(host.querySelector('[role="menu"]')).not.toBeNull()
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(host.querySelector('[role="menu"]')).toBeNull()
  })
  it('keeps the account product allowlist applied', async () => {
    auth.allowed = ['work']; await render()
    expect(host.querySelectorAll('[role="group"][aria-label="Products"] button').length).toBe(1)
    expect(host.querySelector('button')?.getAttribute('aria-label')).toBe('Switch product: Crew')
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(host.querySelector('[role="menu"]')).toBeNull()
  })
  it('hides Vault when the deployment has no gateway', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code', 'mcp-gateway'] }
    await render()
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(host.querySelector('button[aria-label="Vault"]')).toBeNull()
  })
})
