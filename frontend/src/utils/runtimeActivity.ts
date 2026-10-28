import type { ActiveSessionInfo, RuntimeSnapshot, SessionExecutionTreeResponse, TerminalSnapshot } from '../services/api-types'
import { isMainAgentTerminal } from './terminalIdentity'

export type RuntimeDisplayStatus = 'busy' | 'idle' | 'stopped'

export function runtimeDisplayStatus(runtime?: RuntimeSnapshot | null): RuntimeDisplayStatus | undefined {
  switch (runtime?.phase) {
    case 'starting':
    case 'running':
      return 'busy'
    case 'completed':
    case 'failed':
    case 'canceled':
      return 'stopped'
    case 'waiting':
    case 'idle':
      return 'idle'
    default:
      return undefined
  }
}

export function sessionRuntimeStatus(session?: Pick<ActiveSessionInfo, 'runtime_state' | 'display_status' | 'status'> | null): RuntimeDisplayStatus {
  if (!session) return 'idle'
  return runtimeDisplayStatus(session.runtime_state) || session.display_status || legacyDisplayStatus(session.status)
}

export function executionTreeRuntimeStatus(tree?: SessionExecutionTreeResponse | null): RuntimeDisplayStatus | undefined {
  return runtimeDisplayStatus(tree?.runtime_state) || tree?.summary.display_status
}

export function runtimeHasBackgroundAgents(session?: Pick<ActiveSessionInfo, 'runtime_state' | 'has_running_background_agents' | 'running_background_agent_count'> | null): boolean {
  if (!session) return false
  if (session.runtime_state) {
    return runtimeDisplayStatus(session.runtime_state) !== 'stopped' && session.runtime_state.background_live
  }
  return session.has_running_background_agents === true || (session.running_background_agent_count ?? 0) > 0
}

/** The server's verdict that the last turn ended without a response; the next turn clears it. */
export function runtimeTurnFailed(session?: Pick<ActiveSessionInfo, 'runtime_state'> | null): boolean {
  return session?.runtime_state?.phase === 'failed'
}

/** A failure the person has not looked at: it ended after they last opened this chat. */
export function runtimeTurnFailedUnseen(session: Pick<ActiveSessionInfo, 'runtime_state' | 'last_activity'> | null | undefined, lastOpenedAt = 0): boolean {
  if (!runtimeTurnFailed(session)) return false
  const endedAt = Date.parse(session?.runtime_state?.completed_at || session?.last_activity || '')
  return Number.isFinite(endedAt) && endedAt > lastOpenedAt
}

export function runtimeNeedsUserInput(session?: Pick<ActiveSessionInfo, 'runtime_state' | 'needs_user_input'> | null): boolean {
  if (!session) return false
  return session.runtime_state?.waiting_for_user ?? session.needs_user_input === true
}

type RuntimeExecutionRecord = {
  id: string
  status: string
  startedAt?: string
  completedAt?: string
}

const LIVE_EXECUTION_STATUSES = new Set(['active', 'in_progress', 'pending', 'queued', 'running', 'starting'])
const FAILED_EXECUTION_STATUSES = new Set(['canceled', 'cancelled', 'error', 'failed', 'stale'])
const SETTLED_SESSION_STATUSES = new Set(['canceled', 'cancelled', 'completed', 'error', 'failed', 'stopped'])
const GENERIC_EXECUTION_ID_TOKENS = new Set([
  'agent', 'main', 'pulse', 'review', 'reviewer', 'step', 'workflow', 'workshop',
])

function normalizedExecutionTokens(value: string): string[] {
  return value
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .map(token => token.replace(/s$/, ''))
    .filter(token => (
      token.length > 2 &&
      !/^\d+$/.test(token) &&
      !GENERIC_EXECUTION_ID_TOKENS.has(token)
    ))
}

function terminalRuntimeIdentities(terminal: TerminalSnapshot): string[] {
  return [
    terminal.terminal_id,
    terminal.owner_id,
    terminal.execution_id,
    terminal.parent_step_id,
  ].filter((value): value is string => Boolean(value))
}

function runtimeRecordMatchesTerminal(recordID: string, terminal: TerminalSnapshot): boolean {
  const identities = terminalRuntimeIdentities(terminal)
  if (identities.some(identity => identity === recordID || identity.includes(recordID))) return true

  // Wrapper terminals use IDs such as `agent:workshop-review-costs-*`, while
  // the tracked execution is `review-costs-*`. Compare the meaningful slug
  // tokens so both panes inherit the same authoritative lifecycle.
  const recordTokens = normalizedExecutionTokens(recordID)
  if (recordTokens.length === 0) return false
  return identities.some(identity => {
    const identityTokens = new Set(normalizedExecutionTokens(identity))
    return recordTokens.every(token => identityTokens.has(token))
  })
}

function runtimeExecutionRecords(runtime: RuntimeSnapshot): RuntimeExecutionRecord[] {
  return [
    ...(runtime.child_executions || []).map(record => ({
      id: record.execution_id,
      status: record.status,
      startedAt: record.started_at,
      completedAt: record.completed_at,
    })),
    ...(runtime.background_agents || []).map(record => ({
      id: record.agent_id,
      status: record.status,
      startedAt: record.created_at,
      completedAt: record.completed_at,
    })),
  ]
}

function runtimeRecordTime(record: RuntimeExecutionRecord): number {
  const parsed = Date.parse(record.completedAt || record.startedAt || '')
  return Number.isNaN(parsed) ? 0 : parsed
}

/**
 * Whether this terminal is doing work now, as opposed to merely retaining a
 * live interactive CLI process for the next message.
 */
export function terminalTurnIsBusy(
  terminal: TerminalSnapshot,
  runtime?: RuntimeSnapshot | null,
): boolean {
  if (isMainAgentTerminal(terminal) && runtime) {
    return runtime.foreground_turn.busy
  }

  if (runtime) {
    const matches = runtimeExecutionRecords(runtime)
      .filter(record => runtimeRecordMatchesTerminal(record.id, terminal))
    if (matches.length > 0) {
      return matches.some(record => LIVE_EXECUTION_STATUSES.has(record.status.trim().toLowerCase()))
    }
  }

  const state = (terminal.state || '').trim().toLowerCase()
  return terminal.active && (state === 'running' || state === 'starting')
}

/** Whether this conversation still owes the user a response. */
export function terminalConversationHasPendingWork(
  terminal: TerminalSnapshot,
  runtime?: RuntimeSnapshot | null,
  siblingTerminals: TerminalSnapshot[] = [],
): boolean {
  if (!isMainAgentTerminal(terminal)) {
    return terminalTurnIsBusy(terminal, runtime)
  }

  const hasBusyChildTerminal = siblingTerminals.some(sibling => (
    sibling.terminal_id !== terminal.terminal_id &&
    sibling.session_id === terminal.session_id &&
    !isMainAgentTerminal(sibling) &&
    terminalTurnIsBusy(sibling, runtime)
  ))
  if (!runtime) {
    return terminalTurnIsBusy(terminal) || hasBusyChildTerminal
  }

  // A main-agent foreground turn can be idle while an asynchronous child is
  // running. The conversation is still in progress until the main agent
  // consumes that completion and emits its final response.
  return runtimeDisplayStatus(runtime) !== 'stopped' && (
    runtime.foreground_turn.busy || runtime.background_live || hasBusyChildTerminal
  )
}

export function reconcileTerminalRuntimeState(
  terminal: TerminalSnapshot,
  runtime?: RuntimeSnapshot | null,
): TerminalSnapshot {
  const appearsRunning = terminal.active || terminal.state === 'running'
  if (!appearsRunning || !runtime) return terminal

  const matches = runtimeExecutionRecords(runtime)
    .filter(record => runtimeRecordMatchesTerminal(record.id, terminal))
    .sort((left, right) => runtimeRecordTime(right) - runtimeRecordTime(left))

  if (matches.some(record => LIVE_EXECUTION_STATUSES.has(record.status.trim().toLowerCase()))) {
    return terminal
  }

  const sessionSettled = SETTLED_SESSION_STATUSES.has((runtime.raw_session_status || '').trim().toLowerCase())
  if (matches.length === 0 && (!sessionSettled || runtime.foreground_turn.busy)) return terminal

  const latest = matches[0]
  const failed = latest && FAILED_EXECUTION_STATUSES.has(latest.status.trim().toLowerCase())
  return {
    ...terminal,
    active: false,
    state: failed ? 'failed' : 'completed',
  }
}

function legacyDisplayStatus(status?: string): RuntimeDisplayStatus {
  switch ((status || '').trim().toLowerCase()) {
    case 'running':
    case 'active':
    case 'in_progress':
    case 'paused':
      return 'busy'
    case 'completed':
    case 'stopped':
    case 'error':
    case 'failed':
    case 'cancelled':
    case 'canceled':
      return 'stopped'
    default:
      return 'idle'
  }
}
