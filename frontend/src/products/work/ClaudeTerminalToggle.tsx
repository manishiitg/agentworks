import { useEffect, useState } from 'react'
import { Check, Terminal } from 'lucide-react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { changeChatContext, chatContextChangedEvent, menuRowClass } from './ChatContextItems'

/**
 * Claude Code terminal switch inside the model menu. Shown only when the server offers the switch
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
  return <Menu.CheckboxItem className={menuRowClass} checked={on} disabled={busy}
    title="Use the native Claude Code terminal. Turn off to return to normal chat; the conversation continues."
    onSelect={event => event.preventDefault()}
    onCheckedChange={async checked => {
      setBusy(true)
      try { await changeChatContext(sessionId, { claude_terminal: checked === true }) } finally { setBusy(false) }
    }}>
    <Terminal className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
    <span className="flex-1">Claude terminal</span>
    <Menu.ItemIndicator><Check className="h-3.5 w-3.5" aria-hidden="true" /></Menu.ItemIndicator>
  </Menu.CheckboxItem>
}
