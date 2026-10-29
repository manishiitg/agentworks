import { useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { ChatContextView } from '../../services/api-types'
import { chatContextChangedEvent, chatLimitsText, formatTokens, openChatContextMenuEvent } from './ChatContextItems'

/**
 * The one-line status under a chat's composer (like Claude Code's status line): model, context used of its
 * window with a mini bar, and the account's plan limits. Everything comes from /api/chat-context, which only
 * answers the chat's own tab owner, so nobody else sees it. It reads again when a turn ends. A click opens
 * the chat's model menu, whose Context group has the switches.
 */
export default function ChatStatusBar({ sessionId, modelLabel, busy }: { sessionId: string; modelLabel?: string; busy: boolean }) {
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
  const limits = chatLimitsText(view.limits)
  if (!meter && !limits) return null
  const percent = meter ? Math.min(100, Math.round((used / windowTokens) * 100)) : 0
  const text = [modelLabel, meter ? `${formatTokens(used)} / ${formatTokens(windowTokens)}` : '', limits].filter(Boolean).join(' · ')

  return <button type="button" data-testid="chat-status-bar" title={text} aria-label={`Chat status: ${text}. Open the model menu`}
    onClick={() => window.dispatchEvent(new CustomEvent(openChatContextMenuEvent, { detail: { sessionId } }))}
    className="mt-0.5 flex w-full min-w-0 items-center gap-1.5 overflow-hidden whitespace-nowrap rounded px-1 text-left text-[11px] tabular-nums text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    {modelLabel && <span className="max-w-[45%] shrink-0 truncate">{modelLabel}</span>}
    {meter && <>
      {modelLabel && <span aria-hidden="true">·</span>}
      <span className="shrink-0">{formatTokens(used)} / {formatTokens(windowTokens)}</span>
      <span className="h-1 w-10 shrink-0 overflow-hidden rounded-full bg-muted" aria-hidden="true">
        <span className={`block h-full rounded-full ${percent > 50 ? 'bg-amber-500' : 'bg-primary'}`} style={{ width: `${Math.max(percent, 2)}%` }} />
      </span>
    </>}
    {limits && <>
      {(modelLabel || meter) && <span aria-hidden="true">·</span>}
      <span className="min-w-0 truncate">{limits}</span>
    </>}
  </button>
}
