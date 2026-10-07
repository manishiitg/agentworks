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
  const showWeekly = weekly > daily
  // Short: the closer limit as a percentage; the token figures are in the tooltip (owner, 2026-10-07).
  const label = `${Math.min(999, Math.round(Math.max(daily, weekly) * 100))}% ${showWeekly ? 'week' : 'today'}`
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
      className={`inline-flex h-7 items-center rounded-md border px-2 text-[11px] tabular-nums ${tone}`}>
      {label}
    </span>
  )
}

/** The Models panel notice; shown only when the selected account (or the overall cap) has a limit. */
export function SharedTokenUsageNotice({ className = '', accountProvider }: { className?: string; accountProvider?: string | null }) {
  const usage = useSharedTokenUsage()
  if (!usage || accountProvider === null) return null
  const shown = shownTokenFigures(usage, accountProvider)
  if (!shown.daily_limit && !shown.weekly_limit) return null
  const tone = shown.state === 'over'
    ? 'border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/25 dark:text-red-300'
    : shown.state === 'warning'
      ? 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300'
      : 'border-border bg-muted/40 text-muted-foreground'
  const parts: string[] = []
  if (shown.daily_limit) parts.push(`${formatTokens(shown.daily_used)} of ${formatTokens(shown.daily_limit)} today (resets ${resetLabel(usage.day_resets_at, false)})`)
  if (shown.weekly_limit) parts.push(`${formatTokens(shown.weekly_used)} of ${formatTokens(shown.weekly_limit)} this week (resets ${resetLabel(usage.week_resets_at, true)})`)
  return (
    <div role={shown.state === 'ok' ? undefined : 'status'} className={`rounded-lg border px-3 py-2 text-xs ${tone} ${className}`}>
      <p><span className="font-medium">Tokens on {shown.scope}:</span> {parts.join(' · ')}.</p>
      {shown.state === 'over' && <p className="mt-1">New messages on {shown.scope} are paused. Switch to another account here or ask an admin to raise your limit.</p>}
      {shown.state === 'warning' && <p className="mt-1">You have used over 80% of a limit. Your own accounts are not limited.</p>}
    </div>
  )
}
