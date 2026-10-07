import { useShallow } from 'zustand/react/shallow'
import { useChatStore } from '../stores/useChatStore'
import { foregroundTurnState } from '../utils/foregroundTurnActivity'
import { chatRuntimeActivity } from '../utils/chatRuntimeActivity'

// True while the transcript shows the turn as working: the tab's own flags, or
// the same event/active-session classification the "Working…" row reads
// (useChatRuntimeActivity). Stop keys off this so it is never missing while
// "Working…" is shown (PLAT-699: the tab flags dropped while the transcript
// still said Working, leaving only Send). Read-only: no refresh side effect.
export function useTabTurnActive(tabId: string | null | undefined): boolean {
  const tab = useChatStore(useShallow(state => {
    const current = tabId ? state.chatTabs[tabId] : undefined
    const foreground = foregroundTurnState(current?.sessionId ? state.tabEvents[current.sessionId] : undefined)
    return {
      sessionId: current?.sessionId,
      isStreaming: current?.isStreaming,
      hasRunningBgAgents: current?.hasRunningBgAgents,
      isCompleted: current?.isCompleted,
      foregroundTurnCompleted: foreground === 'completed',
      foregroundTurnStarted: foreground === 'running',
    }
  }))
  const session = useChatStore(state => tab.sessionId
    ? state.activeSessionsCache.find(item => item.session_id === tab.sessionId)
    : undefined)
  if (tab.isStreaming || tab.hasRunningBgAgents) return true
  return chatRuntimeActivity(tab, session).state === 'running'
}
