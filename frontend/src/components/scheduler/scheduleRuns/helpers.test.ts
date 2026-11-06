import { describe, expect, it } from 'vitest'
import type { ScheduledJob } from '../../../services/api-types'
import { defaultSchedulePanelView, getPotentialScheduleOverlaps, jobMatchesWorkflowScope, timeScheduledJobs } from './helpers'

describe('defaultSchedulePanelView', () => {
  it('opens global views on the per-workflow grouping', () => {
    expect(defaultSchedulePanelView(false)).toBe('by-workflow')
  })

  it('opens a workflow-scoped view on the schedule list', () => {
    expect(defaultSchedulePanelView(true)).toBe('schedules')
  })
})

describe('timeScheduledJobs', () => {
  it('keeps cron and calendar schedules while leaving webhook jobs to Triggers', () => {
    const jobs = [
      { id: 'cron', schedule_type: 'cron' },
      { id: 'calendar', schedule_type: 'calendar' },
      { id: 'webhook', schedule_type: 'webhook' },
    ] as ScheduledJob[]

    expect(timeScheduledJobs(jobs).map(job => job.id)).toEqual(['cron', 'calendar'])
  })
})

describe('jobMatchesWorkflowScope', () => {
  it('matches a product schedule by stable project identity across public and user-scoped paths', () => {
    const job = {
      entity_type: 'product',
      workflow_id: 'project-1',
      workspace_path: '_users/user-1/Chats/Work/projects/project-1',
    } as ScheduledJob

    expect(jobMatchesWorkflowScope(job, {
      workflowId: 'project-1',
      workspacePath: 'Chats/Work/projects/project-1',
    }, new Map())).toBe(true)
  })

  it('includes child schedules only in the parent Crew supplied by the server', () => {
    const job = { entity_type: 'workflow', workspace_path: 'Workflow/reports', crew_workspace_path: 'Crew/parent' } as ScheduledJob
    expect(jobMatchesWorkflowScope(job, { workspacePath: 'Crew/parent' }, new Map())).toBe(true)
    expect(jobMatchesWorkflowScope(job, { workspacePath: 'Crew/other' }, new Map())).toBe(false)
  })

  it('does not expose another project schedule from the same user', () => {
    const job = {
      entity_type: 'product',
      workflow_id: 'project-2',
      workspace_path: '_users/user-1/Chats/Work/projects/project-2',
    } as ScheduledJob

    expect(jobMatchesWorkflowScope(job, {
      workflowId: 'project-1',
      workspacePath: 'Chats/Work/projects/project-1',
    }, new Map())).toBe(false)
  })
})

describe('getPotentialScheduleOverlaps', () => {
  it('flags close starts only within the same sequential workflow when duration history exists', () => {
    const jobs = [
      { id: 'first', name: 'First', workflow_id: 'one', enabled: true, next_run_at: '2026-09-23T09:00:00Z', avg_duration_ms: 30 * 60_000 },
      { id: 'second', name: 'Second', workflow_id: 'one', enabled: true, next_run_at: '2026-09-23T09:15:00Z' },
      { id: 'other-workflow', name: 'Other', workflow_id: 'two', enabled: true, next_run_at: '2026-09-23T09:10:00Z' },
      { id: 'parallel', name: 'Parallel', workflow_id: 'one', enabled: true, concurrency_mode: 'parallel', next_run_at: '2026-09-23T09:10:00Z' },
    ] as ScheduledJob[]

    const overlaps = getPotentialScheduleOverlaps(jobs, new Map())
    expect(overlaps.get('first')).toBe('Second')
    expect(overlaps.get('second')).toBe('First')
    expect(overlaps.has('other-workflow')).toBe(false)
    expect(overlaps.has('parallel')).toBe(false)
  })
})
