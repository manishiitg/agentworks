const MINUTE = 60
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

export type RelativeTimeStyle = 'compact' | 'long'

/**
 * Formats a Unix timestamp in seconds relative to `nowSeconds`.
 * - `compact` (default): "now", "5m", "3h", "4d"
 * - `long`: "now", "5m ago", "3h ago", "4d ago", "5w ago", "7mo ago"
 */
export function formatRelativeSeconds(
  atSeconds?: number | null,
  options?: { nowSeconds?: number; style?: RelativeTimeStyle },
): string {
  if (atSeconds === undefined || atSeconds === null || !Number.isFinite(atSeconds)) return ''
  const nowSeconds = options?.nowSeconds ?? Date.now() / 1000
  const style = options?.style ?? 'compact'
  const s = Math.max(0, nowSeconds - atSeconds)
  if (s < MINUTE) return 'now'
  if (style === 'long') {
    if (s < HOUR) return `${Math.floor(s / MINUTE)}m ago`
    if (s < DAY) return `${Math.floor(s / HOUR)}h ago`
    if (s < 14 * DAY) return `${Math.floor(s / DAY)}d ago`
    if (s < 60 * DAY) return `${Math.floor(s / (7 * DAY))}w ago`
    return `${Math.floor(s / (30 * DAY))}mo ago`
  }
  if (s < HOUR) return `${Math.floor(s / MINUTE)}m`
  if (s < DAY) return `${Math.floor(s / HOUR)}h`
  return `${Math.floor(s / DAY)}d`
}

/**
 * Formats an ISO-8601 timestamp string relative to `nowMs`.
 * Returns "" when missing or unparseable.
 */
export function formatRelativeIso(
  at?: string | null,
  options?: { nowMs?: number; style?: RelativeTimeStyle },
): string {
  if (!at) return ''
  const parsed = Date.parse(at)
  if (!Number.isFinite(parsed)) return ''
  const nowMs = options?.nowMs ?? Date.now()
  return formatRelativeSeconds(parsed / 1000, {
    nowSeconds: nowMs / 1000,
    style: options?.style,
  })
}

/**
 * A chat message's clock label: just the time for today ("09:15"), then
 * "Yesterday 09:15", "27 Nov 09:15" this year and "27 Nov 2025 09:15" before.
 * Local time; `locale` is only for tests.
 */
export function formatChatClock(at: Date | string | number, now: Date = new Date(), locale?: string): string {
  const date = at instanceof Date ? at : new Date(at)
  if (!Number.isFinite(date.getTime())) return ''
  const time = date.toLocaleTimeString(locale ?? [], { hour: '2-digit', minute: '2-digit' })
  const dayStart = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const days = Math.round((dayStart(now) - dayStart(date)) / (DAY * 1000))
  if (days === 0) return time
  if (days === 1) return `Yesterday ${time}`
  const day = date.toLocaleDateString(locale ?? [], date.getFullYear() === now.getFullYear()
    ? { day: 'numeric', month: 'short' }
    : { day: 'numeric', month: 'short', year: 'numeric' })
  return `${day} ${time}`
}
