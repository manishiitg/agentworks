import { useEffect, useState } from 'react'
import { Terminal } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { changeChatContext, chatContextChangedEvent } from './ChatContextItems'

/**
 * One-click Claude Code terminal switch beside the model chip. Shown only when the server offers the switch
 * for this chat (Claude chats); the same server setting as the switch in the model menu.
 */
export default function ClaudeTerminalToggle({ sessionId, modelKey }: { sessionId: string; modelKey?: string }) {
  const [offered, setOffered] = useState(false)
  const [on, setOn] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    const apply = (view: ChatContextView | null) => {
      if (cancelled) return
      setOffered(Boolean(view?.claude_terminal))
      setOn(view?.claude_terminal?.on === true)
    }
    if (sessionId) agentApi.getChatContext(sessionId).then(apply).catch(() => apply(null))
    else apply(null)
    const changed = (event: Event) => {
      const view = (event as CustomEvent<ChatContextView>).detail
      if (view?.session_id === sessionId) apply(view)
    }
    window.addEventListener(chatContextChangedEvent, changed)
    return () => { cancelled = true; window.removeEventListener(chatContextChangedEvent, changed) }
  }, [sessionId, modelKey])

  if (!offered) return null
  const label = on ? 'Claude Code terminal is on. Click to turn it off.' : 'Run Claude Code in a terminal. Click to turn it on.'
  return (
    <Button
      type="button"
      variant={on ? 'secondary' : 'ghost'}
      size="icon"
      disabled={busy}
      aria-pressed={on}
      aria-label={label}
      title={label}
      onClick={async () => {
        setBusy(true)
        try { await changeChatContext(sessionId, { claude_terminal: !on }) } finally { setBusy(false) }
      }}
      className="h-7 w-7 shrink-0 p-0"
    >
      <Terminal className="h-3.5 w-3.5" aria-hidden="true" />
    </Button>
  )
}
