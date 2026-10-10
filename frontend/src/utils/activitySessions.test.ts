import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo } from '../services/api-types'
import {
  hasActiveSessionWork,
  hasIdleAliveCodingAgent,
  hasLiveBackgroundAgents,
  isProductProjectSession,
  isTerminalActivityStatus,
  isVisibleActivitySession,
  nonWorkflowActivityTitle,
  RETAINED_TMUX_ACTIVE_WINDOW_MS,
} from './activitySessions'

describe('activity session helpers', () => {
  it('recognizes product-project sessions so AgentWorks can keep them out of its monitor', () => {
    expect(isProductProjectSession({ session_id: 'video-studio:project:new-video' })).toBe(true)
    expect(isProductProjectSession({ session_id: 'future-product:project:launch-1' })).toBe(true)
    expect(isProductProjectSession({
      session_id: 'legacy-uuid-session',
      workspace_path: 'Chats/Work/projects/new-project-75db2aa1',
    })).toBe(true)
    expect(isProductProjectSession({
      session_id: 'legacy-uuid-session',
      workspace_path: '_users/alice/Chats/Video-Studio/projects/launch-1',
    })).toBe(true)
    expect(isProductProjectSession({ session_id: 'schedule-cron--workflow_1' })).toBe(false)
    expect(isProductProjectSession({ session_id: 'chat-123' })).toBe(false)
    expect(isProductProjectSession({
      session_id: 'workflow-session',
      workspace_path: 'Workflow/customer-login',
    })).toBe(false)
  })

  it('does not treat an idle retained session as active work', () => {
    expect(hasActiveSessionWork({ status: 'completed' })).toBe(false)
  })

  it('recognizes running and waiting sessions as active work', () => {
    expect(hasActiveSessionWork({ status: 'running' })).toBe(true)
    expect(hasActiveSessionWork({ status: 'waiting_feedback' })).toBe(true)
  })

  it('ignores stale background-agent flags on completed sessions', () => {
    expect(hasLiveBackgroundAgents({
      status: 'completed',
      has_running_background_agents: true,
      running_background_agent_count: 1,
    })).toBe(false)
  })

  it('ignores stale background-agent flags on stopped sessions', () => {
    expect(hasLiveBackgroundAgents({
      status: 'stopped',
      has_running_background_agents: true,
      running_background_agent_count: 1,
    })).toBe(false)
  })

  it('keeps background-agent flags for live sessions', () => {
    expect(hasLiveBackgroundAgents({
      status: 'running',
      has_running_background_agents: true,
      running_background_agent_count: 1,
    })).toBe(true)
  })

  it('recognizes terminal statuses', () => {
    expect(isTerminalActivityStatus('completed')).toBe(true)
    expect(isTerminalActivityStatus('stopped')).toBe(true)
    expect(isTerminalActivityStatus('running')).toBe(false)
  })
})

describe('isVisibleActivitySession', () => {
  const now = Date.parse('2026-08-03T12:00:00Z')
  const session = (overrides: Partial<ActiveSessionInfo> = {}): ActiveSessionInfo => ({
    session_id: 'workflow-1',
    observer_id: '',
    agent_mode: 'workflow',
    status: 'completed',
    last_activity: '2026-08-03T11:55:00Z',
    created_at: '2026-08-03T11:00:00Z',
    ...overrides,
  })

  it('includes a session whose legacy status is completed while runtime is running', () => {
    expect(isVisibleActivitySession(session({
      runtime_state: { phase: 'running' } as ActiveSessionInfo['runtime_state'],
    }), now)).toBe(true)
  })

  it('excludes a completed interactive turn whose retained terminal is only idle', () => {
    expect(isVisibleActivitySession(session({ has_retained_tmux_session: true }), now)).toBe(false)
  })

  it('does not resurrect a completed scheduled run because it retained a terminal', () => {
    expect(isVisibleActivitySession(session({
      triggered_by: 'cron',
      has_retained_tmux_session: true,
    }), now)).toBe(false)
  })

  it('excludes an idle scheduled session that is not waiting for the user', () => {
    expect(isVisibleActivitySession(session({
      triggered_by: 'cron',
      status: 'running',
      display_status: 'idle',
    }), now)).toBe(false)
  })

  it('excludes a settled session with no retained terminal or live runtime', () => {
    expect(isVisibleActivitySession(session(), now)).toBe(false)
  })
})

describe('nonWorkflowActivityTitle', () => {
  it('uses the short backend title for a scheduled agent run', () => {
    expect(nonWorkflowActivityTitle({
      session_id: 'schedule-cron--daily_123',
      triggered_by: 'cron',
      title: 'Daily Financial Compliance Monitor',
      query: 'NORMAL CHIEF OF STAFF TASK RUN.\n\nVery long scheduler envelope',
    })).toBe('Daily Financial Compliance Monitor')
  })

  it('does not leak a scheduled prompt into the activity UI', () => {
    expect(nonWorkflowActivityTitle({
      session_id: 'schedule-cron--daily_123',
      triggered_by: 'cron',
      query: 'NORMAL CHIEF OF STAFF TASK RUN.\n\nVery long scheduler envelope',
    })).toBe('Scheduled agent task')
  })

  it('retains the user prompt fallback for an interactive chat', () => {
    expect(nonWorkflowActivityTitle({
      session_id: 'chat-123',
      query: 'Review my portfolio',
    })).toBe('Review my portfolio')
  })
})

describe('hasIdleAliveCodingAgent', () => {
  const now = Date.parse('2026-07-03T09:00:00Z')

  it('is false when no retained tmux pane', () => {
    expect(hasIdleAliveCodingAgent({ has_retained_tmux_session: false, last_activity: '2026-07-03T08:59:00Z' }, now)).toBe(false)
    expect(hasIdleAliveCodingAgent({ last_activity: '2026-07-03T08:59:00Z' }, now)).toBe(false)
  })

  it('shows an idle-alive pane whose last activity is within the window', () => {
    // completed-but-alive: backend flipped status to completed, pane still up
    expect(hasIdleAliveCodingAgent({
      has_retained_tmux_session: true,
      last_activity: new Date(now - 5 * 60 * 1000).toISOString(),
    }, now)).toBe(true)
  })

  it('hides a pane abandoned longer than the window', () => {
    expect(hasIdleAliveCodingAgent({
      has_retained_tmux_session: true,
      last_activity: new Date(now - RETAINED_TMUX_ACTIVE_WINDOW_MS - 1000).toISOString(),
    }, now)).toBe(false)
  })

  it('shows a live pane with an unknown/unparseable timestamp (pane liveness is the signal)', () => {
    expect(hasIdleAliveCodingAgent({ has_retained_tmux_session: true, last_activity: '' }, now)).toBe(true)
    expect(hasIdleAliveCodingAgent({ has_retained_tmux_session: true, last_activity: 'not-a-date' }, now)).toBe(true)
  })
})
