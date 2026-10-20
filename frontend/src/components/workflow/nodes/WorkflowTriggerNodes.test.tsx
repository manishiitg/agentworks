// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { WorkflowTriggerNode, WorkflowTriggerHeading } from './WorkflowTriggerNodes'
import type { NodeProps } from '@xyflow/react'
vi.mock('@xyflow/react', () => ({ Handle: () => null, Position: { Bottom: 'bottom' } }))
vi.mock('../../../services/api', () => ({ getApiBaseUrl: () => 'https://agent.example', getAuthToken: () => null }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const props = (data: NodeProps['data']): NodeProps => ({ id: 'test', data, type: 'workflow-trigger', selected: false, dragging: false, draggable: false, selectable: false, deletable: false, zIndex: 0, isConnectable: false, positionAbsoluteX: 0, positionAbsoluteY: 0 })
it('shows a paused webhook, its real URL, path highlighting and settings controls', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  const onSelect = vi.fn(), onSettings = vi.fn()
  const data = { id: 'hook', title: 'Review PR', job: { id: 'hook', name: 'Review PR', schedule_type: 'webhook', enabled: false }, routeSummary: { label: 'Choose: Review', canTrace: true }, onSelect, onSettings }
  try {
    await act(async () => root.render(<WorkflowTriggerNode {...props(data)} />))
    expect(host.textContent).toContain('Paused')
    expect(host.textContent).toContain('https://agent.example/api/hooks/workflow/hook')
    expect(host.textContent).toContain('Webhook event')
    expect(host.textContent).toContain('Starts')
    await act(async () => [...host.querySelectorAll('button')].find(b => b.textContent === 'View path')!.click())
    expect(onSelect).toHaveBeenCalledOnce()
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Open settings for Review PR"]')!.click())
    expect(onSettings).toHaveBeenCalledWith('webhooks')
  } finally { await act(async () => root.unmount()) }
})
it('explains manual execution when there are no automatic triggers', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkflowTriggerHeading {...props({ id: 'heading', title: 'Triggers', count: 0 })} />))
    expect(host.textContent).toContain('started manually')
    expect(host.textContent).toContain('Schedules')
    expect(host.textContent).toContain('Webhooks')
  } finally { await act(async () => root.unmount()) }
})
