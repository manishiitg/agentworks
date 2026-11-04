import { useEffect, useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { foregroundTurnState } from '../../utils/foregroundTurnActivity'
import { chatRuntimeActivity } from '../../utils/chatRuntimeActivity'
import { runtimeNeedsUserInput, runtimeTurnFailedUnseen } from '../../utils/runtimeActivity'

export type AgentMenuState = 'waiting' | 'running' | 'failed' | 'done' | null

/** One chat tab's status, the single source for its tab pill, its left-menu row, its icon in the collapsed rail and
 * its team-board card (owner, 4 Nov: "the dots and loading we had before are important"): needs you, working, failed
 * (until opened), a reply not read yet, or nothing. workspaceActivity, when given (a Crew's Main), decides needs-you
 * and working for the whole Crew. Working holds 1.2 s after it ends so a quick tool turn does not flicker. */
export function useAgentMenuState(tabId: string, workspaceActivity?: 'running' | 'waiting' | null, fallbackRunning = false): AgentMenuState {
  const isActive = useChatStore(state => state.activeTabId === tabId)
  // The same signal as the activity monitor and the chat's own footer.
  const needsInput = useChatStore(state => {
    const sessionId = state.chatTabs[tabId]?.sessionId
    return !!sessionId && runtimeNeedsUserInput(state.activeSessionsCache.find(session => session.session_id === sessionId))
  })
  // Red only until the person opens the chat (the same last-opened time that clears the reply dot).
  const failed = useChatStore(state => {
    const tab = state.chatTabs[tabId]
    return !isActive && !!tab?.sessionId && runtimeTurnFailedUnseen(state.activeSessionsCache.find(session => session.session_id === tab.sessionId), tab.lastAccessedAt)
  })
  // The same busy signal as the chat header: a stored completion ends the spinner at once.
  const running = useChatStore(state => {
    const stored = state.chatTabs[tabId]
    if (!stored) return fallbackRunning
    const foreground = foregroundTurnState(stored.sessionId ? state.tabEvents[stored.sessionId] : undefined)
    const session = stored.sessionId ? state.activeSessionsCache.find(item => item.session_id === stored.sessionId) : undefined
    return chatRuntimeActivity({
      isStreaming: stored.isStreaming,
      hasRunningBgAgents: stored.hasRunningBgAgents,
      isCompleted: stored.isCompleted,
      foregroundTurnCompleted: foreground === 'completed',
      foregroundTurnStarted: foreground === 'running',
    }, session).state === 'running'
  })
  const done = useChatStore(state => !isActive && !!state.chatTabs[tabId]?.hasUnreadCompletion)
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
