// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const fixture = vi.hoisted(() => ({ missing: false, error: '', paths: [] as string[] }))
vi.mock('../../services/api', () => ({ agentApi: { getPlannerFileContent: async (path: string) => {
  fixture.paths.push(path)
  if (fixture.missing) throw { response: { status: 404 } }
  return { success: true, data: { content: path.endsWith('dbos_events.jsonl') ? JSON.stringify({ timestamp: 10, level: fixture.error ? 'ERROR' : 'INFO', message: fixture.error || 'Reviewing order', workflow_id: 'native-run', step_id: 7, attempt_number: 2 }) + '\n{partial' : JSON.stringify({ version: 1, durability: 'dbos', execution_model: 'native-dbos', workflow_id: 'native-run', status: fixture.error ? 'failed' : 'completed', error: fixture.error || undefined, attempt_number: 2, calls: [{ id: 'step-7', name: 'verify_order', dbos_step_id: 7, checkpoint_reused: true, status: 'completed', started_at: 10, completed_at: 12, output: { accepted: true }, provider: 'fixture', agent_calls: [{ name: 'lookup', output: { accepted: true } }], tools: [{ name: 'lookup_order', result: { found: true } }] }] }) } }
} } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
import DBOSExecutionTimeline from './DBOSExecutionTimeline'
import { graphFromDBOSHistory } from './relayDBOSGraph'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div'), root = createRoot(host)
afterEach(async () => { await act(async () => root.render(null)); fixture.missing = false; fixture.error = ''; fixture.paths.length = 0 })
it('shows reconciliation clearly after a crash and keeps full traceback details collapsed', async () => {
  fixture.error = 'relay.py failed: Traceback\nRuntimeError: Uncertain platform call requires reconciliation; automatic retry is disabled'
  await act(async () => root.render(<DBOSExecutionTimeline workspacePath="Releases/v4" runFolder="iteration-1-hook" />))
  expect(host.textContent).toContain('Needs reconciliation · Attempt 2')
  expect(host.querySelector('[role="alert"]')?.textContent).toBe('Uncertain platform call requires reconciliation; automatic retry is disabled')
  expect(host.textContent).toContain('Check those actions before retrying this run.')
  const details = [...host.querySelectorAll('details')].filter(node => node.querySelector('summary')?.textContent === 'Full error details')
  expect(details).toHaveLength(2)
  for (const node of details) {
    expect(node.hasAttribute('open')).toBe(false)
    expect(node.textContent).toContain(fixture.error)
  }
})
it('shows native history, recovery and linked agent/tool receipts with correlated log events', async () => {
  const available = vi.fn()
  await act(async () => root.render(<DBOSExecutionTimeline workspacePath="Releases/v2" runFolder="iteration-2-hook" onAvailable={available} />))
  expect(available).toHaveBeenCalledWith(true)
  for (const text of ['native-run', 'Attempt 2', '1 checkpoints reused', 'verify_order', 'DBOS step 7 · 2.00s', 'Reused checkpoint', 'Agent: lookup', 'Tool: lookup_order', 'Reviewing order', 'DBOS logs · 1 recent events']) expect(host.textContent).toContain(text)
  expect(fixture.paths).toContain('Releases/v2/runs/iteration-2-hook/dbos_events.jsonl')
})
it('allows the existing agent file view when a legacy run has no durable trace', async () => {
  fixture.missing = true
  const available = vi.fn()
  await act(async () => root.render(<DBOSExecutionTimeline workspacePath="Workflow/legacy" runFolder="run-1" onAvailable={available} />))
  expect(available).toHaveBeenCalledWith(false)
})
it('keeps repeated DBOS function calls separate in the observed run graph', () => {
  const graph = graphFromDBOSHistory([{ id: 'step-1', name: 'lookup', status: 'completed' }, { id: 'step-3', name: 'lookup', status: 'completed' }])
  expect(graph.native).toBe(true)
  expect(graph.nodes.map(node => node.call).filter(Boolean)).toEqual(['step-1', 'step-3'])
  expect(graph.edges).toHaveLength(3)
})
