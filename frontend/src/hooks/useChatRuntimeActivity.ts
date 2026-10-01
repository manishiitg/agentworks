import { useEffect, useMemo, useRef } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useChatStore } from '../stores/useChatStore'
import { chatRuntimeActivity } from '../utils/chatRuntimeActivity'

export function useChatRuntimeActivity(tabId: string | null | undefined) {
  // Subscribe only to lifecycle fields, never to streamed tokens or drafts.
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
  const busy = Boolean(tab.isStreaming || tab.hasRunningBgAgents)
  const previous = useRef<{ sessionId: string; busy: boolean } | null>(null)
  useEffect(() => {
    const last = previous.current
    previous.current = tab.sessionId ? { sessionId: tab.sessionId, busy } : null
    if (!tab.sessionId || (last?.sessionId === tab.sessionId && last.busy === busy) || (!last && !busy)) return
    // Refresh at the same live busy-state boundary the composer used. This
    // also works in read-only chat views with no mounted composer.
    void useChatStore.getState().getActiveSessions(true).catch(error => {
      console.warn('[ChatRuntimeActivity] Failed to refresh active sessions', error)
    })
  }, [tab.sessionId, busy])
  return useMemo(() => chatRuntimeActivity(tab, session), [tab, session])
}
