// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
vi.mock('./SoulViewer', () => ({ SoulViewer: () => <p>Grow useful customer conversations.</p>, WORKFLOW_SOUL_REFRESH_EVENT: 'test-soul' }))
vi.mock('../../utils/pulseChatTab', () => ({ openWorkflowPulseChatTab: vi.fn(async () => {}) }))
const navigation = vi.hoisted(() => ({ apply: undefined as undefined | ((target: string) => void) }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ useWorkspaceViewTarget: (_view: string, apply: (target: string) => void) => { navigation.apply = apply } }))
import PulseView from './PulseView'

it.each([true, false])('shows one Pulse screen, keeps simple settings and respects OFF (on=%s)', async on => {
 Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
 const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
 try {
  await act(async () => root.render(<PulseView workspacePath="Workflow/example" monitorOn={on} hasSoul monitorSaving={false} onToggleMonitor={() => {}}
   statusError={null} statusLoading={false} onRefresh={() => {}} />))
  for (const label of ['Pulse settings', 'Talk to Pulse']) expect(host.querySelector(`[aria-label="${label}"]`)).not.toBeNull()
  expect(host.querySelector('[role="tablist"]')).toBeNull()
  expect(host.textContent).not.toMatch(/Platform health|Maintenance issues|Review run history|Weekly/)
  expect(host.querySelector('[aria-label="Pulse on/off"]')?.getAttribute('aria-checked')).toBe(String(on))
  const chat = [...host.querySelectorAll('button')].find(button => button.textContent === 'Open Pulse chat')!
  expect(chat.disabled).toBe(!on)
  for (const [target, label] of [['settings', 'Pulse settings'], ['chat', 'Talk to Pulse']]) {
   const section = host.querySelector<HTMLElement>(`section[aria-label="${label}"]`)!
   section.scrollIntoView = vi.fn()
   await act(async () => navigation.apply?.(target))
   expect(section.scrollIntoView).toHaveBeenCalledWith({ block: 'start' })
   expect(document.activeElement).toBe(section)
   expect(chat.disabled).toBe(!on)
  }
 } finally { await act(async () => root.unmount()); host.remove() }
})
