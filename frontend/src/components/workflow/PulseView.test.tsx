// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
vi.mock('./SoulViewer', () => ({ SoulViewer: () => <p>Grow useful customer conversations.</p>, WORKFLOW_SOUL_REFRESH_EVENT: 'test-soul' }))
vi.mock('../../utils/pulseChatTab', () => ({ openWorkflowPulseChatTab: vi.fn(async () => 'pulse-tab'), askWorkflowPulse: vi.fn(async () => {}) }))
vi.mock('./ReportHumanInputPanel', () => ({ ReportHumanInputPanel: () => <p>No decisions waiting.</p> }))
vi.mock('./AskAIButton', () => ({ AskAIButton: () => <button>Edit goals &amp; numbers</button> }))
import { askWorkflowPulse, openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import type { PulseOverviewResponse } from '../../services/api-types'
const navigation = vi.hoisted(() => ({ apply: undefined as undefined | ((target: string) => void) }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ useWorkspaceViewTarget: (_view: string, apply: (target: string) => void) => { navigation.apply = apply } }))
import PulseView from './PulseView'

it.each([true, false])('shows one Pulse screen, keeps simple settings and respects OFF (on=%s)', async on => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  await act(async () => root.render(<PulseView workspacePath="Workflow/example" monitorOn={on} hasSoul monitorSaving={false} onToggleMonitor={() => {}}
   statusError={null} statusLoading={false} onRefresh={() => {}} />))
  for (const label of ['Goals', 'Metrics', 'Pulse activity', 'Decisions', 'Pulse settings', 'Talk to Pulse']) expect(host.querySelector(`[aria-label="${label}"]`)).not.toBeNull()
  expect(host.querySelector('[role="tablist"]')).toBeNull()
  expect(host.textContent).not.toMatch(/Platform health|Maintenance issues|Review run history|Weekly/)
  expect(host.querySelector('[aria-label="Pulse on/off"]')?.getAttribute('aria-checked')).toBe(String(on))
  const chat = [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Pulse chat')!
  expect(chat.disabled).toBe(!on)
  for (const [target, label] of [['settings', 'Pulse settings'], ['goals', 'Goals'], ['metrics', 'Metrics'], ['activity', 'Pulse activity'], ['decisions', 'Decisions']]) {
   const section = host.querySelector<HTMLElement>(`section[aria-label="${label}"]`)!
   section.scrollIntoView = vi.fn()
   await act(async () => navigation.apply?.(target))
   expect(section.scrollIntoView).toHaveBeenCalledWith({ block: 'start' })
   expect(document.activeElement).toBe(section)
   expect(chat.disabled).toBe(!on)
  }
  await act(async () => navigation.apply?.('chat'))
  expect(openWorkflowPulseChatTab).toHaveBeenCalledWith('Workflow/example')
 } finally { await act(async () => root.unmount()); host.remove() }
})

it.each([true, false])('restores saved metrics/history and routes goal, metric and update questions to Pulse (on=%s)', async on => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 vi.mocked(askWorkflowPulse).mockClear()
 const overview: PulseOverviewResponse = { success: true, measurements: {
  metrics: [{ id: 'revenue', criterion_id: 'growth', name: 'Revenue', role: 'primary', goal_id: 'growth', goal_name: 'Grow revenue', unit: 'USD', direction: 'increase', definition: 'Revenue received', source: 'Invoices', window: 'instant', route: '', environment: '', collection_frequency: 'daily', freshness_hours: 48, target: 200 }],
  observations: [{ observation_id: 'reading', criterion_id: 'growth', metric: 'revenue', run_id: 'run-1', unit: 'USD', value: 120, observed_at: '2026-10-21T10:00:00Z', evidence: ['db/invoices.csv'] }],
 }, conversation: { has_goal: on, busy: false, messages: [{ id: 'update', at: '2026-10-21T11:00:00Z', role: 'goal_lead', text: 'Invoices are collecting.' }] } }
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  await act(async () => root.render(<PulseView workspacePath="Workflow/example" monitorOn={on} hasSoul overview={overview} monitorSaving={false} onToggleMonitor={() => {}} statusError={null} statusLoading={false} onRefresh={() => {}} />))
  expect(host.textContent).toContain('Grow revenue')
  expect(host.textContent).toContain('120')
  expect(host.textContent).toContain('db/invoices.csv')
  expect(host.textContent).toContain('Invoices are collecting.')
  for (const label of ['Ask Pulse about this goal', 'Ask Pulse about this metric', 'Ask Pulse about this update']) {
   const button = [...host.querySelectorAll('button')].find(button => button.textContent === label)!
   expect(button.disabled).toBe(!on)
   await act(async () => button.click())
  }
  if (on) {
   expect(askWorkflowPulse).toHaveBeenCalledTimes(3)
   expect(vi.mocked(askWorkflowPulse).mock.calls[0]).toEqual(['Workflow/example', expect.stringContaining('Grow revenue')])
   expect(vi.mocked(askWorkflowPulse).mock.calls[1]).toEqual(['Workflow/example', expect.stringContaining('metric revenue')])
   expect(vi.mocked(askWorkflowPulse).mock.calls[2]).toEqual(['Workflow/example', expect.stringContaining('Invoices are collecting.')])
  } else expect(askWorkflowPulse).not.toHaveBeenCalled()
 } finally { await act(async () => root.unmount()); host.remove() }
})
