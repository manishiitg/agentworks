// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const auth = vi.hoisted(() => ({ allowed: null as string[] | null }))
const app = vi.hoisted(() => ({ setModeCategory: vi.fn(), setShowWorkflowsOverview: vi.fn(), setShowSchedulesOverview: vi.fn(), setAdminPage: vi.fn() }))
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
describe('direct product navigation', () => {
  it('shows permitted products directly and switches without opening a menu', async () => {
    await render()
    const buttons = Array.from(host.querySelectorAll('button'))
    expect(buttons.map(b => b.getAttribute('aria-label'))).toEqual(['Goals', 'Crew', 'Code', 'Vault'])
    expect(host.querySelector('[role="menu"]')).toBeNull()
    expect(host.querySelector('[aria-current="page"]')?.getAttribute('aria-label')).toBe('Crew')
    await act(async () => buttons.find(b => b.getAttribute('aria-label') === 'Code')!.click())
    expect(useProductSurfaceStore.getState().productSurface).toBe('code')
    expect(host.querySelector('[aria-current="page"]')?.getAttribute('aria-label')).toBe('Code')
    await act(async () => buttons.find(b => b.getAttribute('aria-label') === 'Goals')!.click())
    expect(app.setShowSchedulesOverview).toHaveBeenCalledWith(false)
    expect(app.setAdminPage).toHaveBeenCalledWith(null)
    expect(app.setModeCategory).toHaveBeenCalledWith('workflow')
    expect(app.setShowWorkflowsOverview).toHaveBeenCalledWith(true)
  })
  it('keeps the account product allowlist applied to the direct buttons', async () => {
    auth.allowed = ['work']
    await render()
    expect(Array.from(host.querySelectorAll('button')).map(b => b.getAttribute('aria-label'))).toEqual(['Crew'])
  })
  it('hides Vault when the deployment has no gateway', async () => {
    window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['agentworks', 'work', 'code', 'mcp-gateway'] }
    await render()
    expect(host.querySelector('button[aria-label="Vault"]')).toBeNull()
  })
})
