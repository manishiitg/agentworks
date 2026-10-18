// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  user: { id: 'alice', allowed_products: null, allowed_workflow_ids: null } as any,
  tabs: {} as any,
  active: [] as any[],
  selectedTab: null as string | null,
  history: vi.fn(),
  openTab: vi.fn(),
  openActivity: vi.fn(),
  openProduct: vi.fn(),
  resume: vi.fn(),
  close: vi.fn(async () => {}),
  selectCode: vi.fn(),
  selectWork: vi.fn(),
  activeSessions: vi.fn(async () => []),
  app: { showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null },
  openNavigation: vi.fn(() => true),
}))
vi.mock('../../stores/useAppStore', () => ({ useAppStore: (select: any) => select(mocks.app) }))
vi.mock('../../stores/useLLMStore', () => ({ useLLMStore: (select: any) => select({ showLLMModal: false }) }))
vi.mock('../../utils/quickNavigation', () => ({
  quickNavigationItems: (user: any) => !user.allowed_products || user.allowed_products.includes('agentworks') ? [{ id: 'menu:activity', action: 'activity' }] : [],
  openQuickNavigation: mocks.openNavigation,
}))
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: Object.assign((select: any) => select({ user: mocks.user }), {
    getState: () => ({ user: mocks.user }),
  }),
}))
vi.mock('../../stores/useChatStore', () => ({
  useChatStore: Object.assign(
    (select: any) =>
      select({ chatTabs: mocks.tabs, activeSessionsCache: mocks.active, activeTabId: mocks.selectedTab }),
    { getState: () => ({ chatTabs: mocks.tabs, renameTab: vi.fn(), setTabMetadata: vi.fn(), getActiveSessions: mocks.activeSessions, addToast: vi.fn() }) }
  ),
}))
vi.mock('../../stores/useGlobalPresetStore', () => ({
  useGlobalPresetStore: (select: any) => select({ workflowPresets: [], recentPresetOrder: [] }),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({
  useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'local' }),
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: Object.assign((select: any) => select({ productSurface: 'code' }), {
    getState: () => ({
      productSurface: 'code',
      setSelectedCodeProjectId: mocks.selectCode,
      setSelectedWorkProjectId: mocks.selectWork,
    }),
  }),
}))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: async () => ({ jobs: [] }) } }))
vi.mock('../../services/api', () => ({ agentApi: { listChatHistorySessions: mocks.history } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../hooks/useResumePreviousChat', () => ({ useResumePreviousChat: () => mocks.resume }))
vi.mock('../../utils/chatIdentity', () => ({
  captureChatIdentity: () => 1,
  isChatIdentityCurrent: () => true,
}))
vi.mock('../../utils/closeWorkspaceChatTab', () => ({ canCloseWorkspaceChatTab: (tab: any) => tab.metadata?.isViewOnly === true, closeWorkspaceChatTab: mocks.close }))
vi.mock('../../utils/globalProductNavigation', () => ({
  openGlobalTab: mocks.openTab,
  openGlobalActivitySession: mocks.openActivity,
}))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: mocks.openProduct }))
vi.mock('../../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: async () => [] }))
vi.mock('../../products/work/projectProduct', () => ({ CODE_PRODUCT: { profileId: 'code' } }))
vi.mock('../../products/productSurfaceConfig', () => ({
  enabledProductSurfaces: () => ['code', 'work', 'agentworks'],
  intersectAllowedProductSurfaces: (surfaces: string[], allowed: string[] | null) =>
    allowed ? surfaces.filter((surface) => allowed.includes(surface)) : surfaces,
  PRODUCT_SURFACE_LABELS: { code: 'Code', work: 'Crew', agentworks: 'Goals' },
}))
vi.mock('../ProductSurfaceIcon', () => ({ ProductSurfaceIcon: () => null }))
import WorkspaceChatsSidebar from './WorkspaceChatsSidebar'
import { useSidebarHistoryStore } from '../../stores/useSidebarHistoryStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  vi.clearAllMocks()
  mocks.tabs = {}
  mocks.active = []
  mocks.selectedTab = null
  mocks.user = { id: 'alice', allowed_products: null, allowed_workflow_ids: null }
  mocks.app = { showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null }
  useSidebarHistoryStore.setState({ request: null, generation: 0 })
})
async function render(check: (host: HTMLDivElement, rerender: () => Promise<void>) => Promise<void>) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkspaceChatsSidebar />))
    await check(host, async () => {
      await act(async () => root.render(<WorkspaceChatsSidebar />))
    })
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
}
it('opens existing tabs through the shared navigator and hands recent Code history to its product', async () => {
  mocks.tabs = {
    tab: {
      tabId: 'tab',
      sessionId: 'code:project:one',
      name: 'My Code',
      createdAt: 1,
      metadata: { agentProfileId: 'code', agentProfileProjectId: 'one' },
    },
  }
  mocks.history.mockResolvedValue({
    sessions: [{ session_id: 'code:project:two', title: 'Saved Code chat' }],
  })
  await render(async (host) => {
    await act(async () => (host.querySelector('[title="My Code · Code"]') as HTMLButtonElement).click())
    expect(mocks.openTab).toHaveBeenCalledWith('tab')
    await act(async () =>
      (host.querySelector('[title="Saved Code chat · Code"]') as HTMLButtonElement).click()
    )
    expect(mocks.selectCode).toHaveBeenCalledWith('two')
    expect(useSidebarHistoryStore.getState().request?.session.session_id).toBe('code:project:two')
    expect(mocks.history).toHaveBeenCalledWith(30)
  })
})
it('discards an old account’s late history response', async () => {
  let finish!: (value: any) => void
  mocks.history
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    .mockResolvedValue({ sessions: [] })
  await render(async (host, rerender) => {
    mocks.user = { id: 'bob', allowed_products: null, allowed_workflow_ids: null }
    await rerender()
    await act(async () =>
      finish({ sessions: [{ session_id: 'code:project:private', title: 'Alice private chat' }] })
    )
    expect(host.textContent).not.toContain('Alice private chat')
    expect(host.textContent).toContain('No chats yet.')
  })
})
it('does not turn an unavailable history catalog into a false empty state', async () => {
  mocks.history.mockRejectedValue(new Error('offline'))
  await render(async (host) => {
    expect(host.textContent).toContain('Some recent chats could not load')
    expect(host.textContent).not.toContain('No chats yet.')
  })
})

it('shows per-chat running state under one collapsible workspace without merging equal chat names', async () => {
  mocks.tabs = Object.fromEntries(['one', 'two'].map((id) => [id, {
    tabId: id, sessionId: id, name: 'Analysis', createdAt: 1, isStreaming: true,
    metadata: { agentProfileId: 'code', agentProfileProjectId: 'project', agentProfileProjectTitle: 'Accounting' },
  }]))
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host) => {
    const heading = host.querySelector<HTMLButtonElement>('[title="Code · Accounting · 2 chats · 2 running"]')!
    expect(heading.textContent).toContain('2 working')
    const panel = host.querySelector<HTMLDivElement>(`#${heading.getAttribute('aria-controls')}`)!
    expect(panel.querySelectorAll('[title="Analysis · Code"]')).toHaveLength(2)
    expect(panel.querySelectorAll('[aria-label="Running"]')).toHaveLength(2)
    await act(async () => heading.click())
    expect(panel.hidden).toBe(true)
    expect(heading.getAttribute('aria-expanded')).toBe('false')
    await act(async () => heading.click())
    expect(panel.hidden).toBe(false)
  })
})

it('removes the filter field and closes a run view without selecting it', async () => {
  mocks.tabs = {
    builder: { tabId: 'builder', sessionId: 'builder', name: 'Trading', createdAt: 1, metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'wf', agentProfileProjectTitle: 'Trading' } },
    run: { tabId: 'run', sessionId: 'schedule-run', name: 'News Briefing', createdAt: 2, metadata: { mode: 'workflow', presetQueryId: 'wf', isViewOnly: true, isScheduledRun: true, agentProfileProjectTitle: 'Trading' } },
  }
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host) => {
    expect(host.querySelector('input')).toBeNull()
    expect(host.textContent).toContain('Main chat')
    const close = host.querySelector<HTMLButtonElement>('[aria-label="Close News Briefing"]')!
    expect(close).not.toBeNull()
    await act(async () => close.click())
    expect(mocks.close).toHaveBeenCalledWith('run')
    expect(mocks.openTab).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Close Main chat"]')).toBeNull()
  })
})

it('opens single-chat workspaces directly and distinguishes matching names across products', async () => {
  mocks.tabs = {
    code: { tabId: 'code', sessionId: 'code:project:one', name: 'Accounting', createdAt: 1, metadata: { agentProfileId: 'code', agentProfileProjectId: 'one', agentProfileProjectTitle: 'Accounting' } },
    crew: { tabId: 'crew', sessionId: 'work:project:two', name: 'Accounting', createdAt: 2, metadata: { agentProfileId: 'work', agentProfileProjectId: 'two', agentProfileProjectTitle: 'Accounting' } },
  }
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async host => {
    const code = host.querySelector<HTMLButtonElement>('[title="Accounting · Code"]')!
    const crew = host.querySelector<HTMLButtonElement>('[title="Accounting · Crew"]')!
    expect(code.textContent).toBe('AccountingCode')
    expect(crew.textContent).toBe('AccountingCrew')
    expect(code.hasAttribute('aria-expanded')).toBe(false)
    expect(host.textContent).not.toContain('Main chat')
    await act(async () => code.click())
    expect(mocks.openTab).toHaveBeenLastCalledWith('code')
    await act(async () => crew.click())
    expect(mocks.openTab).toHaveBeenLastCalledWith('crew')
    expect(mocks.openProduct).toHaveBeenLastCalledWith('work')
  })
})

it('keeps Goals activity out of the shared chat sidebar', async () => {
  mocks.app.showWorkflowsOverview = true
  mocks.tabs = { code: { tabId: 'code', sessionId: 'code:project:one', name: 'Accounting', createdAt: 1, metadata: { agentProfileId: 'code', agentProfileProjectId: 'one', agentProfileProjectTitle: 'Accounting' } } }
  mocks.selectedTab = 'code'
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host, rerender) => {
    expect(host.querySelector('[aria-label="Activity"]')).toBeNull()
    expect(host.querySelector('[title="Accounting · Code"]')?.hasAttribute('aria-current')).toBe(false)
    expect(mocks.openNavigation).not.toHaveBeenCalled()
    mocks.user.allowed_products = ['work']
    await rerender()
    expect(host.querySelector('[aria-label="Activity"]')).toBeNull()
  })
})
