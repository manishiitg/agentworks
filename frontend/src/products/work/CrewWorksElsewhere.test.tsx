// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { Team, TeamTarget } from './teamApi'
const mocks = vi.hoisted(() => ({ team: undefined as Team | undefined, target: undefined as TeamTarget | undefined, desktop: vi.fn(), request: vi.fn() }))
vi.mock('./teamApi', () => ({ useProjectTeam: () => ({ team: mocks.team, target: mocks.target }), teamRequest: mocks.request }))
vi.mock('../../services/desktopConnections', () => ({ desktopRequest: mocks.desktop }))
import { CrewWhereRow, CrewWorksElsewhere } from './CrewWorksElsewhere'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PLAT-856-543 / PR1140: settings permission must not hide a member's existing location controls.
it('keeps member switching available with Team management permission while excluding the creator', async () => {
  mocks.team = { project_id: 'team', name: 'Team', owner_id: 'creator', owner_name: 'Creator', on: true, people: [], folder: {}, can_manage: true, folder_access: true, where: 'mac' }
  mocks.target = { project: 'team', connection: 'server' }
  mocks.desktop.mockResolvedValue({})
  mocks.request.mockResolvedValue({})
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<CrewWhereRow projectId="team" />))
    const button = container.querySelector('button')
    expect(button).not.toBeNull()
    await act(async () => button!.click())
    expect(mocks.desktop).toHaveBeenCalledWith('/server/crew-teams/team/remove-from-mac', 'POST')
    mocks.target = { project: 'team' }
    await act(async () => root.render(<CrewWorksElsewhere projectId="team" />))
    expect(container.querySelector('button')).not.toBeNull()
    await act(async () => container.querySelector('button')!.click())
    expect(mocks.request).toHaveBeenCalledWith({ project: 'team' }, '/where', 'POST', { where: 'web' })
    mocks.team = { ...mocks.team, where: undefined }
    await act(async () => root.render(<CrewWorksElsewhere projectId="team" />))
    expect(container.querySelector('button')).toBeNull()
    await act(async () => root.render(<CrewWhereRow projectId="team" />))
    expect(container.querySelector('button')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
