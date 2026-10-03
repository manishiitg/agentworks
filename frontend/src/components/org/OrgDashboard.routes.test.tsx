// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { OrgDashboardNotification } from '../../services/api-types'

vi.mock('../../services/api', () => {
  const agentApi = {
    getOrgDashboardNotifications: vi.fn(), listReportHumanInputsAggregate: vi.fn(),
    // The dashboard asks for unanswered plus answered-not-applied decisions; the
    // tests set the decisions through the aggregate mock.
    listNeedsYouDecisionsAggregate: (paths: string[]) => agentApi.listReportHumanInputsAggregate(paths, 'pending'),
  }
  return { agentApi }
})
import { agentApi } from '../../services/api'
import { OrgDashboard } from './OrgDashboard'

describe('Activity route summaries', () => {
  it('opens on the workflow requested by the per-workflow activity shortcut', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const update: OrgDashboardNotification = { id: 'publish-update', workspace_path: 'Workflow/publish', kind: 'run_summary',
      status: 'completed', title: 'Publishing ready', message: 'Drafts saved.', created_at: '2026-09-05T00:00:00Z' }
    vi.mocked(agentApi.listReportHumanInputsAggregate).mockResolvedValue({ success: true, inputs: [] })
    vi.mocked(agentApi.getOrgDashboardNotifications).mockResolvedValue({ success: true, workflows: [
      { workspace_path: 'Workflow/research', recent: [] },
      { workspace_path: 'Workflow/publish', run_summary: update, recent: [update] },
    ] })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<OrgDashboard
        workflows={[{ workspacePath: 'Workflow/research', label: 'Research' }, { workspacePath: 'Workflow/publish', label: 'Publish' }]}
        selectedWorkflowPath="Workflow/publish"
        onOpenWorkflow={vi.fn()}
      />))
      expect(container.querySelector('[aria-label="Publish activity"]')?.textContent).toContain('Publishing ready')
      expect(container.querySelector('[aria-label="View Publish activity"]')?.getAttribute('aria-pressed')).toBe('true')
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })

  it('keeps verbose Pulse evidence collapsed until requested', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const pulse: OrgDashboardNotification = { id: 'pulse-digest', workspace_path: 'Workflow/demo', kind: 'pulse_summary',
      status: 'monitoring', title: 'Pulse · healthy', message: 'No active issues; watch the next cost run.', created_at: '2026-09-05T00:00:00Z',
      reviews: [{ module: 'technical_review', label: 'Technical review', status: 'fixed', summary: 'Repaired the cost calculation.', issues_found: 1, fixes_applied: 1, verification: 'monitoring' }],
      fields: [{ label: 'Verdict', value: 'Healthy' }], sections: [
        { heading: 'Measure AWS cost', body: 'Account finalized daily AWS cost: 55.8621009693 USD · observed at a raw collection timestamp.' },
        { heading: 'Infrastructure health', body: 'Supporting evidence and provenance.' },
      ] }
    vi.mocked(agentApi.listReportHumanInputsAggregate).mockResolvedValue({ success: true, inputs: [] })
    vi.mocked(agentApi.getOrgDashboardNotifications).mockResolvedValue({ success: true, workflows: [{
      workspace_path: 'Workflow/demo', pulse_summary: pulse, recent: [pulse],
    }] })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<OrgDashboard workflows={[{ workspacePath: 'Workflow/demo', label: 'Demo' }]} onOpenWorkflow={vi.fn()} />))
      const pulseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Pulse'))!
      await act(async () => pulseButton.click())
      const evidence = container.querySelector<HTMLDetailsElement>('[aria-label="Evidence and metric details"]')!
      expect(container.querySelector('[aria-label="Pulse reviews"]')?.textContent).toContain('Repaired the cost calculation.')
      expect(evidence.open).toBe(false)
      expect(evidence.querySelector('summary')?.textContent).toContain('2 sections')
      evidence.open = true
      expect(evidence.textContent).toContain('55.8621009693 USD')
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })

  it.each(['failed', 'blocked', 'waiting_for_user', 'waiting_for_platform', 'no_run'] as const)('keeps a %s route visible without flagging a pending decision', async (status) => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const quiet: OrgDashboardNotification = { id: 'old-digest', workspace_path: 'Workflow/demo', kind: 'run_summary',
      status, title: 'Research needs access', message: 'No research output produced.', created_at: '2026-09-01T00:00:00Z', fields: [{ label: 'Blocker', value: 'Missing research credentials' }] }
    const busy: OrgDashboardNotification = { ...quiet, id: 'new-digest', status: 'completed', title: 'Publishing completed',
      message: 'Published three articles.', created_at: '2026-09-05T00:00:00Z' }
    vi.mocked(agentApi.listReportHumanInputsAggregate).mockResolvedValue({ success: true, inputs: [] })
    vi.mocked(agentApi.getOrgDashboardNotifications).mockResolvedValue({ success: true, workflows: [{
      workspace_path: 'Workflow/demo', run_summary: busy, recent: [busy], by_route: [
        { routing_step_id: 'router-a', route_id: 'daily', label: 'Research', run_summary: quiet },
        { routing_step_id: 'router-b', route_id: 'daily', label: 'Publishing', run_summary: busy },
      ],
    }] })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => root.render(<OrgDashboard workflows={[{ workspacePath: 'Workflow/demo', label: 'Demo' }]} onOpenWorkflow={vi.fn()} />))
      expect(container.textContent).toContain('Pending decisions 0')
      const automation = container.querySelector('[aria-label="View Demo activity"]')!
      expect(automation.textContent).toContain('Latest update')
      expect(automation.textContent).not.toContain('Needs attention')
      expect(automation.textContent).not.toContain('Needs your decision')
      const research = container.querySelector('[aria-label="Demo · Research"]')!
      const publishing = container.querySelector('[aria-label="Demo · Publishing"]')!
      expect(research.textContent).toContain('Research needs access')
      expect(research.textContent).not.toContain('No pulse summary recorded yet')
      expect(research.textContent).not.toContain('Publishing completed')
      expect(publishing.textContent).toContain('Publishing completed')
      expect(container.querySelector('[aria-label="Demo · Automation overview"]')).toBeNull()
      const runButton = research.querySelector('button')!
      expect(runButton.getAttribute('aria-expanded')).toBe('false')
      expect(research.textContent).not.toContain('Missing research credentials')
      await act(async () => runButton.click())
      expect(runButton.getAttribute('aria-expanded')).toBe('true')
      expect(research.querySelector('[role="region"]')?.textContent).toContain('Missing research credentials')
      expect(document.querySelector('[role="dialog"]')).toBeNull()
      expect(publishing.querySelector('[role="region"]')).toBeNull()
      await act(async () => runButton.click())
      expect(runButton.getAttribute('aria-expanded')).toBe('false')
      expect(research.querySelector('[role="region"]')).toBeNull()

      const history = container.querySelector('details')!
      history.open = true
      await act(async () => history.querySelector('button')!.click())
      expect(history.querySelector('[role="region"]')?.textContent).toContain('Published three articles.')
      expect(document.querySelector('[role="dialog"]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })

  it('groups decisions and history under the selected automation and filters without mixing them', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
    const blocked: OrgDashboardNotification = { id: 'shared-id', workspace_path: 'Workflow/research', kind: 'run_summary',
      status: 'blocked', title: 'Research blocked', message: 'Research needs access.', created_at: '2026-09-01T00:00:00Z' }
    const completed: OrgDashboardNotification = { ...blocked, workspace_path: 'Workflow/publish',
      status: 'completed', title: 'Publishing ready', message: 'Drafts saved.', created_at: '2026-09-05T00:00:00Z' }
    vi.mocked(agentApi.listReportHumanInputsAggregate).mockResolvedValue({ success: true, inputs: [{
      id: 'decision', workspace_path: 'Workflow/research', source: 'pulse', priority: 'high', question: 'Approve research access?',
      options: [], allow_free_text: false, status: 'pending', created_at: blocked.created_at, updated_at: blocked.created_at,
    }] })
    vi.mocked(agentApi.getOrgDashboardNotifications).mockResolvedValue({ success: true, workflows: [
      { workspace_path: 'Workflow/research', run_summary: blocked, recent: [blocked] },
      { workspace_path: 'Workflow/publish', by_route: [{ route_id: 'publish', label: 'Publish route', run_summary: completed }], recent: [completed] },
    ] })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    const onOpen = vi.fn()
    const workflows = [{ workspacePath: 'Workflow/research', label: 'Research' }, { workspacePath: 'Workflow/publish', label: 'Publish' }]
    const button = (text: string) => Array.from(container.querySelectorAll('button')).find(el => el.textContent === text)!
    try {
      await act(async () => root.render(<OrgDashboard workflows={workflows} onOpenWorkflow={onOpen} />))
      expect(container.querySelector('[aria-label="Research activity"]')?.textContent).toContain('Approve research access?')
      expect(container.querySelector('[aria-label="Research activity"]')?.textContent).not.toContain('Publishing ready')
      expect(container.querySelector('details')?.open).toBe(false)
      expect(container.querySelector('[aria-label="View Research activity"]')?.textContent).toContain('Needs your decision')

      await act(async () => (container.querySelector('[aria-label="View Publish activity"]') as HTMLButtonElement).click())
      expect(container.querySelector('[aria-label="Publish activity"]')?.textContent).toContain('Publishing ready')
      expect(container.textContent).not.toContain('Approve research access?')
      expect(container.querySelector('[aria-label="View Publish activity"]')?.textContent).not.toContain('No activity yet')
      await act(async () => button('Open automation').click())
      expect(onOpen).toHaveBeenLastCalledWith('Workflow/publish')

      await act(async () => button('Pending decisions 1').click())
      expect(container.querySelector('[aria-label="View Publish activity"]')).toBeNull()
      expect(container.querySelector('[aria-label="Research activity"]')?.textContent).toContain('Approve research access?')
      await act(async () => button('All 2').click())
      await act(async () => button('Refresh').click())
      expect(container.querySelector('[aria-label="View Publish activity"]')?.getAttribute('aria-pressed')).toBe('true')
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
})
