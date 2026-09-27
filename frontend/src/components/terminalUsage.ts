// Plan-usage lines for the coding CLI behind a terminal, shown on hover of the
// composer's terminal icon ("5h 17% · resets 3:30 PM").
//
// The server exposes two shapes on status.status_meta:
//   - rate_limit_windows: structured [{name, used_percent, resets_at}] where
//     resets_at is an RFC3339 timestamp (Go time.Time; the zero time means the
//     provider did not state a reset). Preferred: the reset is rendered in the
//     BROWSER's locale/timezone.
//   - status_extras: display-ready strings formatted in the server's timezone
//     ("5h 17% →3:30pm", "ctx 42%"). Only used when windows are missing.

export interface TerminalUsageLine {
  /** Short window label, e.g. "5h", "7d", "7d opus". */
  label: string
  /** Rounded percentage used, when known. */
  usedPercent?: number
  /** Full display text, e.g. "5h 17% · resets 3:30 PM". */
  text: string
  /** True when usage is at or above HIGH_USAGE_PERCENT. */
  high: boolean
}

export const HIGH_USAGE_PERCENT = 90

const DAY_MS = 24 * 60 * 60 * 1000

const NAMED_WINDOW_LABELS: Record<string, string> = {
  five_hour: '5h',
  seven_day: '7d',
  seven_day_opus: '7d opus',
  seven_day_sonnet: '7d sonnet',
}

function minutesLabel(minutes: number): string {
  if (minutes < 60) return `${minutes}m`
  if (minutes % 1440 === 0) return `${minutes / 1440}d`
  if (minutes % 60 === 0) return `${minutes / 60}h`
  return `${minutes}m`
}

export function usageWindowLabel(name: string, windowMinutes?: number): string {
  const trimmed = name.trim()
  const named = NAMED_WINDOW_LABELS[trimmed]
  if (named) return named
  const match = /^window_(\d+)m$/.exec(trimmed)
  if (match) return minutesLabel(Number(match[1]))
  if ((trimmed === 'primary' || trimmed === 'secondary') && windowMinutes && windowMinutes > 0) {
    return minutesLabel(windowMinutes)
  }
  return trimmed
}

/** Parses resets_at (RFC3339 string, or unix seconds/ms) into epoch ms. */
function parseResetsAt(value: unknown): number | null {
  let ms: number
  if (typeof value === 'number' && Number.isFinite(value)) {
    ms = value > 1e12 ? value : value * 1000
  } else if (typeof value === 'string' && value.trim() !== '') {
    ms = Date.parse(value)
  } else {
    return null
  }
  // Go's zero time ("0001-01-01T00:00:00Z") and 0 both mean "unknown".
  if (!Number.isFinite(ms) || ms <= 0) return null
  return ms
}

export function formatUsageReset(resetMs: number, now: number, locale?: string): string {
  const date = new Date(resetMs)
  const time = date.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
  if (resetMs - now >= DAY_MS) {
    const weekday = date.toLocaleDateString(locale, { weekday: 'short' })
    return `${weekday} ${time}`
  }
  return time
}

function windowLines(raw: unknown, now: number, locale?: string): TerminalUsageLine[] {
  if (!Array.isArray(raw)) return []
  const lines: TerminalUsageLine[] = []
  for (const item of raw) {
    if (!item || typeof item !== 'object') continue
    const record = item as Record<string, unknown>
    const name = typeof record.name === 'string' ? record.name : ''
    const used = typeof record.used_percent === 'number' && Number.isFinite(record.used_percent)
      ? record.used_percent
      : null
    if (!name.trim() || used === null) continue
    const windowMinutes = typeof record.window_minutes === 'number' ? record.window_minutes : undefined
    const label = usageWindowLabel(name, windowMinutes)
    const rounded = Math.round(used)
    const resetMs = parseResetsAt(record.resets_at)
    const reset = resetMs !== null ? ` · resets ${formatUsageReset(resetMs, now, locale)}` : ''
    lines.push({ label, usedPercent: rounded, text: `${label} ${rounded}%${reset}`, high: used >= HIGH_USAGE_PERCENT })
  }
  return lines
}

function extrasLines(raw: unknown): TerminalUsageLine[] {
  if (!Array.isArray(raw)) return []
  const lines: TerminalUsageLine[] = []
  for (const value of raw) {
    if (typeof value !== 'string') continue
    const text = value.trim()
    const pct = /(\d+(?:\.\d+)?)\s*%/.exec(text)
    if (!text || !pct || /^ctx\b/i.test(text)) continue
    const used = Number(pct[1])
    lines.push({
      label: text.slice(0, pct.index).trim(),
      usedPercent: Math.round(used),
      text,
      high: used >= HIGH_USAGE_PERCENT,
    })
  }
  return lines
}

/**
 * Plan-usage lines for a terminal's status_meta. Prefers structured
 * rate_limit_windows; falls back to percentage-bearing status_extras
 * (excluding the "ctx …" context-window segment).
 */
export function terminalUsageLines(
  statusMeta: Record<string, unknown> | null | undefined,
  options: { now?: number; locale?: string } = {},
): TerminalUsageLine[] {
  if (!statusMeta) return []
  const now = options.now ?? Date.now()
  const windows = windowLines(statusMeta.rate_limit_windows, now, options.locale)
  if (windows.length > 0) return windows
  return extrasLines(statusMeta.status_extras)
}
