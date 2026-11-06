// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { invalidateChatIdentity } from '../../utils/chatIdentity'
import { DesktopConnections } from './DesktopConnections'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { connectedWorkHistoryScope, useProjectMenuHistoryStore } from '../../stores/useProjectMenuHistoryStore'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { openAppProduct } from '../../utils/appNavigation'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: mocks.request }))
vi.mock('../../services/api', () => ({ default: { get: () => new Promise(() => {}) }, getApiBaseUrl: () => '', getAuthToken: () => '', getLocalApiBaseUrl: () => 'http://127.0.0.1:18743', getDesktopApiBaseUrl: () => 'http://127.0.0.1:18743' }))
vi.mock('../../utils/productWorkspaceNavigation', () => ({ openProductWorkspace: vi.fn() }))
vi.mock('../../utils/appNavigation', () => ({ openAppProduct: vi.fn() }))
beforeEach(() => {
  invalidateChatIdentity()
  const values = new Map([['auth-storage', JSON.stringify({ state: { user: { id: 'local-owner' } } })]])
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
  useProjectMenuHistoryStore.setState({ recent: {}, pinned: {}, startHere: {} })
  vi.clearAllMocks()
})
afterEach(() => vi.unstubAllGlobals())
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('identical project IDs open on their owning server while an offline server leaves other groups usable', async () => {
  const first = 'a'.repeat(32), second = 'b'.repeat(32)
  const open = vi.fn().mockResolvedValue(undefined)
  const previous = window.electronAPI
  window.electronAPI = { getDesktopConnection: () => 'local', openDesktopConnection: open }
  mocks.request.mockImplementation(async (path = '') => {
    if (!path) return { connections: [
      { id: first, name: 'First team', server: 'https://one.example.com' },
      { id: second, name: 'Second team', server: 'https://two.example.com' },
    ] }
    if (path === '/local/inventory') return { status: 'connected', projects: [] }
    if (path === `/${first}/inventory`) return { status: 'unavailable', projects: [] }
    return { status: 'connected', projects: [{ id: 'same-project', name: 'Release crew', kind: 'crew' }] }
  })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections><span>Local draft stays here</span></DesktopConnections>))
    expect(host.textContent).toContain('Local draft stays here')
    expect(host.textContent).toContain('First team unavailable')
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Second team Crews"]')!.click())
    const project = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Release crew'))!
    await act(async () => project.click())
    expect(open).toHaveBeenCalledWith(second, '/crew/projects/same-project')
  } finally {
    await act(async () => root.unmount())
    window.electronAPI = previous
  }
})

it('a wrapper without the app-owned backend explains how to enable connections instead of silently hiding them', async () => {
  const previous = window.electronAPI
  window.electronAPI = { getDesktopConnection: () => null, openDesktopConnection: vi.fn() }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections><span>Local chats</span></DesktopConnections>))
    expect(host.textContent).toContain('Local chats')
    expect(host.textContent).toContain('desktop launcher')
    expect(host.querySelector('form')).toBeNull()
  } finally {
    await act(async () => root.unmount())
    window.electronAPI = previous
  }
})

it('keeps shared projects in the active server beside the owner navigation', async () => {
  // PLAT-746: the active inventory is not redundant; it also supplies Run projects.
  const id = 'a'.repeat(32), previous = window.electronAPI
  window.electronAPI = { getDesktopConnection: () => id, openDesktopConnection: vi.fn() }
  mocks.request.mockImplementation(async (path = '') => !path
    ? { connections: [{ id, name: 'Team', server: 'https://example.com' }] }
    : { status: 'connected', projects: path === `/${id}/inventory` ? [
      { id: 'owned', name: 'Owned crew', kind: 'crew', mode: 'builder', owned_by_caller: true },
      { id: 'shared', name: 'Shared crew', kind: 'crew', mode: 'run', owned_by_caller: false, owner_name: 'Project owner' },
      { id: 'shared2', name: 'Second dashboard', kind: 'crew', mode: 'run', owned_by_caller: false, owner_name: 'Project owner' },
      { id: 'other', name: 'Other dashboard', kind: 'crew', mode: 'run', owned_by_caller: false, owner_name: 'Someone else' },
      { id: 'build', name: 'Build together', kind: 'crew', mode: 'builder', owned_by_caller: false, owner_name: 'Project owner' },
      { id: 'agent', name: 'AgentWorks', kind: 'crew', mode: 'builder', owned_by_caller: true, personal_agent: true },
    ] : [] })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections projects={[{ id: 'owned', name: 'Owned crew', kind: 'crew' }, { id: 'weekly', name: 'Weekly goal', kind: 'goal' }]} />))
    const group = host.querySelector('[aria-label="Connected work"]')!
    const mine = group.querySelector('[aria-label="My Crews"]')!, dashboards = group.querySelector('[aria-label="Dashboards shared with me"]')!
    expect(mine.textContent).toContain('Owned crew')
    expect(mine.textContent).toContain('Build together') // a Crew shared to build in is not a dashboard
    expect(mine.textContent).not.toContain('Shared crew')
    expect(mine.textContent).not.toContain('AgentWorks') // the personal agent is separate from the ordinary Crew list
    expect(group.textContent).toContain('Your agent') // inside its own network, with its own label
    expect(group.textContent!.indexOf('Your agent')).toBeLessThan(group.textContent!.indexOf('My Crews'))
    expect(dashboards.textContent).toContain('Shared crew')
    expect(dashboards.textContent).not.toContain('Owned crew')
    // Grouped by who shared them, the biggest group first and open.
    const groups = [...dashboards.querySelectorAll('details')]
    expect(groups.map(g => g.querySelector('summary')?.textContent)).toEqual(['▸Project owner· 2', '▸Someone else· 1'])
    expect(groups[0].open).toBe(true)
    expect(group.querySelector('section[aria-label="Network: Team"]')?.textContent).toContain('Your agent')
    expect(group.textContent).toContain('Weekly goal')
    expect(group.textContent?.match(/Owned crew/g)).toHaveLength(1)
  } finally {
    await act(async () => root.unmount()); window.electronAPI = previous
  }
})

it('website lists Mac Crews under their Mac, greyed with their last state when the Mac is off (PLAT-856-509)', async () => {
  const previous = window.electronAPI
  window.electronAPI = undefined
  const mac = { device_id: 'dev-1', name: 'MacBook Air', online: false, last_seen: '2026-11-02T10:00:00Z' }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections projects={[
      { id: 'p1', name: 'Login fixes', kind: 'crew', owned_by_caller: true, location: 'mac', mac, state: { state: 'idle', at: '2026-11-02T09:00:00Z' } },
      { id: 'p2', name: 'QA agent', kind: 'crew', owned_by_caller: true, location: 'server' },
    ]} />))
    const macGroup = host.querySelector('section[aria-label="On your Mac: MacBook Air"]')!
    expect(macGroup.textContent).toContain('Login fixes')
    expect(macGroup.textContent).toContain('Idle · last reply')
    expect(macGroup.querySelector('.opacity-60')).not.toBeNull()
    expect(macGroup.textContent).not.toContain('QA agent')
    expect(host.textContent).toContain('On the server')
  } finally {
    await act(async () => root.unmount())
    window.electronAPI = previous
  }
})

// PLAT-746: a saved shortcut cannot grant access or confuse equal Crew IDs on different networks.
it('routes pinned and recent Crews by network and drops shortcuts absent from the current inventory', async () => {
  const remote = 'c'.repeat(32), previous = window.electronAPI
  const open = vi.fn().mockResolvedValue(undefined)
  window.electronAPI = { getDesktopConnection: () => 'local', openDesktopConnection: open }
  const localKey = connectedProjectKey('local', 'crew', 'same')
  const remoteKey = connectedProjectKey(remote, 'crew', 'same')
  const scope = connectedWorkHistoryScope()
  const history = useProjectMenuHistoryStore.getState()
  history.visit(scope, localKey)
  history.visit(scope, remoteKey)
  history.setPinned(scope, localKey, true)
  for (let i = 0; i < 6; i++) history.visit(scope, connectedProjectKey(remote, 'crew', 'more-' + i))
  mocks.request.mockImplementation(async (path = '') => !path
    ? { connections: [{ id: remote, name: 'Team', server: 'https://team.example.com' }] }
    : { status: 'connected', projects: path === '/local/inventory'
      ? [{ id: 'same', name: 'Local crew', kind: 'crew' }]
      : [{ id: 'same', name: 'Remote crew', kind: 'crew' }, ...Array.from({ length: 6 }, (_, i) => ({ id: 'more-' + i, name: 'Other ' + i, kind: 'crew' }))] })
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<DesktopConnections />))
    const recent = () => host.querySelector<HTMLElement>('[aria-label="Recent Crews"]')!
    // One pin plus five unpinned visits; an old pin survives newer visits.
    expect(recent().querySelectorAll('[aria-label*=" · Crew · "]')).toHaveLength(6)
    expect(recent().textContent).toContain('Local crew')
    expect(recent().textContent).not.toContain('Other 0')
    expect(recent().textContent).toContain('On your Mac')
    expect(recent().textContent).toContain('Team')
    await act(async () => useProjectMenuHistoryStore.getState().visit(scope, remoteKey))
    await act(async () => recent().querySelector<HTMLButtonElement>('[aria-label="Remote crew · Crew · Team"]')!.click())
    expect(open).toHaveBeenLastCalledWith(remote, '/crew/projects/same')
    await act(async () => recent().querySelector<HTMLButtonElement>('[aria-label="Local crew · Crew · On your Mac"]')!.click())
    expect(openAppProduct).toHaveBeenLastCalledWith({ surface: 'work', id: 'same' })
    await act(async () => recent().querySelector<HTMLButtonElement>('[aria-label="Unpin Local crew · On your Mac"]')!.click())
    expect(useProjectMenuHistoryStore.getState().pinned[scope]).toEqual([])
    await act(async () => useProjectMenuHistoryStore.getState().setPinned(scope, remoteKey, true))
    await act(async () => useConnectedWorkStore.setState(state => ({ inventory: { ...state.inventory, [remote]: { status: 'connected', projects: [] } } })))
    expect(recent().textContent).not.toContain('Remote crew')
    expect(recent().textContent).toContain('Local crew')
    await act(async () => invalidateChatIdentity())
    await act(async () => root.render(<DesktopConnections />))
    expect(host.querySelector('[aria-label="Recent Crews"]')).toBeNull()
  } finally {
    await act(async () => root.unmount()); window.electronAPI = previous
  }
})
