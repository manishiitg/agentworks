// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const fixture = vi.hoisted(() => ({
  source: 'async def run(INPUT, ctx):\n    # Keep literal escapes and blank lines.\n\n    return {"text": "a\\nb"}\n',
  paths: [] as string[],
  durable: false,
  native: false,
  annotations: '# @relay node {"id":"extract","type":"agent","label":"Invoice assistant","system_prompt":"Extract fields","tools":["lookup_customer"]}\n',
  openWorkspaceView: vi.fn(),
}))
vi.mock('../../services/api', () => ({ agentApi: {
  getRunFolders: async (path: string) => ({ folders: [{ name: path.includes('RelayReleases/') ? 'relay-published' : 'relay-test', metadata: { status: 'completed' } }] }),
  getPlannerFileContent: async (path: string) => {
    fixture.paths.push(path)
    return ({ success: true, data: { content: path.endsWith('relay.py') ? fixture.native ? 'from dbos import DBOS\n@DBOS.workflow()\nasync def run(\n    INPUT,\n):\n    return await verify_order(INPUT)\n' : (path.includes('RelayReleases/') ? fixture.annotations.replace('"label":"Invoice assistant"', '"label":"Published graph","call":"published-extract"') : fixture.annotations) + fixture.source : path.endsWith('relay_result.json') ? '{"answer":42}' : JSON.stringify({ version: 1, status: 'completed', ...(fixture.native ? { durability: 'dbos', execution_model: 'native-dbos', workflow_id: 'native-run' } : {}), ...(fixture.durable ? { durability: 'dbos', attempt_number: 2 } : {}), calls: [{ id: 'call-1', checkpoint_reused: fixture.durable, name: path.includes('RelayReleases/') ? 'published-extract' : 'extract', status: 'completed', model: 'test-model', started_at: 1791300000, output: { total: 42 }, tools: [{ name: 'lookup_customer', args: { id: 'C123' }, result: { found: true } }] }] }) } })
  },
} }))
vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { relayGraph: async () => fixture.native ? ({ native: true, nodes: [{ id: 'verify-6', call: 'verify_order', type: 'agent', label: 'Check order', line: 6 }], edges: [], errors: [] }) : ({ native: false, nodes: [], edges: [], errors: [] }), relayReleases: async () => ({ active_version: 'v1', releases: [{ version: 'v1', workspace_path: 'RelayReleases/id/v1' }] }) } }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: Object.assign((selector: (state: { selectedRunFolder: null; workspaceViewRefreshToken: number }) => unknown) => selector({ selectedRunFolder: null, workspaceViewRefreshToken: 0 }), { getState: () => ({ openWorkspaceView: fixture.openWorkspaceView, setSelectedRunFolder: vi.fn() }) }) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
// DOM wiring uses the real source parser, layout, node cards and detail panel.
// React Flow's browser measurements are covered by the in-app browser check.
vi.mock('@xyflow/react', () => ({
  ReactFlow: ({ nodes, nodeTypes, onNodeClick }: { nodes: { id: string; type: string; data: unknown }[]; nodeTypes: Record<string, React.ComponentType<Record<string, unknown>>>; onNodeClick: (event: unknown, node: unknown) => void }) => <div>{nodes.map(node => { const Card = nodeTypes[node.type]; return <button key={node.id} aria-label={`Select ${node.id}`} onClick={event => onNodeClick(event, node)}><Card {...node} /></button> })}</div>,
  Handle: () => null, Background: () => null, Controls: () => null,
  Position: { Top: 'top', Bottom: 'bottom' }, MarkerType: { ArrowClosed: 'arrowclosed' },
}))
vi.mock('./canvas/VariablesSidebar', () => ({ VariablesSidebar: () => null }))

import RelayPythonView from './RelayPythonView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div')
const root = createRoot(host)
afterEach(async () => { await act(async () => root.render(null)); vi.clearAllMocks(); fixture.paths.length = 0; fixture.durable = false; fixture.native = false })

it('shows recovered checkpoints and the actual process attempt in Runs', async () => {
  fixture.durable = true
  await act(async () => root.render(<RelayPythonView workspacePath="Workflow/python-relay" relayID="id" onBuild={() => {}} />))
  const runs = Array.from(host.querySelectorAll('[role="tab"]')).find(button => button.textContent === 'Runs') as HTMLButtonElement
  await act(async () => runs.click())
  expect(host.textContent).toContain('DBOS recovery enabled · Attempt 2 · 1 checkpoints reused')
  expect(host.textContent).toContain('Reused checkpoint')
})

it('builds the graph from source, keeps code optional, and matches recorded calls to the frozen published graph', async () => {
  await act(async () => root.render(<RelayPythonView workspacePath="Workflow/python-relay" relayID="id" onBuild={() => {}} />))
  expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Graph')
  expect(host.textContent).toContain('Invoice assistant')
  expect(host.querySelector('[aria-label="Relay Python source"]')).toBeNull()
  const code = Array.from(host.querySelectorAll('[role="tab"]')).find(button => button.textContent === 'Code') as HTMLButtonElement
  await act(async () => code.click())
  expect(host.querySelector('[aria-label="Relay Python source"]')?.textContent).toBe(fixture.annotations + fixture.source)
  const openFiles = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.includes('Open in Files'))!
  await act(async () => openFiles.click())
  expect(fixture.openWorkspaceView).toHaveBeenCalledWith('files', 'relay.py')
  const calls = Array.from(host.querySelectorAll('[role="tab"]')).find(button => button.textContent === 'Runs') as HTMLButtonElement
  await act(async () => calls.click())
  expect(host.textContent).toContain('published-extract')
  expect(host.textContent).toContain('test-model')
  expect(host.textContent).toContain('lookup_customer')
  expect(host.textContent).toContain('Final result')
  expect(host.textContent).toContain('"answer": 42')
  expect((host.querySelector('#relay-python-run') as HTMLSelectElement)?.value).toBe('relay-published')
  const runGraph = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'View run graph')!
  await act(async () => runGraph.click())
  expect(host.textContent).toContain('Published graph')
  const node = host.querySelector('[aria-label="Select extract"]') as HTMLButtonElement
  await act(async () => node.click())
  expect(host.querySelector('[aria-label="Relay node details"]')?.textContent).toContain('call-1')
  expect(host.querySelector('[aria-label="Relay node details"]')?.textContent).toContain('lookup_customer')
  expect(fixture.paths.some(path => path.endsWith('relay.md'))).toBe(false)
  const version = host.querySelector('#relay-python-version') as HTMLSelectElement
  await act(async () => { version.value = 'draft'; version.dispatchEvent(new Event('change', { bubbles: true })) })
  expect((host.querySelector('#relay-python-run') as HTMLSelectElement)?.value).toBe('relay-test')
  expect(host.textContent).toContain('extract')
  expect(host.textContent).not.toContain('published-extract')
})

it('offers chat guidance when existing Python has no graph comments', async () => {
  const saved = fixture.annotations
  fixture.annotations = ''
  const onBuild = vi.fn()
  try {
    await act(async () => root.render(<RelayPythonView workspacePath="Workflow/existing-relay" onBuild={onBuild} />))
    expect(host.textContent).toContain('add graph comments')
    expect(host.querySelector('[aria-label="Relay Python source"]')).toBeNull()
    const build = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Add graph in chat')!
    await act(async () => build.click())
    expect(onBuild).toHaveBeenCalledOnce()
  } finally {
    fixture.annotations = saved
  }
})

it('derives the native overview from AST inspection without requiring Relay annotations', async () => {
  fixture.native = true
  await act(async () => root.render(<RelayPythonView workspacePath="Workflow/native" relayID="id" onBuild={() => {}} />))
  expect(host.textContent).toContain('Python source overview')
  expect(host.textContent).toContain('Check order')
  expect(host.textContent).not.toContain('Add graph in chat')
  const code = Array.from(host.querySelectorAll('[role="tab"]')).find(button => button.textContent === 'Code') as HTMLButtonElement
  await act(async () => code.click())
  expect(host.querySelector('[aria-label="Relay Python source"]')?.textContent).toContain('@DBOS.workflow()')
  expect(host.querySelector('[aria-label="Relay Python source"]')?.textContent).not.toContain('@relay')
})
