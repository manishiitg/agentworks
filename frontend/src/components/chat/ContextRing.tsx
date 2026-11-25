import { useChatContextView } from '../../products/work/useChatContextView'
import { openChatContextPanelEvent } from '../workspace/ChatContextSection'

/**
 * How full the chat's context is, as a small ring beside the model (like Claude Code's status line). Neutral, amber
 * past 75%, red past 90%, the same thresholds as ContextMeter. Click opens the Context section in the side pane.
 */
export function ContextRing({ sessionId, busy = false }: { sessionId: string; busy?: boolean }) {
  const view = useChatContextView(sessionId, busy)
  const used = view?.used_tokens ?? 0
  const windowTokens = view?.window_tokens ?? 0
  if (!(used > 0) || !(windowTokens > 0)) return null
  const percent = Math.min(100, Math.round((used / windowTokens) * 100))
  const r = 5, c = 2 * Math.PI * r
  const tone = percent > 90 ? 'text-diff-del' : percent > 75 ? 'text-amber-500' : 'text-muted-foreground'
  return (
    <button type="button" data-testid="context-ring" title={`Context ${percent}% full. Click for details.`} aria-label={`Context ${percent}% full`}
      onClick={() => window.dispatchEvent(new CustomEvent(openChatContextPanelEvent, { detail: { sessionId } }))}
      className="inline-flex items-center rounded p-0.5 hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" className={tone}>
        <circle cx="7" cy="7" r={r} fill="none" stroke="currentColor" strokeOpacity="0.25" strokeWidth="2" />
        <circle cx="7" cy="7" r={r} fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"
          strokeDasharray={`${(Math.max(percent, 3) / 100) * c} ${c}`} transform="rotate(-90 7 7)" />
      </svg>
    </button>
  )
}
