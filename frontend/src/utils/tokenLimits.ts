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

/**
 * In a person's override of one shared account, a field of -1 is "Unlimited":
 * no limit for them on that account even when the account has a default; 0
 * (empty) falls back to the default (PLAT-693).
 */
export const TOKEN_LIMIT_UNLIMITED = -1

/** parseTokenAmount for an account override field: also "Unlimited" (or "none", "∞") -> -1. */
export function parseOverrideAmount(text: string): number | null {
  const t = text.trim().toLowerCase()
  if (t === 'unlimited' || t === 'none' || t === '∞' || t === '-1') return TOKEN_LIMIT_UNLIMITED
  return parseTokenAmount(text)
}

/** The exact stored override value for editing: "Unlimited", a number, or "" (falls back to the default). */
export function shownOverrideLimit(n?: number): string {
  if (n !== undefined && n < 0) return 'Unlimited'
  return n ? String(n) : ''
}

/** "00:00 UTC" style reset time shown next to a limit. */
export function resetLabel(iso: string | undefined, weekly: boolean): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const hm = `${String(d.getUTCHours()).padStart(2, '0')}:${String(d.getUTCMinutes()).padStart(2, '0')} UTC`
  return weekly ? `Monday ${hm}` : hm
}

/** One set of shared-account token figures (overall or one account). */
export interface TokenFigures {
  daily_used: number
  weekly_used: number
  daily_limit?: number
  weekly_limit?: number
  state: 'ok' | 'warning' | 'over'
}

/** The figures a chat shows, with what they count ("the shared Codex account"). */
export interface ShownTokenFigures extends TokenFigures {
  scope: string
}

/**
 * The provider of the shared server account a chat runs on, from its
 * connection ID (PLAT-693): `global:<p>` or `server-default:<p>` name it, empty
 * is the profile's default server account (fallbackProvider), anything else is
 * the person's own account (null: nothing counts or is limited).
 */
export function sharedAccountProvider(connectionId: string | undefined, fallbackProvider?: string): string | null | undefined {
  const id = (connectionId || '').trim()
  if (!id) return fallbackProvider || undefined
  for (const prefix of ['global:', 'server-default:']) {
    if (id.startsWith(prefix)) return id.slice(prefix.length) || fallbackProvider || undefined
  }
  return null
}

const limitFraction = (f: TokenFigures): number => Math.max(
  f.daily_limit ? f.daily_used / f.daily_limit : -1,
  f.weekly_limit ? f.weekly_used / f.weekly_limit : -1,
)

/**
 * What to show for a chat on provider's shared account: the account's own
 * limit or the overall cap across all shared accounts, whichever is nearer
 * its limit; with no limit, the account's plain use. Without a known
 * provider, the overall figures.
 */
export function shownTokenFigures(
  usage: TokenFigures & { accounts?: Record<string, TokenFigures & { label?: string }> },
  provider?: string,
): ShownTokenFigures {
  const overall: ShownTokenFigures = { ...usage, scope: 'the shared accounts' }
  if (!provider) return overall
  const known = usage.accounts?.[provider]
  const account: ShownTokenFigures = known
    ? { ...known, scope: `the shared ${known.label || provider} account` }
    : { daily_used: 0, weekly_used: 0, state: 'ok', scope: 'this shared account' }
  const a = limitFraction(account)
  const o = limitFraction(overall)
  if (a < 0 && o < 0) return account
  return a >= o ? account : overall
}

const SHARED_ACCOUNT_LABELS: Record<string, string> = {
  'claude-code': 'Claude Code', 'codex-cli': 'Codex', 'cursor-cli': 'Cursor', 'muse-cli': 'Muse', 'pi-cli': 'Pi', 'agy-cli': 'Antigravity',
}

/** Short name of a shared account, as in "the shared Codex account". */
export function sharedAccountLabel(provider: string): string {
  return SHARED_ACCOUNT_LABELS[provider] || provider
}
