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

// Tab lifecycle flags are projected from server SSE/REST status (with immediate
// send feedback). Transcript content and generation events never decide completion.
// A retained CLI process alone never means that a turn is running.
export function chatRuntimeActivity(tab: ChatActivityTab, session?: ActiveSessionInfo): ChatRuntimeActivity {
  const tone = session ? statusTone(session) : 'idle'
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
