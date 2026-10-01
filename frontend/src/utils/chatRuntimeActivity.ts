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
}

// The open chat shares the monitor's classification, with immediate tab-local
// start/completion signals to bridge the active-session cache's refresh delay.
// A retained CLI process alone never means that a turn is running.
export function chatRuntimeActivity(tab: ChatActivityTab, session?: ActiveSessionInfo): ChatRuntimeActivity {
  const inFlight = Boolean(tab.isStreaming || tab.hasRunningBgAgents)
  if (tab.isCompleted && !inFlight) return { state: 'ready', label: 'idle' }
  const tone = session ? statusTone(session) : 'idle'
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
