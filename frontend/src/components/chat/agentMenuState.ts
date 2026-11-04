import { useChatStore } from '../../stores/useChatStore'
import { useChatRuntimeActivity } from '../../hooks/useChatRuntimeActivity'
import { runtimeTurnFailedUnseen } from '../../utils/runtimeActivity'

export type AgentMenuState = 'waiting' | 'running' | 'failed' | 'done' | null

/** One agent tab's state for its mark: needs you, working, failed (not seen yet), a reply not read yet, or nothing. */
export function useAgentMenuState(tabId: string, workspaceActivity?: 'running' | 'waiting' | null): AgentMenuState {
  const activity = useChatRuntimeActivity(tabId)
  const failed = useChatStore(state => {
    const tab = state.chatTabs[tabId]
    return !!tab?.sessionId && state.activeTabId !== tabId && runtimeTurnFailedUnseen(state.activeSessionsCache.find(session => session.session_id === tab.sessionId), tab.lastAccessedAt)
  })
  const done = useChatStore(state => state.activeTabId !== tabId && !!state.chatTabs[tabId]?.hasUnreadCompletion)
  if (activity.state === 'waiting' || workspaceActivity === 'waiting') return 'waiting'
  if (activity.state === 'running' || workspaceActivity === 'running') return 'running'
  return failed ? 'failed' : done ? 'done' : null
}

