// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { RunFolderInfo, WorkspaceStateResponse } from '../../services/api-types'

const api = vi.hoisted(() => ({ loadWorkspaceState: vi.fn() }))
vi.mock('../../services/api', () => ({ default: {}, agentApi: api }))
// This editor passes syncWorkflowStore=false; isolate the unrelated store's API dependencies.
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: { getState: () => ({}) } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: () => {} }))
vi.mock('../../utils/workspacePaneChat', () => ({ openWorkspacePaneChatWithDraft: async () => {} }))
vi.mock('./CrewWorkflowSteps', () => ({ CrewWorkflowSteps: () => <div>Steps</div> }))
vi.mock('./CrewFileView', () => ({ CrewFileView: () => null }))
// Inspect the data passed to the run screen while keeping the editor and its real workspace cache.
vi.mock('./CrewWorkflowRuns', () => ({ CrewWorkflowRuns: ({ folders, loading }: { folders: RunFolderInfo[]; loading: boolean }) => <output>{loading ? 'Loading runs' : folders.map(folder => folder.name).join(',')}</output> }))
import { CrewExecutableEditor, type CrewExecutable } from './CrewExecutableEditor'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// PR1607 P2: a cache populated before a run existed must not make that run look unavailable.
it('refreshes cached workspace data and keeps the run screen loading until the fresh read finishes', async () => {
  const reply = (folders: RunFolderInfo[]): WorkspaceStateResponse => ({ success: true, data: { run_folders: folders, phases: [] } })
  api.loadWorkspaceState.mockResolvedValueOnce(reply([]))
  let resolve!: (value: WorkspaceStateResponse) => void
  api.loadWorkspaceState.mockImplementationOnce(() => new Promise<WorkspaceStateResponse>(done => { resolve = done }))
  const item: CrewExecutable = { id: 'report', kind: 'script', workspace_path: 'Crew/example/automation/cache-check', manifest: { label: 'Report' } }
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const props = { item, chatTabId: 'chat', file: null, onFile: vi.fn(), onRuns: vi.fn() }
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
