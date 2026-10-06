// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const fixture = vi.hoisted(() => ({
  source: 'async def run(INPUT, ctx):\n    # Keep literal escapes and blank lines.\n\n    return {"text": "a\\nb"}\n',
  openWorkspaceView: vi.fn(),
}))
vi.mock('../../services/api', () => ({ agentApi: {
  getRunFolders: async (path: string) => ({ folders: [{ name: path.includes('RelayReleases/') ? 'relay-published' : 'relay-test', metadata: { status: 'completed' } }] }),
  getPlannerFileContent: async (path: string) => ({ success: true, data: { content: path.endsWith('relay.py') ? fixture.source : path.endsWith('relay_result.json') ? '{"answer":42}' : JSON.stringify({ version: 1, status: 'completed', calls: [{ id: 'first', name: path.includes('RelayReleases/') ? 'Published invoice' : 'Extract invoice', status: 'completed', model: 'test-model', started_at: 1791300000, output: { total: 42 }, tools: [{ name: 'lookup_customer', args: { id: 'C123' }, result: { found: true } }] }] }) } }),
} }))
vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { relayReleases: async () => ({ active_version: 'v1', releases: [{ version: 'v1', workspace_path: 'RelayReleases/id/v1' }] }) } }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: Object.assign((selector: (state: { selectedRunFolder: null; workspaceViewRefreshToken: number }) => unknown) => selector({ selectedRunFolder: null, workspaceViewRefreshToken: 0 }), { getState: () => ({ openWorkspaceView: fixture.openWorkspaceView, setSelectedRunFolder: vi.fn() }) }) }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('./canvas/VariablesSidebar', () => ({ VariablesSidebar: () => null }))

import RelayPythonView from './RelayPythonView'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div')
const root = createRoot(host)
afterEach(async () => { await act(async () => root.render(null)); vi.clearAllMocks() })

it('shows exact Python source, opens the shared editor, and displays recorded calls and returned JSON', async () => {
  await act(async () => root.render(<RelayPythonView workspacePath="Workflow/python-relay" relayID="id" onBuild={() => {}} />))
  expect(host.querySelector('[aria-label="Relay Python source"]')?.textContent).toBe(fixture.source)
  const openFiles = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.includes('Open in Files'))!
  await act(async () => openFiles.click())
  expect(fixture.openWorkspaceView).toHaveBeenCalledWith('files', 'relay.py')
  const calls = Array.from(host.querySelectorAll('[role="tab"]')).find(button => button.textContent === 'Calls') as HTMLButtonElement
  await act(async () => calls.click())
  expect(host.textContent).toContain('Published invoice')
  expect(host.textContent).toContain('test-model')
  expect(host.textContent).toContain('lookup_customer')
  expect(host.textContent).toContain('Returned JSON')
  expect(host.textContent).toContain('"answer": 42')
  expect((host.querySelector('#relay-python-run') as HTMLSelectElement)?.value).toBe('relay-published')
  const version = host.querySelector('#relay-python-version') as HTMLSelectElement
  await act(async () => { version.value = 'draft'; version.dispatchEvent(new Event('change', { bubbles: true })) })
  expect((host.querySelector('#relay-python-run') as HTMLSelectElement)?.value).toBe('relay-test')
  expect(host.textContent).toContain('Extract invoice')
  expect(host.textContent).not.toContain('Published invoice')
})
