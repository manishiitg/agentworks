// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useAppStore } from '../../stores/useAppStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { schedulerApi } from '../../api/scheduler'
import { workflowManifestApi } from '../../services/api'
import { useWorkflowRunNavigation } from '../../products/work/workflowRunNavigation'
import type { ScheduledJob } from '../../services/api-types'
import SchedulesPage from '../SchedulesPage'

vi.mock('../../services/api', async original => ({
  ...await original<typeof import('../../services/api')>(),
  workflowManifestApi: { listWorkflowManifests: vi.fn() },
}))
vi.mock('../workspace/WorkspaceProjectsProvider', () => ({
  useWorkspaceProjects: () => ({
    projects: [{ id: 'example', name: 'Example Crew', mode: 'builder', location: 'server' }],
    loading: false, error: false, retry: vi.fn(),
  }),
}))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: vi.fn(), getConfig: vi.fn() } }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/chatIdentity', async original => ({
  ...await original<typeof import('../../utils/chatIdentity')>(),
  captureChatIdentity: () => ({}),
  isChatIdentityCurrent: () => true,
}))
vi.mock('../../platform/chat/productProjects', () => ({
  loadProductProjects: async () => [{ id: 'example', title: 'Example Crew' }],
}))
vi.mock('../../api/conditionTriggers', () => ({
  conditionTriggersApi: { list: async () => ({ triggers: [{ id: 'one', name: 'Watch for updates', enabled: true }] }) },
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('lets a Crew owner browse automations without offering schedule mutations or a Crew webhook tab', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const surface = useProductSurfaceStore.getState(), app = useAppStore.getState(), presets = useGlobalPresetStore.getState()
  const job: ScheduledJob = {
    id: 'product-project:work:example:timer', name: 'Daily report', workflow_label: 'Example Crew',
    entity_type: 'product', description: '', enabled: true, run_count: 0, consecutive_failures: 0,
    timezone: 'UTC', cron_expression: '0 9 * * *', starts: 'Sends a message to Analyst',
    status_word: 'Scheduled', timing_summary: 'Every morning at nine',
  }
  vi.mocked(schedulerApi.listJobs).mockResolvedValue({ jobs: [job], total: 1, limit: 200, offset: 0 })
  vi.mocked(schedulerApi.getConfig).mockResolvedValue({ globally_paused: false } as never)

  const render = async () => {
    await act(async () => root.render(<SchedulesPage />))
    await act(async () => { await vi.dynamicImportSettled() })
  }
  const click = async (label: string) => {
    const button = [...host.querySelectorAll('button')].find(item => item.textContent?.startsWith(label))
    expect(button).toBeTruthy()
    await act(async () => button!.click())
    await act(async () => { await vi.dynamicImportSettled() })
  }

  try {
    await act(async () => {
      useProductSurfaceStore.setState({ productSurface: 'work' })
      useAppStore.setState({ showSchedulesOverview: true })
      useGlobalPresetStore.setState({ workflowPresets: [], workflowPresetsLoaded: true })
    })
    await render()
    expect(host.querySelector('header h1')?.textContent).toBe('Automations')
    expect(host.querySelector('header')?.textContent).not.toMatch(/Back|Crew/)
    expect(host.querySelector('nav[aria-label="Automation types"]')?.textContent).not.toMatch(/More|Webhooks/)
    expect(host.querySelector('[type="search"][aria-label="Search schedules"]')).toBeTruthy()
    expect(host.textContent).not.toMatch(/New schedule|Pause all/)
    expect(host.textContent).toContain('Example Crew')

    await click('Daily report')
    expect(host.textContent).toContain('Every morning at nine')
    expect(host.textContent).toContain('Sends a message to Analyst')
    expect(host.querySelector('select,[role="switch"],summary[aria-label^="Actions for"]')).toBeNull()
    expect(host.textContent).not.toMatch(/Run now|Stop|Delete schedule/)

    await click('Watchers')
    await click('Watch for updates')
    expect(host.textContent).toContain('When something changes')
    await click('Open Example Crew')
    expect(useProductSurfaceStore.getState().pendingWorkView).toBe('conditions')

    await act(async () => useProductSurfaceStore.setState({ productSurface: 'agentworks' }))
    expect(host.querySelector('nav[aria-label="Automation types"]')?.textContent).toContain('Webhooks')
  } finally {
    await act(async () => root.unmount())
    useProductSurfaceStore.setState(surface)
    useAppStore.setState(app)
    useGlobalPresetStore.setState(presets)
  }
})


// Legacy standalone workflows and other Crews are not attached to the open
// one; a saved workflow doesn't need a schedule to appear in this list.
it('lists unscheduled workflows by their admitted Crew and opens the exact workflow', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const surface = useProductSurfaceStore.getState(), app = useAppStore.getState(), target = useWorkflowRunNavigation.getState()
  const previousURL = window.location.href
  vi.mocked(workflowManifestApi.listWorkflowManifests).mockResolvedValue({
    success: true, total: 4, workflows: [
      { workspace_path: 'Workflow/report', crew_id: 'example', manifest: { id: 'report', label: 'Expense review', description: 'Checks receipts.', schedules: [] } },
      { workspace_path: 'Workflow/other', crew_id: 'unlisted', manifest: { id: 'other', label: 'Unlisted work' } },
      { workspace_path: 'Workflow/legacy', manifest: { id: 'legacy', label: 'Standalone workflow' } },
      { workspace_path: 'Workflow/relay', crew_id: 'example', manifest: { id: 'relay', label: 'Relay instead', kind: 'relay' } },
    ],
  } as never)
  vi.mocked(schedulerApi.listJobs).mockResolvedValue({ jobs: [], total: 0, limit: 200, offset: 0 })

  const click = async (label: string) => {
    const button = [...host.querySelectorAll('button')].find(item => item.textContent?.startsWith(label))
    expect(button).toBeTruthy()
    await act(async () => button!.click())
    await act(async () => { await vi.dynamicImportSettled() })
  }

  try {
    await act(async () => {
      useProductSurfaceStore.setState({ productSurface: 'work' })
      useAppStore.setState({ showSchedulesOverview: true })
      root.render(<SchedulesPage />)
    })
    await act(async () => { await vi.dynamicImportSettled() })
    expect(workflowManifestApi.listWorkflowManifests).not.toHaveBeenCalled()
    await click('Workflows')
    expect(host.textContent).toContain('Expense review')
    expect(host.textContent).toContain('Example Crew')
    expect(host.textContent).not.toMatch(/Unlisted work|Standalone workflow|Relay instead/)
    expect(host.querySelector('[aria-label="Search workflows"]')).toBeTruthy()
    await click('Expense review')
    expect(host.textContent).toContain('Checks receipts.')
    await click('Open workflow')
    expect(useWorkflowRunNavigation.getState().target).toMatchObject({ crewId: 'example', executableId: 'report', section: 'steps' })
    expect(useAppStore.getState().showSchedulesOverview).toBe(false)
  } finally {
    await act(async () => root.unmount())
    window.history.replaceState({}, '', previousURL)
    useProductSurfaceStore.setState(surface)
    useAppStore.setState(app)
    useWorkflowRunNavigation.setState(target)
    vi.clearAllMocks()
  }
})
