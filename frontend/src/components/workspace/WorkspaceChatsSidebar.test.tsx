// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  user: { id: 'alice', allowed_products: null, allowed_workflow_ids: null } as any,
  tabs: {} as any,
  presets: [] as any[],
  builders: {} as Record<string, any>,
  product: 'work',
  active: [] as any[],
  selectedTab: null as string | null,
  history: vi.fn(),
  navigation: vi.fn(async () => { throw new Error('inventory unavailable') }),
  openTab: vi.fn(),
  setMetadata: vi.fn(),
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
    { getState: () => ({ chatTabs: mocks.tabs, renameTab: vi.fn(), setTabMetadata: mocks.setMetadata, getActiveSessions: mocks.activeSessions, addToast: vi.fn() }) }
  ),
}))
vi.mock('../../stores/useGlobalPresetStore', () => ({
  useGlobalPresetStore: (select: any) => select({ workflowPresets: mocks.presets, recentPresetOrder: [] }),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({
  useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'local' }),
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: Object.assign((select: any) => select({ productSurface: mocks.product }), {
    getState: () => ({
      productSurface: mocks.product,
      setSelectedWorkProjectId: mocks.selectWork,
    }),
  }),
}))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: async () => ({ jobs: [] }) } }))
vi.mock('../../services/api', () => ({ agentApi: { listChatHistorySessions: mocks.history, listWorkspaceNavigation: mocks.navigation } }))
vi.mock('../../hooks/useWorkflowBuilderBindings', () => ({ useWorkflowBuilderBindings: () => mocks.builders }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../hooks/useResumePreviousChat', () => ({ useResumePreviousChat: () => mocks.resume }))
vi.mock('./SidebarStepInspections', () => ({ default: () => null }))
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
vi.mock('../../products/work/projectProduct', () => ({ CREW_PRODUCT: { profileId: 'work' } }))
vi.mock('../../products/productSurfaceConfig', () => ({
  enabledProductSurfaces: () => ['work', 'agentworks'],
  intersectAllowedProductSurfaces: (surfaces: string[], allowed: string[] | null) =>
    allowed ? surfaces.filter((surface) => allowed.includes(surface)) : surfaces,
  PRODUCT_SURFACE_LABELS: { work: 'Crew', agentworks: 'Goals' },
}))
vi.mock('../ProductSurfaceIcon', () => ({ ProductSurfaceIcon: () => null }))
import WorkspaceChatsSidebar from './WorkspaceChatsSidebar'
import { useSidebarHistoryStore } from '../../stores/useSidebarHistoryStore'
import { useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  useSidebarExpansionStore.setState({ expanded: {} })
  vi.clearAllMocks()
  mocks.tabs = {}
  mocks.presets = []
  mocks.builders = {}
  mocks.product = 'work'
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
it('opens existing tabs through the shared navigator and lists no stored history', async () => {
  mocks.tabs = {
    tab: {
      tabId: 'tab',
      sessionId: 'work:project:one',
      name: 'My Crew',
      createdAt: 1,
      metadata: { agentProfileId: 'work', agentProfileProjectId: 'one' },
    },
  }
  mocks.history.mockResolvedValue({
    sessions: [{ session_id: 'work:project:two', title: 'Saved Crew chat' }],
  })
  await render(async (host) => {
    await act(async () => (host.querySelector('[title="My Crew · Crew"]') as HTMLButtonElement).click())
    expect(mocks.openTab).toHaveBeenCalledWith('tab')
    expect(host.querySelector('[title="Saved Crew chat · Crew"]')).toBeNull()
  })
})
it('labels remembered Goal Run chats from server kind while keeping one unnumbered Builder', async () => {
  mocks.product = 'agentworks'
  mocks.presets = [{ id: 'goal', label: 'Campaign', selectedFolder: { filepath: 'Workflow/campaign' } }]
  const metadata = { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'goal', agentProfileBuilder: true }
  mocks.tabs = {
    main: { tabId: 'main', sessionId: 'main', name: 'Campaign', createdAt: 1, metadata },
    one: { tabId: 'one', sessionId: 'pat-one', name: 'Campaign', createdAt: 1, metadata },
    two: { tabId: 'two', sessionId: 'pat-two', name: 'Chat 2', createdAt: 1, metadata },
    named: { tabId: 'named', sessionId: 'pat-named', name: 'Notes lookup', createdAt: 1, metadata },
  }
  mocks.builders = { goal: { session_id: 'main', sessions: {
    main: { is_builder: true, kind: 'builder' },
    'pat-one': { is_builder: false, kind: 'run' }, 'pat-two': { is_builder: false, kind: 'run' },
    'pat-named': { is_builder: false, kind: 'run' },
  } } }
  mocks.history.mockResolvedValue({ sessions: [
    { session_id: 'pat-one', created_at: '2026-10-19T15:00:00Z' },
    { session_id: 'pat-two', created_at: '2026-10-19T16:00:00Z' },
  ] })
  await render(async host => {
    expect(host.querySelectorAll('[aria-label="Builder · Goals"]')).toHaveLength(1)
    const runs = [...host.querySelectorAll<HTMLButtonElement>('button')].filter(button => button.getAttribute('aria-label')?.startsWith('Run · Goals · Run'))
    expect(runs).toHaveLength(2)
    expect(runs[0].textContent).not.toBe(runs[1].textContent)
    expect(runs.every(button => !button.getAttribute('aria-label')?.includes('1970'))).toBe(true)
    expect(host.querySelector('[title^="Notes lookup · Goals"]')?.textContent).toContain('Notes lookup')
    expect(host.textContent).not.toMatch(/Chat [12]/)
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
      finish({ sessions: [{ session_id: 'work:project:private', title: 'Alice private chat' }] })
    )
    expect(host.textContent).not.toContain('Alice private chat')
    expect(host.textContent).toContain('Some projects could not load')
  })
})
it('does not turn an unavailable history catalog into a false empty state', async () => {
  mocks.history.mockRejectedValue(new Error('offline'))
  await render(async (host) => {
    expect(host.textContent).toContain('Some chats could not load')
    expect(host.textContent).not.toContain('No chats yet.')
  })
})

it('shows per-chat running state under one collapsible workspace without merging equal chat names', async () => {
  mocks.tabs = Object.fromEntries(['one', 'two'].map((id) => [id, {
    tabId: id, sessionId: id, name: 'Analysis', createdAt: 1, isStreaming: true,
    metadata: { agentProfileId: 'work', agentProfileProjectId: 'project', agentProfileProjectTitle: 'Accounting' },
  }]))
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host) => {
    const heading = host.querySelector<HTMLButtonElement>('[title="Crew · Accounting · 2 chats · 2 running"]')!
    expect(heading.textContent).toContain('2 working')
    const panel = host.querySelector<HTMLDivElement>(`#${heading.getAttribute('aria-controls')}`)!
    expect(panel.querySelectorAll('[title="Analysis · Crew"]')).toHaveLength(2)
    expect(panel.querySelectorAll('[aria-label="Running"]')).toHaveLength(2)
    expect(panel.hidden).toBe(true)
    expect(heading.getAttribute('aria-expanded')).toBe('false')
    await act(async () => heading.click())
    expect(panel.hidden).toBe(false)
    expect(heading.textContent).not.toContain('2 working')
    await act(async () => heading.click())
    expect(panel.hidden).toBe(true)
    expect(heading.textContent).toContain('2 working')
  })
})

it('shows no Recent chats list and keeps ordinary named chats free of repeated status text', async () => {
  mocks.tabs = {
    analysis: { tabId: 'analysis', sessionId: 'analysis', name: 'Monthly analysis', createdAt: 1, isStreaming: true,
      metadata: { agentProfileId: 'work', agentProfileProjectId: 'one', agentProfileProjectTitle: 'Accounting' } },
  }
  mocks.history.mockResolvedValue({ sessions: [{ session_id: 'work:project:old', title: 'Previous report' }] })
  await render(async host => {
    expect(host.textContent).not.toContain('Recent chats')
    expect(host.textContent).not.toContain('Previous report')
    const chat = host.querySelector<HTMLButtonElement>('[title="Monthly analysis · Crew"]')!
    expect(chat.textContent).toBe('Monthly analysis')
    expect(chat.querySelector('[aria-label="Running"]')).not.toBeNull()
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
    expect(host.textContent).not.toContain('Main chat')
    const close = host.querySelector<HTMLButtonElement>('[aria-label="Close News Briefing"]')!
    expect(close).not.toBeNull()
    await act(async () => close.click())
    expect(mocks.close).toHaveBeenCalledWith('run')
    expect(mocks.openTab).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Close Chat"]')).toBeNull()
  })
})

it('keeps Goals activity out of the shared chat sidebar', async () => {
  mocks.app.showWorkflowsOverview = true
  mocks.tabs = { code: { tabId: 'work', sessionId: 'work:project:one', name: 'Accounting', createdAt: 1, metadata: { agentProfileId: 'work', agentProfileProjectId: 'one', agentProfileProjectTitle: 'Accounting' } } }
  mocks.selectedTab = 'work'
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host, rerender) => {
    expect(host.querySelector('[aria-label="Activity"]')).toBeNull()
    expect(host.querySelector('[title="Accounting · Crew"]')?.hasAttribute('aria-current')).toBe(false)
    expect(mocks.openNavigation).not.toHaveBeenCalled()
    mocks.user.allowed_products = ['work']
    await rerender()
    expect(host.querySelector('[aria-label="Activity"]')).toBeNull()
  })
})

it('labels the parent source and returns from inspection to the parent chat', async () => {
  const target = { session_id:'main', execution_id:'step', workspace_path:'Workflow/goal', name:'Research', step_id:'research', started_at:'2026-10-18T00:00:00Z' }
  mocks.tabs = {
    main: {tabId:'main',sessionId:'main',name:'Planning',createdAt:1,metadata:{mode:'workflow',phaseId:'workflow-builder',presetQueryId:'goal',agentProfileProjectTitle:'My goal',stepInspections:{items:[target],activeId:JSON.stringify(['Workflow/goal','main','step'])}}},
    scheduled: {tabId:'scheduled',sessionId:'schedule-cron--job_1',name:'Daily briefing',createdAt:2,metadata:{mode:'workflow',presetQueryId:'goal',agentProfileProjectTitle:'My goal',isScheduledRun:true}},
    whatsapp: {tabId:'whatsapp',sessionId:'bot-whatsapp-one',name:'Customer question',createdAt:3,metadata:{mode:'workflow',presetQueryId:'goal',agentProfileProjectTitle:'My goal',botPlatform:'whatsapp'}},
  }
  mocks.history.mockResolvedValue({sessions:[]})
  await render(async host=>{
    expect(host.querySelector('[title^="Daily briefing · Goals"]')?.getAttribute('aria-label')).toContain('Schedule')
    expect(host.querySelector('[title="Customer question · Goals"]')?.getAttribute('aria-label')).toContain('WhatsApp')
    expect(host.querySelector('[title^="Daily briefing · Goals"] [title="Schedule"] svg')).not.toBeNull()
    expect(host.querySelector('[title="Customer question · Goals"] [title="WhatsApp"] svg')).not.toBeNull()
    const main=host.querySelector<HTMLButtonElement>('[title="Planning · Goals"]')!
    expect(main.textContent).toBe('Planning')
    expect(main.querySelector('[title="Chat"]')).toBeNull()
    await act(async()=>main.click())
    expect(mocks.setMetadata).toHaveBeenCalledWith('main',{stepInspections:{items:[target],activeId:null}})
    expect(mocks.openTab).toHaveBeenCalledWith('main')
  })
})


it('links to the dashboard library in the shared sidebar and marks its deep link active', async () => {
  window.history.replaceState({}, '', '/dashboards')
  try {
    await render(async host => {
      const link = host.querySelector('nav[aria-label="Dashboards"] a')!
      expect(link.getAttribute('href')).toBe('/dashboards')
      expect(link.getAttribute('aria-current')).toBe('page')
    })
  } finally { window.history.replaceState({}, '', '/') }
})
