// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
vi.mock('../../components/workspace/WorkspaceAutomationLinks', () => ({
  WorkspaceAutomationLinks: () => null,
}))
vi.mock('../../components/workspace/WorkspaceContextBrowser', () => ({ WorkspaceContextBrowser: () => null }))
vi.mock('../../hooks/useWorkspaceViewTarget', () => ({ setWorkspaceViewTarget: vi.fn() }))
const mocks = vi.hoisted(() => ({ jobs: vi.fn(), inputs: vi.fn(), select: vi.fn() }))
vi.mock('../../api/scheduler', () => ({ schedulerApi: { listJobs: mocks.jobs } }))
vi.mock('../../services/api', () => ({ agentApi: { listReportHumanInputs: mocks.inputs } }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../components/workflow/reportDocuments', () => ({
  useReportDocuments: () => ({
    documents: [{ path: 'db/reports/index.html', title: 'Overview' }],
    defaultPath: 'db/reports/index.html',
  }),
  selectReportDocument: mocks.select,
}))
import { workWorkspacePanels } from './workWorkspacePanels'
import { WorkContextPanel, WorkContextTools } from './WorkContextPanel'
import { setWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.clearAllMocks())
it('keeps schedules and suggestions scoped to this Crew and opens its actual views', async () => {
  mocks.jobs.mockResolvedValue({
    jobs: [
      { id: 'one', workspace_path: 'Crew/one', name: 'My briefing', enabled: true },
      { id: 'other', workspace_path: 'Crew/other', name: 'Other Crew schedule', enabled: true },
    ],
    total: 2,
  })
  mocks.inputs.mockResolvedValue({
    success: true,
    inputs: [
      { workspace_path: 'Crew/one', status: 'pending' },
      { workspace_path: 'Crew/other', status: 'pending' },
    ],
  })
  const open = vi.fn()
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkContextPanel workspacePath="Crew/one" onOpen={open} />))
    expect(host.textContent).toContain('My briefing')
    expect(host.textContent).not.toContain('Other Crew schedule')
    expect(host.textContent).toContain('1 waiting')
    await act(async () => {
      Array.from(host.querySelectorAll('button'))
        .find((button) => button.textContent === 'Memory & skills')!
        .click()
    })
    expect(open).toHaveBeenCalledWith('memory')
    await act(async () => {
      Array.from(host.querySelectorAll('button'))
        .find((button) => button.textContent === 'Dashboard')!
        .click()
    })
    expect(mocks.select).toHaveBeenCalledWith('Crew/one', 'db/reports/index.html')
    expect(open).toHaveBeenLastCalledWith('dashboard')
    expect(host.querySelector('[aria-label="Workspace setup"]')).toBeNull()
    expect(host.querySelector('[aria-label="All views"]')).not.toBeNull()
    await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Connections')!.click())
    expect(setWorkspaceViewTarget).toHaveBeenLastCalledWith('mcp', 'overview')
    expect(open).toHaveBeenLastCalledWith('mcp')
    await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'More tools')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
    expect(document.querySelector('[aria-label="All project tools"]')?.textContent).toContain('Files')

  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})
it('does not display disabled report/memory/schedule panels or a false zero after failure', async () => {
  mocks.jobs.mockRejectedValue(new Error('unavailable'))
  mocks.inputs.mockRejectedValue(new Error('unavailable'))
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(<WorkContextPanel workspacePath="Crew/one" onOpen={vi.fn()} enabledPanels={new Set()} />)
    )
    expect(host.textContent).not.toContain('Dashboard')
    expect(host.textContent).not.toContain('Memory & skills')
    expect(host.textContent).not.toContain('Scheduled work')
    expect(host.textContent).not.toContain('Up to date')
    expect(host.textContent).toContain('Suggestions')
    expect(host.textContent).not.toContain('Details & instructions')
    expect(host.textContent).not.toContain('MCPs & secrets')
    for (const label of ['MCPs', 'Secrets', 'Google apps', 'Slack']) expect(host.textContent).not.toContain(label)
    expect(mocks.jobs).not.toHaveBeenCalled()
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('does not read schedule or suggestion summaries when neither panel is admitted', async () => {
  const groups = workWorkspacePanels({ profileId: 'work' })
  const panels = groups.views.filter(panel => panel.id === 'dashboard')
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<WorkContextPanel workspacePath="Crew/one" panels={panels} onOpen={vi.fn()} />))
    expect(mocks.jobs).not.toHaveBeenCalled()
    expect(mocks.inputs).not.toHaveBeenCalled()
    expect(host.textContent).toContain('Dashboard')
    expect(host.textContent).not.toContain('Suggestions')
    for (const label of ['MCPs', 'Secrets', 'Google apps', 'Slack']) expect(host.textContent).not.toContain(label)
  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('keeps the triple-dot menu limited to admitted project settings', async () => {
  const host = document.createElement('div'); const root = createRoot(host); const open = vi.fn()
  try {
    await act(async () => root.render(<WorkContextTools view="memory" onOpen={open} />))
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Project settings"]')!.click())
    const menu = host.querySelector('[role="menu"]')!
    expect([...menu.querySelectorAll('[role="menuitem"]')].map(item => item.textContent?.trim())).toEqual(['Project settings'])
    await act(async () => menu.querySelector<HTMLButtonElement>('button')!.click())
    expect(open).toHaveBeenLastCalledWith('identity')
    expect(menu.hasAttribute('hidden')).toBe(true)
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('keeps Code-origin settings and includes the merged Crew suggestion feature', async () => {
  mocks.jobs.mockResolvedValue({
    jobs: [{ id: 'code-job', workspace_path: 'Code/one', name: 'Code check', enabled: true }],
    total: 1,
  })
  mocks.inputs.mockResolvedValue({ success: true, inputs: [] })
  const groups = workWorkspacePanels({ profileId: 'code', showShell: true })
  const panels = [...groups.views, ...groups.ops, ...groups.setup]
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const open = vi.fn()
  try {
    await act(async () =>
      root.render(
        <WorkContextPanel
          workspacePath="Code/one"
          projectId="one"
          profileId="code"
          panels={panels}
          onOpen={open}
        />
      )
    )
    expect(mocks.inputs).toHaveBeenCalledWith('Code/one', 'pending', 'user_suggestion')
    expect(host.textContent).toContain('Code check')
    expect(host.textContent).toContain('Memory & skills')
    const workspace = Array.from(host.querySelectorAll('section')).find(section => section.querySelector('h2')?.textContent === 'Project tools')!
    await act(async () => Array.from(workspace.querySelectorAll('button')).find(button => button.textContent === 'Files')!.click())
    expect(open).toHaveBeenLastCalledWith('files')
    expect(host.textContent).not.toContain('Crew memory')
    expect(host.textContent).toContain('Suggestions')
    expect(host.textContent).not.toContain('Pulse')
    await act(async () => Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === 'More tools')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
    const tools = document.querySelector('[aria-label="All project tools"]')!
    for (const label of ['Terminal', 'Database', 'Suggestions', 'Files']) expect(tools.textContent).toContain(label)
    await act(async () => Array.from(tools.querySelectorAll<HTMLElement>('[role="menuitem"]')).find(button => button.textContent?.trim() === 'Terminal')!.click())
    expect(open).toHaveBeenLastCalledWith('shell')
    await act(async () => root.render(<WorkContextTools view="context" profileId="code" panels={panels} onOpen={open} />))
    const menu = host.querySelector('[role="menu"]')!
    expect(menu.textContent).toContain('Project settings')
    for (const label of ['Terminal', 'Database', 'Suggestions', 'Connections']) expect(menu.textContent).not.toContain(label)

  } finally {
    await act(async () => root.unmount())
    host.remove()
  }
})

it('does not introduce settings or tool links beyond a restricted workspace inventory', async () => {
  const host = document.createElement('div'); const root = createRoot(host)
  try {
    const groups = workWorkspacePanels({ readOnly: true })
    const panels = [...groups.views, ...groups.ops, ...groups.setup]
    await act(async () => root.render(<WorkContextTools view="dashboard" panels={panels} onOpen={vi.fn()} />))
    expect(host.querySelector('[role="menu"]')?.textContent).not.toContain('Project settings')
    await act(async () => root.render(<WorkContextPanel workspacePath="Crew/one" panels={panels} onOpen={vi.fn()} />))
    for (const label of ['Connections', 'Terminal', 'Database', 'Secrets', 'Slack']) expect(host.textContent).not.toContain(label)
  } finally { await act(async () => root.unmount()); host.remove() }
})
