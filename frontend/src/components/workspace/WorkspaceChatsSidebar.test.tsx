// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({
  user: { id: 'alice', allowed_products: null, allowed_workflow_ids: null } as any,
  tabs: {} as any,
  presets: [] as any[],
  refreshPresets: vi.fn(async () => {}),
  workspace: 'local',
  openProject: vi.fn(),
  product: 'work',
  enabled: ['work', 'agentworks'],
  active: [] as any[],
  selectedTab: null as string | null,
  history: vi.fn(),
  navigation: vi.fn(async (): Promise<any> => ({ workspaces: [] })),
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
  useGlobalPresetStore: Object.assign((select: any) => select({ workflowPresets: mocks.presets, recentPresetOrder: [], activePresetIds: { workflow: null } }), {
    getState: () => ({ refreshPresets: mocks.refreshPresets }),
  }),
}))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({
  useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: mocks.workspace }),
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: Object.assign((select: any) => select({ productSurface: mocks.product }), {
    getState: () => ({
      productSurface: mocks.product,
      setSelectedWorkProjectId: mocks.selectWork,
    }),
  }),
}))
vi.mock('../../services/api', () => ({ agentApi: { listChatHistorySessions: mocks.history, listWorkspaceNavigation: mocks.navigation } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../utils/chatIdentity', () => ({
  captureChatIdentity: () => 1,
  isChatIdentityCurrent: () => true,
}))
vi.mock('../../utils/globalProductNavigation', () => ({
  openGlobalTab: mocks.openTab,
  openGlobalActivitySession: mocks.openActivity,
}))
vi.mock('../../utils/workspaceSettingsNavigation', () => ({ openWorkspaceSettings: mocks.openSettings }))
vi.mock('../../utils/appNavigation', async original => ({ ...await original<typeof import('../../utils/appNavigation')>(), openAppProduct: mocks.openProject }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: mocks.openProduct }))
vi.mock('../../products/work/workSessions', () => ({ loadWorkSessionsIncludingShared: async () => [] }))
vi.mock('../../products/work/projectProduct', () => ({ CREW_PRODUCT: { profileId: 'work' } }))
vi.mock('../../products/productSurfaceConfig', () => ({
  enabledProductSurfaces: () => mocks.enabled,
  isLocalDeployment: () => true,
  intersectAllowedProductSurfaces: (surfaces: string[], allowed: string[] | null) =>
    allowed ? surfaces.filter((surface) => allowed.includes(surface)) : surfaces,
  PRODUCT_SURFACE_LABELS: { work: 'Crew', agentworks: 'Goals', relays: 'Flows', knowledgebase: 'Brain', 'mcp-gateway': 'Vault', bullpen: 'AgentBridge' },
}))
vi.mock('../ProductSurfaceIcon', () => ({ ProductSurfaceIcon: ({ surface }: { surface: string }) => <svg data-product-icon={surface} /> }))
import WorkspaceChatsSidebar from './WorkspaceChatsSidebar'
import { SharedToolShortcuts } from './SharedToolShortcuts'
import { useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
  useSidebarExpansionStore.setState({ expanded: {} })
  vi.clearAllMocks()
  mocks.navigation.mockResolvedValue({ workspaces: [] })
  mocks.tabs = {}
  mocks.presets = []
  mocks.refreshPresets.mockReset().mockResolvedValue(undefined)
  mocks.product = 'work'
  mocks.enabled = ['work', 'agentworks']
  mocks.active = []
  mocks.selectedTab = null
  mocks.user = { id: 'alice', allowed_products: null, allowed_workflow_ids: null }
  mocks.app = { showWorkflowsOverview: false, showSchedulesOverview: false, adminPage: null }
  mocks.workspace = 'local'
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
it('lists admitted Crews and Goals before a chat exists, without projecting chat threads or Flows', async () => {
  mocks.enabled = ['work', 'agentworks', 'relays']
  mocks.navigation.mockResolvedValue({ workspaces: [{ id: 'shared', profile_id: 'work', name: 'Accounting', icon: '📒', workspace_path: 'Crew/shared' }] })
  mocks.presets = [
    { id: 'goal', label: 'Sales', selectedFolder: { filepath: 'Workflow/sales' } },
    { id: 'flow', label: 'Dispatch flow', workflowKind: 'relay' },
  ]
  mocks.tabs = { chat: { tabId: 'chat', name: 'Internal research thread', metadata: { agentProfileId: 'work', agentProfileProjectId: 'shared' } } }
  await render(async host => {
    const group = host.querySelector('section[aria-label="Local projects"]')!
    expect(group.textContent).toContain('Accounting')
    expect(group.textContent).toContain('Sales')
    expect(group.textContent).not.toContain('Internal research thread')
    expect(group.textContent).not.toContain('Dispatch flow')
    expect(mocks.history).not.toHaveBeenCalled()
    expect(mocks.activeSessions).not.toHaveBeenCalled()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Accounting · Crew"]')!.click())
    expect(mocks.openProject).toHaveBeenLastCalledWith({ surface: 'work', id: 'shared' })
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Sales · Goal"]')!.click())
    expect(mocks.openProject).toHaveBeenLastCalledWith({ surface: 'agentworks', id: 'goal' })
    const toggle = group.querySelector<HTMLButtonElement>('[aria-expanded]')!
    await act(async () => toggle.click())
    expect(group.querySelector('[hidden]')).not.toBeNull()
    expect(host.querySelector('[aria-label="Search all chats (Ctrl+K or Command+K)"]')).not.toBeNull()
  })
})

it('honors current product and Goal access and does not invent project rows from saved chats', async () => {
  mocks.user.allowed_products = ['agentworks']
  mocks.user.allowed_workflow_ids = ['permitted']
  mocks.presets = [{ id: 'permitted', label: 'Allowed goal' }, { id: 'private', label: 'Private goal' }]
  mocks.tabs = { stale: { tabId: 'stale', name: 'Private Crew', metadata: { agentProfileId: 'work', agentProfileProjectId: 'private' } } }
  await render(async host => {
    expect(host.textContent).toContain('Allowed goal')
    expect(host.textContent).not.toContain('Private goal')
    expect(host.textContent).not.toContain('Private Crew')
    expect(mocks.navigation).not.toHaveBeenCalled()
  })
})

it('discards a previous server inventory when the active server changes', async () => {
  let resolveOld!: (value: any) => void
  mocks.navigation.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
  await render(async (host, rerender) => {
    mocks.workspace = 'other-server'
    mocks.navigation.mockResolvedValue({ workspaces: [{ id: 'new', name: 'Current Crew', profile_id: 'work' }] })
    await rerender()
    await act(async () => resolveOld({ workspaces: [{ id: 'old', name: 'Previous private Crew', profile_id: 'work' }] }))
    expect(host.textContent).toContain('Current Crew')
    expect(host.textContent).not.toContain('Previous private Crew')
  })
})

it('loads Goals when opening directly on a Crew without first visiting the Goals page', async () => {
  // PLAT-746: the Crew route never mounts AppWorkspace's preset initialization.
  mocks.workspace = 'connected-server'
  mocks.navigation.mockResolvedValue({ workspaces: [{ id: 'crew', profile_id: 'work', name: 'Research crew' }] })
  mocks.refreshPresets.mockImplementation(async () => { mocks.presets = [{ id: 'goal', label: 'Weekly report' }] })
  await render(async (host, rerender) => {
    await rerender()
    expect(host.textContent).toContain('Research crew')
    expect(host.textContent).toContain('Weekly report')
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
