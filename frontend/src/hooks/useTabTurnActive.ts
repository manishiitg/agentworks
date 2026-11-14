import { useShallow } from 'zustand/react/shallow'
import { useChatStore } from '../stores/useChatStore'
import { chatRuntimeActivity } from '../utils/chatRuntimeActivity'
import { runtimeNeedsUserInput } from '../utils/runtimeActivity'

const BACKGROUND_ONLY_LABELS = new Set(['background running', 'waiting for background agents'])

// True while the transcript shows the turn as working: the tab's own flags, or
// the same event/active-session classification the "Working…" row reads
// (useChatRuntimeActivity). Stop keys off this so it is never missing while
// "Working…" is shown (PLAT-699: the tab flags dropped while the transcript
// still said Working, leaving only Send). Read-only: no refresh side effect.
//
// scope 'foreground' (the composer Stop, PLAT-705) counts only the CLI's own
// turn: background work the chat started has its own "N running" pill, and a
// turn waiting on the user's answer has no Stop. 'any' also counts background
// work (the scheduled-run footer, which stops the whole run).
export function useTabTurnActive(tabId: string | null | undefined, scope: 'any' | 'foreground' = 'any'): boolean {
  const tab = useChatStore(useShallow(state => {
    const current = tabId ? state.chatTabs[tabId] : undefined
    return {
      sessionId: current?.sessionId,
      isStreaming: current?.isStreaming,
      hasRunningBgAgents: current?.hasRunningBgAgents,
      isCompleted: current?.isCompleted,
    }
  }))
  const session = useChatStore(state => tab.sessionId
    ? state.activeSessionsCache.find(item => item.session_id === tab.sessionId)
    : undefined)
  if (scope === 'foreground') {
    if (session && runtimeNeedsUserInput(session)) return false
    const activity = chatRuntimeActivity({ ...tab, hasRunningBgAgents: false }, session)
    return activity.state === 'running' && !BACKGROUND_ONLY_LABELS.has(activity.label)
  }
  return chatRuntimeActivity(tab, session).state === 'running'
}
