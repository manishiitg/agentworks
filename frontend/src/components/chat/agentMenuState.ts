import { useEffect, useState } from 'react'
import { useChatStore, type ChatTab } from '../../stores/useChatStore'
import { foregroundTurnState } from '../../utils/foregroundTurnActivity'
import { chatRuntimeActivity } from '../../utils/chatRuntimeActivity'
import { runtimeNeedsUserInput, runtimeTurnFailedUnseen } from '../../utils/runtimeActivity'
import { recordTabMarkTelemetry } from '../../utils/chatDeliveryTelemetry'

export type AgentMenuState = 'waiting' | 'running' | 'failed' | 'done' | null

/** One chat tab's status, the single source for its tab pill, its left-menu row, its icon in the collapsed rail and
 * its team-board card (owner, 4 Nov: "the dots and loading we had before are important"): needs you, working, failed
 * (until opened), a reply not read yet, or nothing. workspaceActivity, when given (a Crew's Main), decides needs-you
 * and working for the whole Crew. given is the caller's own copy of the tab, read when the store does not hold it.
 * Working holds 1.2 s after it ends so a quick tool turn does not flicker. */
export function useAgentMenuState(tabId: string, workspaceActivity?: 'running' | 'waiting' | null, given?: Partial<Omit<ChatTab, 'sessionId'>> & { sessionId?: string | null }): AgentMenuState {
  const isActive = useChatStore(state => state.activeTabId === tabId)
  // The same signal as the activity monitor and the chat's own footer.
  const needsInput = useChatStore(state => {
    const sessionId = (state.chatTabs[tabId] ?? given)?.sessionId
    return !!sessionId && runtimeNeedsUserInput(state.activeSessionsCache.find(session => session.session_id === sessionId))
  })
  // Red only until the person opens the chat (the same last-opened time that clears the reply dot).
  const failed = useChatStore(state => {
    const tab = state.chatTabs[tabId] ?? given
    return !isActive && !!tab?.sessionId && runtimeTurnFailedUnseen(state.activeSessionsCache.find(session => session.session_id === tab.sessionId), tab.lastAccessedAt)
  })
  // The same busy signal as the chat header: a stored completion ends the spinner at once. It returns the signal that
  // keeps the tab working (logged below), or null.
  const runningReason = useChatStore(state => {
    const stored = state.chatTabs[tabId] ?? given
    if (!stored) return null
    const foreground = foregroundTurnState(stored.sessionId ? state.tabEvents[stored.sessionId] : undefined)
    const session = stored.sessionId ? state.activeSessionsCache.find(item => item.session_id === stored.sessionId) : undefined
    const activity = chatRuntimeActivity({
      isStreaming: stored.isStreaming,
      hasRunningBgAgents: stored.hasRunningBgAgents,
      isCompleted: stored.isCompleted,
      foregroundTurnCompleted: foreground === 'completed',
      foregroundTurnStarted: foreground === 'running',
    }, session)
    if (activity.state !== 'running') return null
    const where = state.chatTabs[tabId] ? '' : 'not_in_store:'
    if (foreground === 'completed') return `${where}background_after_completion`
    if (foreground === 'running') return `${where}turn_started_no_end_event`
    if (stored.isStreaming) return `${where}tab_streaming_flag`
    if (stored.hasRunningBgAgents) return `${where}tab_background_flag`
    return `${where}server:${activity.label}`
  })
  const running = runningReason !== null
  const sessionId = useChatStore(state => (state.chatTabs[tabId] ?? given)?.sessionId) || ''
  useEffect(() => {
    if (!runningReason || workspaceActivity !== undefined) return
    recordTabMarkTelemetry(sessionId, tabId, 'on', runningReason)
    const timer = setTimeout(() => recordTabMarkTelemetry(sessionId, tabId, 'still_on', runningReason), 60_000)
    return () => clearTimeout(timer)
  }, [runningReason, sessionId, tabId, workspaceActivity])
  const done = useChatStore(state => !isActive && !!(state.chatTabs[tabId] ?? given)?.hasUnreadCompletion)
  const waiting = workspaceActivity === undefined ? needsInput : needsInput || workspaceActivity === 'waiting'
  const working = workspaceActivity === undefined ? running : workspaceActivity === 'running'
  const raw: AgentMenuState = waiting ? 'waiting' : working ? 'running' : failed ? 'failed' : done ? 'done' : null
  const [state, setState] = useState(raw)
  useEffect(() => {
    if (raw === state) return
    if (state === 'running' && raw !== 'waiting') {
      const timer = setTimeout(() => setState(raw), 1200)
      return () => clearTimeout(timer)
    }
    setState(raw)
  }, [raw, state])
  return state
}
