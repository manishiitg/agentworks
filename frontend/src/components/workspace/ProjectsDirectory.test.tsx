// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { DesktopConnections } from './DesktopConnections'
import { SidebarActions } from './SidebarActions'
import { WorkspaceProjectsProvider } from './WorkspaceProjectsProvider'
import ProjectsDirectory from './ProjectsDirectory'
import { openAppPage, openAppProduct, useProjectsPage } from '../../utils/appNavigation'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { connectedWorkHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { agentApi } from '../../services/api'
import { useSidebarProjectAction } from '../../products/work/sidebarProjectActions'

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
  SidebarProjectRow: ({ name, onNewAgent }: { name: string; onNewAgent?: () => void }) => <div>{name}{onNewAgent && <button onClick={onNewAgent}>Add agent in {name}</button>}</div>,
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
  const sharedDashboard = { ...row('dash', 'Shared dashboard', { owned_by_caller: false }), mode: 'run' as const }
  const shared = row('shared', 'Shared project', { owned_by_caller: false })
  const inventory = [alpha, beta, sharedDashboard, shared]
  const read = vi.spyOn(agentApi, 'listWorkspaceNavigation').mockResolvedValue({ workspaces: inventory })
  const chats = vi.spyOn(agentApi, 'listAgentProfileSideChats').mockImplementation(f.sideChats)
  const previous = localStorage.getItem('auth-storage')
  localStorage.setItem('auth-storage', JSON.stringify({ state: { user: { id: 'owner' } } }))
  const scope = connectedWorkHistoryScope()
  const key = (id: string) => connectedProjectKey('local', 'crew', id)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  window.history.replaceState({}, '', '/')
  function Screen() {
    const projects = useProjectsPage()
    return <><DesktopConnections top={<SidebarActions />} projects={inventory} />{projects && <ProjectsDirectory />}</>
  }
  const side = () => host.querySelector('[aria-label="Connected work"]')!
  const button = (name: string, parent: ParentNode = host) => [...parent.querySelectorAll('button')].find(b => (b.getAttribute('aria-label') || b.textContent)?.trim() === name)!
  const click = async (name: string, parent?: ParentNode) => act(async () => button(name, parent).click())
  try {
    await act(async () => root.render(<WorkspaceProjectsProvider><Screen /></WorkspaceProjectsProvider>))
    // Projects link is removed from the left menu now that More projects covers browsing directly.
    expect(side().textContent).not.toContain('Browse projects')
    expect(button('Projects', side())).toBeUndefined()
    expect(side().textContent).not.toContain('Beta')
    await act(async () => openAppPage('/projects'))
    expect(window.location.pathname).toBe('/projects')
    const page = () => host.querySelector('main')!
    expect(page().querySelector('h1')?.textContent).toBe('Projects')
    expect(page().textContent).toContain('Beta')
    expect(page().textContent).not.toContain('Shared dashboard')
    expect(read).toHaveBeenCalledOnce()
    expect(chats).not.toHaveBeenCalled()

    expect(page().querySelector('select')).toBeNull()
    // Tabs for easy navigation (owner, 27 Nov): a server's tab lists only that server's projects; All is the full page.
    const tab = (name: string) => [...page().querySelectorAll<HTMLButtonElement>('[role="tab"]')].find(item => item.textContent?.startsWith(name))!
    await act(async () => tab('Team space').click())
    expect(page().textContent).toContain('Gamma')
    expect(page().textContent).not.toContain('Beta')
    await act(async () => tab('All').click())
    expect(tab('All').getAttribute('aria-selected')).toBe('true')
    expect(page().textContent).not.toContain('Mine')
    expect(page().querySelector('section[aria-label="Yours"]')?.textContent).toContain('Beta')
    expect(page().querySelector('section[aria-label="Yours"]')?.textContent).toContain('Gamma')
    // A server project shows a cloud naming its server (#4331), not grey text; this Mac's own rows have none.
    expect(page().querySelector('section[aria-label="Yours"] [role="img"]')?.getAttribute('aria-label')).toBe('Runs on Team space')
    expect(page().querySelectorAll('section[aria-label="Yours"] [role="img"]')).toHaveLength(1)
    expect(page().querySelector('section[aria-label="Shared with you"]')?.textContent).toContain('Shared project')
    expect(page().querySelector('section[aria-label="Yours"]')?.textContent).not.toContain('Shared project')
    await click('Pin Beta', page())
    expect(button('Unpin Beta', page()).getAttribute('aria-pressed')).toBe('true')
    expect(side().textContent).toContain('Beta')
    expect(side().querySelector('#recent-projects-heading')).toBeNull()
    expect(side().textContent).not.toContain('Alpha')
    await click('Unpin Beta', page())
    await act(async () => {
      useProductSurfaceStore.setState({ productSurface: 'work', selectedWorkProjectId: 'a' })
      openAppProduct({ surface: 'work', id: 'a' })
    })
    expect(side().querySelector('#recent-projects-heading')?.textContent).toBe('Recent')
    expect(side().textContent).toContain('Alpha')
    expect(side().textContent).not.toContain('Beta')
    // New section control sits above Recent, and remaining projects stay tucked in a collapsed More projects disclosure.
    const newSectionBtn = button('New section', side())
    const recentHeading = side().querySelector('#recent-projects-heading')!
    expect(newSectionBtn.compareDocumentPosition(recentHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const moreToggle = [...side().querySelectorAll('button')].find(b => b.textContent?.includes('More projects'))!
    expect(moreToggle.getAttribute('aria-expanded')).toBe('false')
    await act(async () => moreToggle.click())
    expect(moreToggle.getAttribute('aria-expanded')).toBe('true')
    const macGroupToggle = [...side().querySelectorAll('button')].find(b => b.textContent?.includes('This Mac'))!
    const serverGroupToggle = [...side().querySelectorAll('button')].find(b => b.textContent?.includes('Team space'))!
    expect(macGroupToggle.getAttribute('aria-expanded')).toBe('false')
    expect(serverGroupToggle.getAttribute('aria-expanded')).toBe('false')
    await act(async () => macGroupToggle.click())
    expect(side().textContent).toContain('Beta')
    expect(side().textContent).not.toContain('Gamma')
    await act(async () => serverGroupToggle.click())
    expect(side().textContent).toContain('Gamma')
    await act(async () => moreToggle.click())
    expect(side().textContent).not.toContain('Beta')

    await act(async () => openAppPage('/projects'))
    const query = page().querySelector('input')!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(query, 'beta'); query.dispatchEvent(new Event('input', { bubbles: true })) })
    expect(page().textContent).not.toContain('Alpha')
    await click('Pin Beta', page())
    expect(side().textContent).toContain('Beta')
    // Recent stays next to pins (owner, 24 Nov): Alpha was opened, so it is listed under Recent.
    expect(side().querySelector('[aria-labelledby="recent-projects-heading"]')?.textContent).toContain('Alpha')
    expect(useProjectMenuHistoryStore.getState().pinned[scope]).toEqual([key('b')])
    const openBeta = [...page().querySelectorAll<HTMLButtonElement>('li button')].find(row => row.title === 'Beta')!
    await act(async () => openBeta.click())
    expect(f.open).toHaveBeenCalledWith('crew', 'b')
    f.open.mockImplementationOnce((_kind: string, id: string) => openAppProduct({ surface: 'work', id }))
    await click('Add agent in Beta', side())
    expect(window.location.pathname).toBe('/projects/b')
    expect(useSidebarProjectAction.getState().request).toMatchObject({ projectId: 'b', action: 'new' })
    await act(async () => openAppProduct({ surface: 'work', id: 'a' }))
    expect(useSidebarProjectAction.getState().request).toBeNull()
  } finally {
    await act(async () => root.unmount())
    host.remove()
    useProjectMenuHistoryStore.setState({ recent: {}, pinned: {}, startHere: {}, sections: {} })
    window.history.replaceState({}, '', '/')
    if (previous !== null) localStorage.setItem('auth-storage', previous)
    else localStorage.removeItem('auth-storage')
    useProductSurfaceStore.setState({ selectedWorkProjectId: null })
    vi.restoreAllMocks()
  }
})
