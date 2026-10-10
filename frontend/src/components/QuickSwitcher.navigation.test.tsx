// @vitest-environment happy-dom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

// Persisted stores need a Storage before they are created at import time.
vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (k: string) => memory.get(k) ?? null, setItem: (k: string, v: string) => { memory.set(k, String(v)) }, removeItem: (k: string) => { memory.delete(k) }, clear: () => memory.clear(), key: (i: number) => [...memory.keys()][i] ?? null, get length() { return memory.size } }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})

vi.mock('../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: vi.fn(async () => []) }))

vi.mock('../services/llm-config-api', () => {
  const service = new Proxy({}, { get: (_, key) => vi.fn(async () => key === 'getProviderConnections' ? [] : {}) })
  return { llmConfigService: service, default: service }
})

import { useProductCreateRequest } from '../hooks/useProductCreateRequest'
import { useCommandDialogStore, type ProductCreateSurface } from '../stores/useCommandDialogStore'
import { CreateCodeWorkspaceDialog } from '../products/work/CreateCodeWorkspaceDialog'
import { openProductWorkspace } from '../utils/productWorkspaceNavigation'
import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useAppStore } from '../stores/useAppStore'
import { usePanelSwitcherStore } from '../stores/usePanelSwitcherStore'
import { useLLMStore } from '../stores/useLLMStore'
import { openQuickNavigation, quickNavigationItems } from '../utils/quickNavigation'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach(fn => fn())
  delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__
  useAuthStore.setState({ user: null, isMultiUserMode: false })
  useCommandDialogStore.getState().closeAll()
  usePanelSwitcherStore.setState({ entries: {}, toolbarMinimized: false })
  vi.unstubAllEnvs()
})

async function renderNavigation(query: string, allowed = ['agentworks', 'work', 'code', 'video-studio', 'relays', 'sparkquill', 'mcp-gateway'], admin = true, seed?: () => void) {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: allowed, gatewayUrl: 'http://localhost:99999' } as never
  useAuthStore.setState({ user: { id: 'u1', username: 'u1', is_admin: admin, allowed_products: allowed } as never })
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  useChatStore.setState({ chatTabs: {}, activeTabId: null, activeSessionsCache: [], getActiveSessions: vi.fn(async () => []) })
  useProductSurfaceStore.setState({ productSurface: 'video-studio' })
  seed?.()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  const onClose = vi.fn()
  await act(async () => { root.render(<QuickSwitcher isOpen onClose={onClose} initialQuery={query} />) })
  return { host, onClose }
}

it('defaults to running workflows, Crews, Code and Relays before idle work, with browse and navigation rows at the bottom', async () => {
  const { host } = await renderNavigation('', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'code' })
    useChatStore.setState({ activeTabId: 'idle', chatTabs: {
      idle: { tabId: 'idle', name: 'Idle current', createdAt: 9000, lastAccessedAt: 9000, metadata: { agentProfileId: 'code' } },
      code: { tabId: 'code', name: 'Running Code', createdAt: 1, isStreaming: true, metadata: { agentProfileId: 'code' } },
      crew: { tabId: 'crew', name: 'Crew', createdAt: 2, isSyntheticTurn: true, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Running Crew' } },
    } as never, activeSessionsCache: [
      { session_id: 'workflow-run', agent_mode: 'workflow', status: 'running', preset_query_id: 'workflow' },
      { session_id: 'relay-run', agent_mode: 'workflow', status: 'running', preset_query_id: 'relay' },
    ] as never })
    useGlobalPresetStore.setState({ workflowPresets: [
      { id: 'workflow', label: 'Running workflow', selectedFolder: { filepath: 'Workflow/example' } },
      { id: 'relay', label: 'Running Relay', workflowKind: 'relay', selectedFolder: { filepath: 'Workflow/relay' } },
    ] as never })
  })
  const rows = [...host.querySelectorAll('[data-navigation-id]')]
  expect(rows.length).toBeGreaterThan(5)
  expect(rows.slice(0, 4).map(row => row.textContent)).toEqual(expect.arrayContaining([
    expect.stringContaining('Running workflow'), expect.stringContaining('Running Relay'),
    expect.stringContaining('Running Crew'), expect.stringContaining('Running Code'),
  ]))
  expect(rows[4].textContent).toContain('Idle current')
  expect(rows[0].className).toContain('bg-blue-50')
  expect(rows[5].getAttribute('data-navigation-id')).toBe('browse:active')
  expect(host.querySelector('[data-navigation-id="browse:workflows"]')).not.toBeNull()
  expect(host.querySelector('[data-navigation-id="browse:relays"]')).not.toBeNull()
  expect(host.querySelector('[data-navigation-id="menu:providers"]')).not.toBeNull()
  expect(host.querySelector('[aria-label="Quick navigation shortcuts"]')?.textContent).not.toContain('@')
})

it('keeps a running scheduled Crew visible when its view-only tab has no project row', async () => {
  const { host } = await renderNavigation('', undefined, true, () => {
    const sessionId = 'work:project:crew:trigger:run'
    useChatStore.setState({ chatTabs: {
      scheduled: { tabId: 'scheduled', sessionId, name: 'Scheduled Crew', metadata: { agentProfileId: 'work', isScheduledRun: true, isViewOnly: true } },
    } as never, activeSessionsCache: [{ session_id: sessionId, status: 'running', title: 'Scheduled Crew' }] as never })
  })
  expect(host.querySelector('[data-navigation-id]')?.getAttribute('data-navigation-id')).toBe(`active:work:project:crew:trigger:run`)
  expect(host.querySelector('[data-navigation-id^="active:"]')?.textContent).toContain('Scheduled Crew')
})

it('browses all workflows or Relays from footer icons without requiring typed scopes', async () => {
  const { host, onClose } = await renderNavigation('', undefined, true, () => {
    useGlobalPresetStore.setState({ workflowPresets: [
      { id: 'workflow', label: 'Daily report', selectedFolder: { filepath: 'Workflow/report' } },
      { id: 'relay', label: 'Invoice Relay', workflowKind: 'relay', selectedFolder: { filepath: 'Workflow/invoice' } },
    ] as never })
  })
  const shortcuts = host.querySelector('[aria-label="Quick navigation shortcuts"]')!
  await act(async () => shortcuts.querySelector<HTMLButtonElement>('[aria-label="All workflows"]')!.click())
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['workflow:workflow', 'create:agentworks'])
  expect(host.querySelector('input')!.value).toBe('')
  expect(host.querySelector('[aria-label="Show all work and navigation"]')?.textContent).toContain('All workflows')
  await act(async () => shortcuts.querySelector<HTMLButtonElement>('[aria-label="All Relays"]')!.click())
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['workflow:relay', 'create:relays'])
  expect(onClose).not.toHaveBeenCalled()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Show all work and navigation"]')!.click())
  expect(host.querySelector('[data-navigation-id="browse:crew"]')).not.toBeNull()
})

it.each([
  ['Users and access', 'users'], ['Connect an AI agent (MCP)', 'mcp'], ['Providers', 'providers'],
])('opens %s directly from its footer icon', async (label, destination) => {
  const { host, onClose } = await renderNavigation('')
  useAppStore.setState({ adminPage: null })
  useLLMStore.setState({ showLLMModal: false })
  await act(async () => host.querySelector<HTMLButtonElement>(`[aria-label="Quick navigation shortcuts"] button[aria-label="${label}"]`)!.click())
  if (destination === 'providers') expect(useLLMStore.getState().showLLMModal).toBe(true)
  else expect(useAppStore.getState().adminPage).toBe(destination)
  expect(onClose).toHaveBeenCalled()
})

it.each(['activity', 'schedules'])('opens %s from the scrollable navigation rows', async action => {
  const { host, onClose } = await renderNavigation('')
  useAppStore.setState({ showWorkflowsOverview: false, showSchedulesOverview: false })
  await act(async () => host.querySelector(`[data-navigation-id="menu:${action}"]`)!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('agentworks')
  expect(action === 'activity' ? useAppStore.getState().showWorkflowsOverview : useAppStore.getState().showSchedulesOverview).toBe(true)
  expect(onClose).toHaveBeenCalled()
})

it('shows Vault-specific icons only in Vault and opens its audit page', async () => {
  const { host } = await renderNavigation('', undefined, true, () => useProductSurfaceStore.setState({ productSurface: 'mcp-gateway' }))
  const opened = vi.fn()
  window.addEventListener('vault-open-panel', opened)
  try {
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Quick navigation shortcuts"] button[aria-label="Vault audit logs"]')!.click())
    expect(opened).toHaveBeenCalledWith(expect.objectContaining({ detail: 'audit' }))
    expect(quickNavigationItems(useAuthStore.getState().user, 'code').some(item => item.action === 'vault-audit')).toBe(false)
  } finally { window.removeEventListener('vault-open-panel', opened) }
})

it('lists every available product and opens its workspace from another product', async () => {
  const { host, onClose } = await renderNavigation('@products ')
  expect(host.querySelectorAll('[data-navigation-id^="product:"]')).toHaveLength(7)
  expect(host.textContent).not.toContain('Dominion')
  expect(host.textContent).toContain('Vault')
  expect(host.textContent).toContain('SparkQuill')
  useAppStore.setState({ adminPage: 'users', showSchedulesOverview: true })
  await act(async () => host.querySelector('[data-navigation-id="product:code"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('code')
  expect(useAppStore.getState().adminPage).toBeNull()
  expect(useAppStore.getState().showSchedulesOverview).toBe(false)
  expect(onClose).toHaveBeenCalled()
})

it('opens Users via Enter from an unrelated product', async () => {
  const { host, onClose } = await renderNavigation('Users')
  await act(async () => host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('agentworks')
  expect(useAppStore.getState().adminPage).toBe('users')
  expect(onClose).toHaveBeenCalled()
})

it('hides unavailable products and admin-only menus and opens Providers on an allowed surface', async () => {
  const { host } = await renderNavigation('@menus ', ['code'], false)
  expect(host.textContent).toContain('Providers')
  expect(host.textContent).not.toContain('Users and access')
  expect(host.textContent).toContain('Connect an AI agent') // MCP is for every signed-in person (owner, 2026-10-09)
  expect(host.textContent).not.toContain('Schedules and triggers')
  expect(quickNavigationItems(useAuthStore.getState().user, 'code').filter(item => item.type === 'product').map(item => item.surface)).toEqual(['code'])
  await act(async () => host.querySelector('[data-navigation-id="menu:providers"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('code')
  expect(useLLMStore.getState().showLLMModal).toBe(true)
})

it('rechecks access if a menu was selected after permission changed', async () => {
  await renderNavigation('@menus ')
  const users = quickNavigationItems(useAuthStore.getState().user, 'video-studio').find(item => item.action === 'users')!
  useAuthStore.setState({ user: { is_admin: false, allowed_products: ['code'] } as never })
  expect(openQuickNavigation(users)).toBe(false)
  expect(useProductSurfaceStore.getState().productSurface).toBe('video-studio')
})


it('browses current toolbar panels and opens a nested tab from unified search while the toolbar is hidden', async () => {
  const open = vi.fn()
  const { host, onClose } = await renderNavigation('', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'code' })
    usePanelSwitcherStore.setState({ toolbarMinimized: true })
    usePanelSwitcherStore.getState().register('code', { panels: [
      { id: 'files', label: 'Files' },
      { id: 'integrations', label: 'Integrations', sections: [{ id: 'slack', label: 'Slack', keywords: 'channels messages' }] },
    ], open })
    usePanelSwitcherStore.getState().register('work', { panels: [{ id: 'crew-only', label: 'Crew only panel' }], open: vi.fn() })
  })
  expect(host.querySelector('[data-navigation-id="panel:files"]')).not.toBeNull()
  expect(host.querySelector('[data-navigation-id="panel:integrations:slack"]')).not.toBeNull()
  expect(host.querySelector('[data-navigation-id="panel:crew-only"]')).toBeNull()
  await act(async () => host.querySelector('[data-navigation-id="browse:panels"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['panel:files', 'panel:integrations', 'panel:integrations:slack'])
  await act(async () => host.querySelector('[aria-label="Show all work and navigation"]')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  const input = host.querySelector('input')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'messages integrations')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(host.querySelectorAll('[data-navigation-id]')).toHaveLength(1)
  await act(async () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(open).toHaveBeenCalledWith('integrations', 'slack')
  expect(usePanelSwitcherStore.getState().toolbarMinimized).toBe(false)
  expect(onClose).toHaveBeenCalledOnce()
})

it('opens a parent panel with @panels and refuses stale panel selections after access changes', async () => {
  const open = vi.fn()
  const { host, onClose } = await renderNavigation('@panels Files', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'code' })
    usePanelSwitcherStore.getState().register('code', { panels: [{ id: 'files', label: 'Files' }], open })
  })
  await act(async () => host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(open).toHaveBeenCalledWith('files', undefined)
  expect(onClose).toHaveBeenCalledOnce()
  open.mockClear(); onClose.mockClear()
  const row = host.querySelector('[data-navigation-id="panel:files"]')!
  await act(async () => {
    useAuthStore.setState({ user: { allowed_products: ['work'] } as never })
    row.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  })
  expect(open).not.toHaveBeenCalled()
  expect(onClose).not.toHaveBeenCalled()
})


it('offers creation for every supported product and routes each action from another product', async () => {
  const { host, onClose } = await renderNavigation('@create ')
  const surfaces: ProductCreateSurface[] = ['agentworks', 'relays', 'video-studio', 'work', 'code']
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(surfaces.map(surface => `create:${surface}`))
  for (const surface of surfaces) {
    await act(async () => {
      useProductSurfaceStore.setState({ productSurface: 'mcp-gateway' })
      host.querySelector(`[data-navigation-id="create:${surface}"]`)!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    })
    expect(useProductSurfaceStore.getState().productSurface).toBe(surface)
    expect(useCommandDialogStore.getState().productCreateSurface).toBe(surface)
  }
  expect(onClose).toHaveBeenCalledTimes(surfaces.length)
})

it('retains a Code creation request through lazy mounting, opens the real form once, and cancels superseded requests', async () => {
  const { host, onClose } = await renderNavigation('Create new Code workspace')
  await act(async () => host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(onClose).toHaveBeenCalledOnce()
  expect(useCommandDialogStore.getState().productCreateSurface).toBe('code')
  useLLMStore.setState({ providerManifest: [], providerManifestLoaded: true })
  const opened = vi.fn()
  function CodeCreateHost() {
    const [open, setOpen] = useState(false)
    useProductCreateRequest('code', () => { opened(); setOpen(true) })
    return open ? <CreateCodeWorkspaceDialog onClose={() => setOpen(false)} onCreate={() => {}} submitting={false} error={null} /> : null
  }
  const formHost = document.createElement('div'); document.body.append(formHost)
  const root = createRoot(formHost)
  cleanups.push(() => { act(() => root.unmount()); formHost.remove() })
  await act(async () => root.render(<CodeCreateHost />))
  expect(formHost.querySelector('[role="dialog"]')?.textContent).toContain('New Code workspace')
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
  await act(async () => formHost.querySelector<HTMLButtonElement>('[aria-label="Close"]')!.click())
  await act(async () => root.render(<CodeCreateHost />))
  expect(formHost.querySelector('[role="dialog"]')).toBeNull()
  expect(opened).toHaveBeenCalledOnce()
  await act(async () => {
    useCommandDialogStore.getState().requestProductCreate('work')
    useProductSurfaceStore.getState().setProductSurface('relays')
  })
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
  await act(async () => {
    useCommandDialogStore.getState().requestProductCreate('relays')
    openProductWorkspace('relays')
  })
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
})

it('hides creation for unavailable products and respects the workflow create gate at activation time', async () => {
  // Each product has its own create permission (PLAT-767): this account may create Code projects only.
  const { host } = await renderNavigation('@create ', ['agentworks', 'relays', 'code'], false, () => {
    useAuthStore.setState({ isMultiUserMode: true, user: { is_admin: false, can_create: false, can_create_in: { code: true }, can_write_workflows: true, allowed_products: ['agentworks', 'relays', 'code'] } as never })
  })
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['create:code'])
  await act(async () => useAuthStore.setState({ user: { ...useAuthStore.getState().user!, can_create: true } }))
  const workflow = quickNavigationItems(useAuthStore.getState().user, 'code').find(item => item.id === 'create:agentworks')!
  expect(workflow).toBeDefined()
  await act(async () => useAuthStore.setState({ user: { ...useAuthStore.getState().user!, can_create: false } }))
  expect(openQuickNavigation(workflow)).toBe(false)
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
})

const localProducts = ['agentworks', 'work', 'code', 'relays', 'knowledgebase', 'mcp-gateway']

it.each([false, true])('filters Ctrl+K products for local server-product opt-in=%s', async optIn => {
  const { host } = await renderNavigation('@products ', localProducts, true, () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { deploymentMode: 'local', localServerProducts: optIn })
    useProductSurfaceStore.setState({ productSurface: 'code' })
  })
  const surfaces = [...host.querySelectorAll('[data-navigation-id^="product:"]')].map(row => row.getAttribute('data-navigation-id'))
  expect(surfaces).toEqual(optIn
    ? ['product:agentworks', 'product:relays', 'product:work', 'product:code', 'product:mcp-gateway', 'product:knowledgebase']
    : ['product:agentworks', 'product:work', 'product:code'])
})

it('respects account entitlements within an opted-in local installation', async () => {
  const { host } = await renderNavigation('@products ', localProducts, false, () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { deploymentMode: 'local', localServerProducts: true })
    useAuthStore.setState({ user: { is_admin: false, allowed_products: ['code', 'mcp-gateway'] } as never })
  })
  expect([...host.querySelectorAll('[data-navigation-id^="product:"]')].map(row => row.getAttribute('data-navigation-id')))
    .toEqual(['product:code', 'product:mcp-gateway'])
})

it('rejects a stale server-product shortcut after local opt-in is withdrawn', async () => {
  await renderNavigation('@products ', localProducts, true, () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { deploymentMode: 'local', localServerProducts: true })
  })
  const vault = quickNavigationItems(useAuthStore.getState().user, 'code').find(item => item.id === 'product:mcp-gateway')!
  Object.assign(window.__APP_RUNTIME_CONFIG__!, { localServerProducts: false })
  expect(openQuickNavigation(vault)).toBe(false)
  expect(useProductSurfaceStore.getState().productSurface).toBe('video-studio')
})
