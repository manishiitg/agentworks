import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { chatContextChangedEvent } from './ChatContextItems'

/**
 * Whether the server says this chat runs Claude Code in its terminal (the chat's own switch, PLAT-872-100).
 * The provider list only knows the server-wide Claude default, and the running-session list forgets a chat
 * between turns, so the type-in-terminal mode follows this per-chat answer instead.
 */
export function useChatClaudeTerminal(sessionId: string | null | undefined, busy: boolean): boolean {
  const [on, setOn] = useState(false)
  useEffect(() => {
    if (!sessionId) { setOn(false); return }
    let cancelled = false
    agentApi.getChatContext(sessionId)
      .then(view => { if (!cancelled) setOn(view.claude_terminal?.on === true) })
      .catch(() => { if (!cancelled) setOn(false) })
    return () => { cancelled = true }
  }, [sessionId, busy])
  useEffect(() => {
    const changed = (event: Event) => {
      const view = (event as CustomEvent<ChatContextView>).detail
      if (view?.session_id === sessionId) setOn(view.claude_terminal?.on === true)
    }
    window.addEventListener(chatContextChangedEvent, changed)
    return () => window.removeEventListener(chatContextChangedEvent, changed)
  }, [sessionId])
  return on
}
