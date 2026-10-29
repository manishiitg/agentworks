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
  enabled: ['work', 'agentworks'],
  active: [] as any[],
  selectedTab: null as string | null,
  history: vi.fn(),
  navigation: vi.fn(async () => { throw new Error('inventory unavailable') }),
  openTab: vi.fn(),
  setMetadata: vi.fn(),
  openActivity: vi.fn(),
  openProduct: vi.fn(),
  openSettings: vi.fn(),
  resume: vi.fn(),
  close: vi.fn(async () => {}),
  selectCode: vi.fn(),
  selectWork: vi.fn(),
  activeSessions: vi.fn(async () => []),
  app: { showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null },
  openNavigation: vi.fn(() => true),
}))
vi.mock('./ConnectedAgentsSidebar', () => ({ default: () => <p data-testid="connected-agent-list">Connected agents · reviewer</p> }))
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
vi.mock('./SidebarStepInspections', () => ({ default: ({ visible }: { visible: boolean }) => <div data-testid="step-inspections" data-visible={visible} /> }))
vi.mock('../../utils/chatIdentity', () => ({
  captureChatIdentity: () => 1,
  isChatIdentityCurrent: () => true,
}))
vi.mock('../../utils/closeWorkspaceChatTab', () => ({ canCloseWorkspaceChatTab: (tab: any) => tab.metadata?.isViewOnly === true, closeWorkspaceChatTab: mocks.close }))
vi.mock('../../utils/globalProductNavigation', () => ({
  openGlobalTab: mocks.openTab,
  openGlobalActivitySession: mocks.openActivity,
}))
vi.mock('../../utils/workspaceSettingsNavigation', () => ({ openWorkspaceSettings: mocks.openSettings }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: mocks.openProduct }))
vi.mock('../../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: async () => [] }))
vi.mock('../../products/work/projectProduct', () => ({ CREW_PRODUCT: { profileId: 'work' } }))
vi.mock('../../products/productSurfaceConfig', () => ({
  enabledProductSurfaces: () => mocks.enabled,
  intersectAllowedProductSurfaces: (surfaces: string[], allowed: string[] | null) =>
    allowed ? surfaces.filter((surface) => allowed.includes(surface)) : surfaces,
  PRODUCT_SURFACE_LABELS: { work: 'Crew', agentworks: 'Goals', relays: 'Flows', knowledgebase: 'Brain', 'mcp-gateway': 'Vault', bullpen: 'AgentBridge' },
}))
vi.mock('../ProductSurfaceIcon', () => ({ ProductSurfaceIcon: ({ surface }: { surface: string }) => <svg data-product-icon={surface} /> }))
import WorkspaceChatsSidebar from './WorkspaceChatsSidebar'
import { SharedToolShortcuts } from './SharedToolShortcuts'
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
  mocks.enabled = ['work', 'agentworks']
  mocks.active = []
  mocks.selectedTab = null
  mocks.user = { id: 'alice', allowed_products: null, allowed_workflow_ids: null }
  mocks.app = { showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null }
  useSidebarHistoryStore.setState({ request: null, generation: 0 })
})
async function render(check: (host: HTMLDivElement, rerender: () => Promise<void>) => Promise<void>, content = () => <WorkspaceChatsSidebar />) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(content()))
    await check(host, async () => {
      await act(async () => root.render(content()))
    })
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
}
it('opens a server-listed co-owned Crew before its first chat exists', async () => {
  mocks.history.mockResolvedValue({ sessions: [] })
  mocks.navigation.mockResolvedValueOnce({ workspaces: [{ id: 'co-owned', workspace_id: 'work:co-owned', profile_id: 'work', name: 'Shared builder', workspace_path: 'Crew/co-owned' }] } as never)
  await render(async host => {
    const button = host.querySelector('button[title="Crew · Shared builder"]') as HTMLButtonElement
    expect(button).not.toBeNull()
    expect(host.textContent).not.toContain('No chats yet.')
    await act(async () => button.click())
    expect(mocks.selectWork).toHaveBeenCalledWith('co-owned')
    expect(mocks.openProduct).toHaveBeenCalledWith('work')
    expect(mocks.openActivity).not.toHaveBeenCalled()
  })
})
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
it('keeps saved project icons on every sidebar row layout and falls back for missing or broken images', async () => {
  mocks.enabled = ['work', 'agentworks', 'relays']
  mocks.history.mockResolvedValue({ sessions: [] })
  const image = 'data:image/png;base64,broken'
  mocks.navigation.mockResolvedValueOnce({ workspaces: [
    { id: 'empty', workspace_id: 'work:empty', profile_id: 'work', name: 'Before first chat', icon: image, workspace_path: 'Crew/empty' },
    { id: 'plain', workspace_id: 'work:plain', profile_id: 'work', name: 'No custom icon', workspace_path: 'Crew/plain' },
  ] } as never)
  mocks.presets = [
    { id: 'goal', label: 'Campaign', icon: '📈', selectedFolder: { filepath: 'Workflow/campaign' } },
    { id: 'flow', label: 'Dispatch', icon: '🔀', workflowKind: 'relay', selectedFolder: { filepath: 'Workflow/dispatch' } },
  ]
  mocks.tabs = {
    goal: { tabId: 'goal', sessionId: 'goal-main', name: 'Campaign', createdAt: 1, metadata: { mode: 'workflow', presetQueryId: 'goal' } },
    flow: { tabId: 'flow', sessionId: 'flow-main', name: 'Dispatch', createdAt: 1, metadata: { mode: 'workflow', presetQueryId: 'flow' } },
    run: { tabId: 'run', sessionId: 'flow-run', name: 'Delivery', createdAt: 1, metadata: { mode: 'workflow', presetQueryId: 'flow', isViewOnly: true } },
  }
  mocks.builders = {
    goal: { session_id: 'goal-main', sessions: { 'goal-main': { is_builder: true, kind: 'builder' } } },
    flow: { session_id: 'flow-main', sessions: { 'flow-main': { is_builder: true, kind: 'builder' }, 'flow-run': { kind: 'run' } } },
  }
  await render(async host => {
    // One list of projects with no Crew/Goals/Flows headings (owner, 2026-10-29).
    expect(host.querySelector('section[aria-label$=" projects"]')).toBeNull()
    const crew = host.querySelector<HTMLButtonElement>('[title="Crew · Before first chat"]')!
    const upload = crew.querySelector('img')!
    expect(upload.getAttribute('src')).toBe(image)
    expect(upload.loading).toBe('lazy')
    expect(upload.parentElement?.className).toContain('h-4 w-4')
    expect(upload.parentElement?.className).not.toContain('h-5')
    expect(host.querySelector('[aria-label="Campaign · Goals"]')?.textContent).toContain('📈')
    expect(host.querySelector('[title^="Flows · Dispatch · 2 chats"]')?.textContent).toContain('🔀')
    expect(host.querySelector('[title="Crew · No custom icon"] [data-product-icon="work"]')).not.toBeNull()
    await act(async () => upload.dispatchEvent(new Event('error')))
    expect(crew.querySelector('img')).toBeNull()
    expect(crew.querySelector('[data-product-icon="work"]')).not.toBeNull()
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
    expect(chat.parentElement!.querySelector('[aria-label="Running"]')).not.toBeNull()
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
    // A schedule shows only in the hover label, not as an icon and date line.
    expect(host.querySelector('[title^="Daily briefing · Goals"] [title="Schedule"] svg')).toBeNull()
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

it('collapses a single Goal without closing its chat and remembers the project preference', async () => {
  mocks.product = 'agentworks'
  mocks.presets = [{ id: 'goal', label: 'ICICI corporate', selectedFolder: { filepath: 'Workflow/icici' } }]
  mocks.tabs = {
    main: { tabId: 'main', sessionId: 'main', name: 'ICICI corporate', createdAt: 1, isStreaming: true,
      metadata: { mode: 'workflow', phaseId: 'workflow-builder', presetQueryId: 'goal', agentProfileBuilder: true } },
  }
  mocks.builders = { goal: { session_id: 'main', sessions: { main: { is_builder: true, kind: 'builder' } } } }
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async host => {
    const toggle = host.querySelector<HTMLButtonElement>('[aria-label="Expand ICICI corporate"]')!
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    const panel = host.querySelector<HTMLDivElement>(`#${toggle.getAttribute('aria-controls')}`)!
    expect(panel.hidden).toBe(true)
    expect(panel.querySelector('[data-visible="false"]')).not.toBeNull()
    await act(async () => toggle.click())
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(panel.hidden).toBe(false)
    expect(panel.querySelector('[data-visible="true"]')).not.toBeNull()
    expect(mocks.openTab).not.toHaveBeenCalled()
    expect(mocks.close).not.toHaveBeenCalled()
    await act(async () => host.querySelector<HTMLButtonElement>('[title="ICICI corporate · Goals"]')!.click())
    expect(mocks.openTab).toHaveBeenCalledWith('main')
    expect(panel.hidden).toBe(false)
  })
  await render(async (host, rerender) => {
    const toggle = host.querySelector<HTMLButtonElement>('[aria-label="Collapse ICICI corporate"]')!
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    await act(async () => toggle.click())
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(host.querySelector('[aria-label="Running"]')).not.toBeNull()
    mocks.user = { ...mocks.user, id: 'bob' }
    await rerender()
    expect(host.querySelector('[aria-label="Expand ICICI corporate"]')).not.toBeNull()
  })
  await render(async host => {
    expect(host.querySelector('[aria-label="Expand ICICI corporate"]')).not.toBeNull()
  })
})

it('keeps Brain and Vault available without chats while respecting product access', async () => {
  mocks.enabled = ['agentworks', 'work', 'knowledgebase', 'mcp-gateway']
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host, rerender) => {
    const brain = host.querySelector<HTMLButtonElement>('[aria-label="Brain"]')!
    const vault = host.querySelector<HTMLButtonElement>('[aria-label="Vault"]')!
    expect(brain).not.toBeNull()
    expect(vault).not.toBeNull()
    expect(host.querySelector('[aria-label="Show Brain chats"]')).toBeNull()
    await act(async () => brain.click())
    expect(mocks.openProduct).toHaveBeenLastCalledWith('knowledgebase')
    await act(async () => vault.click())
    expect(mocks.openProduct).toHaveBeenLastCalledWith('mcp-gateway')
    const cli = host.querySelector<HTMLButtonElement>('[aria-label="AgentWorks CLI & MCP"]')!
    expect(cli.textContent).toBe('')
    await act(async () => cli.click())
    expect(mocks.openSettings).toHaveBeenCalledWith('mcp')
    expect(mocks.openTab).not.toHaveBeenCalled()
    mocks.user.allowed_products = ['knowledgebase']
    await rerender()
    expect(host.querySelector('[aria-label="Brain"]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Vault"]')).toBeNull()
    mocks.enabled = ['agentworks', 'work']
    await rerender()
    expect(host.querySelector('[aria-label="Shared tools"]')).toBeNull()
  })
})

it('opens the product directly even when it has existing chats', async () => {
  mocks.enabled = ['knowledgebase', 'mcp-gateway']
  mocks.product = 'knowledgebase'
  mocks.tabs = { brain: { tabId: 'brain', sessionId: 'brain-chat', name: 'Folder access help', createdAt: 1,
    metadata: { agentProfileId: 'knowledgebase' } } }
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async host => {
    const footer = host.querySelector('[data-testid="shared-tool-shortcuts"]')!
    expect(footer.querySelector('[aria-expanded]')).toBeNull()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Brain"]')!.click())
    expect(mocks.openProduct).toHaveBeenCalledWith('knowledgebase')
    expect(mocks.openTab).not.toHaveBeenCalled()
    expect(host.querySelector('[aria-label="Brain"]')?.getAttribute('aria-current')).toBe('page')
  })
})

it('opens AgentBridge directly with current product permission checks', async () => {
  mocks.enabled = ['knowledgebase', 'mcp-gateway', 'bullpen']
  mocks.history.mockResolvedValue({ sessions: [] })
  await render(async (host, rerender) => {
    const bridge = host.querySelector<HTMLButtonElement>('[aria-label="AgentBridge"]')!
    expect(bridge).not.toBeNull()
    expect(bridge.closest('[aria-label="Shared tools"]')).not.toBeNull()
    expect(host.querySelector('[data-testid="connected-agent-list"]')).toBeNull()
    await act(async () => bridge.click())
    expect(mocks.openProduct).toHaveBeenLastCalledWith('bullpen')
    expect(host.querySelector('[aria-label="Show connected agents"]')).toBeNull()
    mocks.user.allowed_products = ['knowledgebase']
    await rerender()
    expect(host.querySelector('[aria-label="AgentBridge"]')).toBeNull()
    expect(host.querySelector('[data-testid="connected-agent-list"]')).toBeNull()
  })
})

it('names expanded shared shortcuts on keyboard focus and rechecks access before navigation', async () => {
  mocks.enabled = ['work', 'agentworks', 'relays', 'knowledgebase', 'mcp-gateway', 'bullpen']
  await render(async host => {
    const brain = host.querySelector<HTMLButtonElement>('[aria-label="Brain"]')!
    const vault = host.querySelector<HTMLButtonElement>('[aria-label="Vault"]')!
    expect(brain.textContent).toBe('')
    expect(host.querySelector('a[aria-label="Dashboards"]')?.getAttribute('href')).toBe('/dashboards')
    await act(async () => brain.focus())
    expect(host.querySelector('[role="tooltip"]')?.textContent).toBe('Brain')
    await act(async () => brain.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(host.querySelector('[role="tooltip"]')).toBeNull()
    mocks.user.allowed_products = ['knowledgebase']
    await act(async () => { vault.click() })
    expect(mocks.openProduct).not.toHaveBeenCalled()
    await act(async () => brain.click())
    expect(mocks.openProduct).toHaveBeenCalledWith('knowledgebase')
  }, () => <SharedToolShortcuts />)
})
