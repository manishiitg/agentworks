// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { createPortal } from 'react-dom'
import { afterEach, expect, it, vi } from 'vitest'
const auth = vi.hoisted(() => ({ user: { id: 'member', username: 'Member', can_create: true, is_admin: false, allowed_products: null } as any, isMultiUserMode: true, providers: [{ type: 'oauth' }], logout: vi.fn() }))
const product = vi.hoisted(() => ({ productSurface: 'work' }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: Object.assign((select?: any) => select ? select(auth) : auth, { getState: () => auth }) }))
vi.mock('../../stores/useProductSurfaceStore', () => ({ useProductSurfaceStore: Object.assign((select: any) => select(product), { getState: () => product }) }))
vi.mock('../topbar/ChangePasswordDialog', () => ({ default: () => null }))
vi.mock('../BrowserConnectionsDialog', () => ({ default: ({ open }: { open: boolean }) => open ? createPortal(<div role="dialog" aria-label="Browser connections" />, document.body) : null }))
import { WorkspaceSetupMenu, WorkspaceAccountMenu } from './WorkspaceSetupMenu'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { TooltipProvider } from '../ui/tooltip'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null, settingsTarget: null })
  auth.user = { id: 'member', username: 'Member', can_create: true, is_admin: false, allowed_products: null }
  auth.isMultiUserMode = true; auth.providers = [{ type: 'oauth' }]
  product.productSurface = 'work'
  window.history.replaceState({}, '', '/')
  delete window.__APP_RUNTIME_CONFIG__
  vi.clearAllMocks()
})
async function render(check: (host: HTMLDivElement, destination: HTMLDivElement) => Promise<void>, sidebar = true, onOpenWalkthrough = vi.fn()) {
  const host = document.createElement('div'), destination = document.createElement('div')
  document.body.append(host, destination)
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: sidebar ? destination : null })
  const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><WorkspaceSetupMenu onOpenWalkthrough={onOpenWalkthrough} onOpenShortcuts={vi.fn()} /><WorkspaceAccountMenu /></TooltipProvider>))
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
    expect(menu.querySelector('[aria-label="My vaults"]')).toBeNull()
    for (const label of ['Workspace', 'Help', 'Sign out']) expect(menu.textContent).toContain(label)
    for (const label of ['Getting started', 'Browser connections', 'Notifications & sound', 'Tips while working']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Change password')
    expect(menu.querySelector('[data-product-option]')).toBeNull()
    for (const label of ['Dashboards', 'Switch product', 'Audit logs', 'Vault MCP endpoint', 'Search']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Keyboard shortcuts')
    expect(menu.querySelector('[aria-label="Connect AI tools"]')).toBeNull()
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="AI models & accounts"]')!.click())
    expect(menu.hidden).toBe(true)
  })
})
it('opens usable manual Vault help outside the closed account menu instead of its paused page tour', async () => {
  await import('../workflow/WorkflowWalkthrough')
  product.productSurface = 'mcp-gateway'
  window.history.replaceState({}, '', '/vault')
  const pausedPageTour = vi.fn()
  await render(async (_host, destination) => {
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="Account & settings"]')!.click())
    const menu = destination.querySelector<HTMLElement>('[role="menu"]')!
    const help = Array.from(menu.querySelectorAll('button')).find(button => button.textContent?.includes('Help & walkthrough'))!
    await act(async () => help.click())
    const dialog = document.querySelector<HTMLElement>('[role="dialog"][aria-label="Vault help walkthrough"]')!
    expect(dialog).not.toBeNull()
    expect(menu.hidden).toBe(true)
    expect(menu.contains(dialog)).toBe(false)
    expect(pausedPageTour).not.toHaveBeenCalled()
    expect(dialog.textContent).toContain('Your connected apps and credentials')
    await act(async () => dialog.querySelector<HTMLButtonElement>('[data-testid="workflow-walkthrough-next"]')!.click())
    expect(dialog.textContent).toContain('Connect an app')
    await act(async () => dialog.querySelector<HTMLButtonElement>('[aria-label="Close walkthrough"]')!.click())
    expect(document.querySelector('[role="dialog"][aria-label="Vault help walkthrough"]')).toBeNull()
  }, true, pausedPageTour)
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
