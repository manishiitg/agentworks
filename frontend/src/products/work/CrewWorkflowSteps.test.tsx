// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
const graph = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: graph }))
beforeEach(() => graph.get.mockReset().mockResolvedValue({ nodes: [], edges: [], errors: ['This code is too large to outline.'] }))
import { CrewWorkflowSteps } from './CrewWorkflowSteps'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Codex finding on PR 1567: a workflow with no outline and no recorded steps (an unrun script over the inspector limit) lost its Run button.
it.each(['absent', 'type notes', 'checker unavailable'])('keeps Run without an outline while advisory status is %s', async advisory => {
  if (advisory === 'type notes') graph.get.mockResolvedValue({ nodes: [], edges: [], errors: [],
    notes: ['Type hints show 1 possible problem. The script can still run.', 'Some calls have no visible saved-result key.'],
    type_notes: [{ file: 'helpers/report.py', line: 12, message: 'A number is expected here.' }],
    unkeyed_calls: [{ file: 'main.py', line: 3, helper: 'agent' }],
  })
  if (advisory === 'checker unavailable') graph.get.mockResolvedValue({ nodes: [], edges: [], errors: [], notes: ['The checker is not installed here. The script can still run.'], type_notes: [] })
  const onRun = vi.fn()
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewWorkflowSteps workspacePath="Crew/example/code/report" title="Report" latestSteps={[]} onRun={onRun} onCode={vi.fn()} onAsk={vi.fn()} />))
    const run = [...host.querySelectorAll('button')].find(button => button.textContent === 'Run')
    expect(run).toBeTruthy()
    await act(async () => run!.click())
    expect(onRun).toHaveBeenCalledOnce()
    const details = host.querySelector('details')
    if (advisory === 'type notes') {
      expect(details).not.toBeNull()
      expect(details?.open).toBe(false)
      details!.open = true
      expect(details?.textContent).toContain('helpers/report.py:12')
      expect(details?.textContent).toContain('A number is expected here.')
      expect(host.textContent?.match(/saved-result key/g)).toHaveLength(1)
      expect(host.textContent).not.toContain('Type hints show')
    } else {
      expect(details).toBeNull()
      if (advisory === 'checker unavailable') expect(host.textContent).toContain('The checker is not installed here.')
    }
    expect(host.querySelector('[role="alert"]')).toBeNull()
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
