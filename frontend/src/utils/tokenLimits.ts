// Shared-account token limits (PLAT-683): formatting and parsing for the
// admin Users page and the person's own Models panel.

/** 1234567 -> "1.2M"; small numbers stay exact. */
export function formatTokens(n: number | undefined): string {
  const v = Math.max(0, Math.round(n || 0))
  if (v >= 1_000_000_000) return `${trim(v / 1_000_000_000)}B`
  if (v >= 1_000_000) return `${trim(v / 1_000_000)}M`
  if (v >= 10_000) return `${trim(v / 1_000)}k`
  return v.toLocaleString('en-US')
}

function trim(x: number): string {
  return x >= 100 ? x.toFixed(0) : x.toFixed(1).replace(/\.0$/, '')
}

/** "5M", "250k", "1,000,000" or "" (unlimited = 0). Returns null when unreadable. */
export function parseTokenAmount(text: string): number | null {
  const t = text.trim().replace(/[,_\s]/g, '').toLowerCase()
  if (t === '') return 0
  const m = /^(\d+(?:\.\d+)?)([kmb]?)$/.exec(t)
  if (!m) return null
  const scale = m[2] === 'k' ? 1_000 : m[2] === 'm' ? 1_000_000 : m[2] === 'b' ? 1_000_000_000 : 1
  return Math.round(parseFloat(m[1]) * scale)
}

/** "00:00 UTC" style reset time shown next to a limit. */
export function resetLabel(iso: string | undefined, weekly: boolean): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const hm = `${String(d.getUTCHours()).padStart(2, '0')}:${String(d.getUTCMinutes()).padStart(2, '0')} UTC`
  return weekly ? `Monday ${hm}` : hm
}
