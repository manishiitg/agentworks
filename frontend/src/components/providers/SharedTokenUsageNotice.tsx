import { useEffect, useState } from 'react'
import { authApi, type SharedAccountTokenUsage } from '../../services/api'
import { formatTokens, resetLabel } from '../../utils/tokenLimits'

/**
 * The person's own use of the shared server accounts against the limits an
 * admin set (PLAT-683). Shown only when a limit is set; amber from 80%, red at
 * the limit, when new turns on a server account are refused.
 */
export function SharedTokenUsageNotice({ className = '' }: { className?: string }) {
  const [usage, setUsage] = useState<SharedAccountTokenUsage | null>(null)
  useEffect(() => {
    let cancelled = false
    // Through a promise so a missing client (older server, tests) is a quiet rejection.
    const load = () => { void Promise.resolve().then(() => authApi.getMyTokenUsage()).then(next => { if (!cancelled) setUsage(next) }).catch(() => undefined) }
    load()
    const timer = window.setInterval(load, 60_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [])
  if (!usage || (!usage.daily_limit && !usage.weekly_limit)) return null
  const tone = usage.state === 'over'
    ? 'border-red-300 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/25 dark:text-red-300'
    : usage.state === 'warning'
      ? 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300'
      : 'border-border bg-muted/40 text-muted-foreground'
  const parts: string[] = []
  if (usage.daily_limit) parts.push(`${formatTokens(usage.daily_used)} of ${formatTokens(usage.daily_limit)} today (resets ${resetLabel(usage.day_resets_at, false)})`)
  if (usage.weekly_limit) parts.push(`${formatTokens(usage.weekly_used)} of ${formatTokens(usage.weekly_limit)} this week (resets ${resetLabel(usage.week_resets_at, true)})`)
  return (
    <div role={usage.state === 'ok' ? undefined : 'status'} className={`rounded-lg border px-3 py-2 text-xs ${tone} ${className}`}>
      <p><span className="font-medium">Shared-account tokens:</span> {parts.join(' · ')}.</p>
      {usage.state === 'over' && <p className="mt-1">New messages on the shared accounts are paused. Use your own account or ask an admin to raise your limit.</p>}
      {usage.state === 'warning' && <p className="mt-1">You have used over 80% of a limit. Your own accounts are not limited.</p>}
    </div>
  )
}
