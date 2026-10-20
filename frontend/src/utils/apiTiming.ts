// Lightweight, in-memory API response-time (and request/response body)
// tracking -- no backend, no third-party analytics, nothing leaves the
// browser. Mirrors the existing window.perfDiag() console-diagnostic
// pattern (see App.tsx) rather than adding a dependency: this is for a
// developer/operator to run in DevTools when a deployment "feels slow" or
// they need to see exactly what an endpoint was sent/returned, not for
// aggregate product analytics.

export interface ApiTimingEntry {
  method: string
  path: string
  status: number | 'error'
  durationMs: number
  timestamp: number
  requestParams?: unknown
  requestBody?: unknown
  responseBody?: unknown
}

const MAX_ENTRIES = 500
const entries: ApiTimingEntry[] = []

// Keys whose values are never stored, regardless of nesting depth --
// login/credential bodies (password), and any token-shaped field that
// could otherwise let someone reconstruct a live session from these logs.
const SENSITIVE_KEY_PATTERN = /password|token|secret|api[-_]?key|authorization|oauth/i
const MAX_BODY_JSON_CHARS = 4000
const MAX_REDACT_DEPTH = 6
const MAX_COLLECTION_ENTRIES = 64

// Copy bounded text instead of retaining a slice backed by a multi-MB response.
function copyPreview(value: string, length: number): string {
  return value.slice(0, length).split('').join('')
}

type BodyBudget = { remaining: number; truncated: boolean }

function redactValue(value: unknown, depth: number, budget: BodyBudget): unknown {
  if (budget.remaining <= 0) { budget.truncated = true; return '[truncated]' }
  if (depth > MAX_REDACT_DEPTH) return '[max depth]'
  if (typeof value === 'string') {
    const length = Math.min(value.length, budget.remaining)
    budget.remaining -= length + 2
    if (length < value.length) budget.truncated = true
    return copyPreview(value, length)
  }
  if (Array.isArray(value)) {
    const out: unknown[] = []
    for (let i = 0; i < value.length; i++) {
      if (i >= MAX_COLLECTION_ENTRIES || budget.remaining <= 0) { budget.truncated = true; break }
      budget.remaining -= 1
      out.push(redactValue(value[i], depth + 1, budget))
    }
    return out
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    let count = 0
    // Do not allocate Object.entries for an unbounded response object.
    for (const key in value) {
      if (!Object.prototype.hasOwnProperty.call(value, key)) continue
      if (count++ >= MAX_COLLECTION_ENTRIES || key.length + 4 > budget.remaining) { budget.truncated = true; break }
      budget.remaining -= key.length + 4
      const safe = SENSITIVE_KEY_PATTERN.test(key) ? '[redacted]' : (value as Record<string, unknown>)[key]
      Object.defineProperty(out, key, { value: redactValue(safe, depth + 1, budget), enumerable: true })
    }
    return out
  }
  budget.remaining -= 8
  return value
}

// Bound traversal BEFORE cloning/serialization. Cloning a full chat response
// just to keep its first 4 KB caused large transient allocations; a sliced
// preview could also keep that entire serialized response alive in the ring.
export function sanitizeApiBody(value: unknown): unknown {
  if (value === undefined || value === null) return value
  try {
    const budget: BodyBudget = { remaining: MAX_BODY_JSON_CHARS, truncated: false }
    const redacted = redactValue(value, 0, budget)
    const json = JSON.stringify(redacted)
    if (json === undefined) return redacted
    if (budget.truncated || json.length > MAX_BODY_JSON_CHARS) {
      return { truncated: true, preview: copyPreview(json, MAX_BODY_JSON_CHARS) }
    }
    return redacted
  } catch {
    return '[unserializable]'
  }
}

// Strips the origin and query string so entries group by endpoint shape
// (e.g. "/api/workflows/manifests"), not by every distinct query/host.
export function apiTimingPathFor(url: string | undefined, baseURL: string | undefined): string {
  if (!url) return '(unknown)'
  try {
    // The dummy fallback base only matters for a relative url with no
    // baseURL; new URL() ignores it entirely when url is already absolute.
    return new URL(url, baseURL || 'http://localhost').pathname
  } catch {
    return url.split('?')[0]
  }
}

export function recordApiTiming(entry: ApiTimingEntry) {
  entries.push(entry)
  if (entries.length > MAX_ENTRIES) entries.shift()
}

export function getApiTimings(): ApiTimingEntry[] {
  return entries.slice()
}

export function clearApiTimings() {
  entries.length = 0
}

// Entries matching an optional path/method substring, most recent last --
// the lookup apiLog() in App.tsx uses to show request/response detail
// (apiPerf()'s console.table rows can't usefully render nested objects).
export function apiLogEntries(filter?: string, limit = 50): ApiTimingEntry[] {
  const matching = filter
    ? entries.filter((e) => e.path.includes(filter) || e.method.includes(filter.toUpperCase()))
    : entries
  return matching.slice(-limit)
}

interface ApiTimingAggregate {
  endpoint: string
  calls: number
  errors: number
  avgMs: number
  p95Ms: number
  maxMs: number
  totalMs: number
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0
  const index = Math.min(sorted.length - 1, Math.ceil((p / 100) * sorted.length) - 1)
  return sorted[Math.max(0, index)]
}

export function summarizeApiTimings(): { aggregates: ApiTimingAggregate[]; recent: ApiTimingEntry[] } {
  const byEndpoint = new Map<string, ApiTimingEntry[]>()
  for (const entry of entries) {
    const key = `${entry.method} ${entry.path}`
    const bucket = byEndpoint.get(key)
    if (bucket) bucket.push(entry)
    else byEndpoint.set(key, [entry])
  }

  const aggregates: ApiTimingAggregate[] = []
  for (const [endpoint, calls] of byEndpoint.entries()) {
    const durations = calls.map((c) => c.durationMs).sort((a, b) => a - b)
    const totalMs = durations.reduce((sum, d) => sum + d, 0)
    aggregates.push({
      endpoint,
      calls: calls.length,
      errors: calls.filter((c) => c.status === 'error' || (typeof c.status === 'number' && c.status >= 400)).length,
      avgMs: Math.round(totalMs / durations.length),
      p95Ms: Math.round(percentile(durations, 95)),
      maxMs: Math.round(durations[durations.length - 1] ?? 0),
      totalMs: Math.round(totalMs),
    })
  }
  aggregates.sort((a, b) => b.totalMs - a.totalMs)

  const recent = entries.slice(-30).slice().sort((a, b) => b.durationMs - a.durationMs)

  return { aggregates, recent }
}
