// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { ScheduledJob } from '../../../services/api-types'
import { ScheduleTableView } from './ScheduleTableView'

vi.mock('../../../services/api', () => ({ getApiBaseUrl: () => 'https://agent.example', getAuthToken: () => null }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('global schedule table', () => {
  it.each([{ isReadOnlyUser: false, isWebhook: false }, { isReadOnlyUser: true, isWebhook: false }, { isReadOnlyUser: false, isWebhook: true }, { isReadOnlyUser: true, isWebhook: true }])('keeps details on demand and respects access for %o', async ({ isReadOnlyUser, isWebhook }) => {
    const job = { schedule_type: isWebhook ? 'webhook' : 'cron', id: 'daily', name: 'Daily report', workflow_label: 'Research', enabled: true,
      cron_expression: '0 8 * * *', run_count: 3, last_status: 'error', last_error: 'Previous run failed',
      messages: ['Collect evidence and prepare the daily report.'], missed_run_count: 2,
      next_run_at: '2026-09-13T08:00:00Z', last_run_at: '2026-09-12T08:00:00Z',
      collision_policy: 'queue_latest', max_start_delay_minutes: 120,
      avg_duration_ms: 18 * 60_000, avg_duration_samples: 10, consecutive_failures: 2,
      after_schedule_ids: ['collector', 'guard'], after_terminal_status: 'completed', after_delay_minutes: 5, dependency_deadline: '10:30',
    } as ScheduledJob
    const trigger = vi.fn()
    const panel = { focusedScheduleId: null as string | null, filteredJobs: [job], presetMap: new Map(), potentialOverlaps: new Map<string, string>(), isSchedulerPaused: true, isReadOnlyUser,
      triggering: null, handleStopRun: vi.fn(), handleTrigger: trigger, handleToggle: vi.fn(), handleDelete: vi.fn(), handleAfterRun: vi.fn(),
      openActionMenuJobId: null, setOpenActionMenuJobId: vi.fn(),
      expandedRunHistoryJobIds: new Set<string>(), runsByJob: {}, runsLoadingJobIds: new Set<string>(), toggleRunHistory: vi.fn(), openScheduledRun: vi.fn(),
    }
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<ScheduleTableView panel={panel} />))
      expect(host.textContent).toContain('Paused globally')
      expect(host.textContent).toContain('Failed')
      expect(host.textContent).toContain('18m 0s')
      expect(host.textContent).toContain('last 10 successful')
      expect(host.textContent).toContain('2 missed')
      expect(host.textContent).toContain('2 failures in a row')
      expect(host.textContent).not.toContain(job.last_error)
      expect(host.textContent).not.toContain(job.messages![0])
      const quickRun = host.querySelector<HTMLButtonElement>('[aria-label="Run missed schedule Daily report now"]')
      expect(Boolean(quickRun)).toBe(!isReadOnlyUser && !isWebhook)
      if (quickRun) {
        await act(async () => quickRun.click())
        expect(trigger).toHaveBeenCalledWith(job)
        expect(host.querySelector('[role="region"]')).toBeNull()
      }
      const toggle = host.querySelector<HTMLButtonElement>('[aria-label="Show Daily report details"]')!
      await act(async () => toggle.click())
      const details = host.querySelector('[role="region"]')!
      expect(details.textContent).toContain(job.messages![0])
      expect(details.textContent).toContain(job.last_error)
      expect(details.textContent).toContain('Coordination and runtime policy')
      expect(details.textContent).toContain('Waits for: collector, guard')
      expect(details.textContent).toContain('Release: completed + 5m delay · Deadline: 10:30 local')
      expect(details.textContent).toContain('When busy: queue latest · Start within 120m')
      expect(details.textContent).toContain('does not replay each missed time')
      const run = Array.from(details.querySelectorAll('button')).find(b => b.textContent === 'Run now')
      expect(Boolean(run)).toBe(!isReadOnlyUser && !isWebhook)
      if (isWebhook) {
        expect(host.textContent).toContain('Webhook · on request')
        expect(details.textContent).toContain('/api/hooks/workflow/daily')
        expect(details.querySelector('[aria-label="Copy webhook URL for Daily report"]')).not.toBeNull()
      }
      if (run) {
        await act(async () => run.click())
        expect(trigger).toHaveBeenCalledWith(job)
      }
      await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Hide Daily report details"]')!.click())
      expect(host.querySelector('[role="region"]')).toBeNull()
      await act(async () => root.render(<ScheduleTableView panel={{...panel, focusedScheduleId: job.id}} />))
      expect(host.querySelector('[role="region"]')?.textContent).toContain(job.messages![0])
    } finally { await act(async () => root.unmount()); host.remove() }
  })

  it('prevents repeat starts while a missed schedule is starting or already queued', async () => {
    const job = { id: 'daily', name: 'Daily report', schedule_type: 'cron', enabled: true, last_status: 'error', missed_run_count: 1, cron_expression: '0 8 * * *' } as ScheduledJob
    const trigger = vi.fn()
    const panel = { focusedScheduleId: null, filteredJobs: [job], presetMap: new Map(), potentialOverlaps: new Map<string, string>(), isSchedulerPaused: false, isReadOnlyUser: false,
      triggering: job.id, handleStopRun: vi.fn(), handleTrigger: trigger, handleToggle: vi.fn(), handleDelete: vi.fn(), handleAfterRun: vi.fn(),
      openActionMenuJobId: null, setOpenActionMenuJobId: vi.fn(), expandedRunHistoryJobIds: new Set<string>(), runsByJob: {}, runsLoadingJobIds: new Set<string>(), toggleRunHistory: vi.fn(), openScheduledRun: vi.fn(),
    }
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<ScheduleTableView panel={panel} />))
      const quickRun = host.querySelector<HTMLButtonElement>('[aria-label="Run missed schedule Daily report now"]')!
      expect(quickRun.disabled).toBe(true)
      expect(quickRun.textContent).toContain('Starting')
      await act(async () => root.render(<ScheduleTableView panel={{ ...panel, triggering: null, filteredJobs: [{ ...job, last_status: 'waiting_for_capacity' }] }} />))
      expect(host.querySelector('[aria-label="Run missed schedule Daily report now"]')).toBeNull()
      expect(trigger).not.toHaveBeenCalled()
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
