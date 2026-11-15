// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const state = vi.hoisted(() => ({
  team: { error: 'unavailable', target: undefined as undefined | { project: string; connection: string } },
  retry: vi.fn(), open: vi.fn(), remote: vi.fn(),
}))
vi.mock('./teamApi', () => ({
  useTeamTab: () => ({ ...state.team, retry: state.retry }),
  crewDashboardsTab: state.remote,
  teamFailure: (error: Error) => error,
  teamPublicLink: vi.fn(),
}))
vi.mock('../../stores/useCapabilitiesStore', () => ({ useCapabilitiesStore: (select: (value: unknown) => unknown) => select({ capabilities: { local_mode: true } }) }))
vi.mock('../../stores/useWorkspaceConnectionStore', () => ({ useWorkspaceConnectionStore: (select: (value: unknown) => unknown) => select({ activeWorkspaceId: 'local' }) }))
vi.mock('../../services/dashboardDirectory', () => ({
  loadDashboardDirectory: async () => ({ dashboards: [
    { product: 'crew', project_id: 'local', dashboard_id: 'page', title: 'Local proposal', workspace: 'Crew/local', document_path: 'db/reports/page.html' },
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
    await act(async () => container.querySelector('button')!.click())
    expect(state.open).toHaveBeenCalledWith('Crew/local', 'db/reports/page.html', true)
    await act(async () => vi.advanceTimersByTime(1000))
    expect(state.retry).toHaveBeenCalledTimes(1)
    await act(async () => vi.advanceTimersByTime(1000))
    expect(state.retry).toHaveBeenCalledTimes(1)
    await act(async () => vi.advanceTimersByTime(1000))
    expect(state.retry).toHaveBeenCalledTimes(2)
    state.team = { error: '', target: { project: 'online', connection: 'server' } }
    state.remote.mockResolvedValue({ workspace: 'Crew/online', intro: '', filter: 'all', filters: [], items: [] })
    await act(async () => root.render(<CrewDashboardsTab projectId="local" />))
    expect(container.textContent).toContain('Online with the Team')
    expect(container.textContent).toContain('Local proposal')
    expect(container.textContent).not.toContain("Couldn't reach the Team server")
  } finally { await act(async () => root.unmount()) }
})
