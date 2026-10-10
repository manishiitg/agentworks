import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo } from '../services/api-types'
import {
  currentActiveSession,
  currentSessionId,
  GLOBAL_ACTIVITY_REFRESH_MS,
  headerStatusLabel,
  statusTone,
  visibleActivitySessions,
} from '../utils/globalActivityMonitorStatus'
import { activityTypeLabels, botPlatformLabel, crewActivityTitle } from '../utils/globalActivityPresentation'
import type { ChatTab } from '../stores/useChatStore'

function minimalSession(overrides: Partial<ActiveSessionInfo> = {}): ActiveSessionInfo {
  return {
    session_id: 'session-1',
    observer_id: '',
    agent_mode: 'workflow',
    status: 'running',
    display_status: 'busy',
    workspace_path: 'Workflow/latency-report',
    workflow_name: 'latency-report',
    has_retained_tmux_session: false,
    has_running_background_agents: false,
    running_background_agent_count: 0,
    created_at: new Date().toISOString(),
    last_activity: new Date().toISOString(),
    ...overrides,
  } as ActiveSessionInfo
}

function retainedWorkflowWithRuntime(phase: 'running' | 'completed'): ActiveSessionInfo {
  return {
    session_id: 'latency-report-session',
    observer_id: '',
    agent_mode: 'workflow',
    status: 'completed',
    display_status: 'stopped',
    workspace_path: 'Workflow/latency-report',
    workflow_name: 'latency-report',
    has_retained_tmux_session: true,
    has_running_background_agents: false,
    running_background_agent_count: 0,
    created_at: new Date().toISOString(),
    last_activity: new Date().toISOString(),
    runtime_state: {
      session_id: 'latency-report-session',
      generation: 2,
      revision: 922,
      phase,
      reason: phase === 'completed' ? 'session completed' : 'session running',
      raw_session_status: phase === 'completed' ? 'completed' : 'running',
      foreground_turn: {
        busy: phase === 'running',
        has_cancel: phase === 'running',
        can_steer: phase === 'running',
        synthetic: false,
      },
      child_executions: [],
      background_agents: [],
      background_live: false,
      terminals: [],
      terminal_busy: phase === 'running',
      waiting_for_user: false,
      last_progress_at: new Date().toISOString(),
      started_at: new Date().toISOString(),
      observed_at: new Date().toISOString(),
    },
  } as ActiveSessionInfo
}

describe('global activity monitor status', () => {
  it('refreshes live activity quickly enough not to leave a running workflow on an idle clock', () => {
    expect(GLOBAL_ACTIVITY_REFRESH_MS).toBeLessThanOrEqual(10_000)
  })

  it('shows a clock for the completed latency-report session with a retained idle CLI', () => {
    const session = retainedWorkflowWithRuntime('completed')

    expect(headerStatusLabel(session)).toBe('idle')
    expect(statusTone(session)).toBe('idle')
  })

  it('keeps a genuinely busy retained CLI on the running spinner', () => {
    const session = retainedWorkflowWithRuntime('running')

    expect(headerStatusLabel(session)).toBe('running')
    expect(statusTone(session)).toBe('running')
  })
})

describe('global activity monitor labels', () => {
  it('shows the Crew identity name instead of the generic Chat tab name', () => {
    const tab = {
      name: 'Chat',
      metadata: {
        agentProfileId: 'work',
        agentProfileIdentityName: 'Release crew',
        agentProfileProjectTitle: 'testing',
      },
    } as ChatTab

    expect(crewActivityTitle(tab, 'Agent chat')).toBe('Release crew')
  })

  it('names the bot platform from bot_platform or the session id', () => {
    expect(botPlatformLabel('slack', 'x')).toBe('Slack')
    expect(botPlatformLabel(undefined, 'bot-whatsapp--abc')).toBe('WhatsApp')
    expect(botPlatformLabel('', 'chat-1')).toBe('')
  })

  it('labels every activity type so each row says what started it', () => {
    for (const type of ['Scheduled', 'Webhook', 'Manual', 'Bot', 'Chat'] as const) {
      expect(activityTypeLabels[type]).toBeTruthy()
    }
  })
})

// currentSessionId/currentActiveSession are the single definition of "which
// session is the user currently looking at", shared by GlobalActivityMonitor
// (which excludes it from pills) and ModePresetBar's current-workflow
// selector (which must show its live status instead). Before this was
// shared, the selector had no equivalent lookup at all and only ever showed
// a static dot — a workflow could be visibly running with its status
// invisible in both places at once.
describe('currentSessionId', () => {
  const tabs = {
    'tab-1': { sessionId: 'session-1', metadata: { mode: 'workflow' } },
    'tab-2': { sessionId: null, metadata: { mode: 'workflow' } },
    'tab-3': { sessionId: 'session-3', metadata: { mode: 'multi-agent' } },
  }

  it('returns the session id for the active tab in the current mode', () => {
    expect(currentSessionId('tab-1', tabs, 'workflow', false)).toBe('session-1')
  })

  it('returns null while the Organization overview is showing', () => {
    expect(currentSessionId('tab-1', tabs, 'workflow', true)).toBeNull()
  })

  it('returns null with no active tab', () => {
    expect(currentSessionId(null, tabs, 'workflow', false)).toBeNull()
  })

  it('returns null when the active tab belongs to a different mode', () => {
    // tab-3 is a multi-agent tab; a workflow-mode caller must not adopt it.
    expect(currentSessionId('tab-3', tabs, 'workflow', false)).toBeNull()
  })

  it('returns null when the active tab has no session yet', () => {
    expect(currentSessionId('tab-2', tabs, 'workflow', false)).toBeNull()
  })
})

describe('currentActiveSession', () => {
  it('resolves the matching session from the cache', () => {
    const cache = [minimalSession({ session_id: 'session-1' }), minimalSession({ session_id: 'session-2' })]
    expect(currentActiveSession(cache, 'session-1')?.session_id).toBe('session-1')
  })

  it('returns null when no id is given', () => {
    expect(currentActiveSession([minimalSession()], null)).toBeNull()
  })

  it('returns null when the id matches nothing in the cache', () => {
    expect(currentActiveSession([minimalSession({ session_id: 'session-1' })], 'session-missing')).toBeNull()
  })

  it('excludes an internal child session even if the id matches', () => {
    const cache = [minimalSession({ session_id: 'session-1', parent_session_id: 'parent-session' })]
    expect(currentActiveSession(cache, 'session-1')).toBeNull()
  })
})

describe('visibleActivitySessions', () => {
  it('keeps a concurrent schedule distinct from the current chat in the same workflow', () => {
    const currentTabSession = minimalSession({ session_id: 'chat-tab-session', workflow_name: 'latency-report' })
    const backgroundPulseSession = minimalSession({
      session_id: 'pulse-schedule-session',
      workflow_name: 'latency-report',
      triggered_by: 'cron',
    })
    const otherWorkflowSession = minimalSession({ session_id: 'other-session', workflow_name: 'upwork' })

    const visible = visibleActivitySessions(
      [currentTabSession, backgroundPulseSession, otherWorkflowSession],
      'chat-tab-session',
    )

    // The current session is the selector's job (its dot and tooltip); the
    // monitor shows everything else, including a schedule of the same workflow.
    expect(visible.map(s => s.session_id)).toEqual(['pulse-schedule-session', 'other-session'])
  })

  it('excludes the current session by id even when no workflow key is known', () => {
    const currentTabSession = minimalSession({ session_id: 'chat-tab-session' })
    const other = minimalSession({ session_id: 'other-session', workflow_name: 'upwork' })

    const visible = visibleActivitySessions([currentTabSession, other], 'chat-tab-session')

    expect(visible.map(s => s.session_id)).toEqual(['other-session'])
  })

  it('does not drop a same-named non-workflow session by accident', () => {
    // An empty workflow key must never become a wildcard match against other
    // sessions that also lack one.
    const currentTabSession = minimalSession({ session_id: 'chat-tab-session', agent_mode: 'multi-agent', workflow_name: undefined })
    const other = minimalSession({ session_id: 'other-session', agent_mode: 'multi-agent', workflow_name: undefined })

    const visible = visibleActivitySessions([currentTabSession, other], 'chat-tab-session')

    expect(visible.map(s => s.session_id)).toEqual(['other-session'])
  })

  it('keeps independent sessions for the same non-current workflow', () => {
    const plain = minimalSession({ session_id: 'plain-session', workflow_name: 'upwork', has_retained_tmux_session: false })
    const retained = minimalSession({ session_id: 'retained-session', workflow_name: 'upwork', has_retained_tmux_session: true })

    const visible = visibleActivitySessions([plain, retained], null)

    expect(visible.map(s => s.session_id)).toEqual(['plain-session', 'retained-session'])
  })
})
