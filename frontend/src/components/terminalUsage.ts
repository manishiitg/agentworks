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

/** The session fields of a terminal status the hover summarizes. */
export interface TerminalSessionUsageInput {
  input_tokens?: number
  output_tokens?: number
  cache_read_input_tokens?: number
  total_input_tokens?: number
  total_output_tokens?: number
  cost_usd?: number
  status_meta?: Record<string, unknown>
}

/** Compact token count: 950, 36k, 1.2M. */
export function formatTokenCount(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1).replace(/\.0$/, '')}M`
  if (value >= 1_000) return `${Math.round(value / 1_000)}k`
  return String(Math.round(value))
}

function positive(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

/**
 * Session lines under the plan windows: context-window fill, tokens used
 * (with the cached share) and cost, plus any other short CLI details
 * (reasoning effort, plan). Each line appears only when the CLI reported it.
 */
export function terminalSessionUsageLines(status: TerminalSessionUsageInput | null | undefined): string[] {
  if (!status) return []
  const lines: string[] = []
  const extras = Array.isArray(status.status_meta?.status_extras)
    ? (status.status_meta?.status_extras as unknown[]).filter((value): value is string => typeof value === 'string' && value.trim() !== '').map(value => value.trim())
    : []
  const context = extras.find(value => /^ctx\b/i.test(value))
  const contextPct = context ? /(\d+(?:\.\d+)?)\s*%/.exec(context) : null
  if (contextPct) lines.push(`Context ${Math.round(Number(contextPct[1]))}%`)

  const input = positive(status.total_input_tokens) || positive(status.input_tokens)
  const output = positive(status.total_output_tokens) || positive(status.output_tokens)
  const cached = positive(status.cache_read_input_tokens)
  const cost = positive(status.cost_usd)
  const parts: string[] = []
  if (input > 0) parts.push(`${formatTokenCount(input)} in${cached > 0 ? ` (${formatTokenCount(cached)} cached)` : ''}`)
  if (output > 0) parts.push(`${formatTokenCount(output)} out`)
  if (cost > 0) parts.push(`$${cost.toFixed(cost >= 100 ? 0 : 2)}`)
  if (parts.length > 0) lines.push(`Session: ${parts.join(' · ')}`)

  const details = extras.filter(value => !/%/.test(value))
  if (details.length > 0) lines.push(details.join(' · '))
  return lines
}

/** Live context fill and the worst plan window, for the chat's working footer (PLAT-554). */
export interface LiveUsageSummary {
  /** "ctx 42%" or, without a known window, "ctx 235k". Empty when unknown. */
  contextText: string
  /** 0-100 when the window is known. */
  contextPercent?: number
  /** Detail for the hover: "235k of 1M tokens in context". */
  contextTitle: string
  /** The most-used plan window at or above HIGH_USAGE_PERCENT, if any. */
  warning?: TerminalUsageLine
}

function positiveNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

export function liveUsageSummary(
  statusMeta: Record<string, unknown> | null | undefined,
  options: { now?: number; locale?: string } = {},
): LiveUsageSummary {
  const summary: LiveUsageSummary = { contextText: '', contextTitle: '' }
  if (!statusMeta) return summary
  const used = positiveNumber(statusMeta.context_used_tokens)
  const window = positiveNumber(statusMeta.context_window_tokens)
  if (used > 0 && window > 0) {
    const pct = Math.min(100, (used / window) * 100)
    summary.contextPercent = pct
    summary.contextText = `ctx ${Math.round(pct)}%`
    summary.contextTitle = `${formatTokenCount(used)} of ${formatTokenCount(window)} tokens in context`
  } else if (used > 0) {
    summary.contextText = `ctx ${formatTokenCount(used)}`
    summary.contextTitle = `${formatTokenCount(used)} tokens in context`
  } else {
    const extras = Array.isArray(statusMeta.status_extras) ? statusMeta.status_extras : []
    const ctx = extras.find((value): value is string => typeof value === 'string' && /^ctx\b/i.test(value.trim()))
    const pct = ctx ? /(\d+(?:\.\d+)?)\s*%/.exec(ctx) : null
    if (pct) {
      summary.contextPercent = Math.min(100, Number(pct[1]))
      summary.contextText = `ctx ${Math.round(Number(pct[1]))}%`
      summary.contextTitle = 'Context window used'
    }
  }
  const high = terminalUsageLines(statusMeta, options).filter(line => line.high)
  if (high.length > 0) {
    summary.warning = high.reduce((worst, line) => ((line.usedPercent ?? 0) > (worst.usedPercent ?? 0) ? line : worst))
  }
  return summary
}
