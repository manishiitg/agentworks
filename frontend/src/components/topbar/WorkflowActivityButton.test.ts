import { describe, expect, it } from 'vitest'
import type { OrgDashboardNotification } from '../../services/api-types'
import { workflowHasActivity, workflowHasRecentActivity } from '../../utils/workflowActivity'

const notification: OrgDashboardNotification = {
  id: 'activity-1',
  workspace_path: 'Workflow/demo',
  kind: 'run_summary',
  status: 'completed',
  message: 'Finished.',
  created_at: '2026-09-17T00:00:00Z',
}

describe('workflowHasActivity', () => {
  it('only reports activity for the selected workflow', () => {
    const response = {
      success: true,
      workflows: [{ workspace_path: 'Workflow/other', run_summary: notification }],
    }
    expect(workflowHasActivity('Workflow/demo', response, [])).toBe(false)
  })

  it('recognizes workflow, route, and pending-decision activity', () => {
    expect(workflowHasActivity('Workflow/demo', {
      success: true,
      workflows: [{ workspace_path: 'Workflow/demo', recent: [notification] }],
    }, [])).toBe(true)

    expect(workflowHasActivity('Workflow/demo', {
      success: true,
      workflows: [{ workspace_path: 'Workflow/demo', by_route: [{ route_id: 'daily', label: 'Daily', run_summary: notification }] }],
    }, [])).toBe(true)

    expect(workflowHasActivity('Workflow/demo', { success: true, workflows: [] }, [
      { workspace_path: 'Workflow/demo' },
    ])).toBe(true)
  })

  it('marks only updates from the last 24 hours as recent', () => {
    const now = Date.parse('2026-09-17T12:00:00Z')
    expect(workflowHasRecentActivity('Workflow/demo', { workflows: [{
      workspace_path: 'Workflow/demo',
      run_summary: { ...notification, created_at: '2026-09-17T00:01:00Z' },
    }] }, [], now)).toBe(true)
    expect(workflowHasRecentActivity('Workflow/demo', { workflows: [{
      workspace_path: 'Workflow/demo',
      run_summary: { ...notification, created_at: '2026-09-16T11:59:00Z' },
    }] }, [], now)).toBe(false)
  })
})
