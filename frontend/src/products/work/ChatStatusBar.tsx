import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { chatContextChangedEvent, chatLimitsShort, chatLimitsText, formatTokens, openChatContextMenuEvent } from './ChatContextItems'

/**
 * The compact usage chip at the right end of the chat box's bottom row, shown when the person turned on
 * "Show in chat box": context used of its window with a mini bar, and the account's plan use
 * ("288K/475K ▓▓░ · 7d 20%"). Everything comes from /api/chat-context, which only answers the chat's own tab
 * owner. It reads again when a turn ends. A click opens the chat's model menu. When the row is narrow the
 * plan use goes first, then the bar, then the numbers truncate (container queries on the parent).
 */
export default function ChatStatusBar({ sessionId, busy }: { sessionId: string; busy: boolean }) {
  const [view, setView] = useState<ChatContextView | null>(null)

  useEffect(() => {
    if (!sessionId) { setView(null); return }
    if (busy) return
    let cancelled = false
    agentApi.getChatContext(sessionId).then(next => { if (!cancelled) setView(next) }).catch(() => { if (!cancelled) setView(null) })
    return () => { cancelled = true }
  }, [sessionId, busy])

  useEffect(() => {
    const changed = (event: Event) => {
      const next = (event as CustomEvent<ChatContextView>).detail
      if (next?.session_id === sessionId) setView(next)
    }
    window.addEventListener(chatContextChangedEvent, changed)
    return () => window.removeEventListener(chatContextChangedEvent, changed)
  }, [sessionId])

  if (!view || view.session_id !== sessionId) return null
  const used = view.used_tokens ?? 0
  const windowTokens = view.window_tokens ?? 0
  const meter = used > 0 && windowTokens > 0
  const limits = chatLimitsShort(view.limits)
  if (!meter && !limits) return null
  const percent = meter ? Math.min(100, Math.round((used / windowTokens) * 100)) : 0
  const full = [meter ? `${formatTokens(used)} of ${formatTokens(windowTokens)} context used` : '', chatLimitsText(view.limits)].filter(Boolean).join(' · ')

  return <button type="button" data-testid="chat-status-chip" title={full} aria-label={`${full}. Open the model menu`}
    onClick={() => window.dispatchEvent(new CustomEvent(openChatContextMenuEvent, { detail: { sessionId } }))}
    className="flex min-w-0 max-w-full items-center gap-1.5 overflow-hidden whitespace-nowrap rounded px-1 py-0.5 text-[11px] tabular-nums text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    {meter && <>
      <span className="min-w-0 truncate">{formatTokens(used)}/{formatTokens(windowTokens)}</span>
      <span className="h-1 w-8 shrink-0 overflow-hidden rounded-full bg-muted [@container(max-width:110px)]:hidden" aria-hidden="true">
        <span className={`block h-full rounded-full ${percent > 50 ? 'bg-amber-500' : 'bg-primary'}`} style={{ width: `${Math.max(percent, 2)}%` }} />
      </span>
    </>}
    {limits && <span className={`min-w-0 truncate ${meter ? '[@container(max-width:190px)]:hidden' : ''}`}>{meter && <span aria-hidden="true">· </span>}{limits}</span>}
  </button>
}
