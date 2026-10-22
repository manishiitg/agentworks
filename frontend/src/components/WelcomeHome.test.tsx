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
vi.mock('../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('./integrations/McpConnectDialog', () => ({ default: ({ onClose, onNavigate }: { onClose: () => void; onNavigate: () => void }) => <div role="dialog" aria-label="AI app setup"><button onClick={onClose}>Back to welcome</button><button onClick={onNavigate}>Open AgentBridge</button></div> }))
vi.mock('./WelcomeAiReadiness', () => ({ default: ({ setupOpen, onCloseSetup }: { setupOpen: boolean; onCloseSetup: () => void }) => setupOpen ? <div role="dialog" aria-label="Set up your AI"><button onClick={onCloseSetup}>Back to getting started</button></div> : null }))

import { WelcomeHome, WELCOME_HOME_DISMISSED_KEY } from './WelcomeHome'
import { useAuthStore } from '../stores/useAuthStore'
import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { useCommandDialogStore } from '../stores/useCommandDialogStore'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
const allProducts = ['work', 'agentworks', 'relays', 'knowledgebase', 'mcp-gateway', 'bullpen', 'llm-gateway', 'video-studio']
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
    delete window.__APP_RUNTIME_CONFIG__
    useAuthStore.setState({ user: null, isMultiUserMode: false })
    useCommandDialogStore.getState().closeAll()
    useAppStore.setState({ adminPage: null })
    useLLMStore.setState({ showLLMModal: false })
    vi.clearAllMocks()
  })

  it('shows only permitted products and remembers dismissal', async () => {
    configure(['work', 'video-studio'])
    const host = await renderWelcome()
    expect(host.textContent).toContain('Welcome to Acme Agents')
    expect(host.querySelectorAll('[data-welcome-product]')).toHaveLength(1)
    expect(host.textContent).not.toContain('Goals')
    expect(host.textContent).not.toContain('Video Studio')
    await act(async () => button(host, 'Open Crew').click())
    expect(openProductWorkspace).toHaveBeenCalledWith('work')
    expect(window.localStorage.getItem(WELCOME_HOME_DISMISSED_KEY)).toBe('true')
    expect(host.querySelector('[role="dialog"]')).toBeNull()
    const returning = await renderWelcome()
    expect(returning.querySelector('[role="dialog"]')).toBeNull()
  })

  it('orders the three cards Crew first without exposing creation to Run-only users', async () => {
    configure()
    const host = await renderWelcome()
    expect([...host.querySelectorAll('[data-welcome-product]')].map(item => item.getAttribute('data-welcome-product'))).toEqual(['work', 'agentworks', 'relays'])
    expect(host.querySelectorAll('[aria-label^="Create a"]')).toHaveLength(0)
    expect(button(host, 'Open Crew')).toBeDefined()
    expect(button(host, 'Open Goals')).toBeDefined()
    expect(button(host, 'Open Flows')).toBeDefined()
  })

  it.each([
    ['work', 'Create a Crew'], ['agentworks', 'Create a Goal'], ['relays', 'Create a Flow'],
  ])('opens the existing %s creation flow only with its product permission', async (surface, label) => {
    configure()
    useAuthStore.setState({ user: { ...useAuthStore.getState().user, can_create_in: { [surface]: true } } as never })
    const host = await renderWelcome()
    expect(host.querySelectorAll('[aria-label^="Create a"]')).toHaveLength(1)
    await act(async () => button(host, label).click())
    expect(openProductWorkspace).toHaveBeenCalledWith(surface)
    expect(useCommandDialogStore.getState().productCreateSurface).toBe(surface)
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  })

  it('keeps an existing-workspace path alongside creation', async () => {
    configure()
    useAuthStore.setState({ isMultiUserMode: false })
    const host = await renderWelcome()
    await act(async () => button(host, 'Open existing Crews').click())
    expect(openProductWorkspace).toHaveBeenCalledWith('work')
    expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
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

  it('leads to app selection while retaining account and CLI setup routes', async () => {
    configure()
    const host = await renderWelcome()
    expect(host.querySelector('code')).toBeNull()
    expect(host.querySelector('a')?.getAttribute('href')).toBe('/agentbridge/connect')
    await act(async () => button(host, 'Connect your AI app').click())
    expect(useAppStore.getState().adminPage).toBeNull()
    expect(host.querySelector('[aria-label="AI app setup"]')).not.toBeNull()
    expect(window.localStorage.getItem(WELCOME_HOME_DISMISSED_KEY)).toBeNull()
    await act(async () => button(host, 'Back to welcome').click())
    expect(host.querySelector('[aria-label="AI app setup"]')).toBeNull()
    expect(host.querySelector('h1')?.textContent).toBe('What would you like to do?')
    await act(async () => button(host, 'AI models & accounts').click())
    expect(host.querySelector('[aria-label="Set up your AI"]')).not.toBeNull()
    expect(useLLMStore.getState().showLLMModal).toBe(false)
    await act(async () => button(host, 'Back to getting started').click())
    expect(host.querySelector('[aria-label="Set up your AI"]')).toBeNull()
    expect(host.querySelector('h1')).not.toBeNull()
  })

  it('lets global navigation leave Welcome rather than covering the selected workspace', async () => {
    configure()
    const host = await renderWelcome()
    await act(async () => button(host, 'Connect your AI app').click())
    await act(async () => window.dispatchEvent(new Event('quick-switcher-opened')))
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  })

  it('returns to the current permitted product through the shared workspace opener', async () => {
    configure()
    useProductSurfaceStore.setState({ productSurface: 'work' })
    const host = await renderWelcome()
    await act(async () => button(host, 'Back to workspace').click())
    expect(openProductWorkspace).toHaveBeenCalledWith('work')
    expect(host.querySelector('[role="dialog"]')).toBeNull()
  })

  it('skips a single-product greeting but allows Getting started to reopen it', async () => {
    configure(['work'])
    const host = await renderWelcome()
    expect(host.querySelector('[role="dialog"]')).toBeNull()
    await act(async () => window.dispatchEvent(new Event('open-welcome-home')))
    expect(host.querySelector('[role="dialog"]')).not.toBeNull()
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
