// @vitest-environment happy-dom
// @vitest-environment happy-dom
import { act } from 'react'
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
  getWorkspaceScopedStorageKey: (key: string) => key,
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: Object.assign((select: any) => select({ productSurface: mocks.product }), {
    getState: () => ({
      productSurface: mocks.product,
      setSelectedWorkProjectId: mocks.selectWork,
    }),
  }),
}))
vi.mock('../../services/api', () => ({ default: { get: async () => ({ data: { agents: [] } }) }, agentApi: { listChatHistorySessions: mocks.history, listWorkspaceNavigation: mocks.navigation } }))
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
  PRODUCT_SURFACE_LABELS: { work: 'Crew', agentworks: 'Goals', knowledgebase: 'Brain', 'mcp-gateway': 'Vault', bullpen: 'AgentBridge' },
}))
vi.mock('../ProductSurfaceIcon', () => ({ ProductSurfaceIcon: ({ surface }: { surface: string }) => <svg data-product-icon={surface} /> }))
import WorkspaceChatsSidebar from './WorkspaceChatsSidebar'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => {
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
it('lists admitted Crews and Goals before a chat exists, without projecting chat threads', async () => {
  mocks.enabled = ['work', 'agentworks']
  mocks.navigation.mockResolvedValue({ workspaces: [{ id: 'shared', profile_id: 'work', name: 'Accounting', icon: '📒', workspace_path: 'Crew/shared' }] })
  mocks.presets = [
    { id: 'goal', label: 'Sales', selectedFolder: { filepath: 'Workflow/sales' } },
  ]
  mocks.tabs = { chat: { tabId: 'chat', name: 'Internal research thread', metadata: { agentProfileId: 'work', agentProfileProjectId: 'shared' } } }
  await render(async host => {
    const group = host
    expect(group.textContent).toContain('Accounting')
    expect(group.textContent).toContain('Sales')
    expect(group.textContent).not.toContain('Internal research thread')
    expect(mocks.history).not.toHaveBeenCalled()
    expect(mocks.activeSessions).not.toHaveBeenCalled()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Accounting · Crew"]')!.click())
    expect(mocks.openProject).toHaveBeenLastCalledWith({ surface: 'work', id: 'shared' })
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Sales · Goal"]')!.click())
    expect(mocks.openProject).toHaveBeenLastCalledWith({ surface: 'agentworks', id: 'goal' })
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
