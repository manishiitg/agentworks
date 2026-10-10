import { describe, expect, it } from 'vitest'
import { isScheduledWorkflowSession, pickWorkflowActiveSession } from './workflowSessionRestore'
import type { ActiveSessionInfo, RunningWorkflowInfo } from '../services/api-types'

const session = (overrides: Partial<ActiveSessionInfo> = {}): ActiveSessionInfo => ({
  session_id: 'wfrun_1',
  observer_id: '',
  agent_mode: 'workflow',
  status: 'running',
  last_activity: '2026-06-30T00:00:00Z',
  created_at: '2026-06-30T00:00:00Z',
  ...overrides,
})

const runningWorkflow = (overrides: Partial<RunningWorkflowInfo> = {}): RunningWorkflowInfo => ({
  query_id: 'query-1',
  session_id: 'wfrun_1',
  workspace_path: 'Workflow/hetznerssh',
  triggered_by: 'manual',
  started_at: '2026-06-30T00:00:00Z',
  ...overrides,
})

describe('workflow session restore classification', () => {
  it('treats cron-triggered workflow runs as scheduled read-only runs', () => {
    expect(isScheduledWorkflowSession(session({ triggered_by: 'cron' }))).toBe(true)
    expect(isScheduledWorkflowSession(session(), runningWorkflow({ triggered_by: 'cron' }))).toBe(true)
  })

  it('does not classify ordinary workflow runs as scheduled', () => {
    expect(isScheduledWorkflowSession(session({ triggered_by: 'manual' }))).toBe(false)
    expect(isScheduledWorkflowSession(session(), runningWorkflow({ triggered_by: 'manual' }))).toBe(false)
  })

  it('keeps the Pulse parent selected instead of its newer internal reviewer', () => {
    const parent = session({
      session_id: 'pulse-root-1',
      preset_query_id: 'latency-report',
      workspace_path: 'Workflow/latency-report',
      last_activity: '2026-08-03T09:00:00Z',
    })
    const child = session({
      session_id: 'pulse-reviewer-1',
      parent_session_id: parent.session_id,
      session_kind: 'pulse_reviewer',
      preset_query_id: 'latency-report',
      workspace_path: 'Workflow/latency-report',
      has_retained_tmux_session: true,
      last_activity: '2026-08-03T09:05:00Z',
    })
    const preset = {
      id: 'latency-report',
      label: 'project-a latency',
      selectedFolder: { filepath: 'Workflow/latency-report' },
    } as Parameters<typeof pickWorkflowActiveSession>[1]

    expect(pickWorkflowActiveSession([child, parent], preset, {})?.session_id).toBe(parent.session_id)
  })

  it('skips a completed turn whose retained terminal is only idle', () => {
    // Since the Sep 18 monitor clarification, a ready idle pane is not
    // active work: restore follows the same visibility rule and does not
    // select it. A live runtime (authoritative running state, needed input,
    // live background agents) still selects.
    const retained = session({
      status: 'completed',
      preset_query_id: 'latency-report',
      workspace_path: 'Workflow/latency-report',
      has_retained_tmux_session: true,
      last_activity: new Date().toISOString(),
    })
    const preset = {
      id: 'latency-report',
      label: 'project-a latency',
      selectedFolder: { filepath: 'Workflow/latency-report' },
    } as Parameters<typeof pickWorkflowActiveSession>[1]

    expect(pickWorkflowActiveSession([retained], preset, {})).toBeUndefined()
  })

  it('uses authoritative running runtime state even when legacy status says completed', () => {
    const running = session({
      status: 'completed',
      preset_query_id: 'build-in-public',
      workspace_path: 'Workflow/build-in-public',
      runtime_state: { phase: 'running' } as ActiveSessionInfo['runtime_state'],
    })
    const preset = {
      id: 'build-in-public',
      label: 'Build in public',
      selectedFolder: { filepath: 'Workflow/build-in-public' },
    } as Parameters<typeof pickWorkflowActiveSession>[1]

    expect(pickWorkflowActiveSession([running], preset, {})?.session_id).toBe(running.session_id)
  })
})
