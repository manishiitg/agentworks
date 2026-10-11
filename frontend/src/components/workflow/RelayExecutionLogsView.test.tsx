// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const fixture = vi.hoisted(() => ({ paths: [] as string[] }))
vi.mock('../../api/workflowWebhooks', () => ({ workflowWebhooksApi: { relayReleases: async () => ({ active_version: 'v2', releases: [{ version: 'v2', workspace_path: 'Published/v2' }, { version: 'v1', workspace_path: 'Published/v1' }] }) } }))
vi.mock('../../services/api', () => ({ agentApi: {
  getRunFolders: async () => ({ folders: [{ name: 'iteration-1-hook' }] }),
  getPlannerFileContent: async (path: string) => {
    fixture.paths.push(path)
    if (path.startsWith('Published/v1')) throw { response: { status: 404 } }
    return { success: true, data: { content: path.endsWith('.jsonl') ? '' : JSON.stringify({ version: 1, durability: 'dbos', execution_model: 'native-dbos', status: 'completed', workflow_id: 'live-order', calls: [] }) } }
  },
} }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('./ExecutionLogsPopup', () => ({ default: () => <p>Existing agent file viewer</p> }))
import RelayExecutionLogsView from './RelayExecutionLogsView'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div'), root = createRoot(host)
afterEach(async () => { await act(async () => root.render(null)); fixture.paths.length = 0 })
it('opens native DBOS history from the active published version and preserves legacy agent files', async () => {
  await act(async () => root.render(<RelayExecutionLogsView relayID="id" draftWorkspacePath="Workflow/draft" draftRunFolders={[]} draftRunFolderInfos={[]} draftSelectedRunFolder={null} onRefreshDraftRuns={() => {}} />))
  expect(host.textContent).toContain('live-order')
  expect(fixture.paths).toContain('Published/v2/runs/iteration-1-hook/relay_trace.json')
  const select = host.querySelector('#relay-log-version') as HTMLSelectElement
  await act(async () => { select.value = 'v1'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  const steps = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'DBOS steps')!
  const files = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Agent files')!
  expect(steps.disabled).toBe(true)
  expect(files.getAttribute('aria-pressed')).toBe('true')
})
