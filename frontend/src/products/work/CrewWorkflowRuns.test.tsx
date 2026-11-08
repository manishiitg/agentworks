// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import type { RunFolderInfo } from '../../services/api-types'

const state = vi.hoisted(() => ({ canEdit: true, access: 'owner' as string | undefined }))
const http = vi.hoisted(() => ({ post: vi.fn(), inspections: vi.fn() }))
vi.mock('../../services/api', () => ({ default: http, agentApi: {
  getRunCostTotals: async () => ({ runs: [] }),
  getWorkspaceStepInspections: http.inspections,
} }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: (value: unknown) => unknown) => select({ user: { can_write_workflows: state.canEdit } }) }))
vi.mock('../../stores/useWorkflowManifestStore', () => ({ useWorkflowManifestStore: (select: (value: unknown) => unknown) => select({ workflows: [{ manifest: { id: 'report' }, workspace_path: 'Workflow/report', my_access: state.access }] }) }))
vi.mock('../../api/runEvidence', () => ({ runEvidenceApi: { get: async () => [] } }))
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: { get: async () => ({ nodes: [], edges: [], errors: [] }) } }))
vi.mock('../../components/workflow/costs/useCostsData', () => ({ useCostsData: () => ({ runCosts: [] }) }))
import { CrewWorkflowRuns } from './CrewWorkflowRuns'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => { state.canEdit = true; state.access = 'owner'; http.post.mockReset(); http.inspections.mockReset().mockResolvedValue({ items: [] }) })

async function screen(options: { status?: string; script?: boolean; refresh?: () => Promise<void> } = {}) {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const onView = vi.fn(); const refresh = options.refresh ?? vi.fn(async () => {})
  const folder: RunFolderInfo = { name: 'run-failed', metadata: { created_at: '2026-11-09T00:00:00Z', execution_id: 'attempt-1', status: options.status ?? 'failed' } }
  const update = async (status = folder.metadata!.status, execution = folder.metadata!.execution_id) => {
    folder.metadata = { ...folder.metadata!, status, execution_id: execution }
    await act(async () => root.render(<CrewWorkflowRuns workflowId="report" workspacePath="Workflow/report" script={options.script ?? true} folders={[{ ...folder }]} loading={false} onRefresh={refresh} onAsk={vi.fn()} view={{ run: folder.name, costs: false, detail: null }} onView={onView} />))
  }
  await update()
  return { host, onView, refresh, update, resume: () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Resume'), close: async () => { await act(async () => root.unmount()); host.remove() } }
}

// PLAT-746: Resume requires a failed Python run AND the server's account/per-workflow editing projections.
it.each([
  { access: 'owner', canEdit: true, status: 'failed', script: true, visible: true },
  { access: 'write', canEdit: true, status: 'failed', script: true, visible: true },
  { access: 'read', canEdit: true, status: 'failed', script: true, visible: false },
  { access: undefined, canEdit: true, status: 'failed', script: true, visible: false },
  { access: 'owner', canEdit: false, status: 'failed', script: true, visible: false },
  { access: 'owner', canEdit: true, status: 'completed', script: true, visible: false },
  { access: 'owner', canEdit: true, status: 'running', script: true, visible: false },
  { access: 'owner', canEdit: true, status: 'failed', script: false, visible: false },
])('Resume visibility for $access/$canEdit/$status/Python=$script', async row => {
  state.access = row.access; state.canEdit = row.canEdit
  const page = await screen(row)
  try { expect(Boolean(page.resume())).toBe(row.visible); expect(http.post).not.toHaveBeenCalled() } finally { await page.close() }
})

it.each([false, true])('resumes the same selected folder without overrides, even if refresh fails=%s', async refreshFails => {
  let accept!: (value: unknown) => void
  http.post.mockImplementationOnce(() => new Promise(done => { accept = done }))
  const refresh = vi.fn(async () => { if (refreshFails) throw new Error('offline') })
  const page = await screen({ refresh })
  try {
    const button = page.resume()!
    await act(async () => button.click())
    expect(button.disabled).toBe(true)
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/api/external/call', { name: 'runs', arguments: { action: 'resume', workflow_id: 'report', run_folder: 'run-failed' } })
    await act(async () => accept({ data: { status: 'started', session_id: 'resume-session', run_folder: 'run-failed', resume_of: 'attempt-1', inputs_reused: true } }))
    expect(refresh).toHaveBeenCalledOnce()
    expect(page.onView).not.toHaveBeenCalled()
    expect(page.resume()?.disabled).toBe(true)
    expect(page.host.querySelector('[role="status"]')?.textContent).toContain('Resume started')
    if (refreshFails) expect(page.host.textContent).toContain('Could not refresh progress')
  } finally { await page.close() }
})

it('shows the server refusal verbatim and leaves the selected run available', async () => {
  const message = "This run's frozen source, hash or inputs are unavailable or changed. Start a new run."
  http.post.mockRejectedValueOnce({ isAxiosError: true, response: { data: { error: { code: 'resume_unavailable', message } } } })
  const page = await screen()
  try {
    await act(async () => page.resume()!.click())
    expect(page.host.textContent).toContain(message)
    expect(page.onView).not.toHaveBeenCalled()
    expect(page.refresh).not.toHaveBeenCalled()
    expect(page.resume()?.disabled).toBe(false)
  } finally { await page.close() }
})

// PR1651 P2: same-folder Resume must replace the failed attempt's stale inspection rows in place.
it('refreshes expandable step records when the same selected run changes attempt or status', async () => {
  const record = (name: string, execution: string) => ({ status: 'completed', inspection: { workspace_path: 'Workflow/report', run_folder: 'run-failed', name, execution_id: execution, session_id: 'session', started_at: '2026-11-09T00:00:00Z' } })
  http.inspections.mockResolvedValueOnce({ items: [record('Failed attempt step', 'old-step')] })
  const page = await screen()
  try {
    expect(page.host.textContent).toContain('Failed attempt step')
    http.inspections.mockResolvedValueOnce({ items: [record('Resumed attempt step', 'new-step')] })
    await page.update('running', 'attempt-2')
    expect(page.host.textContent).toContain('Resumed attempt step')
    expect(page.host.textContent).not.toContain('Failed attempt step')
    expect([...page.host.querySelectorAll('button')].some(button => button.textContent?.includes('Resumed attempt step'))).toBe(true)
    http.inspections.mockResolvedValueOnce({ items: [record('Final resumed step', 'final-step')] })
    await page.update('completed', 'attempt-2')
    expect(page.host.textContent).toContain('Final resumed step')
    expect(page.host.textContent).not.toContain('Resumed attempt step')
    expect(page.onView).not.toHaveBeenCalled()
  } finally { await page.close() }
})
