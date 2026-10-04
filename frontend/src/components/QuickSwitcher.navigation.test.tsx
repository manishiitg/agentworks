// @vitest-environment happy-dom
import { act } from 'react'
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
  const service = new Proxy({}, { get: () => vi.fn(async () => ({})) })
  return { llmConfigService: service, default: service }
})

import QuickSwitcher from './QuickSwitcher'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useAppStore } from '../stores/useAppStore'
import { useLLMStore } from '../stores/useLLMStore'
import { openQuickNavigation, quickNavigationItems } from '../utils/quickNavigation'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach(fn => fn())
  delete (window as Window & { __APP_RUNTIME_CONFIG__?: unknown }).__APP_RUNTIME_CONFIG__
  useAuthStore.setState({ user: null })
})

async function renderNavigation(query: string, allowed = ['agentworks', 'work', 'code', 'video-studio', 'relays', 'dominion', 'sparkquill', 'mcp-gateway'], admin = true, seed?: () => void) {
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

it('defaults to running workflows, Crews, Code and Relays before idle work, without product or menu rows', async () => {
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
  expect(rows).toHaveLength(5)
  expect(rows.slice(0, 4).map(row => row.textContent)).toEqual(expect.arrayContaining([
    expect.stringContaining('Running workflow'), expect.stringContaining('Running Relay'),
    expect.stringContaining('Running Crew'), expect.stringContaining('Running Code'),
  ]))
  expect(rows[4].textContent).toContain('Idle current')
  expect(rows[0].className).toContain('bg-blue-50')
  expect(host.querySelector('[data-navigation-id^="product:"]')).toBeNull()
  expect(host.querySelector('[data-navigation-id^="menu:"]')).toBeNull()
})

it('keeps a running scheduled Crew visible when its view-only tab has no project row', async () => {
  const { host } = await renderNavigation('', undefined, true, () => {
    const sessionId = 'work:project:crew:trigger:run'
    useChatStore.setState({ chatTabs: {
      scheduled: { tabId: 'scheduled', sessionId, name: 'Scheduled Crew', metadata: { agentProfileId: 'work', isScheduledRun: true, isViewOnly: true } },
    } as never, activeSessionsCache: [{ session_id: sessionId, status: 'running', title: 'Scheduled Crew' }] as never })
  })
  expect(host.querySelectorAll('[data-navigation-id]')).toHaveLength(1)
  expect(host.querySelector('[data-navigation-id^="active:"]')?.textContent).toContain('Scheduled Crew')
})

it('lists every available product and opens its workspace from another product', async () => {
  const { host, onClose } = await renderNavigation('@products ')
  expect(host.querySelectorAll('[data-navigation-id^="product:"]')).toHaveLength(8)
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

it('hides unavailable products and admin menus and opens Providers on an allowed surface', async () => {
  const { host } = await renderNavigation('@menus ', ['code'], false)
  expect(host.textContent).toContain('Providers')
  expect(host.textContent).not.toContain('Users and access')
  expect(host.textContent).not.toContain('Connect an AI agent')
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
