// @vitest-environment happy-dom
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it, vi } from 'vitest'
import { GoalStatusCard } from './GoalStatusCard'

vi.mock('../../api/scheduler', () => ({ schedulerApi: {} }))
vi.mock('../../utils/pulseChatTab', () => ({ openWorkflowPulseChatTab: vi.fn() }))

it('shows a DB measurement without a folder link and waits for an agent progress verdict', () => {
  const html = renderToStaticMarkup(<GoalStatusCard goal={{
    facts: { status: 'ok', has_goal: true, summary: 'Measured', key_metric: 'responses', key_value: 0,
      last_measured_at: '2026-10-09T00:00:00Z', days_since_run_measured: -1, days_since_run: 0, alarms: [], schedules_paused: false },
    latest_check: { status: 'not_measured', checked_at: '2026-10-08T00:00:00Z', summary: 'Old measurement verdict' },
  }} />)
  expect(html).toContain('Last measured')
  expect(html).toContain('0 (responses)')
  expect(html).toContain('Awaiting goal review')
  expect(html).not.toContain('Never measured by a run')
  expect(html).not.toContain('On track')
  expect(html).not.toContain('Old measurement verdict')
})
