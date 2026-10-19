// @vitest-environment happy-dom
import React, { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterAll, afterEach, expect, it, vi } from 'vitest'
vi.hoisted(() => vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {}, removeItem: () => {} }))
const auth = vi.hoisted(() => ({ user: { id: 'alice', is_admin: false, can_create: true, allowed_products: null } as any }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select(auth) }))
import { ProductSettingsMenu } from './ProductSettingsMenu'
import { WorkspaceToolsMenu } from './WorkspaceToolsMenu'
import { WorkspaceTopToolbar } from './WorkspaceTopToolbar'
import { ProductTopBar } from './ProductTopBar'
import { TopBarEntitySelector } from '../topbar/TopBarEntitySelector'
vi.mock('./WorkspaceChatsSidebar', () => ({ default: () => null }))
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import ProvidersControl from '../topbar/ProvidersControl'
import McpControl from '../topbar/McpControl'
import UsersControl from '../topbar/UsersControl'
import VaultsControl from '../topbar/VaultsControl'
import { TooltipProvider } from '../ui/tooltip'
import { useAppStore } from '../../stores/useAppStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { visibleProductSurfaceIDs } from '../../products/productSurfaceConfig'
import type { ProductSurface } from '../../products/productSurfaceConfig'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterAll(() => vi.unstubAllGlobals())
afterEach(() => { delete window.__APP_RUNTIME_CONFIG__; auth.user = { id: 'alice', is_admin: false, can_create: true, allowed_products: null }; useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null }) })
it.each([false, true])('keeps existing permission gates inside the labeled menu (admin=%s)', async admin => {
  auth.user.is_admin = admin
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><ProductSettingsMenu><ProvidersControl /><McpControl /><VaultsControl /><UsersControl /></ProductSettingsMenu></TooltipProvider>))
    const menu = host.querySelector<HTMLElement>('[role="menu"]')!
    expect(menu.hidden).toBe(true)
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Settings"]')!.click())
    expect(menu.hidden).toBe(false)
    expect(menu.textContent).toContain('AI models & accounts')
    expect(menu.textContent).toContain('Connect AI tools')
    expect(menu.querySelector('[aria-label="Users and access"]') !== null).toBe(admin)
    expect(menu.querySelector('[aria-label="My vaults"]')).not.toBeNull()
    await act(async () => menu.querySelector<HTMLButtonElement>('[aria-label="Connect an AI agent (MCP)"]')!.click())
    expect(useAppStore.getState().adminPage).toBe('mcp')
    expect(menu.hidden).toBe(true)
  } finally { await act(async () => root.unmount()); host.remove() }
})
it('keeps personal accounts available without shared Vault access, and supports keyboard dismissal', async () => {
  auth.user.allowed_products = ['work']
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><ProductSettingsMenu><VaultsControl /><UsersControl /></ProductSettingsMenu></TooltipProvider>))
    expect(host.querySelector('[aria-label="My vaults"]')).not.toBeNull()
    expect(visibleProductSurfaceIDs(auth.user.allowed_products)).not.toContain('mcp-gateway')
    expect(host.querySelector('[aria-label="Users and access"]')).toBeNull()
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Settings"]')!
    await act(async () => trigger.click())
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(trigger)
    auth.user.can_create = false
    await act(async () => root.render(<TooltipProvider><ProductSettingsMenu><VaultsControl /><UsersControl /></ProductSettingsMenu></TooltipProvider>))
    expect(host.querySelector('[aria-label="My vaults"]')).toBeNull()
    expect(host.querySelector('[aria-label="Users and access"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})

it.each((['agentworks', 'work', 'code', 'relays'] as ProductSurface[]).flatMap(surface => [false, true].map(admin => ({ surface, admin }))))('shares Setup tiles in $surface with permission gates (admin=$admin)', async ({ surface, admin }) => {
  useProductSurfaceStore.setState({ productSurface: surface })
  auth.user.is_admin = admin
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  useProductNavigationToolbarStore.setState({ target: host, settingsTarget: null })
  function Picker() {
    const [open, setOpen] = useState(false)
    return <TopBarEntitySelector label="Company CA" placeholder="Choose a Crew member" open={open} onToggle={() => setOpen(value => !value)} onClose={() => setOpen(false)}><div>Existing workspaces</div></TopBarEntitySelector>
  }
  try {
    await act(async () => root.render(<TooltipProvider><ProductTopBar><Picker /><ProductSettingsMenu><ProvidersControl /><McpControl /><VaultsControl /><UsersControl /></ProductSettingsMenu></ProductTopBar><WorkspaceToolsMenu>{close => <button role="menuitem" onClick={close}>Memory</button>}</WorkspaceToolsMenu></TooltipProvider>))
    expect(host.querySelector('button[aria-label="Settings"]')).toBeNull()
    const tools = host.querySelector<HTMLElement>('[role="menu"][aria-label="More workspace tools"]')!
    expect(tools.querySelector('[data-product-navigation-action]')).toBeNull()
    const picker = host.querySelector<HTMLElement>('.preset-dropdown')!
    const setup = picker.querySelector<HTMLElement>('[data-picker-setup]')!
    expect(picker.querySelector('[aria-label="Products"]')!.compareDocumentPosition(setup) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const mcp = setup.querySelector<HTMLButtonElement>('[aria-label="Connect an AI agent (MCP)"]')!
    expect(setup.querySelector('[aria-label="Users and access"]') !== null).toBe(admin)
    expect(setup.querySelector('[aria-label="My vaults"]')).not.toBeNull()
    expect(setup.querySelector('[aria-label="Setup"]')?.className).toContain('grid-cols-2')
    expect(setup.textContent).toContain('AI models & accounts')
    expect(setup.textContent).toContain('CLI, Cowork & ChatGPT')
    expect(picker.hidden).toBe(true)
    await act(async () => window.dispatchEvent(new Event('open-product-settings')))
    expect(picker.hidden).toBe(false)
    mcp.focus()
    await act(async () => mcp.dispatchEvent(new KeyboardEvent('keydown', {key:'ArrowUp',bubbles:true})))
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Providers')
    await act(async () => mcp.click())
    expect(useAppStore.getState().adminPage).toBe('mcp')
    expect(picker.hidden).toBe(true)
    expect(setup.querySelector('[aria-label="Connect an AI agent (MCP)"]')).toBe(mcp)
    await act(async () => window.dispatchEvent(new Event('open-product-settings')))
    expect(picker.hidden).toBe(false)
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',bubbles:true})))
    expect(picker.hidden).toBe(true)
  } finally {
    await act(async () => root.unmount()); host.remove()
    expect(useProductNavigationToolbarStore.getState().settingsTarget).toBeNull()
    useProductNavigationToolbarStore.setState({ target: null })
  }
})

it('closes lifted picker state before Setup navigation reparents controls into a page header', async () => {
  useAppStore.setState({ adminPage: null, showWorkflowsOverview: false, showSchedulesOverview: false })
  useProductNavigationToolbarStore.setState({ target: null, targetKind: null, settingsTarget: null, workspaceTrigger: null })
  function Navigation() {
    const [open, setOpen] = useState(false)
    const page = useAppStore(state => state.adminPage)
    const toolbar = <WorkspaceTopToolbar navigationKind={page ? 'page' : 'workspace'}><span>{page ? 'Connect AI tools' : 'Workspace'}</span></WorkspaceTopToolbar>
    return <><ProductTopBar>
      <TopBarEntitySelector label="Workflow" placeholder="Select Automation" open={open} onToggle={() => setOpen(value => !value)} onClose={() => setOpen(false)}><span>Existing workflows</span></TopBarEntitySelector>
      <ProductSettingsMenu><McpControl /></ProductSettingsMenu>
    </ProductTopBar>{page ? <header>{toolbar}</header> : toolbar}</>
  }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipProvider><Navigation /></TooltipProvider>))
    await act(async () => window.dispatchEvent(new Event('open-product-settings')))
    expect(host.querySelector<HTMLElement>('.preset-dropdown')?.hidden).toBe(false)
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Connect an AI agent (MCP)"]')!.click())
    expect(useProductNavigationToolbarStore.getState().targetKind).toBe('page')
    expect(host.querySelector<HTMLElement>('.preset-dropdown')?.hidden).toBe(true)
    expect(host.querySelector('header [aria-label="Product navigation"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})

it.each((['agentworks', 'work', 'code', 'relays'] as ProductSurface[]).flatMap(surface => [false, true].map(admin => ({ surface, admin }))))('keeps bottom Setup permission-filtered in $surface (admin=$admin)', async ({ surface, admin }) => {
  auth.user.is_admin = admin
  useProductSurfaceStore.setState({ productSurface: surface })
  window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'work', 'code', 'relays', 'mcp-gateway'], gatewayUrl: 'http://127.0.0.1:18136' }
  const destination = document.createElement('div'); document.body.append(destination)
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: destination })
  function Picker() {
    const [open, setOpen] = useState(false)
    return <TopBarEntitySelector label="My workspace" placeholder="Choose workspace" open={open} onToggle={() => setOpen(value => !value)} onClose={() => setOpen(false)}><span>Existing workspaces</span></TopBarEntitySelector>
  }
  try {
    await act(async () => root.render(<TooltipProvider><ProductTopBar><Picker /><ProductSettingsMenu><ProvidersControl /><McpControl /><VaultsControl /><UsersControl /></ProductSettingsMenu></ProductTopBar></TooltipProvider>))
    expect(host.querySelector('[data-picker-setup]')).toBeNull()
    expect(destination.querySelector('[aria-label="Users and access"]') !== null).toBe(admin)
    expect(destination.querySelector('[aria-label="My vaults"]')).not.toBeNull()
    const menu = destination.querySelector<HTMLElement>('[role="menu"]')!
    const picker = host.querySelector<HTMLElement>('.preset-dropdown')!
    await act(async () => window.dispatchEvent(new Event('open-product-settings')))
    expect(menu.hidden).toBe(false)
    expect(picker.hidden).toBe(true)
    expect(menu.className).toContain('bottom-full')
    await act(async () => destination.querySelector<HTMLButtonElement>('[aria-label="Connect an AI agent (MCP)"]')!.click())
    expect(useAppStore.getState().adminPage).toBe('mcp')
    expect(menu.hidden).toBe(true)
    await act(async () => useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null }))
    expect(destination.textContent).toBe('')
    expect(host.querySelector('[data-picker-setup]')).not.toBeNull()
    await act(async () => window.dispatchEvent(new Event('open-product-settings')))
    expect(picker.hidden).toBe(false)
  } finally {
    await act(async () => root.unmount()); host.remove(); destination.remove()
    useProductNavigationToolbarStore.setState({ sidebarSettingsTarget: null })
  }
})
