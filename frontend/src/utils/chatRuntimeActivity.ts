import type { ActiveSessionInfo } from '../services/api-types'
import { headerStatusLabel, statusTone } from './globalActivityMonitorStatus'
import { sessionStreamingState } from './sessionStreamingState'
import { runtimeHasBackgroundAgents } from './runtimeActivity'

export type ChatRuntimeActivity = {
  state: 'running' | 'waiting' | 'ready'
  label: string
}

export type ChatActivityTab = {
  isStreaming?: boolean
  hasRunningBgAgents?: boolean
  isCompleted?: boolean
  foregroundTurnCompleted?: boolean
  foregroundTurnStarted?: boolean
}

// The open chat shares the monitor's classification, with immediate tab-local
// start/completion signals to bridge the active-session cache's refresh delay.
// A retained CLI process alone never means that a turn is running.
export function chatRuntimeActivity(tab: ChatActivityTab, session?: ActiveSessionInfo): ChatRuntimeActivity {
  // A durable completion in this chat wins over stale tab/cache foreground
  // flags. A new user/start event resets this signal before its request begins.
  if (tab.foregroundTurnCompleted) {
    return tab.hasRunningBgAgents || (session && runtimeHasBackgroundAgents(session))
      ? { state: 'running', label: 'background running' }
      : { state: 'ready', label: 'idle' }
  }
  const tone = session ? statusTone(session) : 'idle'
  // A new message/start stays active until its completion. Polling can still
  // describe the previous idle turn while the request is being prepared or
  // accepted; those stale flags must not make the header blink off and on.
  if (tab.foregroundTurnStarted) return tone === 'needs-input'
    ? { state: 'waiting', label: 'waiting for input' }
    : { state: 'running', label: 'running' }
  const inFlight = Boolean(tab.isStreaming || tab.hasRunningBgAgents)
  if (tab.isCompleted && !inFlight) return { state: 'ready', label: 'idle' }
  if (tone === 'needs-input') return { state: 'waiting', label: 'waiting for input' }
  if (inFlight) return {
    state: 'running',
    label: tab.hasRunningBgAgents && !tab.isStreaming ? 'background running' : 'running',
  }
  if (session && (sessionStreamingState(session).isActive || runtimeHasBackgroundAgents(session)) && (tone === 'running' || tone === 'background')) {
    return { state: 'running', label: headerStatusLabel(session) }
  }
  return { state: 'ready', label: 'idle' }
}
