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
import { observeProductHistory } from '../utils/productUrlNavigation'
import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useAppStore } from '../stores/useAppStore'
import { usePanelSwitcherStore } from '../stores/usePanelSwitcherStore'
import { useLLMStore } from '../stores/useLLMStore'
import { openQuickNavigation, quickNavigationItems } from '../utils/quickNavigation'
import { WORKFLOW_PANEL_SECTIONS, workPanelSections } from '../products/productPanels'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach(fn => fn())
  delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__
  useAuthStore.setState({ user: null, isMultiUserMode: false })
  useCommandDialogStore.getState().closeAll()
  usePanelSwitcherStore.setState({ entries: {}, toolbarMinimized: false })
  useGlobalPresetStore.setState({ recentPresetAccessedAt: {} })
  vi.unstubAllEnvs()
})

async function renderNavigation(query: string, allowed = ['agentworks', 'work', 'relays', 'knowledgebase', 'mcp-gateway'], admin = true, seed?: () => void) {
  window.__APP_RUNTIME_CONFIG__ = { enabledProductSurfaces: allowed, gatewayUrl: 'http://localhost:99999' } as never
  useAuthStore.setState({ user: { id: 'u1', username: 'u1', is_admin: admin, allowed_products: allowed } as never })
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  useChatStore.setState({ chatTabs: {}, activeTabId: null, activeSessionsCache: [], getActiveSessions: vi.fn(async () => []) })
  useProductSurfaceStore.setState({ productSurface: 'relays' })
  seed?.()
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  cleanups.push(() => { act(() => root.unmount()); host.remove() })
  const onClose = vi.fn()
  await act(async () => { root.render(<QuickSwitcher isOpen onClose={onClose} initialQuery={query} />) })
  return { host, onClose }
}

it('defaults to running workflows, Crews and Relays before idle work, with browse and navigation rows at the bottom', async () => {
  const { host } = await renderNavigation('', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'work' })
    useChatStore.setState({ activeTabId: 'idle', chatTabs: {
      idle: { tabId: 'idle', name: 'Idle current', createdAt: 9000, lastAccessedAt: 9000, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Idle current' } },
      code: { tabId: 'work', name: 'Running project', createdAt: 1, isStreaming: true, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Running project' } },
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
    expect.stringContaining('Running Crew'), expect.stringContaining('Running project'),
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

it('ranks exact names first, then recency before match strength and activity, with scopes preserved', async () => {
  const { host } = await renderNavigation('  REPORT  ', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'work' })
    useChatStore.setState({ activeTabId: 'current', chatTabs: {
      exact: { tabId: 'exact', name: 'Report', createdAt: 1, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Report' } },
      running: { tabId: 'running', name: 'Report Z running', createdAt: 1, isStreaming: true, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Report Z running' } },
      current: { tabId: 'current', name: 'Report Y current', createdAt: 1, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Report Y current' } },
      recent: { tabId: 'recent', name: 'Report X recent', createdAt: 1, lastAccessedAt: 9000, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Report X recent' } },
      olderB: { tabId: 'olderB', name: 'Report B older', createdAt: 1, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Report B older' } },
      olderA: { tabId: 'olderA', name: 'Report A older', createdAt: 1, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Report A older' } },
      substring: { tabId: 'substring', name: 'Weekly Report', createdAt: 10000, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Weekly Report' } },
    } as never })
    // The most recently opened keyword match outranks old prefix matches,
    // but even an old exact name stays first.
    useChatStore.setState({ activeSessionsCache: [
      { session_id: 'keyword-run', agent_mode: 'workflow', status: 'running', preset_query_id: 'keyword' },
    ] as never })
    useGlobalPresetStore.setState({ recentPresetAccessedAt: { keyword: 11000 }, workflowPresets: [
      { id: 'keyword', label: 'A inventory', selectedFolder: { filepath: 'Workflow/report' } },
    ] as never })
    usePanelSwitcherStore.getState().register('work', { panels: [{ id: 'analytics', label: 'Analytics', sections: [
      { id: 'exact', label: 'Report' },
      { id: 'name', label: 'Annual Report' },
      { id: 'keywords', label: 'A summary', keywords: 'annual report' },
    ] }], open: vi.fn() })
  })
  const rowIds = () => [...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))
  expect(rowIds()).toEqual([
    'crew:exact', 'panel:analytics:exact', 'panel:analytics:name',
    'workflow:keyword', 'crew:substring', 'crew:recent',
    'crew:running', 'crew:current', 'crew:olderA', 'crew:olderB',
    'panel:analytics:keywords',
  ])
  expect(host.querySelector('[data-navigation-id="crew:exact"]')?.className).toContain('bg-blue-50')

  // Preserve the panel scope and word-order-independent search, while giving
  // words found entirely in the tab name priority over its keywords.
  const input = host.querySelector('input')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '@panels report annual')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(rowIds()).toEqual(['panel:analytics:name', 'panel:analytics:keywords'])
})

it('keeps named panel commands ahead of recently visited incidental matches, with stable ties', async () => {
  const { host } = await renderNavigation('sett', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'work' })
    useChatStore.setState({ chatTabs: {
      recent: { tabId: 'recent', name: 'Weekly settings report', createdAt: 9000, metadata: { agentProfileId: 'work', agentProfileIdentityName: 'Weekly settings report' } },
    } as never })
    usePanelSwitcherStore.getState().register('work', { panels: [
      { id: 'z-settings', label: 'Settings' }, { id: 'a-settings', label: 'Settings' },
    ], open: vi.fn() })
  })
  const ids = [...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))
  expect(ids.slice(0, 2)).toEqual(['panel:a-settings', 'panel:z-settings'])
  expect(ids).toContain('crew:recent')
})

it('browses all workflows or Relays from footer icons without requiring typed scopes', async () => {
  const { host, onClose } = await renderNavigation('', undefined, true, () => {
    useGlobalPresetStore.setState({ workflowPresets: [
      { id: 'workflow', label: 'Daily report', selectedFolder: { filepath: 'Workflow/report' } },
      { id: 'relay', label: 'Invoice Relay', workflowKind: 'relay', selectedFolder: { filepath: 'Workflow/invoice' } },
    ] as never })
  })
  const shortcuts = host.querySelector('[aria-label="Quick navigation shortcuts"]')!
  await act(async () => shortcuts.querySelector<HTMLButtonElement>('[aria-label="All Goals"]')!.click())
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['workflow:workflow', 'create:agentworks'])
  expect(host.querySelector('input')!.value).toBe('')
  expect(host.querySelector('[aria-label="Show all work and navigation"]')?.textContent).toContain('All Goals')
  await act(async () => shortcuts.querySelector<HTMLButtonElement>('[aria-label="All Flows"]')!.click())
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['workflow:relay', 'create:relays'])
  expect(onClose).not.toHaveBeenCalled()
  await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Show all work and navigation"]')!.click())
  expect(host.querySelector('[data-navigation-id="browse:crew"]')).not.toBeNull()
})

it.each([
  ['Users and access', 'users'], ['Connect AI tools', 'mcp'], ['AI models & accounts', 'providers'],
])('opens %s directly from its footer icon', async (label, destination) => {
  const { host, onClose } = await renderNavigation('')
  useAppStore.setState({ adminPage: null })
  useLLMStore.setState({ showLLMModal: false })
  await act(async () => host.querySelector<HTMLButtonElement>(`[aria-label="Quick navigation shortcuts"] button[aria-label="${label}"]`)!.click())
  if (destination === 'providers') expect(useLLMStore.getState().showLLMModal).toBe(true)
  else expect(useAppStore.getState().adminPage).toBe(destination)
  expect(onClose).toHaveBeenCalled()
})

it.each(['schedules'])('opens %s from the scrollable navigation rows', async action => {
  const { host, onClose } = await renderNavigation('')
  useAppStore.setState({ showWorkflowsOverview: false, showSchedulesOverview: false, activityWorkflowPath: 'Workflow/previous-filter' })
  await act(async () => host.querySelector(`[data-navigation-id="menu:${action}"]`)!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('agentworks')
  expect(useAppStore.getState().showSchedulesOverview).toBe(true)
  expect(onClose).toHaveBeenCalled()
})

it('shows Vault-specific icons only in Vault and opens its audit page', async () => {
  const { host } = await renderNavigation('', undefined, true, () => useProductSurfaceStore.setState({ productSurface: 'mcp-gateway' }))
  const opened = vi.fn()
  window.addEventListener('vault-open-panel', opened)
  try {
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Quick navigation shortcuts"] button[aria-label="Vault audit logs"]')!.click())
    expect(opened).toHaveBeenCalledWith(expect.objectContaining({ detail: 'audit' }))
    expect(quickNavigationItems(useAuthStore.getState().user, 'work').some(item => item.action === 'vault-audit')).toBe(false)
  } finally { window.removeEventListener('vault-open-panel', opened) }
})

it('lists every available product and opens its workspace from another product', async () => {
  const { host, onClose } = await renderNavigation('@products ')
  expect(host.textContent).not.toContain('Dominion')
  expect(host.textContent).toContain('Vault')
  expect(host.textContent).toContain('Flows')
  useAppStore.setState({ adminPage: 'users', showSchedulesOverview: true })
  await act(async () => host.querySelector('[data-navigation-id="product:work"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('work')
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
  const { host } = await renderNavigation('@menus ', ['work'], false)
  expect(host.textContent).toContain('AI models & accounts')
  expect(host.textContent).not.toContain('Users and access')
  expect(host.textContent).toContain('Connect AI tools') // MCP is for every signed-in person (owner, 2026-10-09)
  expect(host.textContent).toContain('Schedules and triggers')
  expect(quickNavigationItems(useAuthStore.getState().user, 'work').filter(item => item.type === 'product').map(item => item.surface)).toEqual(['work'])
  await act(async () => host.querySelector('[data-navigation-id="menu:providers"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('work')
  expect(useLLMStore.getState().showLLMModal).toBe(true)
})

it('rechecks access if a menu was selected after permission changed', async () => {
  await renderNavigation('@menus ')
  const users = quickNavigationItems(useAuthStore.getState().user, 'relays').find(item => item.action === 'users')!
  useAuthStore.setState({ user: { is_admin: false, allowed_products: ['work'] } as never })
  expect(openQuickNavigation(users)).toBe(false)
  expect(useProductSurfaceStore.getState().productSurface).toBe('relays')
})


it('browses current toolbar panels and opens a nested tab from unified search while the toolbar is hidden', async () => {
  const open = vi.fn()
  const { host, onClose } = await renderNavigation('', undefined, true, () => {
    useProductSurfaceStore.setState({ productSurface: 'work' })
    usePanelSwitcherStore.setState({ toolbarMinimized: true })
    usePanelSwitcherStore.getState().register('work', { panels: [
      { id: 'files', label: 'Files' },
      { id: 'integrations', label: 'Integrations', sections: [{ id: 'slack', label: 'Slack', keywords: 'channels messages' }] },
    ], open })
    usePanelSwitcherStore.getState().register('agentworks', { panels: [{ id: 'crew-only', label: 'Other product panel' }], open: vi.fn() })
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
    useProductSurfaceStore.setState({ productSurface: 'work' })
    usePanelSwitcherStore.getState().register('work', { panels: [{ id: 'files', label: 'Files' }], open })
  })
  await act(async () => host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(open).toHaveBeenCalledWith('files', undefined)
  expect(onClose).toHaveBeenCalledOnce()
  open.mockClear(); onClose.mockClear()
  const row = host.querySelector('[data-navigation-id="panel:files"]')!
  await act(async () => {
    useAuthStore.setState({ user: { allowed_products: ['relays'] } as never })
    row.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  })
  expect(open).not.toHaveBeenCalled()
  expect(onClose).not.toHaveBeenCalled()
})


it('offers creation for every supported product and routes each action from another product', async () => {
  const { host, onClose } = await renderNavigation('@create ')
  const surfaces: ProductCreateSurface[] = ['agentworks', 'relays', 'work']
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

it('retains a Crew creation request through lazy mounting, opens the real form once, and cancels superseded requests', async () => {
  const { host, onClose } = await renderNavigation('Create new Crew')
  await act(async () => host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(onClose).toHaveBeenCalledOnce()
  expect(useCommandDialogStore.getState().productCreateSurface).toBe('work')
  useLLMStore.setState({ providerManifest: [], providerManifestLoaded: true })
  const opened = vi.fn()
  function CodeCreateHost() {
    const [open, setOpen] = useState(false)
    useProductCreateRequest('work', () => { opened(); setOpen(true) })
    return open ? <CreateCodeWorkspaceDialog onClose={() => setOpen(false)} onCreate={() => {}} submitting={false} error={null} /> : null
  }
  const formHost = document.createElement('div'); document.body.append(formHost)
  const root = createRoot(formHost)
  cleanups.push(() => { act(() => root.unmount()); formHost.remove() })
  await act(async () => root.render(<CodeCreateHost />))
  expect(formHost.querySelector('[role="dialog"]')?.textContent).toContain('New Crew')
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
  // Each product has its own create permission (PLAT-767): this account may create Crew projects only.
  const { host } = await renderNavigation('@create ', ['agentworks', 'relays', 'work'], false, () => {
    useAuthStore.setState({ isMultiUserMode: true, user: { is_admin: false, can_create: false, can_create_in: { work: true }, can_write_workflows: true, allowed_products: ['agentworks', 'relays', 'work'] } as never })
  })
  expect([...host.querySelectorAll('[data-navigation-id]')].map(row => row.getAttribute('data-navigation-id'))).toEqual(['create:work'])
  await act(async () => useAuthStore.setState({ user: { ...useAuthStore.getState().user!, can_create: true } }))
  const workflow = quickNavigationItems(useAuthStore.getState().user, 'work').find(item => item.id === 'create:agentworks')!
  expect(workflow).toBeDefined()
  await act(async () => useAuthStore.setState({ user: { ...useAuthStore.getState().user!, can_create: false } }))
  expect(openQuickNavigation(workflow)).toBe(false)
  expect(useCommandDialogStore.getState().productCreateSurface).toBeNull()
})


const localProducts = ['agentworks', 'work', 'relays', 'knowledgebase', 'mcp-gateway']

it.each([false, true])('filters Ctrl+K products for local server-product opt-in=%s', async optIn => {
  const { host } = await renderNavigation('@products ', localProducts, true, () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { deploymentMode: 'local', localServerProducts: optIn })
    useProductSurfaceStore.setState({ productSurface: 'work' })
  })
  const surfaces = [...host.querySelectorAll('[data-navigation-id^="product:"]')].map(row => row.getAttribute('data-navigation-id'))
  expect(surfaces).toEqual(optIn
    ? ['product:agentworks', 'product:relays', 'product:work', 'product:mcp-gateway', 'product:knowledgebase']
    : ['product:agentworks', 'product:work'])
})

it('respects account entitlements within an opted-in local installation', async () => {
  const { host } = await renderNavigation('@products ', localProducts, false, () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { deploymentMode: 'local', localServerProducts: true })
    useAuthStore.setState({ user: { is_admin: false, allowed_products: ['work', 'mcp-gateway'] } as never })
  })
  expect([...host.querySelectorAll('[data-navigation-id^="product:"]')].map(row => row.getAttribute('data-navigation-id')))
    .toEqual(['product:work', 'product:mcp-gateway'])
})

it.each(['mcp-gateway', 'relays'])('rejects a stale %s shortcut after local opt-in is withdrawn', async product => {
  await renderNavigation('@products ', localProducts, true, () => {
    Object.assign(window.__APP_RUNTIME_CONFIG__!, { deploymentMode: 'local', localServerProducts: true })
  })
  const shortcut = quickNavigationItems(useAuthStore.getState().user, 'work').find(item => item.id === `product:${product}`)!
  Object.assign(window.__APP_RUNTIME_CONFIG__!, { localServerProducts: false })
  expect(openQuickNavigation(shortcut)).toBe(false)
  expect(useProductSurfaceStore.getState().productSurface).toBe('relays')
})


it('finds the Crew Watchers tab and opens Automation on that section', async () => {
  expect(workPanelSections('schedules', false).some(section => section.id === 'conditions')).toBe(true)
  expect(WORKFLOW_PANEL_SECTIONS.workshop?.some(section => section.id === 'conditions')).toBe(true)
  const open = vi.fn()
  const { host, onClose } = await renderNavigation('watchers', ['work'], false, () => {
    useProductSurfaceStore.setState({ productSurface: 'work' })
    usePanelSwitcherStore.getState().register('work', { panels: [
      { id: 'schedules', label: 'Automation', sections: workPanelSections('schedules', true) },
    ], open })
  })
  expect(host.querySelector('[data-navigation-id="panel:schedules:conditions"]')?.textContent).toContain('Watchers')
  await act(async () => host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })))
  expect(open).toHaveBeenCalledWith('schedules', 'conditions')
  expect(onClose).toHaveBeenCalledOnce()
})

it('keeps the legacy Code search keyword opening the single Crew product', async () => {
  const { host } = await renderNavigation('Code')
  const products = host.querySelectorAll('[data-navigation-id^="product:"]')
  expect(products).toHaveLength(1)
  expect(products[0].textContent).toContain('Crew')
  await act(async () => products[0].dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(useProductSurfaceStore.getState().productSurface).toBe('work')
})

it('finds Connections through its previous Integrations name without exposing other products', async () => {
  const open = vi.fn()
  const { host } = await renderNavigation('integrations', ['work'], false, () => {
    useProductSurfaceStore.setState({ productSurface: 'work' })
    usePanelSwitcherStore.getState().register('work', { panels: [{ id: 'mcp', label: 'Connections' }], open })
    usePanelSwitcherStore.getState().register('agentworks', { panels: [{ id: 'mcp', label: 'Hidden Goal tools' }], open: vi.fn() })
  })
  expect(host.querySelector('[data-navigation-id="panel:mcp"]')?.textContent).toContain('Connections')
  expect(host.textContent).not.toContain('Hidden Goal tools')
  await act(async () => host.querySelector('[data-navigation-id="panel:mcp"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
  expect(open).toHaveBeenCalledWith('mcp', undefined)
})


it('groups Crew chats by project ID with one project title and linear keyboard navigation', async () => {
  const { host, onClose } = await renderNavigation('Shared Crew', ['work'], false, () => {
    useChatStore.setState({ chatTabs: Object.fromEntries(['main', 'Finance', 'Research'].map((name, index) => [name, {
      tabId: name, name, createdAt: index + 1, config: {}, events: [],
      metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: 'p1', agentProfileProjectTitle: 'Shared Crew', agentProfileConversationKey: index ? `p1:chat:${name}` : 'p1' },
    }])) as never })
  })
  const rows = [...host.querySelectorAll('[role="option"]')]
  expect(rows[0].getAttribute('data-navigation-id')).toBe('crew-project:p1')
  expect(rows[0].textContent).toContain('Shared Crew')
  const children = rows.slice(1)
  expect(children).toHaveLength(3)
  expect(children.every(row => row.getAttribute('data-navigation-parent') === 'crew:p1')).toBe(true)
  expect(children.map(row => row.textContent)).toEqual(expect.arrayContaining(['Builder', 'Finance', 'Research']))
  expect(children.every(row => !row.textContent?.includes('Shared Crew'))).toBe(true)
  const input = host.querySelector('input')!
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })) })
  expect(rows[0].getAttribute('aria-selected')).toBe('true')
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })) })
  expect(children[0].getAttribute('aria-selected')).toBe('true')
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true })) })
  expect(rows[0].getAttribute('aria-selected')).toBe('true')
  await act(async () => {
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
  })
  await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
  expect(useChatStore.getState().activeTabId).toBe(children[0].getAttribute('data-navigation-id')!.slice('crew:'.length))
  expect(Object.keys(useChatStore.getState().chatTabs)).toHaveLength(3)
  expect(onClose).toHaveBeenCalledOnce()
})

it('keeps same-named projects separate and a matching child selected beneath its project', async () => {
  const { host } = await renderNavigation('Research', ['work'], false, () => {
    useChatStore.setState({ chatTabs: Object.fromEntries(['p1', 'p2'].map((project, index) => [project, {
      tabId: project, name: 'Research', createdAt: index + 1, config: {}, events: [],
      metadata: { mode: 'multi-agent', agentProfileId: 'work', agentProfileProjectId: project, agentProfileProjectTitle: 'Same project name', agentProfileConversationKey: `${project}:chat:research` },
    }])) as never })
  })
  expect(host.querySelectorAll('[data-navigation-id^="crew-project:"]')).toHaveLength(2)
  const chosen = host.querySelector('[role="option"][aria-selected="true"]')!
  expect(chosen.getAttribute('data-navigation-id')).toBe('crew:p2')
  expect(chosen.getAttribute('data-navigation-parent')).toBe('crew:p2')
  expect(host.querySelector('input')!.getAttribute('aria-activedescendant')).toBe(chosen.id)
  await act(async () => { host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
  expect(useChatStore.getState().activeTabId).toBe('p2')
})

it.each(['goal', 'relay'] as const)('finds a named %s Builder chat under its project and opens that exact chat', async kind => {
  const { host, onClose } = await renderNavigation(`@${kind === 'relay' ? 'relays' : 'workflows'} Research`, ['agentworks', 'relays'], false, () => {
    useGlobalPresetStore.setState({ workflowPresets: [{ id: 'plan', label: 'Example project', workflowKind: kind === 'relay' ? 'relay' : undefined, selectedFolder: { filepath: 'Workflow/example' } }] as never })
    useChatStore.setState({ chatTabs: { research: { tabId: 'research', name: 'Research', createdAt: 1, config: {}, events: [], metadata: { mode: 'workflow', presetQueryId: 'plan', phaseId: 'workflow-builder', workflowOwnerTabID: 'owner-tab' } } } as never })
  })
  const rows = [...host.querySelectorAll('[role="option"]')]
  expect(rows).toHaveLength(2)
  expect(rows[0].getAttribute('data-navigation-id')).toBe('workflow:plan')
  expect(rows[0].textContent).toContain('Example project')
  expect(rows[1].getAttribute('data-navigation-parent')).toBe('workflow:plan')
  expect(rows[1].getAttribute('aria-selected')).toBe('true')
  await act(async () => { host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })) })
  expect(useChatStore.getState().activeTabId).toBe('research')
  expect(useProductSurfaceStore.getState().productSurface).toBe(kind === 'relay' ? 'relays' : 'agentworks')
  expect(onClose).toHaveBeenCalledOnce()
})

it('omits global Goal activity from navigation and search', async () => {
  for (const query of ['', 'Goal activity']) {
    const { host } = await renderNavigation(query)
    expect(host.querySelector('[data-navigation-id="menu:activity"]')).toBeNull()
  }
})

it('keeps Home during creation instead of navigating to a remembered project', async () => {
  await renderNavigation('@create ')
  const stop = observeProductHistory()
  try {
    for (const surface of ['work', 'agentworks', 'relays'] as const) {
      window.history.replaceState({}, '', '/home')
      useProductSurfaceStore.setState({ selectedWorkProjectId: 'existing-crew' })
      useGlobalPresetStore.setState({ workflowPresets: [{ id: 'existing-goal', label: 'Existing goal', workflowKind: surface === 'relays' ? 'relay' : 'workflow' }] as never, activePresetIds: { ...useGlobalPresetStore.getState().activePresetIds, workflow: 'existing-goal' } })
      const creation = quickNavigationItems(useAuthStore.getState().user, useProductSurfaceStore.getState().productSurface).find(item => item.id === `create:${surface}`)!
      await act(async () => {
        expect(openQuickNavigation(creation)).toBe(true)
        await new Promise(resolve => setTimeout(resolve, 10))
      })
      expect(window.location.pathname).toBe('/home')
      expect(useCommandDialogStore.getState().productCreateSurface).toBe(surface)
    }
  } finally { stop(); window.history.replaceState({}, '', '/'); useGlobalPresetStore.setState({ activePresetIds: { ...useGlobalPresetStore.getState().activePresetIds, workflow: null } }) }
})

// PLAT-746-158 A3: one existing launcher opens the current main chat's search.
it('opens find in the mounted chat from the existing quick switcher', async () => {
  const transcript = document.createElement('div')
  transcript.dataset.chatFindRoot = ''
  transcript.getClientRects = () => [new DOMRect(0, 0, 600, 400)] as unknown as DOMRectList
  const open = vi.fn()
  transcript.addEventListener('open-current-chat-find', open)
  document.body.append(transcript)
  cleanups.push(() => transcript.remove())
  const { host, onClose } = await renderNavigation('Find in this chat')
  const row = host.querySelector<HTMLElement>('[data-navigation-id="chat:find"]')!
  expect(row).not.toBeNull()
  await act(async () => { row.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); await new Promise(resolve => requestAnimationFrame(resolve)) })
  expect(onClose).toHaveBeenCalledOnce()
  expect(open).toHaveBeenCalledOnce()
})
