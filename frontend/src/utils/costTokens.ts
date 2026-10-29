import type { CostAggregate } from '../services/api-types'
import { formatTokens } from './formatTokens'

// A priced subtotal is not the total when other usage has no known price.
export const costAmountLabel = (
  usage: Pick<CostAggregate, 'total_cost_usd' | 'unpriced_call_count' | 'missing_usage_call_count'>,
  format: (amount: number) => string,
): string => {
  const incomplete = Math.max(usage.unpriced_call_count ?? 0, usage.missing_usage_call_count ?? 0) > 0
  if (incomplete && usage.total_cost_usd === 0) return 'Not priced'
  return `${format(usage.total_cost_usd)}${incomplete ? ' (incomplete)' : ''}`
}

export const inputTokens = (usage?: Partial<CostAggregate>) => {
  if (usage?.input_tokens !== undefined) return usage.input_tokens
  // Compatibility with older servers: Muse input already contains cache.
  const cache = usage?.provider === 'muse-cli' || usage?.provider === 'muse_cli'
    ? 0 : (usage?.cache_read_tokens ?? 0) + (usage?.cache_write_tokens ?? 0)
  return (usage?.prompt_tokens ?? 0) + cache
}

export const tokenSummary = (usage: CostAggregate) =>
  `${formatTokens(inputTokens(usage))} input · ${formatTokens(usage.completion_tokens ?? 0)} output`

export const pricingCoverageText = (usage: CostAggregate) => {
  const missing = usage.missing_usage_call_count ?? 0
  const unpriced = Math.max(0, (usage.unpriced_call_count ?? 0) - missing)
  return [
    missing > 0 ? `${missing.toLocaleString()} ${missing === 1 ? 'call did' : 'calls did'} not report tokens or cost. Their usage and cost are unknown.` : '',
    unpriced > 0 ? `${unpriced.toLocaleString()} ${unpriced === 1 ? 'call has' : 'calls have'} tokens but no price: the provider did not report a cost and no model rate is available. These tokens are included; their cost is unknown.` : '',
  ].filter(Boolean).join(' ')
}

export const totalTokens = (usage?: Partial<CostAggregate>) =>
  inputTokens(usage) + (usage?.completion_tokens ?? 0)

/**
 * Total input already includes cache reads exactly once: the ledger
 * normalizes each entry by its provider's reporting convention
 * (prompt_tokens_include_cache), and inputTokens() applies the same rule to
 * raw usage. So fresh input is what remains after cache reads and writes;
 * never re-add cache on top of the reported input.
 */
export function inputBreakdown(usage?: Partial<CostAggregate>): { total: number; fresh: number; cached: number; writes: number } {
  const raw = inputTokens(usage)
  const cached = usage?.cache_read_tokens ?? 0
  const writes = usage?.cache_write_tokens ?? 0
  // Unnormalized run-file usage: cache can exceed the reported input.
  if (cached > raw) return { total: raw + cached + writes, fresh: raw, cached, writes }
  return { total: raw, fresh: Math.max(0, raw - cached - writes), cached, writes }
}
