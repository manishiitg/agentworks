// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const auth = vi.hoisted(() => ({ allowed: null as string[] | null }))
const app = vi.hoisted(() => ({ setModeCategory: vi.fn(), setAgentMode: vi.fn(), setShowWorkflowsOverview: vi.fn(), setShowSchedulesOverview: vi.fn(), setAdminPage: vi.fn(), setActivityWorkflowPath: vi.fn() }))
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
  it.each(['workspace', 'service', 'standard'])('uses the same permitted product grid in a %s picker', async placement => {
    auth.allowed = ['agentworks', 'work', 'code']
    await act(async () => root.render(placement === 'standard' ? <ProductSurfaceSwitcher /> : <ProductTopBar sidebar><ProductSurfaceSwitcher menuOnly={placement === 'workspace'} /></ProductTopBar>))
    if (placement !== 'workspace') await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click())
    const menu = host.querySelector('[data-product-switch-menu]')!
    expect([...menu.querySelectorAll('[data-product-option]')].map(button => button.getAttribute('aria-label'))).toEqual(['Goals', 'Crew'])
    expect(menu.textContent).not.toContain('Vault')
    const goals = menu.querySelector<HTMLButtonElement>('[aria-label="Goals"]')!
    const crew = menu.querySelector<HTMLButtonElement>('[aria-label="Crew"]')!
    goals.focus()
    await act(async () => goals.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })))
    expect(document.activeElement).toBe(crew)
    await act(async () => crew.click())
    expect(useProductSurfaceStore.getState().productSurface).toBe('code')
  })
  it('preserves deployment title and favicon when entering Vault', async () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { appName: 'customer', faviconUrl: '/brand/icon.svg' })
    const icon = document.createElement('link'); icon.rel = 'icon'; document.head.append(icon)
    try {
      await render()
      await act(async () => useProductSurfaceStore.setState({ productSurface: 'mcp-gateway' }))
      expect(document.title).toBe('customer')
      expect(icon.getAttribute('href')).toBe('/brand/icon.svg')
      await act(async () => useProductSurfaceStore.setState({ productSurface: 'work' }))
      expect(document.title).toBe('customer')
      expect(icon.getAttribute('href')).toBe('/brand/icon.svg')
    } finally { icon.remove() }
  })
  it('shows the current icon and reveals permitted products on hover', async () => {
    await render()
    expect(host.querySelectorAll('[role="group"][aria-label="Products"] button').length).toBe(1)
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label^="Switch product:"]')!
    expect(trigger.getAttribute('aria-label')).toBe('Switch product: Crew')
    const group = host.querySelector('[role="group"]')!
    await act(async () => group.dispatchEvent(new MouseEvent('mouseover', { bubbles: true })))
    expect(Array.from(host.querySelectorAll('[role="menuitem"]')).map(b => b.getAttribute('aria-label'))).toEqual(['Goals', 'Crew', 'Vault'])
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Crew"]')!.click())
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
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label^="Switch product:"]')!.click())
    expect(host.querySelector('[role="menu"]')).not.toBeNull()
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(host.querySelector('[role="menu"]')).toBeNull()
  })
  it('keeps the account product allowlist applied', async () => {
    auth.allowed = ['work']; await render()
    expect(host.querySelectorAll('[role="group"][aria-label="Products"] button').length).toBe(1)
    expect(host.querySelector('[aria-label^="Switch product:"]')?.getAttribute('aria-label')).toBe('Switch product: Crew')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label^="Switch product:"]')!.click())
    expect(host.querySelector('[role="menu"]')).toBeNull()
  })
  it('hides Vault when the deployment has no gateway', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code', 'mcp-gateway'] }
    await render()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label^="Switch product:"]')!.click())
    expect(host.querySelector('button[aria-label="Vault"]')).toBeNull()
  })
})
