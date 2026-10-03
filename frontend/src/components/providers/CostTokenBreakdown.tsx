import type { CostAggregate } from '../../services/api-types'
import { inputTokens, pricingCoverageText } from '../../utils/costTokens'
import { formatTokens } from '../workflow/costs/helpers'

export default function CostTokenBreakdown({ usage, compact = false }: { usage: Partial<CostAggregate>; compact?: boolean }) {
  const input = inputTokens(usage)
  const cached = usage.cache_read_tokens ?? 0
  const writes = usage.cache_write_tokens ?? 0
  const fresh = Math.max(0, input - cached - writes)
  const share = input > 0 ? `${(cached / input * 100).toFixed(1)}%` : '—'
  return <section className="rounded-lg border border-border bg-muted/20 p-3 text-xs">
    <h4 className="mb-2 font-semibold text-foreground">Input and cache</h4>
    <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {[
        ['Total input', formatTokens(input)],
        ['Fresh input', formatTokens(fresh)],
        ['Cached input (read)', formatTokens(cached)],
        ['Output', formatTokens(usage.completion_tokens ?? 0)],
        ...(writes > 0 ? [['Cache writes', formatTokens(writes)]] : []),
      ].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd className="mt-1 font-mono font-semibold text-foreground">{value}</dd></div>)}
    </dl>
    <details className="mt-3 text-muted-foreground">
      <summary className="w-fit cursor-pointer hover:text-foreground">{input > 0 ? `${share} of input cached · ` : ''}Token details</summary>
      <p className="mt-2">Fresh input{writes > 0 ? ', cache reads and cache writes' : ' and cache reads'} add up to total input; output is separate.</p>
      {!compact && <p className="mt-1">Cache reuses a matching input prefix. New history, tool results and outputs still require processing. Identical user messages do not guarantee identical full inputs.</p>}
    </details>
  </section>
}

/** Keep unknown costs visible without repeating the full accounting explanation. */
export function CostPricingNotice({ usage, className = '' }: { usage: CostAggregate; className?: string }) {
  const message = pricingCoverageText(usage)
  if (!message) return null
  const missing = usage.missing_usage_call_count ?? 0
  const unpriced = Math.max(0, (usage.unpriced_call_count ?? 0) - missing)
  const label = [
    missing > 0 ? `${missing.toLocaleString()} ${missing === 1 ? 'call' : 'calls'} missing usage` : '',
    unpriced > 0 ? `${unpriced.toLocaleString()} unpriced ${unpriced === 1 ? 'call' : 'calls'}` : '',
  ].filter(Boolean).join(' · ')
  return <details className={`text-xs text-muted-foreground ${className}`}>
    <summary className="w-fit cursor-pointer hover:text-foreground">{label}</summary>
    <p className="mt-2">{message}</p>
  </details>
}
