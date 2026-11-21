import { useEffect, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { useChatContextView } from '../../products/work/useChatContextView'
import { changeChatContext, chatLimitsText } from '../../products/work/ChatContextItems'
import { ContextMeter } from '../chat/ContextMeter'
import { formatTokens } from '../../utils/formatTokens'
import type { ChatContextCost, ChatContextModelCost, ChatContextPart, ChatContextTokens, ChatContextView } from '../../services/api-types'

/** Sent by the chat box chip: open the right pane's Context section for this chat ({ sessionId }). */
export const openChatContextPanelEvent = 'agentworks:open-chat-context-panel'

const usd = (value: number) => `$${value.toFixed(2)}`

const dayLabel = (iso?: string) => {
  const date = iso ? new Date(iso) : null
  return date && !Number.isNaN(date.getTime()) && date.getFullYear() > 2000 ? date.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : ''
}

/**
 * The cost heading: what this chat costs on the app it uses now. A chat that moved apps keeps one session, so the
 * server splits its spend at the move (#3480); an estimate (subscription equivalent) is never a plain dollar figure.
 */
export function costHeading(cost: ChatContextCost): { label: string; value: string; note?: string } {
  const amount = cost.current.total_usd
  const day = dayLabel(cost.current.from)
  const since = cost.earlier ? `Since you switched to ${cost.current.apps?.[0] || 'this app'}${day ? ` (${day})` : ''}` : 'Cost so far'
  if (cost.basis === 'actual') return { label: since, value: usd(amount) }
  if (cost.basis === 'estimate') return { label: `${since} (estimate)`, value: `about ${usd(amount)}`, note: 'Subscription equivalent: you are not charged this.' }
  return { label: `${since} (partly estimated)`, value: `about ${usd(amount)}`, note: 'Includes subscription-equivalent spend you are not charged.' }
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
      {view.cost?.current && <CostBlock cost={view.cost} />}
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

const tokenKinds: { key: keyof ChatContextTokens; label: string; color: string }[] = [
  { key: 'new_input', label: 'New input', color: 'bg-primary' },
  { key: 'cache_read', label: 'Read from cache', color: 'bg-emerald-500' },
  { key: 'cache_write', label: 'Written to cache', color: 'bg-amber-500' },
  { key: 'output', label: 'Output', color: 'bg-sky-500' },
  { key: 'thinking', label: 'Thinking', color: 'bg-violet-500' },
]

/** Share of all tokens; a real but tiny share reads "under 1%", never 0%. */
export function tokenShare(value: number, total: number): string {
  if (!(total > 0) || !(value > 0)) return ''
  const percent = (value / total) * 100
  return percent < 1 ? 'under 1%' : `${Math.round(percent)}%`
}

export function TokensBlock({ tokens }: { tokens: ChatContextTokens }) {
  const kinds = tokenKinds.filter(kind => tokens[kind.key] > 0)
  const total = kinds.reduce((sum, kind) => sum + tokens[kind.key], 0)
  if (!(total > 0)) return null
  return <div>
    <div className="my-1 flex h-1.5 overflow-hidden rounded-full bg-muted">{kinds.map(kind => <div key={kind.key} className={kind.color} style={{ width: `${Math.max(1, (tokens[kind.key] / total) * 100)}%` }} />)}</div>
    <ul className="space-y-0.5">{kinds.map(kind => <li key={kind.key} className="flex items-center gap-2 text-[11px] tabular-nums">
      <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-sm ${kind.color}`} />
      <span className="min-w-0 flex-1 truncate">{kind.label}</span>
      <span className="text-muted-foreground">{formatTokens(tokens[kind.key])}</span>
      <span className="w-14 text-right text-muted-foreground">{tokenShare(tokens[kind.key], total)}</span>
    </li>)}</ul>
  </div>
}

function ModelRow({ model }: { model: ChatContextModelCost }) {
  return <details className="group/model">
    <summary className="flex cursor-pointer list-none items-center gap-2 py-0.5 text-[11px] tabular-nums [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground transition-transform group-open/model:rotate-90" />
      <span className="min-w-0 flex-1 truncate">{model.model}{model.app && <span className="text-muted-foreground"> · {model.app}</span>}</span>
      <span className="shrink-0 text-muted-foreground">{model.calls.toLocaleString()} {model.calls === 1 ? 'call' : 'calls'}</span>
      <span className="shrink-0">{usd(model.usd)}</span>
    </summary>
    <div className="pb-1 pl-5"><TokensBlock tokens={model.tokens} /></div>
  </details>
}

function CostBlock({ cost }: { cost: ChatContextCost }) {
  const heading = costHeading(cost)
  const now = cost.current
  const earlier = cost.earlier
  return <div className="space-y-3">
    <div>
      <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">{heading.label}</h4>
      <div className="text-lg font-semibold tabular-nums">{heading.value}</div>
      {heading.note && <p className="text-[11px] text-muted-foreground">{heading.note}</p>}
      <p className="text-[11px] text-muted-foreground">
        {now.calls.toLocaleString()} model {now.calls === 1 ? 'call' : 'calls'}
        {now.helper_usd > 0 && ` · helper agents ${usd(now.helper_usd)} of this`}
      </p>
    </div>
    <div>
      <h4 className="text-[11px] font-medium text-muted-foreground">Tokens</h4>
      <TokensBlock tokens={now.tokens} />
      <p className="mt-1 text-[11px] text-muted-foreground">
        {now.cache_percent > 0 && `${now.cache_percent}% of input came from cache`}
        {now.cache_percent > 0 && (now.cache_saved_usd ?? 0) > 0 && `, which saved about ${usd(now.cache_saved_usd ?? 0)} at list prices`}
        {now.cache_percent > 0 && '. '}
        {cost.unpriced_calls > 0 && `${cost.unpriced_calls} ${cost.unpriced_calls === 1 ? 'call has' : 'calls have'} no price yet and ${cost.unpriced_calls === 1 ? 'is' : 'are'} not counted.`}
      </p>
    </div>
    {now.models.length > 0 && <div>
      <h4 className="text-[11px] font-medium text-muted-foreground">By model</h4>
      {now.models.map(model => <ModelRow key={`${model.provider}:${model.model}`} model={model} />)}
    </div>}
    {earlier && <div className="border-t border-border pt-2">
      <details className="group/earlier">
        <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[11px] text-muted-foreground [&::-webkit-details-marker]:hidden">
          <ChevronRight aria-hidden="true" className="h-3 w-3 shrink-0 transition-transform group-open/earlier:rotate-90" />
          <span className="min-w-0 flex-1 truncate">Before the switch: {(earlier.apps ?? []).join(', ')}{dayLabel(earlier.from) && `, ${dayLabel(earlier.from)} – ${dayLabel(earlier.to)}`}</span>
          <span className="shrink-0 tabular-nums">{usd(earlier.total_usd)}</span>
        </summary>
        <div className="pt-1 pl-4">{earlier.models.map(model => <ModelRow key={`${model.provider}:${model.model}`} model={model} />)}</div>
      </details>
      <p className="mt-1 flex gap-2 text-[11px] tabular-nums"><span className="flex-1 text-muted-foreground">Whole chat since it started</span><span>{cost.basis === 'actual' ? '' : 'about '}{usd(cost.total_usd)}</span></p>
    </div>}
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
