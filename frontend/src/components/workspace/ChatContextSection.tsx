import { useEffect, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { useChatContextView } from '../../products/work/useChatContextView'
import { changeChatContext, chatLimitsPercent, chatLimitsText, resetDate, type ChatContextChange } from '../../products/work/ChatContextItems'
import { openSubscriptionLimits } from '../../utils/subscriptionLimitsNavigation'
import { ContextMeter } from '../chat/ContextMeter'
import { formatTokens } from '../../utils/formatTokens'
import type { ChatContextCost, ChatContextCostPeriod, ChatContextModelCost, ChatContextPart, ChatContextTokens, ChatContextView } from '../../services/api-types'

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
  const since = cost.earlier ? `Cost since ${day || `the switch to ${cost.current.apps?.[0] || 'this app'}`}` : 'Cost so far'
  if (cost.basis === 'actual') return { label: since, value: usd(amount) }
  if (cost.basis === 'estimate') return { label: since, value: `about ${usd(amount)}`, note: 'Not charged: subscription equivalent' }
  return { label: `${since} (partly estimated)`, value: `about ${usd(amount)}`, note: 'Partly subscription equivalent, not charged' }
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
  const projectId = useChatStore(state => (state.activeTabId ? state.chatTabs[state.activeTabId]?.metadata?.agentProfileProjectId : '') || '')
  // Closed by default at the end of the pane: its header carries the short line (size, plan use).
  const [open, setOpen] = useState(false)
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
  if (!view || (!(used > 0) && !view.cost && !(view.limits?.length) && !view.idle_compaction)) return null
  const percent = windowTokens > 0 ? Math.min(100, Math.round((used / windowTokens) * 100)) : 0
  const act = async (change: ChatContextChange) => { if (working) return; setWorking(true); try { await changeChatContext(sessionId, change) } finally { setWorking(false) } }

  return <details ref={ref} open={open} onToggle={event => setOpen((event.target as HTMLDetailsElement).open)} data-testid="chat-context-section" className="group/ctx border-t border-border pt-2 first:border-t-0 first:pt-0">
    <summary className="flex cursor-pointer list-none items-center gap-1.5 py-1 text-[11px] font-medium text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="h-3 w-3 transition-transform group-open/ctx:rotate-90" />Context
      <ContextSummary used={used} windowTokens={windowTokens} limits={view.limits} />
    </summary>
    <div className="space-y-3 pb-1 pl-1.5 pr-1 text-xs">
      {/* Approved order (owner, 22 Nov): how full, plan use, cost in one line, then the detail folded away. */}
      {used > 0 && <FullnessBlock view={view} used={used} windowTokens={windowTokens} percent={percent} working={working} onAct={act} />}
      <PlanBlock view={view} projectId={projectId} />
      {view.cost?.current && <CostBlock cost={view.cost} />}
      <DetailsBlock view={view} used={used} />
      <SettingsBlock view={view} working={working} onAct={act} />
    </div>
  </details>
}

/** The short line in the section header: "331k ▬ · 5h 26% · 7d 56%" (full numbers on hover). */
function ContextSummary({ used, windowTokens, limits }: { used: number; windowTokens: number; limits: ChatContextView['limits'] }) {
  const plan = chatLimitsPercent(limits)
  if (!(used > 0) && !plan) return null
  const full = [used > 0 ? `${formatTokens(used)}${windowTokens > 0 ? ` of ${formatTokens(windowTokens)}` : ''} context used` : '', chatLimitsText(limits)].filter(Boolean).join(' · ')
  return <span data-testid="chat-context-summary" title={full} className="ml-auto group-open/ctx:hidden flex min-w-0 items-center gap-1.5 truncate font-normal tabular-nums">
    {used > 0 && <span>{formatTokens(used)}</span>}
    {used > 0 && windowTokens > 0 && <ContextMeter decorative className="w-8 shrink-0" used={used} windowTokens={windowTokens} />}
    {plan && <span className="truncate">{used > 0 && <span aria-hidden="true">· </span>}{plan}</span>}
  </span>
}

const quietLink = 'text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

function FullnessBlock({ view, used, windowTokens, percent, working, onAct }: { view: ChatContextView; used: number; windowTokens: number; percent: number; working: boolean; onAct: (change: ChatContextChange) => void }) {
  const full = windowTokens > 0 && percent > 90
  return <div>
    <div className="flex items-baseline gap-2">
      {windowTokens > 0 && <span className="text-lg font-semibold tabular-nums">{percent}%</span>}
      <span className="text-[11px] tabular-nums text-muted-foreground">{formatTokens(used)}{windowTokens > 0 ? ` of ${formatTokens(windowTokens)} used` : ' used'}</span>
    </div>
    {windowTokens > 0 && <ContextMeter className="mt-1.5 h-2" used={used} windowTokens={windowTokens} />}
    {full && <p className="mt-1.5 text-[11px] text-diff-del">Almost full. This chat will compact soon so it can keep going.</p>}
    {!full && percent > 50 && view.can_compact && <p className="mt-1.5 text-[11px] text-muted-foreground">Over half full. Compact when you reach a good stopping point.</p>}
    {/* Buttons only once the chat is getting full; before that, quiet links so nothing urges an action. */}
    {view.can_compact && (percent > 50 ? <div className="mt-2 flex flex-wrap gap-1.5">
      {view.can_compact && <button type="button" disabled={working} title="Summarise this chat now, so each next step reads less." onClick={() => onAct({ action: 'compact' })} className={`rounded-md border px-2 py-1 text-xs disabled:opacity-50 ${full ? 'border-primary bg-primary text-primary-foreground' : 'border-border hover:bg-muted'}`}>Compact now</button>}
    </div> : <div className="mt-1.5 flex flex-wrap gap-x-3 text-[11px]">
      {view.can_compact && <button type="button" disabled={working} title="Summarise this chat now, so each next step reads less." onClick={() => onAct({ action: 'compact' })} className={quietLink}>Compact now</button>}
    </div>)}
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

function ModelRow({ model, showApp }: { model: ChatContextModelCost; showApp: boolean }) {
  return <details className="group/model">
    <summary className="flex cursor-pointer list-none items-center gap-2 py-0.5 text-[11px] tabular-nums [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground transition-transform group-open/model:rotate-90" />
      <span className="min-w-0 flex-1 break-words" title={model.model}>{model.name || model.model}{showApp && model.app && <span className="text-muted-foreground"> · {model.app}</span>}</span>
      <span className="shrink-0 text-muted-foreground">{model.calls.toLocaleString()} {model.calls === 1 ? 'call' : 'calls'}</span>
      <span className="shrink-0">{usd(model.usd)}</span>
    </summary>
    <div className="pb-1 pl-5"><TokensBlock tokens={model.tokens} /></div>
  </details>
}

/** Only Claude Code counts its thinking inside output, so the pane says so rather than show no thinking at all. */
function thinkingNote(period: ChatContextCostPeriod): string {
  return period.tokens.thinking > 0 || !(period.tokens.output > 0) || !period.providers?.includes('claude-code') ? '' : 'Claude counts its thinking in output.'
}

/** "98% of input came from cache. Without it this would have cost about $857; the cache saved about $786." */
export function cacheText(period: ChatContextCostPeriod): string {
  if (!(period.cache_percent > 0)) return ''
  const saved = period.cache_saved_usd ?? 0
  const head = `${period.cache_percent}% of input came from cache`
  return saved > 0 ? `${head}. Without it this would have cost about ${usd(period.total_usd + saved)} at list prices; the cache saved about ${usd(saved)}.` : `${head}.`
}

const unpricedText = (calls: number) => calls > 0 ? `${calls.toLocaleString()} ${calls === 1 ? 'call has' : 'calls have'} no price yet and ${calls === 1 ? 'is' : 'are'} not counted.` : ''

function PeriodDetail({ period }: { period: ChatContextCostPeriod }) {
  const showApp = (period.apps ?? []).length > 1
  const notes = [cacheText(period), thinkingNote(period), unpricedText(period.unpriced_calls ?? 0)].filter(Boolean).join(' ')
  return <>
    <div>
      <h4 className="text-[11px] font-medium text-muted-foreground">Tokens</h4>
      <TokensBlock tokens={period.tokens} />
      {notes && <p className="mt-1 text-[11px] text-muted-foreground">{notes}</p>}
    </div>
    {period.models.length > 0 && <div>
      <h4 className="text-[11px] font-medium text-muted-foreground">By model</h4>
      {period.models.map(model => <ModelRow key={`${model.provider}:${model.model}`} model={model} showApp={showApp} />)}
    </div>}
  </>
}

/** The cost in one line (owner, 22 Nov: it is not charged money on a subscription, so it must not dominate). */
function CostBlock({ cost }: { cost: ChatContextCost }) {
  const heading = costHeading(cost)
  const now = cost.current
  const about = cost.basis === 'actual' ? '' : 'about '
  const line = [heading.note, (cost.today_usd ?? 0) > 0 ? `today ${about}${usd(cost.today_usd ?? 0)}` : '', `${now.calls.toLocaleString()} model ${now.calls === 1 ? 'call' : 'calls'}`, now.helper_usd > 0 ? `helper agents ${usd(now.helper_usd)} of this` : ''].filter(Boolean).join(' · ')
  return <div>
    <div className="flex items-baseline gap-2 text-[11px]"><span className="min-w-0 flex-1 text-muted-foreground">{heading.label}</span><span className="shrink-0 text-xs font-medium tabular-nums">{heading.value}</span></div>
    <p className="text-[11px] tabular-nums text-muted-foreground">{line}</p>
    {cost.earlier && <p className="flex gap-2 text-[11px] tabular-nums"><span className="flex-1 text-muted-foreground">Whole chat{dayLabel(cost.earlier.from) ? ` since ${dayLabel(cost.earlier.from)}` : ''}</span><span>{about}{usd(cost.total_usd)}</span></p>}
  </div>
}

/** What fills the context and where the tokens went, folded away: most people never need it. */
function DetailsBlock({ view, used }: { view: ChatContextView; used: number }) {
  const cost = view.cost
  const earlier = cost?.earlier
  const parts = view.parts ?? []
  const totalOnly = view.total_only && used > 0 && !parts.length
  if (!parts.length && !cost?.current && !totalOnly) return null
  return <details className="group/details">
    <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[11px] font-medium text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
      <ChevronRight aria-hidden="true" className="h-3 w-3 transition-transform group-open/details:rotate-90" />Details
    </summary>
    <div className="space-y-3 pt-2 pl-4">
      {parts.length > 0 && <PartsBlock parts={parts} />}
      {totalOnly && <p className="text-[11px] text-muted-foreground">This chat runs in a coding agent's own app, which reports its total but not what fills it.</p>}
      {cost?.current && <PeriodDetail period={cost.current} />}
      {earlier && <details className="group/earlier border-t border-border pt-2">
        <summary className="flex cursor-pointer list-none items-start gap-1.5 text-[11px] text-muted-foreground [&::-webkit-details-marker]:hidden">
          <ChevronRight aria-hidden="true" className="mt-0.5 h-3 w-3 shrink-0 transition-transform group-open/earlier:rotate-90" />
          <span className="min-w-0 flex-1 break-words">Before the switch: {(earlier.apps ?? []).join(', ')}{dayLabel(earlier.from) && `, ${dayLabel(earlier.from)} – ${dayLabel(earlier.to)}`}</span>
          <span className="shrink-0 tabular-nums">{usd(earlier.total_usd)}</span>
        </summary>
        <div className="space-y-3 pt-2 pl-4"><PeriodDetail period={earlier} /></div>
      </details>}
    </div>
  </details>
}

/** The chat's compact switches, moved here from the model menu: the server offers them and saves each change. */
function SettingsBlock({ view, working, onAct }: { view: ChatContextView; working: boolean; onAct: (change: ChatContextChange) => void }) {
  if (!view.idle_compaction && !view.compact_early) return null
  const row = (label: string, on: boolean, hint: string | undefined, change: ChatContextChange) => <label className="flex cursor-pointer items-start gap-2 text-[11px]" title={hint}>
    <input type="checkbox" className="mt-0.5 accent-primary" checked={on} disabled={working} onChange={() => onAct(change)} />
    <span className="min-w-0 flex-1"><span>{label}</span>{hint && <span className="block text-muted-foreground">{hint}</span>}</span>
  </label>
  return <div className="space-y-1.5">
    <h4 className="text-[11px] font-medium text-muted-foreground">Compacting</h4>
    {view.idle_compaction && row('Auto-compact when idle', view.idle_compaction.on, view.idle_compaction_hint, { idle_compaction: !view.idle_compaction.on })}
    {view.compact_early && row('Compact at 300K', view.compact_early.on, view.compact_early_hint, { compact_early: !view.compact_early.on })}
  </div>
}

/** The account's plan windows with their reset times, and where to see every account and this Crew's usage. */
function PlanBlock({ view, projectId }: { view: ChatContextView; projectId: string }) {
  const limits = view.limits ?? []
  const link = 'text-[11px] text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  return <div>
    <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">Your plan</h4>
    {limits.map(limit => {
      const value = Math.round(limit.used_percent)
      const resets = resetDate(limit.resets_at)
      return <div key={limit.label} className="mb-1 text-[11px] tabular-nums">
        <div className="flex gap-2"><span className="w-8 shrink-0">{limit.label}</span><span className="flex-1 text-muted-foreground">{value}% used{resets ? ` · resets ${resets}` : ''}</span></div>
        <div className="mt-0.5 h-1 overflow-hidden rounded-full bg-muted"><div className={`h-1 rounded-full ${value >= 90 ? 'bg-diff-del' : value >= 75 ? 'bg-amber-500' : 'bg-muted-foreground/60'}`} style={{ width: `${Math.min(100, Math.max(0, value))}%` }} /></div>
      </div>
    })}
    {!limits.length && <p className="text-[11px] text-muted-foreground">This account has not reported its plan use yet.</p>}
    <div className="mt-1 flex flex-wrap gap-x-3">
      <button type="button" className={link} onClick={openSubscriptionLimits}>All accounts and usage</button>
      {projectId && <button type="button" className={link} onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-chat-usage', { detail: { projectId } }))}>Usage history</button>}
    </div>
  </div>
}

/** What fills the context, as an estimate: the model reports one total, so each part is sized from the prompt. */
export function PartsBlock({ parts }: { parts: ChatContextPart[] }) {
  const total = parts.reduce((sum, part) => sum + part.tokens, 0)
  return <div>
    <h4 className="mb-0.5 text-[11px] font-medium text-muted-foreground">What fills it (estimate)</h4>
    <ul className="space-y-1">{parts.map(part => <li key={part.key} className="text-[11px] tabular-nums">
      <div className="flex gap-2"><span className="min-w-0 flex-1 break-words">{part.label}</span><span className="text-muted-foreground">about {formatTokens(part.tokens)}</span></div>
      <div className="mt-0.5 h-1 rounded-full bg-muted"><div className={`h-1 rounded-full ${part.note ? 'bg-amber-500' : 'bg-primary/60'}`} style={{ width: `${total > 0 ? Math.max(2, Math.round((part.tokens / total) * 100)) : 0}%` }} /></div>
      {part.note && <p className="mt-0.5 text-amber-600 dark:text-amber-400">{part.note}</p>}
    </li>)}</ul>
  </div>
}
