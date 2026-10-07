// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { ScheduleListView } from './ScheduleListView'
import type { ScheduledJob } from '../../../services/api-types'

vi.mock('../../../services/api', () => ({ getApiBaseUrl: () => 'https://agent.example', getAuthToken: () => null }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const job = (overrides: Partial<ScheduledJob> & { id: string; name: string }): ScheduledJob => ({
  description: '',
  entity_type: 'workflow',
  cron_expression: '0 9 * * 1-5',
  timezone: 'UTC',
  enabled: true,
  run_count: 0,
  consecutive_failures: 0,
  ...overrides,
})

function stubPanel(filteredJobs: ScheduledJob[], handleAfterRun: (job: ScheduledJob, next: NonNullable<ScheduledJob['after_run']>) => Promise<void> = async () => {}) {
  const noop = () => {}
  const asyncNoop = async () => {}
  return {
    filteredJobs,
    presetMap: new Map(),
    showWorkflowIdentityInScheduleRows: false,
    isReadOnlyUser: false,
    handleStopRun: asyncNoop,
    handleTrigger: asyncNoop,
    triggering: null,
    handleToggle: asyncNoop,
    handleRunDestination: asyncNoop,
    handleAfterRun,
    openActionMenuJobId: null,
    setOpenActionMenuJobId: noop,
    handleDelete: asyncNoop,
    expandedRunHistoryJobIds: new Set<string>(),
    runsByJob: {},
    runsLoadingJobIds: new Set<string>(),
    deletingRunSessionIds: new Set<string>(),
    toggleRunHistory: asyncNoop,
    openScheduledRun: asyncNoop,
    deleteScheduledRunSession: asyncNoop,
  }
}

async function renderList(filteredJobs: ScheduledJob[], handleAfterRun?: (job: ScheduledJob, next: NonNullable<ScheduledJob['after_run']>) => Promise<void>) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<ScheduleListView panel={stubPanel(filteredJobs, handleAfterRun)} />))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

describe('ScheduleListView declutter', () => {
  it('hides a single group but shows multiple groups', async () => {
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Solo', group_names: ['Default Group'], run_count: 1 }),
      job({ id: 'b', name: 'Multi', group_names: ['Team A', 'Team B'], run_count: 1 }),
    ])
    try {
      expect(host.textContent).not.toContain('Default Group')
      expect(host.textContent).toContain('Groups: Team A, Team B')
    } finally {
      await unmount()
    }
  })

  it('shows section counts without description lines', async () => {
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Solo', run_count: 0 }),
    ])
    try {
      expect(host.textContent).toContain('Automation schedules')
      expect(host.textContent).toContain('· 1')
      expect(host.textContent).not.toContain('idle, paused, or waiting')
    } finally {
      await unmount()
    }
  })

  it('keeps the route pill next to the schedule name', async () => {
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Trade', route_selections: { step: 'trade' }, run_count: 1 }),
    ])
    try {
      const titleRow = host.querySelector('[title="Trade"]')?.parentElement
      expect(titleRow?.textContent).toContain('Route: trade')
    } finally {
      await unmount()
    }
  })

  it('says Not run yet instead of Last ran: never', async () => {
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Fresh', run_count: 0 }),
    ])
    try {
      expect(host.textContent).toContain('Not run yet')
      expect(host.textContent).not.toContain('Last ran: never')
    } finally {
      await unmount()
    }
  })

  // PLAT-697 phase 0: backup, publish and notify are a schedule's own
  // after-run checkboxes, replacing pulse_mode.
  it('shows the after-run checkboxes and saves a change', async () => {
    const handleAfterRun = vi.fn(async () => {})
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Daily', after_run: { backup: true, publish: false, notify: true } }),
    ], handleAfterRun)
    try {
      const group = host.querySelector('[aria-label="After each run of Daily"]')
      const boxes = Array.from(group?.querySelectorAll('input[type="checkbox"]') ?? []) as HTMLInputElement[]
      expect(boxes.map(box => box.checked)).toEqual([true, false, true])
      await act(async () => { boxes[1].click() })
      expect(handleAfterRun).toHaveBeenCalledWith(expect.objectContaining({ id: 'a' }), { backup: true, publish: true, notify: true })
    } finally {
      await unmount()
    }
  })
})
