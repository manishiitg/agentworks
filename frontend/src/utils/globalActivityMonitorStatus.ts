import type { ActiveSessionInfo, RunningWorkflowInfo } from '../services/api-types'
import {
  hasIdleAliveCodingAgent,
  hasLiveBackgroundAgents,
  isVisibleActivitySession,
  normalizedActivityStatus,
} from './activitySessions'
import { runtimeNeedsUserInput, sessionRuntimeStatus } from './runtimeActivity'
import { isInternalChildSession } from './workflowSessionKinds'

// The global monitor is the user's live activity indicator. A 30-second cache
// made a newly-started schedule look idle for an entire turn of attention even
// though the backend had already marked it busy. Keep this bounded and explicit;
// the monitor force-refreshes on this cadence while other consumers may retain
// the broader active-session cache for cheaper background checks. 10s (not
// 5s): at 5s this one poll was the app's single largest request source.
export const GLOBAL_ACTIVITY_REFRESH_MS = 10_000

function isWorkflowSession(session: ActiveSessionInfo): boolean {
  return session.agent_mode?.toLowerCase().includes('workflow') ?? false
}

export function headerStatusLabel(session: ActiveSessionInfo, workflow?: RunningWorkflowInfo): string {
  if (runtimeNeedsUserInput(session)) return 'waiting for input'
  const hasBackgroundAgents = hasLiveBackgroundAgents(session)
  if (session.runtime_state) {
    const status = sessionRuntimeStatus(session)
    if (status === 'idle') return hasBackgroundAgents ? 'waiting for background agents' : 'idle'
    // A completed foreground turn can retain its coding CLI tmux so the user
    // can continue the conversation. The authoritative runtime is correctly
    // stopped, but the retained pane is idle—not active work. Resolve this
    // before the generic stopped branch so the header renders a clock instead
    // of a spinner. A genuinely busy runtime still falls through to running.
    if (status === 'stopped' && hasIdleAliveCodingAgent(session)) return 'idle'
    if (status === 'stopped') return 'stopped'
    return hasBackgroundAgents && !session.runtime_state.foreground_turn.busy
      ? 'background running'
      : 'running'
  }
  const status = normalizedActivityStatus(workflow?.status || session.status)
  if (status === 'paused') return 'paused'
  if (status === 'idle') return 'idle'
  if ((status === 'waiting' || status === 'waiting_feedback') && hasBackgroundAgents) return 'waiting for background agents'
  if (status === 'waiting' || status === 'waiting_feedback') return 'waiting'
  if ((status === 'completed' || status === 'idle') && hasBackgroundAgents) return 'background running'
  // Idle-but-alive coding CLI (backend marked it completed once the turn ended,
  // but the tmux agent is still up waiting for input): show it as idle (clock),
  // never as a spinner. A genuinely-running session keeps status "running".
  if ((status === 'completed' || status === 'idle') && hasIdleAliveCodingAgent(session)) return 'idle'
  if (status === 'completed' && isWorkflowSession(session)) return 'idle'
  return status || 'running'
}

// The narrow slice of a ChatTab this needs. Kept local instead of importing
// the store's ChatTab type to avoid coupling this pure-logic module to
// useChatStore.
interface ActiveTabForCurrentSession {
  sessionId: string | null
  metadata?: { mode?: string }
}

/**
 * Identifies which session, if any, the user is currently looking at, so a
 * header component can render its live status.
 *
 * This must stay the single definition of "current session": before it
 * existed, GlobalActivityMonitor computed this inline to exclude the current
 * session from its pills, and the header's plain workflow-name selector had
 * no equivalent — so a session could be visibly running while the header's
 * live-status pills genuinely never showed it anywhere, on the theory that
 * the plain selector already covered it. It never carried live status.
 * Two independent inline copies of this same lookup is exactly the failure
 * mode that produced that gap; callers must share this one.
 */
export function currentSessionId(
  activeTabId: string | null,
  chatTabs: Record<string, ActiveTabForCurrentSession>,
  selectedModeCategory: string | null,
  isOrganizationView: boolean,
): string | null {
  if (isOrganizationView || !activeTabId) return null
  const activeTab = chatTabs[activeTabId]
  if (!activeTab || activeTab.metadata?.mode !== selectedModeCategory) return null
  return activeTab.sessionId ?? null
}

/**
 * Resolves the full ActiveSessionInfo for currentSessionId(...), applying the
 * same visibility rules GlobalActivityMonitor's pill list applies, so a
 * session that would never render as a pill (internal child, not otherwise
 * visible) also does not render a spurious status on the header selector.
 */
export function currentActiveSession(
  activeSessionsCache: ActiveSessionInfo[],
  sessionId: string | null,
): ActiveSessionInfo | null {
  if (!sessionId) return null
  return activeSessionsCache.find(session =>
    session.session_id === sessionId &&
    !isInternalChildSession({ parentSessionId: session.parent_session_id, sessionKind: session.session_kind }) &&
    isVisibleActivitySession(session)
  ) ?? null
}

/**
 * The global monitor shows work the user is NOT looking at. The session open
 * in the chat surface already carries its live status on the workflow
 * selector (ModePresetBar's dot and tooltip), so repeating it as a pill on
 * the right showed the same workflow twice on one bar. History: the current
 * session was excluded, then included on 2026-08-17 when the selector's
 * indicator was removed, then the selector's indicator came back on
 * 2026-08-21 without this exclusion returning. Exclusion is by session id
 * only: a scheduled run of the same workflow is a different session with its
 * own lifecycle and stays visible.
 */
export function visibleActivitySessions(
  activeSessions: ActiveSessionInfo[],
  currentSessionId: string | null,
  representedSessionIds: readonly string[] = [],
): ActiveSessionInfo[] {
  const represented = new Set(representedSessionIds)
  return activeSessions.filter(session => session.session_id !== currentSessionId &&
    (!represented.has(session.session_id) || hasLiveBackgroundAgents(session)))
}

export function statusTone(
  session: ActiveSessionInfo,
  workflow?: RunningWorkflowInfo,
): 'running' | 'needs-input' | 'paused' | 'background' | 'idle' {
  const status = headerStatusLabel(session, workflow)
  if (status === 'waiting for input') return 'needs-input'
  if (status === 'idle' || status === 'waiting') return 'idle'
  if (status === 'paused') return 'paused'
  if (status === 'background running' || status === 'waiting for background agents') return 'background'
  return 'running'
}
