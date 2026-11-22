// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { DesktopConnections } from './DesktopConnections'
import { SidebarActions } from './SidebarActions'
import { WorkspaceProjectsProvider } from './WorkspaceProjectsProvider'
import ProjectsDirectory from './ProjectsDirectory'
import { useProjectsPage } from '../../utils/appNavigation'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { connectedWorkHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openAppProduct } from '../../utils/appNavigation'
import { agentApi } from '../../services/api'

const f = vi.hoisted(() => ({
  open: vi.fn(),
  sideChats: vi.fn(async () => []),
  user: { id: 'owner', allowed_products: ['work'] },
}))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../stores/useConnectedWorkStore', async () => {
  const { captureChatIdentity } = await import('../../utils/chatIdentity')
  const connections = [{ id: 'local', name: 'Local' }, { id: 'remote', name: 'Team space' }]
  const state = () => ({ identity: captureChatIdentity(), activity: {}, inventory: { remote: {
    status: 'connected', projects: [{ id: 'gamma', kind: 'crew', name: 'Gamma' }],
  } } })
  return {
    hasDesktopConnections: () => true,
    connectedLocations: () => connections,
    useConnectedWorkPolling: () => {},
    useConnectedWorkStore: (select?: (value: unknown) => unknown) => select ? select(state()) : state(),
  }
})
vi.mock('../../stores/useAuthStore', async original => ({
  ...await original<typeof import('../../stores/useAuthStore')>(),
  useAuthStore: Object.assign((select: (state: unknown) => unknown) => select({ user: f.user }), {
    getState: () => ({ user: f.user }),
    subscribe: () => () => {},
  }),
}))
vi.mock('../chat/AgentCard', () => ({
  useCrewAgentsStore: (select: (state: unknown) => unknown) => select({ agents: [], projectId: '' }),
}))
vi.mock('./TeamInviteRow', () => ({ TeamInvites: () => null }))
vi.mock('./WorkspaceTopToolbar', () => ({ WorkspaceTopToolbar: ({ children }: { children: React.ReactNode }) => <header>{children}</header> }))
vi.mock('./SidebarServerGroup', () => ({
  openSidebarProject: f.open,
  SidebarProjectRow: ({ name }: { name: string }) => <div>{name}</div>,
}))
vi.mock('../../products/work/teamApi', () => ({
  useTeamTab: () => null, teamSideChats: async () => [],
  agentSetupForTab: () => null, teamAgentForTab: () => null,
}))
vi.mock('../../products/work/OwnTerminalAgents', () => ({ OwnTerminalAgents: () => null }))
vi.mock('../../services/llm-config-api', () => ({
  llmConfigService: {
    getMenuFilter: async () => ({ network: '', filter: 'all' }),
    setMenuFilter: async (value: unknown) => value,
  },
}))
vi.mock('../../utils/quickNavigation', () => ({ quickNavigationItems: () => [], openQuickNavigation: vi.fn() }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const row = (id: string, name: string, fields = {}) => ({
  id, name, profile_id: 'work' as const, kind: 'crew' as const, mode: 'builder' as const,
  owned_by_caller: true, workspace_id: 'local', workspace_path: '', ...fields,
})

it('opens Projects from the sidebar and keeps browsing, pins and recent shortcuts distinct', async () => {
  const alpha = row('a', 'Alpha'), beta = row('b', 'Beta')
  const excluded = row('home', 'Personal agent', { personal_agent: true })
  const sharedDashboard = { ...row('dash', 'Shared dashboard', { owned_by_caller: false }), mode: 'run' as const }
  const shared = row('shared', 'Shared project', { owned_by_caller: false })
  const inventory = [alpha, beta, excluded, sharedDashboard, shared]
  const read = vi.spyOn(agentApi, 'listWorkspaceNavigation').mockResolvedValue({ workspaces: inventory })
  const chats = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockImplementation(f.sideChats)
  const previous = localStorage.getItem('auth-storage')
  localStorage.setItem('auth-storage', JSON.stringify({ state: { user: { id: 'owner' } } }))
  const scope = connectedWorkHistoryScope()
  const key = (id: string) => connectedProjectKey('local', 'crew', id)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  window.history.replaceState({}, '', '/home')
  function Screen() {
    const projects = useProjectsPage()
    return <><DesktopConnections top={<SidebarActions />} projects={inventory} showBrowse />{projects && <ProjectsDirectory />}</>
  }
  const side = () => host.querySelector('[aria-label="Connected work"]')!
  const button = (name: string, parent: ParentNode = host) => [...parent.querySelectorAll('button')].find(b => (b.getAttribute('aria-label') || b.textContent)?.trim() === name)!
  const click = async (name: string, parent?: ParentNode) => act(async () => button(name, parent).click())
  try {
    await act(async () => root.render(<WorkspaceProjectsProvider><Screen /></WorkspaceProjectsProvider>))
    expect(button('Browse projects', side())).toBeTruthy()
    expect(side().textContent).not.toContain('Beta')
    await click('Browse projects', side())
    expect(window.location.pathname).toBe('/projects')
    const page = () => host.querySelector('main')!
    expect(page().querySelector('h1')?.textContent).toBe('Projects')
    expect(page().textContent).toContain('Beta')
    expect(page().textContent).not.toContain('Personal agent')
    expect(page().textContent).not.toContain('Shared dashboard')
    expect(read).toHaveBeenCalledOnce()
    expect(chats).not.toHaveBeenCalled()

    expect(page().querySelector('select')).toBeNull()
    expect(page().querySelectorAll('section[aria-label="Mac"]')).toHaveLength(1)
    expect(page().textContent).not.toContain('On your Mac')
    await click('Shared with me', page())
    expect(page().textContent).toContain('Shared project')
    expect(page().textContent).not.toContain('Beta')
    await click('Mine', page())
    expect(page().textContent).toContain('Beta')
    expect(page().textContent).not.toContain('Shared project')
    const openFilters = async () => act(async () => page().querySelector<HTMLButtonElement>('button[aria-haspopup="dialog"][aria-expanded]')!.click())
    const filters = () => document.querySelector('[aria-label="Project filters"]')!
    await openFilters()
    await click('All locations', filters())
    expect(page().querySelector('section[aria-label="Team space"]')?.textContent).toContain('Gamma')
    await openFilters()
    await click('Team space', filters())
    expect(page().textContent).toContain('Gamma')
    expect(page().textContent).not.toContain('Beta')
    await click('Shared with me', page())
    expect(page().textContent).toContain('No matching projects.')
    await click('Clear search and filters', page())
    expect(page().textContent).toContain('Beta')

    await click('Pin Beta', page())
    expect(button('Unpin Beta', page()).getAttribute('aria-pressed')).toBe('true')
    expect(side().textContent).toContain('Beta')
    expect(side().textContent).not.toContain('Recent projects')
    expect(side().textContent).not.toContain('Alpha')
    await click('Unpin Beta', page())
    expect(button('Browse projects', side())).toBeTruthy()
    await act(async () => {
      useProductSurfaceStore.setState({ productSurface: 'work', selectedWorkProjectId: 'a' })
      openAppProduct({ surface: 'work', id: 'a' })
    })
    expect(side().textContent).toContain('Recent projects')
    expect(side().textContent).toContain('Alpha')
    expect(side().textContent).not.toContain('Beta')

    await click('Projects', side())
    const query = page().querySelector('input')!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(query, 'beta'); query.dispatchEvent(new Event('input', { bubbles: true })) })
    expect(page().textContent).not.toContain('Alpha')
    await click('Pin Beta', page())
    expect(side().textContent).toContain('Beta')
    expect(side().textContent).not.toContain('Alpha')
    expect(useProjectMenuHistoryStore.getState().pinned[scope]).toEqual([key('b')])
    const openBeta = [...page().querySelectorAll<HTMLButtonElement>('li button')].find(row => row.title === 'Beta')!
    await act(async () => openBeta.click())
    expect(f.open).toHaveBeenCalledWith('crew', 'b')
  } finally {
    await act(async () => root.unmount())
    host.remove()
    useProjectMenuHistoryStore.setState({ recent: {}, pinned: {}, startHere: {} })
    window.history.replaceState({}, '', '/')
    if (previous !== null) localStorage.setItem('auth-storage', previous)
    else localStorage.removeItem('auth-storage')
    useProductSurfaceStore.setState({ selectedWorkProjectId: null })
    vi.restoreAllMocks()
  }
})
