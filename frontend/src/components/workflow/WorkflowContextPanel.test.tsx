// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.mock('../workspace/WorkspaceAutomationLinks', () => ({ WorkspaceAutomationLinks: () => null }))
vi.mock('../workspace/WorkspaceContextBrowser', () => ({ WorkspaceContextBrowser: () => null }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true }))
const mocks = vi.hoisted(() => ({ list: vi.fn(), open: vi.fn(), select: vi.fn() }))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: mocks.list } }))
vi.mock('../../stores/useWorkflowStore', () => ({
  useWorkflowStore: (select: (state: unknown) => unknown) => select({ openWorkspaceView: mocks.open }),
}))
vi.mock('../../stores/useWorkflowManifestStore', () => ({
  useWorkflowManifestStore: (select: (state: unknown) => unknown) =>
    select({ workflows: [{ workspace_path: 'Workflow/one', manifest: { pulse: { enabled: true } } }] }),
}))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: () => false }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('./hooks/usePendingDecisionCount', () => ({
  usePendingDecisionState: () => ({ loaded: true, count: 2 }),
}))
vi.mock('./reportDocuments', () => ({
  useReportDocuments: () => ({
    documents: [
      { path: 'db/reports/index.html', title: 'Overview' },
      { path: 'db/reports/review.html', title: 'Weekly review' },
    ],
    defaultPath: 'db/reports/index.html',
  }),
  selectReportDocument: mocks.select,
}))
import { WorkflowContextPanel, WorkflowContextTools } from './WorkflowContextPanel'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())
it('shows only this workspace’s schedules and opens real report/Pulse/decision destinations', async () => {
  mocks.list.mockResolvedValue({
    jobs: [
      { id: 'one', name: 'My schedule', workspace_path: 'Workflow/one', enabled: true },
      { id: 'two', name: 'Other account schedule', workspace_path: 'Workflow/two', enabled: true },
    ],
    total: 2,
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkflowContextPanel workspacePath="Workflow/one" />))
    expect(host.textContent).toContain('My schedule')
    expect(host.textContent).not.toContain('Other account schedule')
    expect(host.textContent).toContain('2 waiting')
    const click = async (text: string) =>
      act(async () => {
        const button = Array.from(host.querySelectorAll('button')).find((item) =>
          item.textContent?.includes(text)
        )
        expect(button).toBeDefined()
        button!.click()
      })
    await click('Weekly review')
    expect(mocks.select).toHaveBeenCalledWith('Workflow/one', 'db/reports/review.html')
    expect(mocks.open).toHaveBeenLastCalledWith('report')
    await click('Pulse')
    expect(mocks.open).toHaveBeenLastCalledWith('pulse')
    await click('Decisions')
    expect(mocks.open).toHaveBeenLastCalledWith('human-actions')
    expect(host.querySelector('[aria-label="Workspace setup"]')).toBeNull()
    await click('Connections')
    expect(mocks.open).toHaveBeenLastCalledWith('mcp', 'overview')
    await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'More tools')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
    expect(document.querySelector('[aria-label="All project tools"]')?.textContent).toContain('Run logs')
    expect(host.querySelector('[aria-label="All views"]')).not.toBeNull()

  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
it('reports a failed schedule read rather than pretending there are no schedules', async () => {
  mocks.list.mockRejectedValue(new Error('unavailable'))
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkflowContextPanel workspacePath="Workflow/one" />))
    expect(host.textContent).toContain('Could not load schedules')
    expect(host.textContent).not.toContain('No schedules yet')
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('keeps only project settings in the triple-dot menu', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkflowContextTools />))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Project settings"]')!.click())
    const menu = host.querySelector('[role="menu"]')!
    for (const label of ['Project settings', 'Backup', 'Publish', 'Notify', 'Workflow playbooks']) expect(menu.textContent).toContain(label)
    for (const label of ['Connections', 'MCPs', 'Slack', 'Dashboard', 'Browser', 'Pulse', 'Decisions', 'Schedules']) expect(menu.textContent).not.toContain(label)
    expect(menu.textContent).not.toContain('Access')
    await act(async () => Array.from(menu.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Project settings')!.click())
    expect(mocks.open).toHaveBeenLastCalledWith('identity')
  } finally { await act(async () => root.unmount()); host.remove() }
})
