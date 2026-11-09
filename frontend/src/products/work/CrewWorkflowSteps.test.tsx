// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
import { crewScriptGraphApi } from '../../api/crewScriptGraph'
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: { get: vi.fn() } }))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Codex1730 P2: the single-agent display of a decorated step must retain its
// failed receipt and checks, even with another recorded step of the same name.
it('shows the decorated step failure and checks after collapsing its agent row', async () => {
  vi.mocked(crewScriptGraphApi.get).mockResolvedValue({ nodes: [{ id: 'review', type: 'agent', label: 'Review', line: 6, step_call: 'main.py:review@main.py:6' }], edges: [], errors: [] })
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewWorkflowSteps workspacePath="Workflow/example" title="Example" onCode={() => {}} onAsk={() => {}} latestSteps={[
      { execution_id: 'failed', id: 'failed', name: 'Review', source_call: 'main.py:review@main.py:6', status: 'failed', started_at: '2026-01-01T00:00:00Z', detail: 'Receipt failure evidence' },
      { execution_id: 'other', id: 'other', name: 'Review', source_call: 'main.py:review@main.py:12', status: 'completed', started_at: '2026-01-01T00:00:00Z', detail: 'Other receipt' },
    ]} latestEvidence={[{ id: 'check-failed', at: '2026-01-01T00:00:00Z', kind: 'check', step_execution_id: 'failed', ok: false, detail: 'Failed check' }]} />))
    expect(host.textContent).toContain('Receipt failure evidence')
    expect(host.textContent).toContain('1 of 1 check failed')
    expect(host.textContent).not.toContain('Other receipt')
  } finally { await act(async () => root.unmount()) }
})

// Codex1763 P2: a helper's same source call may appear under two branches.
// One branch's receipt is ambiguous and must not paint both outline rows.
it('keeps a shared helper receipt off duplicated outline rows', async () => {
  vi.mocked(crewScriptGraphApi.get).mockResolvedValue({ nodes: [
    { id: 'left', type: 'agent', label: 'Inner', line: 4, step_call: 'helpers.py:inner@helpers.py:4' },
    { id: 'right', type: 'agent', label: 'Inner', line: 4, step_call: 'helpers.py:inner@helpers.py:4' },
  ], edges: [], errors: [] })
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewWorkflowSteps workspacePath="Workflow/example" title="Example" onCode={() => {}} onAsk={() => {}} latestSteps={[
      { execution_id: 'only-run', id: 'only-run', name: 'Inner', source_call: 'helpers.py:inner@helpers.py:4', status: 'failed', started_at: '2026-01-01T00:00:00Z', detail: 'Ambiguous branch receipt' },
    ]} latestEvidence={[{ id: 'check', at: '2026-01-01T00:00:00Z', kind: 'check', step_execution_id: 'only-run', ok: false, detail: 'Failed' }]} />))
    expect(host.textContent).not.toContain('Ambiguous branch receipt')
    expect(host.textContent).not.toContain('check failed')
  } finally { await act(async () => root.unmount()) }
})
