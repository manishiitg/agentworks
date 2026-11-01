// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const values = new Map<string, string>()
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, String(value)) }, removeItem: (key: string) => { values.delete(key) }, clear: () => values.clear(), key: (index: number) => [...values.keys()][index] ?? null, get length() { return values.size } }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  Object.defineProperty(window, 'localStorage', { configurable: true, value: storage })
})
vi.mock('../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn((surface: string, options?: { keepHome?: boolean }) => { if (options?.keepHome) return; window.history.pushState({}, '', `/${surface === 'work' ? 'crew' : surface === 'agentworks' ? 'goals' : surface}`) }) }))
vi.mock('./WelcomeAiReadiness', () => ({ default: ({ onSetup, crewReadyView }: { onSetup: () => void; crewReadyView?: unknown }) => readiness.crewReady ? crewReadyView : <button onClick={onSetup}>Review AI setup</button> }))
vi.mock('../services/api', async importOriginal => ({ ...await importOriginal<typeof import('../services/api')>(), default: { post: vi.fn() } }))
vi.mock('./HomeRecentWork', () => ({ HomeRecentWork: () => <section>Recent work</section> }))
const readiness = vi.hoisted(() => ({ crewReady: false }))

import { WelcomeHome, WELCOME_HOME_DISMISSED_KEY } from './WelcomeHome'
import { useAuthStore } from '../stores/useAuthStore'
import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useCommandDialogStore } from '../stores/useCommandDialogStore'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { useProductNavigationToolbarStore } from '../stores/useProductNavigationToolbarStore'
import api from '../services/api'
import { productRoutePath } from '../utils/productRoutes'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
const allProducts = ['work', 'agentworks', 'relays', 'knowledgebase', 'mcp-gateway', 'bullpen', 'llm-gateway']
function configure(allowed = allProducts) {
  window.__APP_RUNTIME_CONFIG__ = { appName: 'Acme Agents', gatewayUrl: 'http://localhost:18136', enabledProductSurfaces: allProducts } as never
  useAuthStore.setState({ user: { id: 'person', allowed_products: allowed, can_create: false } as never, isMultiUserMode: true })
}
async function renderWelcome(autoOpen = true) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  await act(async () => root.render(<WelcomeHome autoOpen={autoOpen} />))
  return host
}
function button(host: HTMLElement, name: string) {
  const match = [...host.querySelectorAll('button')].find(item => item.getAttribute('aria-label') === name || item.textContent?.trim() === name)
  expect(match, name).toBeDefined()
  return match as HTMLButtonElement
}

describe('WelcomeHome', () => {
  afterEach(() => {
    cleanups.splice(0).forEach(cleanup => cleanup())
    window.localStorage.clear()
    window.history.replaceState({}, '', '/')
    delete window.__APP_RUNTIME_CONFIG__
    useAuthStore.setState({ user: null, isMultiUserMode: false })
    useCommandDialogStore.getState().closeAll()
    useAppStore.setState({ adminPage: null })
    useLLMStore.setState({ showLLMModal: false })
    useProductNavigationToolbarStore.setState({ sidebarWidth: 0 })
    readiness.crewReady = false
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  it('shows only the AgentWorks chat when Crew is ready and sends the message for the server to deliver', async () => {
    configure()
    readiness.crewReady = true
    const ready = await renderWelcome()
    expect(ready.textContent).not.toContain('Review AI setup')
    vi.spyOn(window.location, 'assign').mockImplementation(() => {})
    vi.mocked(api.post).mockResolvedValue({ data: { crew_id: 'crew-1' } })
    const box = ready.querySelector<HTMLTextAreaElement>('#welcome-agentworks-message')!
    expect(box.placeholder).toBe('Tell AgentWorks what you want to get done')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(box, '  Plan my week  ')
      box.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
    expect(api.post).toHaveBeenCalledWith('/api/agentworks', { message: 'Plan my week' })
    expect(window.location.pathname).toBe(productRoutePath({ surface: 'work', id: 'crew-1' }))
  })

  it('shows only permitted products and remembers dismissal', async () => {
    configure(['work', 'mcp-gateway'])
    const host = await renderWelcome()
    expect(host.textContent).toContain('Acme Agents')
    expect(host.querySelectorAll('[data-welcome-product]')).toHaveLength(1)
    expect(host.textContent).not.toContain('Goals')
    await act(async () => button(host, 'Open Vault').click())
    expect(openProductWorkspace).toHaveBeenCalledWith('mcp-gateway')
    expect(window.localStorage.getItem(WELCOME_HOME_DISMISSED_KEY)).toBe('true')
    expect(host.querySelector('main[aria-label="Home"]')).toBeNull()
    const returning = await renderWelcome()
    expect(returning.querySelector('main[aria-label="Home"]')).toBeNull()
  })

  it('orders the cards Crew first and disables creation for Run-only users', async () => {
    configure()
    const host = await renderWelcome()
    expect([...host.querySelectorAll('[data-welcome-product]')].map(item => item.getAttribute('data-welcome-product'))).toEqual(['work', 'agentworks', 'relays'])
    expect([...host.querySelectorAll<HTMLButtonElement>('[aria-label^="Create a"]')].every(item => item.disabled)).toBe(true)
    expect(host.querySelector('[aria-label="Direct product creation"] [aria-label^="Open"]')).toBeNull()
  })

  it.each([
    ['work', 'Create a Crew'], ['agentworks', 'Create a Goal'], ['relays', 'Create a Flow'],
  ])('opens the existing %s creation flow only with its product permission', async (surface, label) => {
    configure()
    useAuthStore.setState({ user: { ...useAuthStore.getState().user, can_create_in: { [surface]: true } } as never })
    const host = await renderWelcome()
    expect(host.querySelectorAll('[aria-label^="Create a"]:not(:disabled)')).toHaveLength(1)
    await act(async () => button(host, label).click())
    expect(openProductWorkspace).toHaveBeenCalledWith(surface, { keepHome: true })
    expect(useCommandDialogStore.getState().productCreateSurface).toBe(surface)
    expect(host.querySelector('main[aria-label="Home"]')).not.toBeNull()
  })

  it('uses the existing Crew create permission and request for Studio', async () => {
    configure()
    const host = await renderWelcome()
    expect(button(host, 'Create Studio').disabled).toBe(true)
    await act(async () => useAuthStore.setState({ user: { ...useAuthStore.getState().user, can_create_in: { work: true } } as never }))
    await act(async () => button(host, 'Create Studio').click())
    expect(useCommandDialogStore.getState().productCreateSurface).toBe('work')
    expect(useCommandDialogStore.getState().productCreateIntent).toBe('studio')
  })

  it('keeps existing work in Recent work and removes Open from creation cards', async () => {
    configure()
    const host = await renderWelcome()
    expect(host.textContent).toContain('Recent work')
    expect(host.querySelector('[aria-label="Direct product creation"] [aria-label^="Open"]')).toBeNull()
  })

  it('shows permitted shared tools without a disclosure and preserves other services', async () => {
    configure(['work', 'mcp-gateway', 'bullpen', 'llm-gateway'])
    const host = await renderWelcome()
    const services = host.querySelector('[aria-label="Shared services"]')!
    expect(services.closest('details')).toBeNull()
    expect(services.textContent).toContain('Vault')
    expect(services.textContent).toContain('AgentBridge')
    expect(services.textContent).toContain('LLM Gateway')
    expect(services.textContent).not.toContain('Brain')
    await act(async () => button(host, 'Open Vault').click())
    expect(openProductWorkspace).toHaveBeenCalledWith('mcp-gateway')
  })

  it('shows shared tools even for accounts without workspace products', async () => {
    configure(['mcp-gateway', 'knowledgebase'])
    const host = await renderWelcome()
    expect(host.querySelector('[data-welcome-product]')).toBeNull()
    const services = host.querySelector('[aria-label="Shared services"]')!
    expect(services.textContent!.indexOf('Brain')).toBeLessThan(services.textContent!.indexOf('Vault'))
    expect(host.querySelector('a[href="/agentbridge/connect"]')).toBeNull()
  })

  it('uses the account card and app setup without duplicate footer links', async () => {
    configure()
    const host = await renderWelcome()
    expect(host.querySelector('code')).toBeNull()
    expect(host.querySelector('footer')).toBeNull()
    expect(host.textContent).not.toContain('CLI & agent messaging')
    await act(async () => button(host, 'Connect your AI app').click())
    expect(useAppStore.getState().adminPage).toBe('mcp')
    expect(window.location.pathname).toBe('/settings/ai-tools')
    expect(host.querySelector('[role="dialog"]')).toBeNull()
    expect(host.querySelector('main')).toBeNull()
    window.history.replaceState({}, '', '/home')
    await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
    await act(async () => button(host, 'Review AI setup').click())
    expect(window.location.pathname).toBe('/settings/ai-accounts')
    expect(useLLMStore.getState().showLLMModal).toBe(true)
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  })

  it('stays on Home while opening search, and leaves when a destination is selected', async () => {
    configure()
    const host = await renderWelcome()
    await act(async () => window.dispatchEvent(new Event('quick-switcher-opened')))
    expect(host.querySelector('main[aria-label="Home"]')).not.toBeNull()
    window.history.pushState({}, '', '/crew')
    await act(async () => window.dispatchEvent(new Event('product-navigation-changed')))
    expect(host.querySelector('main[aria-label="Home"]')).toBeNull()
  })

  it('is a directly addressable page without modal close or workspace return controls', async () => {
    configure()
    window.history.replaceState({}, '', '/home')
    const host = await renderWelcome(false)
    expect(host.querySelector('main[aria-label="Home"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Close welcome"]')).toBeNull()
    expect(host.textContent).not.toContain('Back to workspace')
    expect(host.querySelector('[aria-modal]')).toBeNull()
    await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
    expect(window.location.pathname).toBe('/home')
    expect(host.querySelector('main[aria-label="Home"]')).not.toBeNull()
  })

  it('skips a single-product greeting but allows Getting started to reopen it', async () => {
    configure(['work'])
    const host = await renderWelcome()
    expect(host.querySelector('main[aria-label="Home"]')).toBeNull()
    await act(async () => window.dispatchEvent(new Event('open-welcome-home')))
    expect(host.querySelector('main[aria-label="Home"]')).not.toBeNull()
    await act(async () => useProductNavigationToolbarStore.setState({ sidebarWidth: 300 }))
    const guide = host.querySelector<HTMLElement>('main[aria-label="Home"]')!
    expect(guide.style.getPropertyValue('--welcome-sidebar-width')).toBe('300px')
    expect(guide.getAttribute('aria-modal')).toBeNull()
    const sidebar = document.createElement('div')
    sidebar.setAttribute('data-product-navigation-mode', 'fixed')
    const project = document.createElement('button')
    sidebar.append(project); document.body.append(sidebar)
    await act(async () => project.click())
    sidebar.remove()
    expect(host.querySelector('main[aria-label="Home"]')).not.toBeNull()
    expect(window.location.pathname).toBe('/home')
    window.history.pushState({}, '', '/crew')
    await act(async () => window.dispatchEvent(new Event('product-navigation-changed')))
    expect(host.querySelector('main[aria-label="Home"]')).toBeNull()
  })

  it('updates visible choices when permissions change while open', async () => {
    configure()
    const host = await renderWelcome()
    await act(async () => useAuthStore.setState({ user: { allowed_products: ['knowledgebase'] } as never }))
    expect(host.querySelector('[data-welcome-product]')).toBeNull()
    expect(host.querySelectorAll('[aria-label="Shared services"] button')).toHaveLength(1)
    expect(button(host, 'Open Brain')).toBeDefined()
  })
})
