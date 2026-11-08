// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import type { RunFolderInfo, WorkspaceStateResponse } from '../../services/api-types'

const api = vi.hoisted(() => ({ loadWorkspaceState: vi.fn(), post: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { post: api.post }, agentApi: api }))
// This editor passes syncWorkflowStore=false; isolate the unrelated store's API dependencies.
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({}) } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: async () => {} }))
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: { get: async () => ({ nodes: [], edges: [], errors: [] }) } }))
vi.mock('./CrewFileView', () => ({ CrewFileView: () => null }))
// Inspect the data passed to the run screen while keeping the editor and its real workspace cache.
vi.mock('./CrewWorkflowRuns', () => ({ CrewWorkflowRuns: ({ folders, loading }: { folders: RunFolderInfo[]; loading: boolean }) => <output>{loading ? 'Loading runs' : folders.map(folder => folder.name).join(',')}</output> }))
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => { api.loadWorkspaceState.mockReset(); api.post.mockReset() })

// PR1607 P2: a cache populated before a run existed must not make that run look unavailable.
it('refreshes cached workspace data and keeps the run screen loading until the fresh read finishes', async () => {
  const reply = (folders: RunFolderInfo[]): WorkspaceStateResponse => ({ success: true, data: { run_folders: folders, phases: [] } })
  api.loadWorkspaceState.mockResolvedValueOnce(reply([]))
  let resolve!: (value: WorkspaceStateResponse) => void
  api.loadWorkspaceState.mockImplementationOnce(() => new Promise<WorkspaceStateResponse>(done => { resolve = done }))
  const item: CrewExecutable = { id: 'report', kind: 'script', workspace_path: 'Crew/example/automation/cache-check', manifest: { label: 'Report' } }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const props = { item, chatTabId: 'chat', file: null, onFile: vi.fn(), onRuns: vi.fn(), onStarted: vi.fn() }
  try {
    await act(async () => root.render(<CrewExecutableEditor key="report" {...props} section="steps" runs={{ run: null, costs: false, detail: null }} />))
    expect(api.loadWorkspaceState).toHaveBeenCalledTimes(1)
    await act(async () => root.render(<CrewExecutableEditor key="report:run-new" {...props} section="runs" runs={{ run: 'run-new', costs: false, detail: null }} />))
    expect(api.loadWorkspaceState).toHaveBeenCalledTimes(2)
    expect(host.querySelector('output')?.textContent).toBe('Loading runs')
    await act(async () => resolve(reply([{ name: 'run-new', metadata: { created_at: '', status: 'completed', execution_id: 'execution-new' } }])))
    expect(host.querySelector('output')?.textContent).toBe('run-new')
  } finally { await act(async () => root.unmount()); host.remove() }
})

// PR1567 P2: Run must expose the missing-input refusal and send the selected saved preset, never invent a value.
it.each([{ shape: 'plain', saved: false }, { shape: 'structured', saved: true }])('keeps the $shape refusal and offers saved settings only when present', async ({ shape, saved }) => {
  const message = 'missing required input "REPORT_DATE"; supply it or set a workflow default'
  api.loadWorkspaceState.mockResolvedValue({ success: true, data: { run_folders: [], phases: [] } })
  api.post.mockRejectedValueOnce({ isAxiosError: true, response: { data: shape === 'plain' ? message : { error: { message } } } }).mockResolvedValueOnce({ data: { session_id: 'accepted' } })
  const item: CrewExecutable = { id: 'report', kind: 'script', workspace_path: `Crew/example/automation/required-${shape}`, manifest: { label: 'Report' }, inputs: [{ name: 'REPORT_DATE', type: 'string', required: true, value: '' }], input_presets: saved ? [{ name: 'Weekly', values: { REPORT_DATE: '2026-11-09' } }] : [] }
  const onStarted = vi.fn()
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  try {
    await act(async () => root.render(<CrewExecutableEditor item={item} chatTabId="chat" section="steps" file={null} onFile={vi.fn()} runs={{ run: null, costs: false, detail: null }} onRuns={vi.fn()} onStarted={onStarted} />))
    const run = [...host.querySelectorAll('button')].find(button => button.textContent === 'Run')!
    await act(async () => run.click())
    expect(api.post).toHaveBeenNthCalledWith(1, '/api/workflow/run', {}, { params: { workspace_path: item.workspace_path } })
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(message)
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('chat or the CLI with inputs')
    expect(onStarted).not.toHaveBeenCalled()
    const presets = host.querySelector<HTMLSelectElement>('[aria-label="Run with saved settings"]')
    if (!saved) {
      expect(presets).toBeNull()
      expect(host.querySelector('[role="alert"]')?.textContent).toContain('ask your agent to save a preset')
      return
    }
    expect(presets).not.toBeNull()
    await act(async () => { presets!.value = 'Weekly'; presets!.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(api.post).toHaveBeenNthCalledWith(2, '/api/workflow/run', { input_preset: 'Weekly' }, { params: { workspace_path: item.workspace_path } })
    expect(onStarted).toHaveBeenCalledOnce()
    expect(host.querySelector('[role="alert"]')).toBeNull()
  } finally { await act(async () => root.unmount()); host.remove() }
})
