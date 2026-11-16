// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TeamInviteRow, TeamInvites, TeamInviteProvider } from './TeamInviteRow'
import { useTeamInviteNotice } from './teamInviteNotice'
import { answerTeamInvite } from '../../products/work/teamApi'
import { desktopRequest } from '../../services/desktopConnections'

vi.mock('../../services/desktopConnections', () => ({ desktopRequest: vi.fn() }))
let listed: { project_id: string; name: string; owner_name: string }[] = []
vi.mock('../../products/work/teamApi', () => ({ answerTeamInvite: vi.fn(), useTeamInvites: () => listed }))
vi.mock('./SidebarServerGroup', () => ({ openSidebarProject: vi.fn() }))
let mac = true, person = 'first'
// Hoisted: the store mock is read while modules load (api.ts reads the workspace at import).
const ws = vi.hoisted(() => ({ id: 'local' }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => mac }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (state: { user: { id: string } }) => unknown) => select({ user: { id: person } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getRawActiveWorkspaceId: () => ws.id, getWorkspaceScopedStorageKey: (key: string) => key, useWorkspaceConnectionStore: (select: (state: { activeWorkspaceId: string }) => unknown) => select({ activeWorkspaceId: ws.id }) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// #2775 (owner, 16 Nov): accepting a Team invite on a Mac is one action that creates the Team's own project, with
// no "existing project" choice; the two dead ends say what to do next.
it('adds to the Mac in one click with the suggested folder, and gives a next step for the two dead ends', async () => {
  vi.mocked(desktopRequest).mockImplementation(async (_route, method) => method === 'POST' ? {} : { folder: '/example/new-team', projects: [{ project_id: 'mine', name: 'My project', can_join: true, agents: [] }] } as never)
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === label)!
  const invite = { project_id: 'invited', name: 'Team example', owner_name: 'Teammate', connection: 'saved', can_add_to_mac: true }
  try {
    await act(async () => root.render(<TeamInviteRow invite={invite} />))
    await act(async () => button('Add to my Mac').click())
    expect(desktopRequest).toHaveBeenLastCalledWith('/saved/crew-teams/invited/join', 'POST', { folder: '/example/new-team' })
    expect(host.querySelector('input[type="radio"], select')).toBeNull()
    await act(async () => root.render(<TeamInviteRow key="no-folder" invite={{ ...invite, can_add_to_mac: false }} />))
    expect(button('Add to my Mac')).toBeUndefined()
    await act(async () => button('Join online').click())
    expect(answerTeamInvite).toHaveBeenLastCalledWith({ ...invite, can_add_to_mac: false }, true)
    await act(async () => root.render(<TeamInviteRow key="no-network" invite={{ ...invite, connection: undefined }} />))
    expect(host.textContent).toContain('choose Connect server')
  } finally { await act(async () => root.unmount()); host.remove() }
})

// Owner, 15 Nov: an invite pops up over the app; Minimize keeps it in the menu (with a count for the closed menu)
// and it does not pop up again, while a new invite does.
it('pops up waiting invites one at a time, and only a new invite after Minimize', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]')
  const invite = (id: string) => ({ project_id: id, name: `Team ${id}`, owner_name: 'Teammate' })
  try {
    listed = [invite('a'), invite('b')]
    await act(async () => root.render(<TeamInviteProvider><TeamInvites /></TeamInviteProvider>))
    expect(dialog()?.textContent).toContain('Invite 1 of 2')
    expect(useTeamInviteNotice.getState().count).toBe(2)
    await act(async () => [...dialog()!.querySelectorAll('button')].find(b => b.textContent === 'Minimize')!.click())
    expect(dialog()).toBeNull()
    expect(host.querySelectorAll('[aria-label^="Team invite:"]').length).toBe(2)
    listed = [invite('a'), invite('b'), invite('c')]
    await act(async () => root.render(<TeamInviteProvider><TeamInvites key="poll" /></TeamInviteProvider>))
    expect(dialog()?.textContent).toContain('Team c')
    expect(dialog()?.textContent).not.toContain('Invite 1 of')
  } finally { await act(async () => root.unmount()); host.remove(); listed = []; useTeamInviteNotice.setState({ count: 0 }) }
})

// PR2585 mounted the observer inside mobile navigation: closed menu meant no popup/count.
it('keeps invites and badge count alive with mobile navigation closed, without reopening on navigation', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const render = (menu: boolean, page: string) => root.render(<TeamInviteProvider><div data-app-shell>
    <button>Menu</button><div>{page}</div>{menu && <TeamInvites />}
  </div></TeamInviteProvider>)
  const popup = () => document.body.querySelector<HTMLElement>('[role="dialog"]')
  try {
    listed = [{ project_id: 'one', name: 'Team one', owner_name: 'Teammate' }]
    await act(async () => render(false, 'Chat'))
    expect(popup()?.textContent).toContain('Team one')
    expect(useTeamInviteNotice.getState().count).toBe(1)
    expect(host.querySelector<HTMLElement>('[data-app-shell]')?.inert).toBe(true)
    await act(async () => popup()!.querySelector<HTMLButtonElement>('button')!.click())
    expect(popup()).toBeNull()
    expect(host.querySelector<HTMLElement>('[data-app-shell]')?.inert).toBe(false)
    await act(async () => render(true, 'Files'))
    expect(host.querySelector('[aria-label="Team invite: Team one"]')).not.toBeNull()
    expect(popup()).toBeNull()
    await act(async () => render(false, 'Chat again'))
    expect(useTeamInviteNotice.getState().count).toBe(1)
    expect(popup()).toBeNull()
    // A full app-shell restart, unlike navigation, must show pending invites again.
    await act(async () => root.render(null))
    await act(async () => render(false, 'After restart'))
    expect(popup()?.textContent).toContain('Team one')
  } finally { await act(async () => root.unmount()); host.remove(); listed = []; useTeamInviteNotice.setState({ count: 0 }) }
})

it('does not carry minimized invitations between people or networks and retains web answers in both places', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const render = () => root.render(<TeamInviteProvider><TeamInvites /></TeamInviteProvider>)
  const popup = () => document.body.querySelector<HTMLElement>('[role="dialog"]')
  try {
    mac = false
    const invite = { project_id: 'web', name: 'Web team', owner_name: 'Teammate' }
    listed = [invite]
    await act(async () => render())
    await act(async () => popup()!.querySelector<HTMLButtonElement>('button')!.click())
    expect(popup()).toBeNull()
    person = 'second'
    await act(async () => render())
    expect(popup()?.textContent).toContain('Web team')
    await act(async () => [...popup()!.querySelectorAll('button')].find(b => b.textContent === 'Join')!.click())
    expect(answerTeamInvite).toHaveBeenLastCalledWith(invite, true)
    await act(async () => popup()!.querySelector<HTMLButtonElement>('button')!.click())
    ws.id = 'other-network'
    await act(async () => render())
    expect(popup()).not.toBeNull()
    await act(async () => [...popup()!.querySelectorAll('button')].find(b => b.textContent === 'Decline')!.click())
    expect(answerTeamInvite).toHaveBeenLastCalledWith(invite, false)
    await act(async () => popup()!.querySelector<HTMLButtonElement>('button')!.click())
    await act(async () => [...host.querySelectorAll('button')].find(b => b.textContent === 'Decline')!.click())
    expect(answerTeamInvite).toHaveBeenLastCalledWith(invite, false)
    listed = []
    await act(async () => render())
    expect(host.textContent).toBe('')
    expect(useTeamInviteNotice.getState().count).toBe(0)
    // A withdrawn then reissued invitation is newly pending, even for the same project.
    listed = [invite]
    await act(async () => render())
    expect(popup()).not.toBeNull()
  } finally { await act(async () => root.unmount()); host.remove(); listed = []; mac = true; person = 'first'; ws.id = 'local'; useTeamInviteNotice.setState({ count: 0 }) }
})
