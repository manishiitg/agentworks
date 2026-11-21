import { useEffect, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { useChatContextView } from '../../products/work/useChatContextView'
import { changeChatContext, chatLimitsText } from '../../products/work/ChatContextItems'
import { ContextMeter } from '../chat/ContextMeter'
import { formatTokens } from '../../utils/formatTokens'
import type { ChatContextCost, ChatContextPart, ChatContextView } from '../../services/api-types'

/** Sent by the chat box chip: open the right pane's Context section for this chat ({ sessionId }). */
export const openChatContextPanelEvent = 'agentworks:open-chat-context-panel'

const usd = (value: number) => `$${value.toFixed(2)}`

/** One cost line: an estimate (subscription equivalent) is never shown as a plain dollar figure. */
export function costHeading(cost: ChatContextCost): { label: string; value: string; note?: string } {
  if (cost.basis === 'actual') return { label: 'Cost so far', value: usd(cost.total_usd) }
  if (cost.basis === 'estimate') return { label: 'Cost so far (estimate)', value: `about ${usd(cost.total_usd)}`, note: 'Subscription equivalent: you are not charged this.' }
  return { label: 'Cost so far (partly estimated)', value: `about ${usd(cost.total_usd)}`, note: 'Includes subscription-equivalent spend you are not charged.' }
}

/**
 * The right pane's Context section for one chat (approved mockup, #3480): how full it is, what it has cost, and the
 * plan windows. Everything is the server's view (/api/chat-context); this only shows it. A chat run by a coding agent's
 * own CLI reports its total only, and says so. Opens from the chat box chip.
 */
/** The pane describes the chat the person is in; another open chat's section must not stack under it. */
export function isFocusedChat(sessionId: string, focusedSessionId?: string | null): boolean {
  return Boolean(sessionId) && sessionId === focusedSessionId
}

export function ChatContextSection({ sessionId, busy = false }: { sessionId: string; busy?: boolean }) {
  const focusedSessionId = useChatStore(state => (state.activeTabId ? state.chatTabs[state.activeTabId]?.sessionId : null))
  // Only the focused chat loads its figures: an unfocused one must not poll the server.
  return isFocusedChat(sessionId, focusedSessionId) ? <FocusedChatContext sessionId={sessionId} busy={busy} /> : null
}

function FocusedChatContext({ sessionId, busy }: { sessionId: string; busy: boolean }) {
  const view = useChatContextView(sessionId, busy)
  const [open, setOpen] = useState(true)
  const [working, setWorking] = useState(false)
  const ref = useRef<HTMLDetailsElement>(null)
  useEffect(() => {
    const listener = (event: Event) => {
      if ((event as CustomEvent<{ sessionId?: string }>).detail?.sessionId !== sessionId) return
      setOpen(true)
      ref.current?.scrollIntoView?.({ block: 'nearest' })
    }
    window.addEventListener(openChatContextPanelEvent, listener)
    return () => window.removeEventListener(openChatContextPanelEvent, listener)
  }, [sessionId])

  const used = view?.used_tokens ?? 0
  const windowTokens = view?.window_tokens ?? 0
  if (!view || (!(used > 0) && !view.cost && !(view.limits?.length))) return null
  const percent = windowTokens > 0 ? Math.min(100, Math.round((used / windowTokens) * 100)) : 0
  const act = async (action: 'compact' | 'fresh') => { setWorking(true); await changeChatContext(sessionId, { action }); setWorking(false) }

  return <details ref={ref} open={open} onToggle={event => setOpen((event.target as HTMLDetailsElement).open)} data-testid="chat-context-section" className="group/ctx border-t border-border pt-2 first:border-t-0 first:pt-0">
    <summary className="flex cursor-pointer list-none items-center gap-1.5 py-1 text-[11px] font-medium text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="h-3 w-3 transition-transform group-open/ctx:rotate-90" />Context
    </summary>
    <div className="space-y-3 pb-1 pl-1.5 pr-1 text-xs">
      {used > 0 && <FullnessBlock view={view} used={used} windowTokens={windowTokens} percent={percent} working={working} onAct={act} />}
      {view.cost && <CostBlock cost={view.cost} />}
      {Boolean(view.parts?.length) && <PartsBlock parts={view.parts ?? []} />}
      {view.total_only && used > 0 && <p className="text-[11px] text-muted-foreground">This chat runs in a coding agent's own app, which reports its total but not what fills it.</p>}
      {Boolean(view.limits?.length) && <div>
        <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">Your plan</h4>
        <p className="text-[11px] tabular-nums text-muted-foreground">{chatLimitsText(view.limits)}</p>
      </div>}
    </div>
  </details>
}

function FullnessBlock({ view, used, windowTokens, percent, working, onAct }: { view: ChatContextView; used: number; windowTokens: number; percent: number; working: boolean; onAct: (action: 'compact' | 'fresh') => void }) {
  const full = windowTokens > 0 && percent > 90
  return <div>
    <div className="flex items-baseline gap-2">
      {windowTokens > 0 && <span className="text-lg font-semibold tabular-nums">{percent}%</span>}
      <span className="text-[11px] tabular-nums text-muted-foreground">{formatTokens(used)}{windowTokens > 0 ? ` of ${formatTokens(windowTokens)} used` : ' used'}</span>
    </div>
    {windowTokens > 0 && <ContextMeter className="mt-1.5 h-2" used={used} windowTokens={windowTokens} />}
    {full && <p className="mt-1.5 text-[11px] text-diff-del">Almost full. This chat will compact soon so it can keep going.</p>}
    {!full && percent > 50 && view.can_compact && <p className="mt-1.5 text-[11px] text-muted-foreground">Over half full. Compact when you reach a good stopping point.</p>}
    {(percent > 50) && (view.can_compact || view.can_fresh) && <div className="mt-2 flex gap-1.5">
      {view.can_compact && <button type="button" disabled={working} onClick={() => onAct('compact')} className={`rounded-md border px-2 py-1 text-xs disabled:opacity-50 ${full ? 'border-primary bg-primary text-primary-foreground' : 'border-border hover:bg-muted'}`}>Compact now</button>}
      {full && view.can_fresh && <button type="button" disabled={working} onClick={() => onAct('fresh')} className="rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">Start fresh</button>}
    </div>}
  </div>
}

function CostBlock({ cost }: { cost: ChatContextCost }) {
  const heading = costHeading(cost)
  return <div>
    <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">{heading.label}</h4>
    <div className="text-lg font-semibold tabular-nums">{heading.value}</div>
    {heading.note && <p className="text-[11px] text-muted-foreground">{heading.note}</p>}
    {cost.models.length > 1 && <ul className="mt-1 space-y-0.5">{cost.models.map(model => <li key={model.model} className="flex gap-2 text-[11px] tabular-nums"><span className="min-w-0 flex-1 truncate">{model.model}</span><span className="text-muted-foreground">{usd(model.usd)}</span></li>)}</ul>}
    <p className="mt-1 text-[11px] text-muted-foreground">
      {cost.cache_percent > 0 && `${cost.cache_percent}% of input came from cache. `}
      {cost.helper_usd > 0 && `Helper agents: ${usd(cost.helper_usd)} of the total. `}
      {cost.unpriced_calls > 0 && `${cost.unpriced_calls} ${cost.unpriced_calls === 1 ? 'call has' : 'calls have'} no price yet and ${cost.unpriced_calls === 1 ? 'is' : 'are'} not counted.`}
    </p>
  </div>
}

/** What fills the context, as an estimate: the model reports one total, so each part is sized from the prompt. */
export function PartsBlock({ parts }: { parts: ChatContextPart[] }) {
  const total = parts.reduce((sum, part) => sum + part.tokens, 0)
  return <div>
    <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">What fills it (estimate)</h4>
    <ul className="space-y-1">{parts.map(part => <li key={part.key} className="text-[11px] tabular-nums">
      <div className="flex gap-2"><span className="min-w-0 flex-1 truncate">{part.label}</span><span className="text-muted-foreground">about {formatTokens(part.tokens)}</span></div>
      <div className="mt-0.5 h-1 rounded-full bg-muted"><div className="h-1 rounded-full bg-primary/60" style={{ width: `${total > 0 ? Math.max(2, Math.round((part.tokens / total) * 100)) : 0}%` }} /></div>
    </li>)}</ul>
  </div>
}
