// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({
  team: { error: 'unavailable', target: undefined as undefined | { project: string; connection: string } },
  retry: vi.fn(), open: vi.fn(), remote: vi.fn(),
}))
vi.mock('./teamApi', () => ({
  useTeamTab: () => ({ ...state.team, view: {team: {on: true, can_manage: true}}, retry: state.retry }),
  crewDashboardsTab: state.remote,
  teamFailure: (error: Error) => error,
  teamPublicLink: vi.fn(),
}))
vi.mock('../../stores/useCapabilitiesStore', () => ({ useCapabilitiesStore: (select: (value: unknown) => unknown) => select({ capabilities: { local_mode: true } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (value: unknown) => unknown) => select({ activeWorkspaceId: 'local' }) }))
vi.mock('../../services/dashboardDirectory', () => ({
  loadDashboardDirectory: async () => ({ dashboards: [
    { product: 'crew', project_id: 'local', dashboard_id: 'page', can_edit: true, title: 'Local proposal', workspace: 'Crew/local', document_path: 'db/reports/page.html' },
    { product: 'crew', project_id: 'different', dashboard_id: 'other', title: 'Different project' },
  ] }),
  dashboardDisplayName: (row: { title: string }) => row.title,
}))
vi.mock('./TeamTab', () => ({ teamCard: 'card', DashboardPeopleCard: () => null }))
vi.mock('../../components/workflow/PublicLinkControl', () => ({ PublicLinkControl: () => null }))
vi.mock('../../components/workflow/reportDocuments', () => ({ selectReportDocument: state.open }))
vi.mock('../../utils/agentPurposeDraft', () => ({ fillMainDraft: vi.fn() }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: { getState: () => ({ addToast: vi.fn() }) } }))
import { CrewDashboardsTab } from './CrewDashboardsTab'

afterEach(() => { vi.useRealTimers(); vi.clearAllMocks() })

it('keeps local artifacts usable during Team failure, retries with backoff and recovers', async () => {
  vi.useFakeTimers()
  state.team = { error: 'unavailable', target: undefined }
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<CrewDashboardsTab projectId="local" />))
    expect(container.textContent).toContain('Local proposal')
    expect(container.textContent).not.toContain('Different project')
    expect(container.textContent).toContain("Couldn't reach the Team server, retrying")
    expect(state.remote).not.toHaveBeenCalled()
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent?.includes('Local proposal'))!.click())
    expect(state.open).toHaveBeenCalledWith('Crew/local', 'db/reports/page.html', true)
    await act(async () => vi.advanceTimersByTime(1000))
    expect(state.retry).toHaveBeenCalledTimes(1)
    await act(async () => vi.advanceTimersByTime(1000))
    expect(state.retry).toHaveBeenCalledTimes(1)
    await act(async () => vi.advanceTimersByTime(1000))
    expect(state.retry).toHaveBeenCalledTimes(2)
    state.team = { error: '', target: { project: 'online', connection: 'server' } }
    state.remote.mockResolvedValue({ workspace: 'Crew/online', intro: '', filter: 'all', filters: [{ key: 'all', label: 'All', count: 1 }, { key: 'dashboard', label: 'Dashboards', count: 0 }, { key: 'motion', label: 'Motion', count: 1 }], items: [{ id: 'online', title: 'Team film', kind: 'motion', path: 'db/reports/film.html' }], empty: { title: 'No items anywhere' } })
    await act(async () => root.render(<CrewDashboardsTab projectId="local" />))
    expect(container.textContent).toContain('On the Team')
    expect(container.textContent).toContain('All 2')
    expect(container.textContent).toContain('Dashboards 1')
    expect(container.textContent).toContain('Motion 1')
    expect(container.textContent).not.toContain('No items anywhere')
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Motion 1')!.click())
    expect(container.textContent).not.toContain('Local proposal')
    expect(container.textContent).toContain('Team film')
    expect(container.textContent).not.toContain('No items anywhere')
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'All 2')!.click())
    expect(container.textContent).toContain('Local proposal')
    expect(container.textContent).not.toContain("Couldn't reach the Team server")
  } finally { await act(async () => root.unmount()) }
})
