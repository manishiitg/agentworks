// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: { get: async () => ({ nodes: [], edges: [], errors: ['This code is too large to outline.'] }) } }))
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Codex finding on PR 1567: a workflow with no outline and no recorded steps (an unrun script over the inspector limit) lost its Run button.
it('keeps Run on a workflow that has no outline and has not run yet', async () => {
  const onRun = vi.fn()
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewWorkflowSteps workspacePath="Crew/example/code/report" title="Report" latestSteps={[]} onRun={onRun} onCode={vi.fn()} onAsk={vi.fn()} />))
    const run = [...host.querySelectorAll('button')].find(button => button.textContent === 'Run')
    expect(run).toBeTruthy()
    await act(async () => run!.click())
    expect(onRun).toHaveBeenCalledOnce()
  } finally { await act(async () => root.unmount()); host.remove() }
})

// Real runs can give several steps the same helper call: those checks belong in the full run list, not every step.
it('uses actual step IDs across shared helper calls and leaves old source-only checks unassigned', async () => {
  const base = { started_at: '2026-11-08T10:00:00Z', status: 'completed' }
  const steps = [
    { ...base, execution_id: 'one', id: 'one', name: 'Gather material', source_call: 'main.py:gather@main.py:10' },
    { ...base, execution_id: 'two', id: 'two', name: 'Review', source_call: 'main.py:helper@main.py:20' },
    { ...base, execution_id: 'three', id: 'three', name: 'Save', source_call: 'main.py:helper@main.py:20' },
  ]
  const checks = [
    { id: 'a', kind: 'check' as const, at: '', name: 'Material exists', detail: '', ok: true, source_call: steps[0].source_call },
    { id: 'b', kind: 'check' as const, at: '', name: 'Reviewed', detail: '', ok: false, step_execution_id: 'two', source_call: steps[1].source_call },
    { id: 'c', kind: 'check' as const, at: '', name: 'Saved', detail: '', ok: true, step_execution_id: 'three', source_call: steps[2].source_call },
    { id: 'd', kind: 'check' as const, at: '', name: 'Whole run', detail: '', ok: true },
  ]
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewWorkflowSteps workspacePath="Crew/example/code/checks" title="Report" latestSteps={steps} latestEvidence={checks} onRun={vi.fn()} onCode={vi.fn()} onAsk={vi.fn()} />))
    expect(host.textContent?.match(/1 check passed/g)).toHaveLength(1)
    expect(host.textContent).not.toContain('2 checks')
    expect(host.textContent).toContain('1 of 1 check failed')
    const first = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('Gather material'))
    expect(first?.textContent).not.toContain('check passed')
  } finally { await act(async () => root.unmount()); host.remove() }
})
