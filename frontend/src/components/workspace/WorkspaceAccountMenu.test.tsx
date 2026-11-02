// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { createPortal } from 'react-dom'
import { afterEach, expect, it, vi } from 'vitest'
const auth = vi.hoisted(() => ({ user: { id: 'member', username: 'Member', can_create: true, is_admin: false, allowed_products: null } as any, isMultiUserMode: true, providers: [{ type: 'oauth' }], logout: vi.fn() }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select?: any) => select ? select(auth) : auth, { getState: () => auth }) }))
vi.mock('../topbar/ChangePasswordDialog', () => ({ default: () => null }))
vi.mock('../BrowserConnectionsDialog', () => ({ default: ({ open }: { open: boolean }) => open ? createPortal(<div role="dialog" aria-label="Browser connections" />, document.body) : null }))
import { WorkspaceAccountMenu } from './WorkspaceAccountMenu'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { TooltipProvider } from '../ui/tooltip'
import { WORKSPACE_SETTINGS_PATHS } from '../../utils/workspaceSettingsRoutes'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null, settingsTarget: null, compactSettingsTarget: null })
  auth.user = { id: 'member', username: 'Member', can_create: true, is_admin: false, allowed_products: null }
  auth.isMultiUserMode = true; auth.providers = [{ type: 'oauth' }]
  window.history.replaceState({}, '', '/')
  delete window.__APP_RUNTIME_CONFIG__
  vi.clearAllMocks()
})
async function render(check: (host: HTMLDivElement, destination: HTMLDivElement) => Promise<void>, sidebar = true) {
  const host = document.createElement('div'), destination = document.createElement('div')
  document.body.append(host, destination)
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: sidebar ? destination : null })
  const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><WorkspaceAccountMenu /></TooltipProvider>))
    await check(host, destination)
  } finally { await act(async () => root.unmount()); host.remove(); destination.remove() }
}
it.each([false, true])('keeps account administration without walkthroughs and with existing admin gates (admin=%s)', async admin => {
  auth.user.is_admin = admin
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['work', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  await render(async (host, destination) => {
    expect(host.querySelector('[aria-label^="Account:"]')).toBeNull()
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="Account & settings"]')!.click())
    const menu = destination.querySelector<HTMLElement>('[role="menu"]')!
    expect(menu.hidden).toBe(false)
    expect(menu.querySelector('[aria-label="Users and access"]') !== null).toBe(admin)
    expect(menu.querySelector('[aria-label="My vaults"]')).toBeNull()
    for (const label of ['Workspace', 'Sign out']) expect(menu.textContent).toContain(label)
    for (const label of ['Getting started', 'Browser connections', 'Notifications & sound', 'Tips while working']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Change password')
    expect(menu.querySelector('[data-product-option]')).toBeNull()
    for (const label of ['Dashboards', 'Switch product', 'Audit logs', 'Vault MCP endpoint', 'Search']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Keyboard shortcuts')
    expect(menu.textContent).not.toContain('Help & walkthrough')
    expect(menu.querySelector('[aria-label="Connect AI tools"]')).toBeNull()
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="AI models & accounts"]')!.click())
    expect(menu.hidden).toBe(true)
  })
})
it('retains password eligibility and Run restrictions', async () => {
  auth.providers = [{ type: 'credentials' }]; auth.user.can_create = false
  await render(async (_host, destination) => {
    expect(destination.textContent).toContain('Change password')
    expect(destination.querySelector('[aria-label="My vaults"]')).toBeNull()
    expect(destination.querySelector('[aria-label="Users and access"]')).toBeNull()
  })
})
it('retains the explicit browser-setup event without a menu entry', async () => {
  await render(async (_host, destination) => {
    await act(async () => window.dispatchEvent(new Event('open-browser-connections')))
    expect(destination.querySelector<HTMLElement>('[role="menu"]')!.hidden).toBe(true)
    expect(document.querySelector('[role="dialog"][aria-label="Browser connections"]')).not.toBeNull()
  })
})
it('keeps a bottom-left entry with Escape dismissal when the sidebar is collapsed', async () => {
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['work', 'agentworks', 'relays', 'knowledgebase', 'bullpen', 'mcp-gateway'], gatewayUrl: 'https://example.com' }
  auth.user.allowed_products = ['work', 'bullpen']
  await render(async host => {
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Account & settings"]')!
    expect(trigger.parentElement?.className).toContain('bottom-3 left-3')
    await act(async () => trigger.click())
    const menu = host.querySelector<HTMLElement>('[role="menu"]')!
    expect(menu.hidden).toBe(false)
    expect(menu.className).toContain('left-full')
    expect([...menu.querySelectorAll('[data-product-option]')].map(item => item.getAttribute('aria-label'))).toEqual(['Crew', 'AgentBridge', 'Dashboards', 'CLI / MCP'])
    expect(useProductNavigationToolbarStore.getState().sidebarSettingsTarget).toBeNull()
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(menu.hidden).toBe(true)
    expect(document.activeElement).toBe(trigger)
    await act(async () => trigger.click())
    await act(async () => menu.querySelector<HTMLButtonElement>('[aria-label="Dashboards"]')!.click())
    expect(window.location.pathname).toBe('/dashboards')
    expect(menu.hidden).toBe(true)
    await act(async () => trigger.click())
    await act(async () => menu.querySelector<HTMLButtonElement>('[aria-label="CLI / MCP"]')!.click())
    expect(window.location.pathname).toBe(WORKSPACE_SETTINGS_PATHS.mcp)
    expect(menu.hidden).toBe(true)
  }, false)
})
it('keeps local identity without password and sign-out actions', async () => {
  auth.isMultiUserMode = false; auth.providers = [{ type: 'credentials' }]
  await render(async (_host, destination) => {
    expect(destination.textContent).toContain('Local account')
    expect(destination.textContent).not.toContain('Change password')
    expect(destination.textContent).not.toContain('Sign out')
  })
})
it.each([true, false])('shows the Mac download only on the website (desktop=%s)', async desktop => {
  // PLAT-746-302: download metadata requests must never carry server/account credentials.
  const fetchRelease = vi.fn(async () => ({ ok: true, json: async () => ({ tag_name: 'v1.2.3', assets: [{ name: 'AgentWorks-1.2.3-arm64.dmg', browser_download_url: 'https://example.invalid/untrusted' }] }) }))
  vi.stubGlobal('fetch', fetchRelease)
  if (desktop) window.electronAPI = { getDesktopConnection: () => 'local' } as never
  try {
    await render(async host => {
      const button = host.querySelector<HTMLButtonElement>('[aria-label="Get the Mac app"]')
      expect(Boolean(button)).toBe(!desktop)
      if (!button) return
      expect(fetchRelease).not.toHaveBeenCalled()
      await act(async () => button.click())
      expect(fetchRelease).toHaveBeenCalledWith('https://api.github.com/repos/manishiitg/agentworks/releases/latest', expect.objectContaining({ credentials: 'omit' }))
      const panel = host.querySelector('[role="dialog"]')!
      expect(panel.querySelector('a')?.href).toBe('https://github.com/manishiitg/agentworks/releases/download/v1.2.3/AgentWorks-1.2.3-arm64.dmg')
      await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
      expect(host.querySelector('[role="dialog"]')).toBeNull()
      expect(document.activeElement).toBe(button)
    }, false)
  } finally { delete window.electronAPI; vi.unstubAllGlobals() }
})
