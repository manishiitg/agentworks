import type { ActiveSessionInfo } from '../services/api-types'
import { runtimeHasBackgroundAgents, runtimeNeedsUserInput, sessionRuntimeStatus } from './runtimeActivity'
import { isScheduledSession } from './workflowSessionKinds'

export function normalizedActivityStatus(status?: string): string {
  return (status || '').toLowerCase().trim()
}

/**
 * Product project conversations use the durable `<product-id>:project:<id>`
 * identity. They are deliberately visible inside their own product surfaces,
 * but AgentWorks' global monitor represents AgentWorks work only. Keep the
 * recognition structural rather than hard-coding a product so future
 * project-based products inherit the boundary automatically.
 */
export function isProductProjectSession(session: Pick<ActiveSessionInfo, 'session_id' | 'workspace_path'>): boolean {
  if (/^[a-z][a-z0-9-]*:project:/i.test(session.session_id.trim())) return true

  // Product conversations created through the older UUID session path still
  // carry their authoritative product scope in workspace_path. Recognize both
  // the user-relative and canonical `_users/<id>/...` forms so product activity
  // never leaks into AgentWorks' global monitor.
  const workspacePath = (session.workspace_path || '').trim().replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  return /(?:^|\/)chats\/[^/]+\/projects\/[^/]+(?:\/|$)/i.test(workspacePath)
}

export function isTerminalActivityStatus(status?: string): boolean {
  const normalized = normalizedActivityStatus(status)
  return normalized === 'completed' ||
    normalized === 'stopped' ||
    normalized === 'error' ||
    normalized === 'failed' ||
    normalized === 'cancelled' ||
    normalized === 'canceled'
}

export function hasLiveBackgroundAgents(
  session: Pick<ActiveSessionInfo, 'status' | 'runtime_state' | 'display_status' | 'has_running_background_agents' | 'running_background_agent_count'>,
): boolean {
  if (session.runtime_state) return runtimeHasBackgroundAgents(session)
  if (isTerminalActivityStatus(session.status)) return false
  return runtimeHasBackgroundAgents(session)
}

const ACTIVE_SESSION_WORK_STATUSES = new Set([
  'running',
  'active',
  'in_progress',
  'paused',
  'waiting',
  'waiting_feedback',
])

export function hasActiveSessionWork(
  session?: Pick<ActiveSessionInfo, 'status' | 'runtime_state' | 'display_status' | 'needs_user_input' | 'has_running_background_agents' | 'running_background_agent_count'> | null,
): boolean {
  if (!session || sessionRuntimeStatus(session) === 'stopped') return false
  if (session.runtime_state) return sessionRuntimeStatus(session) === 'busy' || session.runtime_state.waiting_for_user || runtimeHasBackgroundAgents(session)
  return ACTIVE_SESSION_WORK_STATUSES.has(normalizedActivityStatus(session.status)) ||
    session.needs_user_input === true ||
    hasLiveBackgroundAgents(session)
}

/**
 * Title for a non-workflow activity item. Scheduled agent sessions
 * must never expose their scheduler envelope or task prompt as a UI label.
 * New backend sessions provide `title`; the generic fallback covers sessions
 * created by an older backend and restored history that predates that field.
 */
export function nonWorkflowActivityTitle(
  session: Pick<ActiveSessionInfo, 'session_id' | 'triggered_by' | 'current_execution_name' | 'title' | 'query'>,
): string {
  const explicitTitle = session.current_execution_name?.trim() || session.title?.trim()
  if (explicitTitle) return explicitTitle
  if (isScheduledSession({ sessionId: session.session_id, triggeredBy: session.triggered_by })) {
    return 'Scheduled agent task'
  }
  return session.query?.trim() || 'Agent chat'
}

// A main-agent coding CLI may retain its tmux pane after a turn finishes so the
// next message can reuse it. This helper describes process liveness; a ready idle
// pane is not active work and therefore does not belong in the global monitor.
export const RETAINED_TMUX_ACTIVE_WINDOW_MS = 30 * 60 * 1000
export function hasIdleAliveCodingAgent(
  session: Pick<ActiveSessionInfo, 'has_retained_tmux_session' | 'last_activity'>,
  now: number = Date.now(),
): boolean {
  if (session.has_retained_tmux_session !== true) return false
  const last = session.last_activity ? Date.parse(session.last_activity) : NaN
  // Unknown/unparseable timestamp: the live pane is the primary signal — show it
  // (the backend reaper still bounds how long the pane itself stays alive).
  if (Number.isNaN(last)) return true
  return now - last < RETAINED_TMUX_ACTIVE_WINDOW_MS
}

/**
 * Canonical visibility rule for live activity surfaces such as the global
 * monitor and Quick Switcher's `@active` scope. Keep this shared so the
 * monitor's overflow count cannot disagree with the expanded list.
 */
export function isVisibleActivitySession(
  session: ActiveSessionInfo,
  _now: number = Date.now(),
): boolean {
  const status = sessionRuntimeStatus(session)

  // Scheduled runs disappear once settled. A retained terminal from an old
  // schedule is not an interactive session the user can resume.
  if (isScheduledSession({ sessionId: session.session_id, triggeredBy: session.triggered_by })) {
    return runtimeNeedsUserInput(session) || status === 'busy' || hasLiveBackgroundAgents(session)
  }

  return runtimeNeedsUserInput(session) ||
    hasLiveBackgroundAgents(session) ||
    status === 'busy'
}
