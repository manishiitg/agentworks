// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
vi.mock('./SoulViewer', () => ({ SoulViewer: () => <p>Grow useful customer conversations.</p>, WORKFLOW_SOUL_REFRESH_EVENT: 'test-soul' }))
vi.mock('../../utils/pulseChatTab', () => ({ openWorkflowPulseChatTab: vi.fn(async () => 'pulse-tab'), askWorkflowPulse: vi.fn(async () => {}) }))
vi.mock('./AskAIButton', () => ({ AskAIButton: () => <button>Edit goals &amp; numbers</button> }))
import { askWorkflowPulse, openWorkflowPulseChatTab } from '../../utils/pulseChatTab'
import type { PulseOverviewResponse } from '../../services/api-types'
const navigation = vi.hoisted(() => ({ apply: undefined as undefined | ((target: string) => void) }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ useWorkspaceViewTarget: (_view: string, apply: (target: string) => void) => { navigation.apply = apply } }))
import PulseView from './PulseView'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { WorkspacePanelNavigationProvider } from '../workspace/WorkspacePanelNavigation'
import { WorkspacePanelHeader } from '../workspace/WorkspacePanelHeader'
import { AskAIButton } from './AskAIButton'

beforeEach(() => {
 useWorkflowStore.setState({ workspaceViewTarget: null })
 HTMLElement.prototype.scrollIntoView = vi.fn()
})

it.each([true, false])('separates goals from settings and decisions and omits hosted Help (on=%s)', async on => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  await act(async () => root.render(<WorkspacePanelNavigationProvider workspacePath="Workflow/example" view="pulse">
   <WorkspacePanelHeader workspacePath="Workflow/example" view="pulse" title="Autopilot" onBack={() => {}} />
   <PulseView workspacePath="Workflow/example" monitorOn={on} hasSoul monitorSaving={false} onToggleMonitor={() => {}}
    statusError={null} statusLoading={false} onRefresh={() => {}} headerAction={<AskAIButton workspacePath="Workflow/example" message="Explain Autopilot" iconOnly />} />
  </WorkspacePanelNavigationProvider>))
  for (const label of ['Goals', 'Metrics', 'Talk to Autopilot']) expect(host.querySelector(`section[aria-label="${label}"]`)).not.toBeNull()
  expect(host.querySelector('section[aria-label="Autopilot activity"]')).toBeNull()
  expect(host.querySelector('section[aria-label="Decisions"]')).toBeNull()
  expect(host.querySelector('section[aria-label="Autopilot settings"]')).toBeNull()
  expect(host.querySelectorAll('[aria-label="Help with this page"]')).toHaveLength(0)
  expect(host.querySelector('[aria-label^="Walkthrough:"]')).toBeNull()
  const chat = [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Autopilot chat')!
  expect(chat.disabled).toBe(!on)
  await act(async () => navigation.apply?.('settings'))
  expect(host.querySelector('section[aria-label="Goals"]')).toBeNull()
  expect(host.querySelectorAll('section[aria-label="Autopilot settings"]')).toHaveLength(1)
  expect(host.querySelector('[aria-label="Autopilot on/off"]')?.getAttribute('aria-checked')).toBe(String(on))
  expect(host.querySelectorAll('[aria-label="Help with this page"]')).toHaveLength(0)
  for (const [target, label] of [['goals', 'Goals'], ['metrics', 'Metrics']]) {
   await act(async () => navigation.apply?.(target))
   expect(document.activeElement).toBe(host.querySelector(`section[aria-label="${label}"]`))
   expect(host.querySelector('section[aria-label="Autopilot settings"]')).toBeNull()
  }
  await act(async () => navigation.apply?.('chat'))
  expect(openWorkflowPulseChatTab).toHaveBeenCalledWith('Workflow/example')
 } finally { await act(async () => root.unmount()); host.remove() }
})

it.each([true, false])('restores saved metrics/history and routes goal and metric questions to Autopilot (on=%s)', async on => {
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
  expect(host.textContent).not.toContain('Invoices are collecting.')
  for (const label of ['Ask Autopilot about this goal', 'Ask Autopilot about this metric']) {
   const button = [...host.querySelectorAll('button')].find(button => button.textContent === label)!
   expect(button.disabled).toBe(!on)
   await act(async () => button.click())
  }
  if (on) {
   expect(askWorkflowPulse).toHaveBeenCalledTimes(2)
   expect(vi.mocked(askWorkflowPulse).mock.calls[0]).toEqual(['Workflow/example', expect.stringContaining('Grow revenue')])
   expect(vi.mocked(askWorkflowPulse).mock.calls[1]).toEqual(['Workflow/example', expect.stringContaining('metric revenue')])
  } else expect(askWorkflowPulse).not.toHaveBeenCalled()
 } finally { await act(async () => root.unmount()); host.remove() }
})
