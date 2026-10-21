// Named value formatters. Maps a named preset (ReportFormatterName) to a
// stringification function.
// Keep these as named presets — not user-supplied expressions — so the schema
// stays bounded and predictable.

import type { ReportFormatterName } from '../services/api-types'

function asNumber(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (trimmed === '') return null
    const n = Number(trimmed)
    return Number.isFinite(n) ? n : null
  }
  return null
}

// Parses the date shapes that actually appear in workspace db files:
//   ISO with zone (2026-05-04T01:49:01Z / ...+05:30), naive ISO (2026-05-04T01:49:01),
//   space-separated (2026-05-04 01:49:01), and date-only (2026-05-04).
// Date-only and zone-less datetimes are interpreted in LOCAL time so a value
// never shifts a day across the UTC boundary (the bare `new Date('2026-05-04')`
// trap, which parses as UTC midnight). Anything carrying an explicit zone is
// passed to the native parser unchanged.
function asDate(value: unknown): Date | null {
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value
  if (typeof value === 'number') {
    const d = new Date(value)
    return Number.isNaN(d.getTime()) ? null : d
  }
  if (typeof value !== 'string') return null
  const s = value.trim()
  if (s === '') return null
  // Date-only YYYY-MM-DD → local midnight.
  const dateOnly = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s)
  if (dateOnly) {
    return new Date(Number(dateOnly[1]), Number(dateOnly[2]) - 1, Number(dateOnly[3]))
  }
  // Zone-less datetime "YYYY-MM-DD[ T]HH:MM[:SS]" → local.
  const naive = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?$/.exec(s)
  if (naive) {
    return new Date(
      Number(naive[1]), Number(naive[2]) - 1, Number(naive[3]),
      Number(naive[4]), Number(naive[5]), Number(naive[6] ?? '0'),
    )
  }
  // Explicit zone (Z / ±HH:MM) or any other format → native parse.
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? null : d
}

// Used by sortable column headers to compare cells. Numeric/date values sort
// natively; everything else falls back to localeCompare.
export function compareValues(a: unknown, b: unknown): number {
  if (a == null && b == null) return 0
  if (a == null) return 1
  if (b == null) return -1
  const an = asNumber(a)
  const bn = asNumber(b)
  if (an != null && bn != null) return an - bn
  const ad = asDate(a)
  const bd = asDate(b)
  if (ad && bd) return ad.getTime() - bd.getTime()
  return String(a).localeCompare(String(b), undefined, { numeric: true })
}
