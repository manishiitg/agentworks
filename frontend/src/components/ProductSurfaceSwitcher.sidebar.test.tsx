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
  it.each([['knowledgebase', 'Brain'], ['mcp-gateway', 'Vault']] as const)('keeps %s switching compact and accessible', async (surface, label) => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'knowledgebase', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18163' }
    auth.allowed = [surface, 'agentworks']
    useProductSurfaceStore.setState({ productSurface: surface })
    await render()
    const trigger = host.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!
    expect(trigger.getAttribute('aria-label')).toBe(`Switch product: ${label}`)
    expect(trigger.querySelector('span.sr-only')?.textContent).toBe(label)
    await act(async () => trigger.click())
    expect([...host.querySelectorAll('[data-product-option]')].map(button => button.getAttribute('aria-label'))).toEqual(['Goals', label, 'Dashboards'])
  })
  it.each(['workspace', 'service', 'standard'])('uses the same permitted product grid in a %s picker', async placement => {
    auth.allowed = ['agentworks', 'work', 'code']
    await act(async () => root.render(placement === 'standard' ? <ProductSurfaceSwitcher /> : <ProductTopBar sidebar><ProductSurfaceSwitcher menuOnly={placement === 'workspace'} /></ProductTopBar>))
    if (placement !== 'workspace') await act(async () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click())
    const menu = host.querySelector('[data-product-switch-menu]')!
    expect([...menu.querySelectorAll('[data-product-option]')].map(button => button.getAttribute('aria-label'))).toEqual(['Goals', 'Crew', 'Dashboards'])
    expect(menu.textContent).not.toContain('Vault')
    const goals = menu.querySelector<HTMLButtonElement>('[aria-label="Goals"]')!
    const crew = menu.querySelector<HTMLButtonElement>('[aria-label="Crew"]')!
    goals.focus()
    await act(async () => goals.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })))
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Dashboards')
    await act(async () => crew.click())
    expect(useProductSurfaceStore.getState().productSurface).toBe('work')
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
    expect(Array.from(host.querySelectorAll('[data-product-option]')).map(b => b.getAttribute('aria-label'))).toEqual(['Goals', 'Crew', 'Vault', 'Dashboards'])
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Crew"]')!.click())
    expect(useProductSurfaceStore.getState().productSurface).toBe('work')
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
    expect(host.querySelector('[role="menu"] [aria-label="Dashboards"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Goals"]')).toBeNull()
  })
  it('hides Vault when the deployment has no gateway', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code', 'mcp-gateway'] }
    await render()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label^="Switch product:"]')!.click())
    expect(host.querySelector('button[aria-label="Vault"]')).toBeNull()
  })
})

it.each(['header', 'chevron'] as const)('keeps the permitted products in the %s header switcher', async variant => {
  auth.allowed = ['work']
  await act(async () => root.render(<ProductSurfaceSwitcher variant={variant} />))
  const trigger = host.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!
  expect(trigger.getAttribute('aria-label')).toBe('Switch product: Crew')
  expect(trigger.textContent).toBe(variant === 'header' ? 'Crew' : '')
  await act(async () => trigger.click())
  expect([...host.querySelectorAll('[data-product-option]')].map(button => button.getAttribute('aria-label'))).toEqual(['Crew', 'Dashboards'])
  const crew = host.querySelector<HTMLButtonElement>('[data-product-option]')!
  crew.focus()
  await act(async () => crew.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
  expect(host.querySelector('[role="menu"]')).toBeNull()
  expect(document.activeElement).toBe(trigger)
})

it('uses Dashboards as the current heading on the dashboard page', async () => {
  const previous = window.location.href
  window.history.replaceState({}, '', '/dashboards')
  try {
    await act(async () => root.render(<ProductSurfaceSwitcher variant="header" />))
    expect(host.querySelector('h1')?.textContent).toBe('Dashboards')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!.click())
    expect(host.querySelector('[aria-current="page"]')?.getAttribute('aria-label')).toBe('Dashboards')
  } finally { window.history.replaceState({}, '', previous) }
})
