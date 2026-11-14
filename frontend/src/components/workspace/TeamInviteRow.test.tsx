// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { TeamInviteRow } from './TeamInviteRow'
import { desktopRequest } from '../../services/desktopConnections'

vi.mock('../../services/desktopConnections', () => ({ desktopRequest: vi.fn() }))
vi.mock('../../products/work/teamApi', () => ({ answerTeamInvite: vi.fn(), useTeamInvites: () => [] }))
vi.mock('./SidebarServerGroup', () => ({ openSidebarProject: vi.fn() }))
vi.mock('../../stores/useConnectedWorkStore', () => ({ hasDesktopConnections: () => true }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ getRawActiveWorkspaceId: () => 'local' }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-746-976: unchecked existing agents must stay private; the screen sends
// the selected local identity, rather than making another project or trusting an online target.
it('sends the existing project and only selected agents, while retaining new-project acceptance', async () => {
  vi.mocked(desktopRequest).mockImplementation(async (_route, method) => method === 'POST' ? {} : {
    folder: '/example/new-team', instructions_notice: 'Instruction notice from server', projects: [
      { project_id: 'mine', name: 'My project', can_join: true, agents: [{ tab: 'main', name: 'Main' }, { tab: 'research', name: 'Research' }] },
      { project_id: 'online', name: 'Online project', can_join: false, reason: 'Turn Run online off first', agents: [] },
    ],
  } as never)
  const host = document.createElement('div'), root = createRoot(host)
  document.body.append(host)
  const button = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('button')].find(b => b.textContent === label)!
  const invite = { project_id: 'invited', name: 'Team example', owner_name: 'Teammate', connection: 'saved', can_add_to_mac: true }
  try {
    await act(async () => root.render(<TeamInviteRow invite={invite} />))
    await act(async () => button('Add to my Mac').click())
    expect(host.querySelector<HTMLOptionElement>('option[value="online"]')?.disabled).toBe(true)
    await act(async () => host.querySelectorAll<HTMLInputElement>('input[type="radio"]')[1].click())
    expect(host.textContent).toContain('Instruction notice from server')
    const agents = host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')
    expect([...agents].every(agent => !agent.checked)).toBe(true)
    await act(async () => agents[1].click())
    await act(async () => button('OK').click())
    expect(desktopRequest).toHaveBeenLastCalledWith('/saved/crew-teams/invited/join', 'POST', { local_project: 'mine', agents: ['research'], confirm: true })
    await act(async () => root.render(<TeamInviteRow key="new" invite={invite} />))
    await act(async () => button('Add to my Mac').click())
    await act(async () => button('OK').click())
    expect(desktopRequest).toHaveBeenLastCalledWith('/saved/crew-teams/invited/join', 'POST', { folder: '/example/new-team' })
  } finally { await act(async () => root.unmount()); host.remove() }
})
