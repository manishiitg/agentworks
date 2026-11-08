// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, expect, it, vi } from 'vitest'
import type { RunFolderInfo, RunMetadata } from '../../services/api-types'

const http = vi.hoisted(() => ({ post: vi.fn(), inspections: vi.fn() }))
vi.mock('../../services/api', () => ({ default: http, agentApi: {
  getRunCostTotals: async () => ({ runs: [] }),
  getWorkspaceStepInspections: http.inspections,
} }))
vi.mock('../../api/runEvidence', () => ({ runEvidenceApi: { get: async () => [] } }))
vi.mock('../../api/crewScriptGraph', () => ({ crewScriptGraphApi: { get: async () => ({ nodes: [], edges: [], errors: [] }) } }))
vi.mock('../../components/workflow/costs/useCostsData', () => ({ useCostsData: () => ({ runCosts: [] }) }))
import { CrewWorkflowRuns } from './CrewWorkflowRuns'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
beforeEach(() => { http.post.mockReset(); http.inspections.mockReset().mockResolvedValue({ items: [] }) })

async function screen(options: { status?: string; script?: boolean; refresh?: () => Promise<void>; metadata?: Partial<RunMetadata> } = {}) {
  const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
  const onView = vi.fn(); const refresh = options.refresh ?? vi.fn(async () => {})
  const folder: RunFolderInfo = { name: 'run-failed', metadata: { created_at: '2026-11-09T00:00:00Z', execution_id: 'attempt-1', status: options.status ?? 'failed', can_resume: true, ...options.metadata } }
  const update = async (status = folder.metadata!.status, execution = folder.metadata!.execution_id) => {
    folder.metadata = { ...folder.metadata!, status, execution_id: execution }
    await act(async () => root.render(<CrewWorkflowRuns workflowId="report" workspacePath="Workflow/report" script={options.script ?? true} folders={[{ ...folder }]} loading={false} onRefresh={refresh} onAsk={vi.fn()} view={{ run: folder.name, costs: false, detail: null }} onView={onView} />))
  }
  await update()
  return { host, onView, refresh, update, resume: () => [...host.querySelectorAll('button')].find(button => button.textContent === 'Resume'), close: async () => { await act(async () => root.unmount()); host.remove() } }
}

// PLAT-746: Resume visibility consumes server eligibility; missing or denied eligibility shows no control or explanation.
it.each([true, false, undefined])('uses metadata.can_resume=%s without inferring permission', async canResume => {
  const page = await screen({ metadata: { can_resume: canResume } })
  try {
    expect(Boolean(page.resume())).toBe(canResume === true)
    if (!canResume) expect(page.host.textContent).not.toContain('Uses saved source and inputs')
    expect(http.post).not.toHaveBeenCalled()
  } finally { await page.close() }
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

// Same helper source, distinct durable step IDs: checks must stay on their own execution.
it('assigns check counts by server step ID and shows per-call saved-result reuse', async () => {
  const metadata: Partial<RunMetadata> = {
    can_resume: false,
    python_steps: {
      first: { id: 'first', execution_id: 'execution-one', name: 'Gather', status: 'completed', started_at: '', source_call: 'main.py:shared@10' },
      second: { id: 'second', execution_id: 'execution-two', name: 'Review', status: 'failed', started_at: '', source_call: 'main.py:shared@10' },
    },
    python_evidence: [
      { id: 'a', kind: 'check', at: '', detail: 'First check', ok: true, step_execution_id: 'first', source_call: 'main.py:shared@10' },
      { id: 'b', kind: 'check', at: '', detail: 'Second check', ok: false, step_execution_id: 'second', source_call: 'main.py:shared@10' },
      { id: 'c', kind: 'check', at: '', detail: 'Old unassigned check', ok: true, source_call: 'main.py:shared@10' },
    ],
    python_resume_calls: [
      { key: 'gather-record', action: 'agent', reused: true, reason: 'completed_result', execution_id: 'attempt-2', at: '' },
      { key: 'review-record', action: 'agent', reused: false, reason: 'missing_result', execution_id: 'attempt-2', at: '' },
    ],
  }
  http.inspections.mockResolvedValueOnce({ items: [{ status: 'completed', inspection: { workspace_path: 'Workflow/report', run_folder: 'run-failed', name: 'Gather', execution_id: 'execution-one', session_id: 'session', started_at: '' } }] })
  const page = await screen({ metadata })
  try {
    const rows = [...page.host.querySelectorAll('button')]
    expect(rows.find(button => button.textContent?.includes('Gather'))?.textContent).toContain('1 check passed')
    expect(rows.find(button => button.textContent?.includes('Review'))?.textContent).toContain('1 of 1 check failed')
    expect(page.host.textContent).toContain('Old unassigned check')
    expect(rows.find(button => button.textContent?.includes('gather-record'))?.textContent).toContain('Reused saved result')
    expect(rows.find(button => button.textContent?.includes('review-record'))?.textContent).toContain('Ran again')
  } finally { await page.close() }
})
