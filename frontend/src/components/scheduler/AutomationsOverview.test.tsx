// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import SchedulesPage from '../SchedulesPage'
import { ScheduleListView } from './scheduleRuns/ScheduleListView'

const state = vi.hoisted(() => ({ local: true }))
vi.mock('../../stores/useCapabilitiesStore', () => ({ useCapabilitiesStore: (select: (value: unknown) => unknown) => select({ capabilities: { local_mode: state.local } }) }))
vi.mock('../workspace/WorkspacePageHeader', () => ({ WorkspacePageHeader: ({ title }: { title: string }) => <h1>{title}</h1> }))
vi.mock('./SimpleSchedulesOverview', () => ({ default: () => <div>Saved schedules</div> }))
vi.mock('./GlobalTriggersView', () => ({ default: ({ mode }: { mode: string }) => <div data-kind={mode}>Saved {mode}</div> }))
vi.mock('../ScheduleExecutionHistoryList', () => ({ ScheduleExecutionHistoryList: () => null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Owner: local is simpler, but an existing webhook is still reachable; switching
// to a server promotes Webhooks without changing what either endpoint permits.
it('keeps local webhooks under More and exposes server automation categories', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  const click = async (label: string) => act(async () => {
    const button = [...host.querySelectorAll('button')].find(item => item.textContent?.startsWith(label))
    expect(button).toBeTruthy(); button!.click()
  })
  try {
    state.local = true
    await act(async () => root.render(<SchedulesPage />))
    expect(host.textContent).toContain('requires your Mac to be on')
    expect(host.querySelector('details')?.textContent).toContain('Webhooks')
    await click('Watchers')
    expect(host.querySelector('[data-kind="watchers"]')).toBeTruthy()
    await click('Webhooks')
    expect(host.querySelector('[data-kind="webhooks"]')).toBeTruthy()
    state.local = false
    await act(async () => root.render(<SchedulesPage />))
    expect(host.textContent).toContain('runs while your Mac is off')
    expect(host.querySelector('nav details')).toBeNull()
    expect(host.querySelector('nav')?.textContent).toContain('Webhooks')
  } finally { await act(async () => root.unmount()) }
})

// The reported over-wide list must not expose a giant switch or combine every
// detail in one sentence; existing pause actions remain available via the menu.
it('keeps compact schedule actions in a menu and respects existing read access', async () => {
  const host = document.createElement('div'), root = createRoot(host), toggle = vi.fn()
  const panel = { filteredJobs: [{ id: 'one', name: 'Report', description: '', entity_type: 'product', workflow_label: 'Example', starts: 'Sends a message to Analyst', enabled: true, cron_expression: '0 9 * * *', timezone: 'UTC', run_count: 0, status_word: 'Scheduled' }], presetMap: new Map(), showWorkflowIdentityInScheduleRows: true, isReadOnlyUser: false, handleToggle: toggle, expandedRunHistoryJobIds: new Set(), runsByJob: {}, runsLoadingJobIds: new Set(), deletingRunSessionIds: new Set() }
  try {
    await act(async () => root.render(<ScheduleListView compact panel={panel as never} />))
    expect(host.querySelector('[role="switch"]')).toBeNull()
    expect(host.querySelector('summary[aria-label="Actions for Report"]')).toBeTruthy()
    expect(host.textContent).toContain('Example → Sends a message to Analyst')
    await act(async () => [...host.querySelectorAll('button')].find(item => item.textContent === 'Pause')!.click())
    expect(toggle).toHaveBeenCalledWith(panel.filteredJobs[0])
    await act(async () => root.render(<ScheduleListView compact panel={{ ...panel, isReadOnlyUser: true } as never} />))
    expect(host.querySelector('summary')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
