import { formatTokens } from '../utils/formatTokens'

/** The chat's live context fill for the transcript footer (PLAT-554), from its terminal status. The server
 * fills the main terminal's figures from the chat's one usage reader; plan limits are never on a terminal. */
export interface LiveUsageSummary {
  /** "ctx 42%" or, without a known window, "ctx 235k". Empty when unknown. */
  contextText: string
  /** Detail for the hover: "235k of 1M tokens in context". */
  contextTitle: string
  usedTokens: number
  /** 0 when the window is unknown. */
  windowTokens: number
}

function positiveNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

export function liveUsageSummary(statusMeta: Record<string, unknown> | null | undefined): LiveUsageSummary {
  const used = positiveNumber(statusMeta?.context_used_tokens)
  const window = positiveNumber(statusMeta?.context_window_tokens)
  if (used > 0 && window > 0) {
    return { contextText: `ctx ${Math.round(Math.min(100, (used / window) * 100))}%`, contextTitle: `${formatTokens(used)} of ${formatTokens(window)} tokens in context`, usedTokens: used, windowTokens: window }
  }
  if (used > 0) return { contextText: `ctx ${formatTokens(used)}`, contextTitle: `${formatTokens(used)} tokens in context`, usedTokens: used, windowTokens: 0 }
  return { contextText: '', contextTitle: '', usedTokens: 0, windowTokens: 0 }
}
