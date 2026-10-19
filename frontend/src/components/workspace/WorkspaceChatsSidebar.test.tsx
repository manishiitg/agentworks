// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  user: { id: 'alice', allowed_products: null, allowed_workflow_ids: null } as any,
  tabs: {} as any,
  presets: [] as any[],
  builders: {} as Record<string, any>,
  product: 'code',
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
      setSelectedCodeProjectId: mocks.selectCode,
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
vi.mock('./SidebarWorkspaceInspections', () => ({ default: (props: any) => React.createElement('div', { 'data-workspace-discovery': props.workflowId, 'data-selected': String(props.selected), 'data-excluded': props.listedSessions.join(',') }) }))
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
  mocks.presets = []
  mocks.builders = {}
  mocks.product = 'code'
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
    expect(host.querySelectorAll('[aria-label="Chat · Goals"]')).toHaveLength(1)
    const runs = [...host.querySelectorAll<HTMLButtonElement>('button')].filter(button => button.getAttribute('aria-label')?.startsWith('Run chat · Goals · Run'))
    expect(runs).toHaveLength(2)
    expect(runs[0].textContent).not.toBe(runs[1].textContent)
    expect(runs.every(button => !button.getAttribute('aria-label')?.includes('1970'))).toBe(true)
    expect(host.querySelector('[title="Notes lookup · Goals"]')?.textContent).toBe('Notes lookup')
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
      finish({ sessions: [{ session_id: 'code:project:private', title: 'Alice private chat' }] })
    )
    expect(host.textContent).not.toContain('Alice private chat')
    expect(host.textContent).toContain('Some projects could not load')
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
    expect(heading.textContent).not.toContain('2 working')
    const panel = host.querySelector<HTMLDivElement>(`#${heading.getAttribute('aria-controls')}`)!
    expect(panel.querySelectorAll('[title="Analysis · Code"]')).toHaveLength(2)
    expect(panel.querySelectorAll('[aria-label="Running"]')).toHaveLength(2)
    await act(async () => heading.click())
    expect(panel.hidden).toBe(true)
    expect(heading.getAttribute('aria-expanded')).toBe('false')
    expect(heading.textContent).toContain('2 working')
    await act(async () => heading.click())
    expect(panel.hidden).toBe(false)
    expect(heading.textContent).not.toContain('2 working')
  })
})

it('starts Recent collapsed and keeps ordinary named chats free of repeated status text', async () => {
  mocks.tabs = {
    analysis: { tabId: 'analysis', sessionId: 'analysis', name: 'Monthly analysis', createdAt: 1, isStreaming: true,
      metadata: { agentProfileId: 'code', agentProfileProjectId: 'one', agentProfileProjectTitle: 'Accounting' } },
  }
  mocks.history.mockResolvedValue({ sessions: [{ session_id: 'code:project:old', title: 'Previous report' }] })
  await render(async host => {
    const recent = host.querySelector<HTMLButtonElement>('[aria-controls="sidebar-chats-recent"]')!
    const panel = host.querySelector<HTMLDivElement>('#sidebar-chats-recent')!
    expect(recent.getAttribute('aria-expanded')).toBe('false')
    expect(panel.hidden).toBe(true)
    const chat = host.querySelector<HTMLButtonElement>('[title="Monthly analysis · Code"]')!
    expect(chat.textContent).toBe('Monthly analysis')
    expect(chat.querySelector('[aria-label="Running"]')).not.toBeNull()
    await act(async () => recent.click())
    expect(panel.hidden).toBe(false)
    expect(panel.textContent).toContain('Previous report')
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

it('labels the parent source and returns from inspection to the parent chat', async () => {
  const target = { session_id:'main', execution_id:'step', workspace_path:'Workflow/goal', name:'Research', step_id:'research', started_at:'2026-10-18T00:00:00Z' }
  mocks.tabs = {
    main: {tabId:'main',sessionId:'main',name:'Planning',createdAt:1,metadata:{mode:'workflow',phaseId:'workflow-builder',presetQueryId:'goal',agentProfileProjectTitle:'My goal',stepInspections:{items:[target],activeId:JSON.stringify(['Workflow/goal','main','step'])}}},
    scheduled: {tabId:'scheduled',sessionId:'schedule-cron--job_1',name:'Daily briefing',createdAt:2,metadata:{mode:'workflow',presetQueryId:'goal',agentProfileProjectTitle:'My goal',isScheduledRun:true}},
    whatsapp: {tabId:'whatsapp',sessionId:'bot-whatsapp-one',name:'Customer question',createdAt:3,metadata:{mode:'workflow',presetQueryId:'goal',agentProfileProjectTitle:'My goal',botPlatform:'whatsapp'}},
  }
  mocks.history.mockResolvedValue({sessions:[]})
  await render(async host=>{
    expect(host.querySelector('[title="Daily briefing · Goals"]')?.getAttribute('aria-label')).toContain('Schedule')
    expect(host.querySelector('[title="Customer question · Goals"]')?.getAttribute('aria-label')).toContain('WhatsApp')
    expect(host.querySelector('[title="Daily briefing · Goals"] [title="Schedule"] svg')).not.toBeNull()
    expect(host.querySelector('[title="Customer question · Goals"] [title="WhatsApp"] svg')).not.toBeNull()
    const main=host.querySelector<HTMLButtonElement>('[title="Planning · Goals"]')!
    expect(main.textContent).toBe('Planning')
    expect(main.querySelector('[title="Chat"]')).toBeNull()
    await act(async()=>main.click())
    expect(mocks.setMetadata).toHaveBeenCalledWith('main',{stepInspections:{items:[target],activeId:null}})
    expect(mocks.openTab).toHaveBeenCalledWith('main')
  })
})

it('mounts workspace-level Inspect discovery for the selected Goal even when only its main chat is listed', async () => {
  mocks.product = 'agentworks'
  mocks.presets = [{ id: 'goal', label: 'Research', selectedFolder: { filepath: 'Workflow/demo' } }]
  mocks.tabs = { main: { tabId: 'main', sessionId: 'main-chat', name: 'Research', createdAt: 1, metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'goal' } } }
  mocks.selectedTab = 'main'
  mocks.builders = { goal: { sessions: { 'main-chat': { is_builder: true } } } }
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async host => {
    const discovery = host.querySelector('[data-workspace-discovery="goal"]')!
    expect(discovery).not.toBeNull()
    expect(discovery.getAttribute('data-selected')).toBe('true')
    expect(discovery.getAttribute('data-excluded')).toBe('main-chat')
  })
})
