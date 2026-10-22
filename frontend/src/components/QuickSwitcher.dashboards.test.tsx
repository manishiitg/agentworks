// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

vi.hoisted(() => {
  const memory = new Map<string, string>()
  const storage = { getItem: (key: string) => memory.get(key) ?? null, setItem: (key: string, value: string) => { memory.set(key, String(value)) }, removeItem: (key: string) => { memory.delete(key) }, clear: () => memory.clear() }
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true })
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true })
})
const directory = vi.hoisted(() => ({ load: vi.fn() }))
vi.mock('../services/dashboardDirectory', () => ({ loadDashboardDirectory: directory.load, dashboardDisplayName: (row: { title: string }) => row.title, dashboardDirectoryLink: (row: { short_path: string }) => row.short_path }))
vi.mock('../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: vi.fn(async () => []) }))
vi.mock('../services/llm-config-api', () => { const service = new Proxy({}, { get: () => vi.fn(async () => ({})) }); return { llmConfigService: service, default: service } })

import QuickSwitcher from './QuickSwitcher'
import { useAuthStore } from '../stores/useAuthStore'
import { useChatStore } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('finds named owned/shared dashboards, opens their exact links and clears them on account changes', async () => {
  useAuthStore.setState({ user: { id: 'owner' } as never })
  useWorkspaceConnectionStore.setState({ activeWorkspaceId: 'local', profiles: [{ id: 'local', apiBaseUrl: '' }] as never })
  useGlobalPresetStore.setState({ workflowPresetsLoaded: true, workflowPresets: [] })
  useChatStore.setState({ chatTabs: {}, activeSessionsCache: [], getActiveSessions: vi.fn(async () => []) })
  directory.load.mockResolvedValueOnce({ dashboards: [
    { dashboard_id: 'solar', title: 'Solar Plan', project_title: 'Operations', workspace: 'Crew/operations', document_path: 'db/reports/solar.html', short_path: '/d/23456789ab', shared_with_me: false },
    { dashboard_id: 'budget', title: 'Team Budget', project_title: 'Finance', workspace: 'Crew/finance', document_path: 'db/reports/budget.html', short_path: '/d/3456789abc', shared_with_me: true },
  ], partial: false })
  let resolveNext!: (result: unknown) => void
  directory.load.mockReturnValueOnce(new Promise(resolve => { resolveNext = resolve }))
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => {})
  try {
    await act(async () => root.render(<QuickSwitcher isOpen initialQuery="@dashboards" onClose={vi.fn()} />))
    await vi.waitFor(() => expect(host.querySelectorAll('[data-navigation-id^="dashboard:"]')).toHaveLength(2))
    const row = host.querySelector<HTMLElement>('[data-navigation-id="dashboard:Crew/finance:budget"]')!
    expect(row.textContent).toContain('Team Budget')
    expect(row.textContent).toContain('Shared with you')
    await act(async () => row.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })))
    expect(assign).toHaveBeenCalledExactlyOnceWith('/d/3456789abc')
    await act(async () => useAuthStore.setState({ user: { id: 'other' } as never }))
    expect(host.querySelector('[data-navigation-id^="dashboard:"]')).toBeNull()
    await act(async () => resolveNext({ dashboards: [], partial: false }))
    expect(host.querySelector('[data-navigation-id^="dashboard:"]')).toBeNull()
  } finally {
    await act(async () => root.unmount()); host.remove(); assign.mockRestore(); directory.load.mockReset()
  }
})
