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

import { WelcomeHome, WELCOME_HOME_DISMISSED_KEY } from './WelcomeHome'
import { useAuthStore } from '../stores/useAuthStore'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'

describe('WelcomeHome', () => {
  afterEach(() => { document.body.innerHTML = ''; window.localStorage.clear(); delete (window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ })

  it('shows only the products this person may open, once', async () => {
    ;(window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { appName: 'Acme Agents', enabledProductSurfaces: ['agentworks', 'work', 'code', 'video-studio'] }
    useAuthStore.setState({ user: { allowed_products: ['code', 'work', 'video-studio'] } } as never)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<WelcomeHome />))
    expect(host.textContent).toContain('Welcome to Acme Agents')
    expect(host.textContent).toContain('/api/external/v1/mcp')
    const cards = Array.from(host.querySelectorAll('section[aria-labelledby="welcome-products"] button')).map(b => b.textContent)
    expect(cards).toHaveLength(1)
    expect(cards.some(t => t?.includes('Crew'))).toBe(true)
    expect(cards.some(t => t?.includes('Goals'))).toBe(false)
    // A personal product is enabled for this person but never named in the shared welcome.
    expect(host.textContent).not.toContain('Video Studio')
    await act(async () => (Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Crew')) as HTMLButtonElement).click())
    expect(openProductWorkspace).toHaveBeenCalledWith('code')
    expect(window.localStorage.getItem(WELCOME_HOME_DISMISSED_KEY)).toBe('true')
    expect(host.querySelector('[role="dialog"]')).toBeNull()
    await act(async () => root.unmount())
  })

  it('does not greet someone whose account opens a single product, but the menu can still open it', async () => {
    ;(window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { appName: 'Acme Agents', enabledProductSurfaces: ['agentworks', 'work', 'code'] }
    useAuthStore.setState({ user: { allowed_products: ['code'] } } as never)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<WelcomeHome />))
    expect(host.querySelector('[role="dialog"]')).toBeNull()
    await act(async () => { window.dispatchEvent(new Event('open-welcome-home')) })
    expect(host.querySelector('[role="dialog"]')).not.toBeNull()
    await act(async () => root.unmount())
  })

  it('keeps permitted services secondary to workspaces and excludes inaccessible products', async () => {
    ;(window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { gatewayUrl: 'http://localhost:18136', enabledProductSurfaces: ['work', 'mcp-gateway', 'knowledgebase', 'bullpen'] }
    useAuthStore.setState({ user: { allowed_products: ['work', 'mcp-gateway', 'bullpen'] } } as never)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<WelcomeHome />))
    const workspaces = host.querySelector('section[aria-labelledby="welcome-products"]')
    expect(workspaces?.textContent).toContain('Crew')
    expect(workspaces?.textContent).not.toContain('Vault')
    const services = host.querySelector('details')!
    expect(services.open).toBe(false)
    expect(services.textContent).toContain('Vault')
    expect(services.textContent).toContain('AgentBridge')
    expect(services.textContent).not.toContain('Brain')
    await act(async () => root.unmount())
  })

  it('opens shared services immediately when there are no workspace products', async () => {
    ;(window as { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__ = { gatewayUrl: 'http://localhost:18136', enabledProductSurfaces: ['mcp-gateway', 'knowledgebase'] }
    useAuthStore.setState({ user: { allowed_products: ['mcp-gateway', 'knowledgebase'] } } as never)
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    await act(async () => root.render(<WelcomeHome />))
    expect(host.querySelector('section[aria-labelledby="welcome-products"]')).toBeNull()
    expect(host.querySelector('details')?.open).toBe(true)
    expect(host.querySelector('[aria-label="Shared services"]')?.textContent).toContain('Vault')
    await act(async () => root.unmount())
  })
})
