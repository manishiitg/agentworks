// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({ remote: vi.fn(), action: vi.fn() }))
vi.mock('./teamApi', () => ({
  useTeamTab: () => ({ target: { project: 'p', connection: 'server' }, error: '', retry: vi.fn() }),
  crewDashboardsTab: state.remote,
  teamDashboardAction: state.action,
  teamFailure: (error: Error) => error,
  teamPublicLink: vi.fn(),
}))
vi.mock('../../stores/useCapabilitiesStore', () => ({ useCapabilitiesStore: (select: (value: unknown) => unknown) => select({ capabilities: { local_mode: false } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (value: unknown) => unknown) => select({ activeWorkspaceId: 'server' }) }))
vi.mock('./TeamTab', () => ({ teamCard: 'card', DashboardPeopleCard: () => null }))
vi.mock('../../components/workflow/PublicLinkControl', () => ({ PublicLinkControl: () => null }))
vi.mock('../../components/workflow/reportDocuments', () => ({ selectReportDocument: vi.fn() }))
vi.mock('../../utils/agentPurposeDraft', () => ({ fillMainDraft: vi.fn() }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
import { CrewDashboardsTab } from './CrewDashboardsTab'

afterEach(() => { vi.clearAllMocks() })

// Owner, 25 Nov: a simple delete. One click deletes on the server; Undo brings the same page back.
it('deletes a page in one click and Undo brings it back', async () => {
  const page = { id: 'plan', title: 'Release plan', path: 'db/reports/managed/plan/index.html', kind: 'artifact', line: '', who: '', when: '2 h ago', group: 'Today', can_delete: true }
  state.remote.mockResolvedValue({ workspace: 'Crew/p', intro: '', can_change: true, filter: 'all', filters: [], items: [page] })
  state.action.mockResolvedValue({ deleted: true })
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<CrewDashboardsTab projectId="p" />))
    expect(container.textContent).toContain('Release plan')
    expect(container.textContent).toContain('Today')
    state.remote.mockResolvedValue({ workspace: 'Crew/p', intro: '', can_change: true, filter: 'all', filters: [], items: [] })
    await act(async () => (container.querySelector('[aria-label="Delete Release plan"]') as HTMLButtonElement).click())
    expect(state.action).toHaveBeenCalledWith({ project: 'p', connection: 'server' }, 'plan', 'delete')
    expect(container.textContent).toContain('Deleted “Release plan”')
    state.remote.mockResolvedValue({ workspace: 'Crew/p', intro: '', can_change: true, filter: 'all', filters: [], items: [page] })
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Undo')!.click())
    expect(state.action).toHaveBeenLastCalledWith({ project: 'p', connection: 'server' }, 'plan', 'undelete')
    expect(container.textContent).toContain('Release plan')
    expect(container.textContent).not.toContain('Deleted “Release plan”')
  } finally { await act(async () => root.unmount()) }
})
