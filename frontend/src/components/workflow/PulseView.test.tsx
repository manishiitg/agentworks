// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
vi.mock('./GoalLeadPanel', () => ({ GoalLeadPanel: () => <section aria-label="Needs you">Needs you</section> }))
vi.mock('./SoulViewer', () => ({ SoulViewer: () => <p>Grow useful customer conversations.</p>, WORKFLOW_SOUL_REFRESH_EVENT: 'test-soul' }))
vi.mock('../../utils/pulseChatTab', () => ({ openWorkflowPulseChatTab: vi.fn(async () => {}) }))
import PulseView from './PulseView'

it.each([true, false])('shows one Pulse screen, retains the last check and respects OFF (on=%s)', async on => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  await act(async () => root.render(<PulseView workspacePath="Workflow/example" monitorOn={on} hasSoul monitorSaving={false} onToggleMonitor={() => {}}
   statusError={null} statusLoading={false} onRefresh={() => {}} goalStatus={{facts:{status:'not_measured',summary:'Waiting',has_goal:true,days_since_run_measured:0,days_since_run:0,alarms:[],schedules_paused:false},latest_check:{checked_at:'2026-10-17T10:00:00Z',status:'not_measured',summary:'The change still needs measurement.'}}} />))
  for (const label of ['Goal', 'Pulse settings', 'Last change', 'Needs you', 'Next', 'Last check', 'Talk to Pulse']) expect(host.querySelector(`[aria-label="${label}"]`)).not.toBeNull()
  expect(host.querySelector('[role="tablist"]')).toBeNull()
  expect(host.textContent).not.toMatch(/Platform health|Maintenance issues|Review run history|Weekly/)
  expect(host.textContent).toContain('The change still needs measurement.')
  expect(host.querySelector('[aria-label="Pulse on/off"]')?.getAttribute('aria-checked')).toBe(String(on))
  const chat = [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Pulse chat')!
  expect(chat.disabled).toBe(!on)
 } finally { await act(async () => root.unmount()); host.remove() }
})
