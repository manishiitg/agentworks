// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { QuickSwitcher } from './QuickSwitcher'

const fixture = vi.hoisted(() => ({
  post: vi.fn(async () => ({
    data: {
      results: [{ session_id: 'chat', id: 'one', title: 'Release review', sender: 'Assistant', snippet: 'The release notes are ready.' }],
      next_cursor: '',
    },
  })),
  findFiles: vi.fn(),
  chats: {
    activeTabId: null,
    chatTabs: { release: { tabId: 'release', name: 'Release review', metadata: { mode: 'multi-agent' } } },
    activeSessionsCache: [],
    getActiveSessions: vi.fn(async () => []),
  },
  presets: { activePresetIds: {}, workflowPresets: [], recentPresetOrder: [], recentPresetAccessedAt: {}, workflowPresetsLoaded: true },
  auth: { user: { id: 'owner' }, isMultiUserMode: false },
}))
vi.mock('../services/api', async importOriginal => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return { ...actual, default: { ...actual.default, post: fixture.post } }
})
vi.mock('../services/workspaceGit', () => ({ workspaceGitApi: { find: fixture.findFiles } }))
vi.mock('../utils/projectFileLinks', () => ({ projectFilesFolder: () => 'project-files', requestOpenProjectFile: vi.fn() }))
vi.mock('../services/dashboardDirectory', () => ({ loadDashboardDirectory: async () => ({ dashboards: [] }) }))
vi.mock('../api/scheduler', () => ({ schedulerApi: { listJobs: async () => ({ jobs: [], total: 0 }) } }))
vi.mock('../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: async () => [{ id: 'planning', title: 'Release planning' }] }))
vi.mock('../stores', () => ({ useChatStore: Object.assign((select: (state: typeof fixture.chats) => unknown) => select(fixture.chats), { getState: () => fixture.chats }) }))
vi.mock('../stores/useGlobalPresetStore', () => ({ useGlobalPresetStore: Object.assign((select: (state: typeof fixture.presets) => unknown) => select(fixture.presets), { getState: () => fixture.presets }) }))
vi.mock('../stores/useAuthStore', async importOriginal => ({
  ...await importOriginal<typeof import('../stores/useAuthStore')>(),
  useAuthStore: Object.assign((select: (state: typeof fixture.auth) => unknown) => select(fixture.auth), { getState: () => fixture.auth }),
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('keeps names in All and looks up saved messages only while Chats is selected', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const input = () => host.querySelector<HTMLInputElement>('[role="combobox"]')!
  const tab = (name: string) => [...host.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find(button => button.textContent === name)!
  const message = () => host.querySelector('[data-navigation-id="message:chat:one"]')
  const flushDebounce = () => act(async () => { await vi.advanceTimersByTimeAsync(400) })
  vi.useFakeTimers()
  try {
    await act(async () => root.render(<QuickSwitcher isOpen onClose={() => {}} initialQuery="release" />))
    await act(async () => { await vi.dynamicImportSettled() })
    await flushDebounce()
    expect(tab('All').getAttribute('aria-selected')).toBe('true')
    expect(host.querySelector('[data-navigation-id="chat:release"]')).not.toBeNull()
    expect(host.querySelector('[data-navigation-id="crew-project:planning"]')).not.toBeNull()
    expect(fixture.post).not.toHaveBeenCalled()
    expect(fixture.findFiles).not.toHaveBeenCalled()

    await act(async () => tab('Chats').click())
    expect(input().value).toBe('release')
    await flushDebounce()
    expect(fixture.post).toHaveBeenCalledExactlyOnceWith('/api/chat-search', { query: 'release', cursor: '', page: 0 }, expect.objectContaining({ skipSessionContext: true }))
    expect(message()).not.toBeNull()
    expect(host.querySelector('[data-navigation-id="chat:release"]')).not.toBeNull()

    await act(async () => tab('All').click())
    await flushDebounce()
    expect(input().value).toBe('release')
    expect(message()).toBeNull()
    expect(fixture.post).toHaveBeenCalledTimes(1)
  } finally {
    await act(async () => root.unmount())
    vi.useRealTimers()
  }
})
