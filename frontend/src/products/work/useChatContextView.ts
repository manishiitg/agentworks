import { useEffect, useState } from 'react'
import { useChatStore } from '../../stores/useChatStore'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'

// Display refresh only: the server remains the source for context and account
// usage. A compaction can happen without the foreground turn ending.
export function useChatContextView(sessionId: string, busy = false) {
  const revision = useChatStore(state => {
    const events = state.tabEvents[sessionId] || []
    for (let i = events.length - 1; i >= 0; i--) {
      const event = events[i]
      if (['status_line', 'token_usage', 'context_compaction', 'unified_completion', 'conversation_end', 'conversation_error'].includes(event.type)) return event.id
    }
    return undefined
  })
  const [view, setView] = useState<ChatContextView | null>(null)
  useEffect(() => {
    let cancelled = false
    const refresh = () => {
      if (!sessionId || document.visibilityState === 'hidden') return
      void agentApi.getChatContext(sessionId).then(next => {
        if (!cancelled) setView(next)
      }).catch(() => undefined)
    }
    refresh()
    const changed = (event: Event) => {
      const next = (event as CustomEvent<ChatContextView>).detail
      if (next?.session_id === sessionId) setView(next)
    }
    const interval = busy ? window.setInterval(refresh, 30000) : undefined
    window.addEventListener('agentworks:chat-context-changed', changed)
    document.addEventListener('visibilitychange', refresh)
    return () => {
      cancelled = true
      if (interval !== undefined) window.clearInterval(interval)
      window.removeEventListener('agentworks:chat-context-changed', changed)
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [sessionId, busy, revision])
  return view?.session_id === sessionId ? view : null
}
