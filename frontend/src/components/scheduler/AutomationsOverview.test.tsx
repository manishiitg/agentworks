// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useAppStore } from '../../stores/useAppStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { schedulerApi } from '../../api/scheduler'
import type { ScheduledJob } from '../../services/api-types'
import SchedulesPage from '../SchedulesPage'

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
