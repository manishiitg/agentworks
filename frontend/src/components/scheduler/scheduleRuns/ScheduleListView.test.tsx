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

function stubPanel(filteredJobs: ScheduledJob[]) {
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

async function renderList(filteredJobs: ScheduledJob[]) {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<ScheduleListView panel={stubPanel(filteredJobs)} />))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

describe('ScheduleListView run destination', () => {
  // PLAT-825-96: a Crew schedule can run in a tab, so the menu lists the tabs the server names and shows the current one.
  it('lists the tabs the server returns and selects the one the schedule runs in', async () => {
    const { host, unmount } = await renderList([
      job({
        id: 'p', name: 'Upwork check', entity_type: 'product', run_destination: 'chat', chat_id: 'abc12345',
        run_destinations: [{ id: 'main', label: 'Main chat' }, { id: 'abc12345', label: 'Upwork' }, { id: 'isolated', label: 'Own conversation' }],
      }),
    ])
    try {
      const select = host.querySelector('select[aria-label="Run destination for Upwork check"]') as HTMLSelectElement
      expect(Array.from(select.options).map(option => option.textContent)).toEqual(['Main chat', 'Upwork', 'Own conversation'])
      expect(select.value).toBe('abc12345')
    } finally {
      await unmount()
    }
  })
})

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

  it('shows quiet rows with no coloured group headers', async () => {
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Solo', run_count: 0, status_word: 'Scheduled' }),
    ])
    try {
      expect(host.textContent).not.toContain('Automation schedules')
      expect(host.textContent).not.toContain('Missed schedules')
      expect(host.textContent).toContain('Scheduled')
      expect(host.innerHTML).not.toContain('text-warning')
    } finally {
      await unmount()
    }
  })

  it('shows the route as plain secondary text beside the schedule, not as a pill', async () => {
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Trade', route_selections: { step: 'trade' }, run_count: 1 }),
    ])
    try {
      expect(host.querySelector('h3')?.textContent).toBe('Trade')
      expect(host.textContent).toContain('Route: trade')
    } finally {
      await unmount()
    }
  })

  it('shows the status word and last-run sentence the server wrote, with the raw error only as a tooltip', async () => {
    const raw = 'workshop turn 1/1 (schedule-message-1) produced no response: message_sequence'
    const { host, unmount } = await renderList([
      job({ id: 'a', name: 'Late', status_word: 'Needs attention', last_status: 'error', last_error: raw, last_result: 'The last run did not finish.' }),
      job({ id: 'b', name: 'Held', status_word: 'Paused', missed_run_count: 3 }),
    ])
    try {
      expect(host.textContent).toContain('Needs attention')
      expect(host.textContent).toContain('The last run did not finish.')
      expect(host.textContent).not.toContain('message_sequence')
      expect(host.querySelector(`[title="${raw}"]`)).not.toBeNull()
      expect(host.textContent).toContain('Paused')
      expect(host.textContent).not.toContain('Missed')
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

})
