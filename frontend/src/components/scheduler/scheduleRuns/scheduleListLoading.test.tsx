// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { schedulerApi } from '../../../api/scheduler'
import { useGlobalPresetStore } from '../../../stores/useGlobalPresetStore'
import { useAuthStore } from '../../../stores/useAuthStore'
import { useScheduleRunsData, type ScheduleRunsPanelState } from './useScheduleRunsData'
import { expandCronForMonth } from './cron'

vi.mock('../../../api/scheduler', () => ({ schedulerApi: { listJobs: vi.fn(), getConfig: vi.fn() } }))
vi.mock('../../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
vi.mock('../../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../../utils/chatIdentity', async original => ({ ...await original<typeof import('../../../utils/chatIdentity')>(), captureChatIdentity: () => ({}), isChatIdentityCurrent: () => true }))
vi.mock('./cron', async original => ({ ...await original<typeof import('./cron')>(), expandCronForMonth: vi.fn(() => []) }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Reported slow list: it must paint jobs before optional config and never expand
// three months of cron occurrences unless the person actually opens Calendar.
it('loads the schedule list without waiting for config or building an unopened calendar', async () => {
  const presets = useGlobalPresetStore.getState(), auth = useAuthStore.getState()
  const refresh = vi.fn()
  useGlobalPresetStore.setState({ workflowPresets: [], workflowPresetsLoaded: false, refreshPresets: refresh })
  let releaseConfig!: (value: never) => void
  vi.mocked(schedulerApi.getConfig).mockReturnValue(new Promise(resolve => { releaseConfig = resolve }))
  const job = { id: 'product-project:work:example:timer', name: 'Daily report', entity_type: 'product', description: '', enabled: true, cron_expression: '* * * * *', timezone: 'UTC', run_count: 0, consecutive_failures: 0 }
  vi.mocked(schedulerApi.listJobs).mockResolvedValue({ jobs: [job], total: 1, limit: 200, offset: 0 } as never)
  let state!: ScheduleRunsPanelState
  function Probe() { state = useScheduleRunsData({ entityType: 'product', initialView: 'schedules', onClose: () => {} }); return <span>{state.isLoading ? 'Loading' : state.panelJobs.map(item => item.name).join(',')}</span> }
  const host = document.createElement('div'), root = createRoot(host)
  try {
    await act(async () => root.render(<Probe />))
    expect(host.textContent).toBe('Daily report')
    expect(state.schedulerConfig).toBeNull()
    expect(refresh).not.toHaveBeenCalled()
    expect(expandCronForMonth).not.toHaveBeenCalled()
    await act(async () => state.setActiveView('calendar'))
    expect(expandCronForMonth).toHaveBeenCalledTimes(3)
    // A retained global page must not paint the previous person's jobs on account switch.
    vi.mocked(schedulerApi.listJobs).mockReturnValue(new Promise(() => {}))
    await act(async () => useAuthStore.setState({ user: { ...auth.user, id: 'different-person' } as never }))
    expect(state.panelJobs).toHaveLength(0)
    await act(async () => releaseConfig({ globally_paused: false } as never))
  } finally { await act(async () => root.unmount()); useGlobalPresetStore.setState(presets); useAuthStore.setState(auth); vi.clearAllMocks() }
})
