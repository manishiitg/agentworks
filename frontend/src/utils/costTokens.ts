import type { CostAggregate } from '../services/api-types'
import { formatTokens } from '../components/workflow/costs/helpers'

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
