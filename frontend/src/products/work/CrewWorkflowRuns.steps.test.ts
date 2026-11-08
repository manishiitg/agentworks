import { describe, expect, it } from 'vitest'
import type { RelaySourceGraph } from '../../components/workflow/relayGraphAnnotations'
import type { WorkspaceStepInspection } from '../../services/api-types'
import { runStepLines } from './CrewWorkflowRuns'

const record = (execution: string, sourceCall: string | undefined, name: string): WorkspaceStepInspection => ({
  inspection: { session_id: 's', execution_id: execution, workspace_path: 'Crew/x', name, source_call: sourceCall, started_at: '2026-11-08T05:00:00Z' },
  status: 'completed',
  parent: { session_id: 's', workspace_path: 'Crew/x', status: 'completed' },
})
const outline: RelaySourceGraph = {
  native: true, errors: [], edges: [],
  nodes: [
    { id: 'step-a', type: 'script', label: 'Collect evidence', call: 'a', line: 3 },
    { id: 'other-5', type: 'script', label: 'Other code', line: 5 },
    { id: 'step-b', type: 'script', label: 'Write report', call: 'b', line: 7 },
  ],
}

describe('runStepLines', () => {
  it('lists the outline in order with "not reached" steps, and keeps records the outline does not name', () => {
    const lines = runStepLines([record('2', 'unknown', 'Stray'), record('1', 'a', 'collect')], outline)
    expect(lines.map(line => [line.title, line.record?.inspection.execution_id])).toEqual([['Collect evidence', '1'], ['Write report', undefined], ['Stray', '2']])
  })
  it('keeps every repeat of a step', () => {
    const lines = runStepLines([record('1', 'a', 'collect'), record('2', 'a', 'collect')], outline)
    expect(lines.filter(line => line.title === 'Collect evidence')).toHaveLength(2)
  })
  it('lists the records as they are when the workflow has no outline', () => {
    expect(runStepLines([record('1', undefined, 'Research')], null).map(line => line.title)).toEqual(['Research'])
  })
})
