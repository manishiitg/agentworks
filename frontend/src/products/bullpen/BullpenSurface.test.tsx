// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.hoisted(() => {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
})
const auth = vi.hoisted(() => ({ user: { id: 'person', role: 'user', allowed_products: ['bullpen'] } }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select(auth) }))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: Object.assign((select: any) => select({ adminPage: null }), { getState: () => ({ setAdminPage: vi.fn() }) }) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: any) => select({ showLLMModal: false }) }))
vi.mock('../../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: (select: any) => select({ productSurface: 'bullpen' }) }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../hooks/useProductUrlView', () => ({ useProductUrlView: vi.fn() }))
vi.mock('../../components/ModePresetBar', () => ({ ModePresetBar: () => null }))
vi.mock('../../components/topbar/LlmModalHost', () => ({ default: () => null }))
vi.mock('./AgentMessagingSetupPanel', () => ({ AgentMessagingSetupPanel: () => <p>Existing connection setup</p> }))
vi.mock('./AdminPanel', () => ({ AdminPanel: () => <p>Existing administration</p> }))
vi.mock('./HowBullpenWorks', () => ({ HowBullpenWorks: () => null }))
vi.mock('./BullpenUsage', () => ({ BullpenUsageOverview: () => null }))
vi.mock('../../services/bullpenApi', () => ({ bullpenApi: { notices: async () => [], agents: async () => [], agentUsage: async () => [] }, bullpenError: (error: Error) => error.message }))
import { BullpenSurface } from './BullpenSurface'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { delete window.__APP_RUNTIME_CONFIG__; auth.user.role = 'user'; window.localStorage?.removeItem('bullpen:view') })

it.each(['user', 'admin'])('uses only header navigation and preserves AgentBridge administration access (%s)', async role => {
  auth.user.role = role
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: ['bullpen', 'work'] }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<BullpenSurface />))
    expect(host.querySelector('[aria-label="AgentBridge views"]')).toBeNull()
    expect(host.querySelector('[aria-label="Workspace context"]')).toBeNull()
    const open = async () => act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Switch product: AgentBridge"]')!.click())
    await open()
    const pages = host.querySelector('[aria-label="Product pages"]')!
    expect(pages.textContent).toContain('Agents')
    expect(pages.textContent?.includes('Administration')).toBe(role === 'admin')
    expect(pages.textContent).not.toContain('Help')
    expect(pages.textContent).toContain('Token usage')
    await act(async () => [...pages.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Connect an agent')!.click())
    expect(host.textContent).toContain('Existing connection setup')
    expect(host.querySelector('[data-product-picker-menu]')).toBeNull()
    if (role === 'admin') {
      await open()
      await act(async () => [...host.querySelectorAll<HTMLButtonElement>('[aria-label="Product pages"] button')].find(button => button.textContent === 'Administration')!.click())
      expect(host.textContent).toContain('Existing administration')
    }
  } finally { await act(async () => root.unmount()); host.remove() }
})
