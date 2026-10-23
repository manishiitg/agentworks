// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { createPortal } from 'react-dom'
import { afterEach, expect, it, vi } from 'vitest'
const auth = vi.hoisted(() => ({ user: { id: 'member', username: 'Member', can_create: true, is_admin: false, allowed_products: null } as any, isMultiUserMode: true, providers: [{ type: 'oauth' }], logout: vi.fn() }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select?: any) => select ? select(auth) : auth, { getState: () => auth }) }))
vi.mock('../topbar/ChangePasswordDialog', () => ({ default: () => null }))
vi.mock('../BrowserConnectionsDialog', () => ({ default: ({ open }: { open: boolean }) => open ? createPortal(<div role="dialog" aria-label="Browser connections" />, document.body) : null }))
import { WorkspaceSetupMenu } from './WorkspaceSetupMenu'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { TooltipProvider } from '../ui/tooltip'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null, settingsTarget: null })
  auth.user = { id: 'member', username: 'Member', can_create: true, is_admin: false, allowed_products: null }
  auth.isMultiUserMode = true; auth.providers = [{ type: 'oauth' }]
  delete window.__APP_RUNTIME_CONFIG__
  vi.clearAllMocks()
})
async function render(check: (host: HTMLDivElement, destination: HTMLDivElement) => Promise<void>, sidebar = true) {
  const host = document.createElement('div'), destination = document.createElement('div')
  document.body.append(host, destination)
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: sidebar ? destination : null })
  const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><WorkspaceSetupMenu onOpenWalkthrough={vi.fn()} onOpenShortcuts={vi.fn()} /></TooltipProvider>))
    await check(host, destination)
  } finally { await act(async () => root.unmount()); host.remove(); destination.remove() }
}
it.each([false, true])('combines account and Setup with existing admin gates (admin=%s)', async admin => {
  auth.user.is_admin = admin
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['work', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  await render(async (host, destination) => {
    expect(host.querySelector('[aria-label^="Account:"]')).toBeNull()
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="Account & settings"]')!.click())
    const menu = destination.querySelector<HTMLElement>('[role="menu"]')!
    expect(menu.hidden).toBe(false)
    expect(menu.querySelector('[aria-label="Users and access"]') !== null).toBe(admin)
    expect(menu.querySelector('[aria-label="My vaults"]') !== null).toBe(true) // everyone who can create sees My vaults, admins included
    for (const label of ['Workspace', 'Help', 'Getting started', 'Sign out']) expect(menu.textContent).toContain(label)
    for (const label of ['Browser connections', 'Notifications & sound', 'Tips while working']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Change password')
    expect(menu.querySelector('[data-product-option]')).toBeNull()
    for (const label of ['Dashboards', 'Switch product', 'Audit logs', 'Vault MCP endpoint', 'Search']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Keyboard shortcuts')
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="Connect AI tools"]')!.click())
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
  await render(async host => {
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Account & settings"]')!
    expect(trigger.parentElement?.className).toContain('bottom-3 left-3')
    await act(async () => trigger.click())
    const menu = host.querySelector<HTMLElement>('[role="menu"]')!
    expect(menu.hidden).toBe(false)
    expect(menu.className).toContain('left-full')
    expect(useProductNavigationToolbarStore.getState().sidebarSettingsTarget).toBeNull()
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(menu.hidden).toBe(true)
    expect(document.activeElement).toBe(trigger)
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

it.each([
  { allowed: ['work'], admin: true, reader: false, fallback: true },
  { allowed: ['mcp-gateway'], admin: false, reader: true, fallback: false },
  { allowed: ['mcp-gateway'], admin: false, reader: false, fallback: true },
])('retains personal vault access only when shared Vault navigation is unavailable ($allowed, admin=$admin, reader=$reader)', async ({ allowed, admin, reader, fallback }) => {
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['work', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  Object.assign(auth.user, { allowed_products: allowed, is_admin: admin, is_vault_reader: reader })
  await render(async (_host, destination) => {
    expect(destination.querySelector('[aria-label="My vaults"]') !== null).toBe(fallback)
    expect(destination.querySelector('[data-product-switch-menu]')).toBeNull()
  })
})
