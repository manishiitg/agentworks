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
