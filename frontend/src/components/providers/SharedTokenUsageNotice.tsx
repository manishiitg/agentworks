import { useEffect, useState } from 'react'
import { authApi, type SharedAccountTokenUsage } from '../../services/api'
import { formatTokens, resetLabel, shownTokenFigures } from '../../utils/tokenLimits'

/**
 * The person's own use of the shared server accounts against the limits an
 * admin set (PLAT-683). Each shared account has its own limit and there may be
 * an overall cap (PLAT-693): a chat shows the account it uses, or the overall
 * cap when that is nearer. Amber from 80%, red at the limit, when new turns on
 * that account are refused.
 */
function useSharedTokenUsage(): SharedAccountTokenUsage | null {
  const [usage, setUsage] = useState<SharedAccountTokenUsage | null>(null)
  useEffect(() => {
    let cancelled = false
    // Through a promise so a missing client (older server, tests) is a quiet rejection.
    const load = () => { void Promise.resolve().then(() => authApi.getMyTokenUsage()).then(next => { if (!cancelled) setUsage(next) }).catch(() => undefined) }
    load()
    const timer = window.setInterval(load, 60_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [])
  return usage
}

/**
 * The same usage as a small chip for the chat input's toolbar: the tighter of
 * the two limits, with both in the tooltip. Plain use when no limit is set.
 * accountProvider is the shared account the chat runs on (null: the person's
 * own account, which shows nothing; undefined: unknown, the overall figures).
 */
export function SharedTokenUsageChip({ accountProvider }: { accountProvider?: string | null }) {
  const usage = useSharedTokenUsage()
  // A chat on the person's own account neither counts nor is limited, so it shows nothing (owner, 2026-10-07).
  if (!usage || accountProvider === null) return null
  const shown = shownTokenFigures(usage, accountProvider)
  // Only with a limit set on this account or the overall cap (owner, 2026-10-07).
  if (!shown.daily_limit && !shown.weekly_limit) return null
  const daily = shown.daily_limit ? shown.daily_used / shown.daily_limit : -1
  const weekly = shown.weekly_limit ? shown.weekly_used / shown.weekly_limit : -1
  // Short, like Claude Code's usage line: "Day: 32%  Week: 70%" (only the limits that are set); figures and resets in the
  // tooltip (owner, 2026-10-07).
  const pct = (v: number) => `${Math.min(999, Math.round(v * 100))}%`
  const label = [daily >= 0 ? `Day: ${pct(daily)}` : '', weekly >= 0 ? `Week: ${pct(weekly)}` : ''].filter(Boolean).join('  ')
  const tone = shown.state === 'over'
    ? 'border-red-300 text-red-700 dark:border-red-900 dark:text-red-300'
    : shown.state === 'warning'
      ? 'border-amber-500/50 text-amber-700 dark:text-amber-300'
      : 'border-border text-muted-foreground'
  const detail: string[] = [`Tokens on ${shown.scope}.`]
  if (shown.daily_limit) detail.push(`Today: ${formatTokens(shown.daily_used)} of ${formatTokens(shown.daily_limit)} (resets ${resetLabel(usage.day_resets_at, false)})`)
  if (shown.weekly_limit) detail.push(`This week: ${formatTokens(shown.weekly_used)} of ${formatTokens(shown.weekly_limit)} (resets ${resetLabel(usage.week_resets_at, true)})`)
  detail.push(shown.state === 'over' ? `New messages on ${shown.scope} are paused; switch to another account in Models.` : 'Your own accounts are not limited.')
  return (
    <span data-testid="shared-token-usage-chip" title={detail.join('\n')} aria-label={`Shared-account tokens: ${detail.join(' ')}`}
      className={`inline-flex h-7 items-center whitespace-pre rounded-md border px-2 text-[11px] tabular-nums ${tone}`}>
      {label}
    </span>
  )
}

/**
 * The Models panel line next to the selected account: which kind of account the chat runs on and its
 * limits (owner, 2026-10-07). "Your own account · not limited", or "Shared account · limits: Day 32% ·
 * Week 70% (resets …)" with only the limits that exist, "no limit" when none. accountProvider is the
 * shared account's provider (null: the person's own account; undefined: not known yet, nothing shown).
 */
export function SharedTokenUsageNotice({ className = '', accountProvider }: { className?: string; accountProvider?: string | null }) {
  const usage = useSharedTokenUsage()
  if (accountProvider === undefined) return null
  const base = 'rounded-lg border px-3 py-2 text-xs'
  if (accountProvider === null) {
    return <p data-testid="account-limits" className={`${base} border-border bg-muted/40 text-muted-foreground ${className}`}>Your own account · not limited</p>
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
    ? `Shared account · limits: ${limits.join(' · ')}${resets.length ? ` (resets ${resets.join(', ')})` : ''}`
    : usage ? 'Shared account · no limit' : 'Shared account'
  const state = limits.length ? shown?.state : 'ok'
  const tone = state === 'over'
    ? 'border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/25 dark:text-red-300'
    : state === 'warning'
      ? 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300'
      : 'border-border bg-muted/40 text-muted-foreground'
  const detail: string[] = []
  if (shown?.daily_limit) detail.push(`Today: ${formatTokens(shown.daily_used)} of ${formatTokens(shown.daily_limit)}`)
  if (shown?.weekly_limit) detail.push(`This week: ${formatTokens(shown.weekly_used)} of ${formatTokens(shown.weekly_limit)}`)
  return (
    <div data-testid="account-limits" role={state === 'ok' ? undefined : 'status'} title={detail.length ? `Tokens on ${shown?.scope}. ${detail.join('. ')}.` : undefined}
      className={`${base} ${tone} ${className}`}>
      <p>{text}</p>
      {state === 'over' && <p className="mt-1">New messages on {shown?.scope} are paused. Switch to another account here or ask an admin to raise your limit.</p>}
      {state === 'warning' && <p className="mt-1">You have used over 80% of a limit. Your own accounts are not limited.</p>}
    </div>
  )
}
