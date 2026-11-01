import { desktopRequest } from '../../services/desktopConnections'
import { useEffect, useState } from 'react'
import { authApi, type SharedAccountTokenUsage } from '../../services/api'
import { resetLabel, shownTokenFigures } from '../../utils/tokenLimits'
import { formatTokens } from '../../utils/formatTokens'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { openChatContextMenuEvent } from '../../products/work/ChatContextItems'

/**
 * The person's own use of the shared server accounts against the limits an
 * admin set (PLAT-683). Each shared account has its own limit and there may be
 * an overall cap (PLAT-693): a chat shows the account it uses, or the overall
 * cap when that is nearer. Amber from 80%, red at the limit, when new turns on
 * that account are refused.
 */
function useSharedTokenUsage(serverConnection?: string): SharedAccountTokenUsage | null {
  const [usage, setUsage] = useState<SharedAccountTokenUsage | null>(null)
  useEffect(() => {
    let cancelled = false
    setUsage(null)
    // Through a promise so a missing client (older server, tests) is a quiet rejection. A company account on the
    // Mac counts on its server: that server's figures, fetched over the connection.
    const fetchUsage = () => serverConnection
      ? desktopRequest<SharedAccountTokenUsage>(`/${encodeURIComponent(serverConnection)}/token-usage`)
      : authApi.getMyTokenUsage()
    const load = () => { void Promise.resolve().then(fetchUsage).then(next => { if (!cancelled) setUsage(next) }).catch(() => undefined) }
    load()
    const timer = window.setInterval(load, 60_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [serverConnection])
  return usage
}

/** The usage on the account a chat runs on, or null when nothing is limited there (own account, no limit set). */
function chatTokenLimits(usage: SharedAccountTokenUsage | null, accountProvider: string | null | undefined) {
  // A chat on the person's own account neither counts nor is limited, so it shows nothing (owner, 2026-10-07).
  if (!usage || accountProvider === null) return null
  const shown = shownTokenFigures(usage, accountProvider)
  // Only with a limit set on this account or the overall cap (owner, 2026-10-07).
  if (!shown.daily_limit && !shown.weekly_limit) return null
  const pct = (used: number, limit: number) => Math.min(999, Math.round((used / limit) * 100))
  const rows = [
    shown.daily_limit ? { label: 'Today', percent: pct(shown.daily_used, shown.daily_limit), used: shown.daily_used, limit: shown.daily_limit, resets: resetLabel(usage.day_resets_at, false) } : null,
    shown.weekly_limit ? { label: 'This week', percent: pct(shown.weekly_used, shown.weekly_limit), used: shown.weekly_used, limit: shown.weekly_limit, resets: resetLabel(usage.week_resets_at, true) } : null,
  ].filter(row => row !== null)
  const detail: string[] = [`Tokens on ${shown.scope}.`]
  if (usage.counting_note) detail.push(usage.counting_note)
  if (shown.daily_cache_read_tokens || shown.weekly_cache_read_tokens) detail.push(`Raw cache reads: ${formatTokens(shown.daily_cache_read_tokens)} today; ${formatTokens(shown.weekly_cache_read_tokens)} this week.`)
  for (const row of rows) detail.push(`${row.label}: ${formatTokens(row.used)} of ${formatTokens(row.limit)} (resets ${row.resets})`)
  detail.push(shown.state === 'over' ? `New messages on ${shown.scope} are paused; switch to another account in Models.` : 'Your own accounts are not limited.')
  return { shown, rows, detail, tighter: Math.max(...rows.map(row => row.percent)) }
}

// Green while there is room, amber from 80%, red at the limit (the server's state).
const stateColor = (state: string | undefined) => state === 'over' ? 'text-red-500' : state === 'warning' ? 'text-amber-500' : 'text-emerald-500'

/**
 * The chat box's sign of the shared-account limits: a small ring filled to the tighter of the day and week
 * limits and coloured by how near it is (owner, 2026-10-30: the numbers live in the model menu). Hover shows
 * the figures; a click opens this chat's model menu. accountProvider is the shared account the chat runs on
 * (null: the person's own account, which shows nothing; undefined: unknown, the overall figures).
 */
export function SharedTokenUsageChip({ accountProvider, sessionId }: { accountProvider?: string | null; sessionId?: string }) {
  const limits = chatTokenLimits(useSharedTokenUsage(), accountProvider)
  if (!limits) return null
  const fill = Math.min(100, Math.max(limits.tighter, 4))
  return (
    <button type="button" data-testid="shared-token-usage-chip" title={limits.detail.join('\n')} aria-label={`Shared-account tokens: ${limits.detail.join(' ')}`}
      disabled={!sessionId} onClick={() => window.dispatchEvent(new CustomEvent(openChatContextMenuEvent, { detail: { sessionId } }))}
      className={`inline-flex h-7 w-7 items-center justify-center rounded-md hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:hover:bg-transparent ${stateColor(limits.shown.state)}`}>
      <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 -rotate-90" aria-hidden="true">
        <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeOpacity="0.25" strokeWidth="2.5" />
        <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeWidth="2.5" pathLength="100" strokeDasharray={`${fill} 100`} strokeLinecap="round" />
      </svg>
    </button>
  )
}

/** The same limits as rows in a chat's model menu: "Today 3%" and "This week 13%" with their resets. */
export function SharedTokenUsageItems({ accountProvider }: { accountProvider?: string | null }) {
  const limits = chatTokenLimits(useSharedTokenUsage(), accountProvider)
  if (!limits) return null
  return <>
    <Menu.Separator className="my-1 h-px bg-border" />
    <Menu.Label className="px-2.5 pb-1 pt-1.5 text-[11px] font-medium text-muted-foreground">Shared account limits</Menu.Label>
    {limits.rows.map(row => <div key={row.label} title={limits.detail.join('\n')} className="px-2.5 py-1 text-xs">
      <div className="flex items-center gap-2 tabular-nums">
        <span className="flex-1">{row.label}</span>
        <span className={stateColor(row.percent >= 100 ? 'over' : row.percent >= 80 ? 'warning' : 'ok')}>{row.percent}%</span>
        <span className="text-muted-foreground">resets {row.resets}</span>
      </div>
    </div>)}
    {limits.shown.state === 'over' && <p className="px-2.5 pb-1 text-xs text-destructive">New messages on {limits.shown.scope} are paused; switch to another account.</p>}
  </>
}

/**
 * The Models panel line next to the selected account: which kind of account the chat runs on and its
 * limits (owner, 2026-10-07). "Your own account · not limited", or "Shared account · limits: Day 32% ·
 * Week 70% (resets …)" with only the limits that exist, "no limit" when none. accountProvider is the
 * shared account's provider (null: the person's own account; undefined: not known yet, nothing shown).
 */
export function SharedTokenUsageNotice({ className = '', accountProvider, inline = false, serverConnection }: { className?: string; accountProvider?: string | null; inline?: boolean; serverConnection?: string }) {
  const usage = useSharedTokenUsage(serverConnection)
  if (accountProvider === undefined) return null
  const base = inline ? 'text-xs' : 'rounded-lg border px-3 py-2 text-xs'
  if (accountProvider === null) {
    return <p data-testid="account-limits" className={`${base} ${inline ? '' : 'border-border bg-muted/40'} text-muted-foreground ${className}`}>Your own account · not limited</p>
  }
  const shown = usage ? shownTokenFigures(usage, accountProvider) : null
  const pct = (used: number, limit: number) => `${Math.min(999, Math.round((used / limit) * 100))}%`
  const limits: string[] = []
  const resets: string[] = []
  if (shown?.daily_limit) {
    limits.push(`Day ${pct(shown.daily_used, shown.daily_limit)}`)
    if (usage?.day_resets_at) resets.push(`day ${resetLabel(usage.day_resets_at, false)}`)
  }
  if (shown?.weekly_limit) {
    limits.push(`Week ${pct(shown.weekly_used, shown.weekly_limit)}`)
    if (usage?.week_resets_at) resets.push(`week ${resetLabel(usage.week_resets_at, true)}`)
  }
  const text = limits.length
    ? `${serverConnection ? 'Company account' : 'Shared account'} · limits: ${limits.join(' · ')}${resets.length ? ` (resets ${resets.join(', ')})` : ''}`
    : `${serverConnection ? 'Company account' : 'Shared account'}${usage ? ' · no limit' : ''}`
  const state = limits.length ? shown?.state : 'ok'
  const tone = state === 'over'
    ? 'border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/25 dark:text-red-300'
    : state === 'warning'
      ? 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300'
      : 'border-border bg-muted/40 text-muted-foreground'
  const detail: string[] = []
  if (usage?.counting_note) detail.push(usage.counting_note)
  if (shown?.daily_cache_read_tokens || shown?.weekly_cache_read_tokens) detail.push(`Raw cache reads: ${formatTokens(shown.daily_cache_read_tokens)} today; ${formatTokens(shown.weekly_cache_read_tokens)} this week`)
  if (shown?.daily_limit) detail.push(`Today: ${formatTokens(shown.daily_used)} of ${formatTokens(shown.daily_limit)}`)
  if (shown?.weekly_limit) detail.push(`This week: ${formatTokens(shown.weekly_used)} of ${formatTokens(shown.weekly_limit)}`)
  const inlineTone = state === 'over' ? 'text-destructive' : state === 'warning' ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'
  return (
    <div data-testid="account-limits" role={state === 'ok' ? undefined : 'status'} title={detail.length ? `Tokens on ${shown?.scope}. ${detail.join('. ')}.` : undefined}
      className={`${base} ${inline ? inlineTone : tone} ${className}`}>
      <p>{text}</p>
      {state === 'over' && <p className="mt-1">New messages on {shown?.scope} are paused. Switch to another account here or ask an admin to raise your limit.</p>}
      {state === 'warning' && <p className="mt-1">You have used over 80% of a limit. Your own accounts are not limited.</p>}
    </div>
  )
}
