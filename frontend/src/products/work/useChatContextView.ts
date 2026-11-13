import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'

// Display refresh only: the server remains the source for context and account
// usage. A compaction can happen without the foreground turn ending.
export function useChatContextView(sessionId: string, busy = false) {
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
    const interval = window.setInterval(refresh, 5000)
    window.addEventListener('agentworks:chat-context-changed', changed)
    document.addEventListener('visibilitychange', refresh)
    return () => {
      cancelled = true
      window.clearInterval(interval)
      window.removeEventListener('agentworks:chat-context-changed', changed)
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [sessionId, busy])
  return view?.session_id === sessionId ? view : null
}
