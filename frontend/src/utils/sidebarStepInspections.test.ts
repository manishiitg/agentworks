import { expect, it } from 'vitest'
import type { ExecutionInspectionTarget, PollingEvent, SessionExecutionTreeResponse } from '../services/api-types'
import { sidebarStepInspections } from './sidebarStepInspections'

const target: ExecutionInspectionTarget = { session_id: 'parent', execution_id: 'step', workspace_path: 'Workflow/demo', name: 'Research', step_id: 'research', started_at: '2026-10-18T00:00:00Z' }
const tree = (inspection: unknown = target) => ({ session_id: 'parent', root: { session_id: 'parent', execution_id: 'session:parent', children: [{ session_id: 'parent', execution_id: 'step', status: 'running', inspection }] } }) as SessionExecutionTreeResponse

it('uses exact server receipts and merges a saved inspection with its live status', () => {
  expect(sidebarStepInspections('parent', 'Workflow/demo', tree(), [target])).toEqual([{ target, status: 'running' }])
  expect(sidebarStepInspections('parent', 'Workflow/demo', tree(null))).toEqual([])
})
it('rejects foreign parents, workspaces, non-Goal receipts and wrong root identities', () => {
  for (const patch of [{ session_id: 'other' }, { workspace_path: 'Workflow/other' }, { workflow_kind: 'relay' }, { step_id: undefined }]) {
    expect(sidebarStepInspections('parent', 'Workflow/demo', tree({ ...target, ...patch }))).toEqual([])
  }
  expect(sidebarStepInspections('parent', 'Workflow/demo', { ...tree(), session_id: 'other' })).toEqual([])
  expect(sidebarStepInspections('parent', 'Workflow/demo', { ...tree(), root: { ...tree().root, session_id: 'other' } })).toEqual([])
})
it('never guesses a receipt from a step-shaped name or execution ID', () => {
  const data = tree(null)
  Object.assign(data.root.children![0], { kind: 'workflow_step', name: 'Research', metadata: { step_id: 'research' } })
  expect(sidebarStepInspections('parent', 'Workflow/demo', data)).toEqual([])
})
it('keeps distinct retries and visits cyclic trees only once', () => {
  const data = tree()
  data.root.children!.push(data.root)
  const earlier = { ...target, execution_id: 'retry-1', started_at: '2026-10-17T00:00:00Z' }
  expect(sidebarStepInspections('parent', 'Workflow/demo', data, [earlier]).map(item => item.target.execution_id)).toEqual(['step', 'retry-1'])
})
it('retains explicit completed chat receipts when the live registry has gone away', () => {
  const event = (type: string, receipt = target) => ({type,session_id:'parent',data:{data:{agent_id:receipt.execution_id,metadata:{inspection:receipt}}}}) as PollingEvent
  expect(sidebarStepInspections('parent','Workflow/demo',undefined,[],[event('background_agent_started'),event('background_agent_completed')])).toEqual([{target,status:'completed'}])
  expect(sidebarStepInspections('parent','Workflow/demo',undefined,[],[event('background_agent_completed',{...target,session_id:'other'})])).toEqual([])
})
it('keeps internal tasks inside their parent step inspector', () => {
  const data = tree()
  data.root.children![0].children = [{...data.root.children![0],execution_id:'task',inspection:{...target,execution_id:'task',name:'Internal task'}}]
  expect(sidebarStepInspections('parent','Workflow/demo',data).map(item=>item.target.name)).toEqual(['Research'])
})
