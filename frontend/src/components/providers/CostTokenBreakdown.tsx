import type { CostAggregate } from '../../services/api-types'
import { inputTokens } from '../../utils/costTokens'
import { formatTokens } from '../workflow/costs/helpers'

export default function CostTokenBreakdown({ usage, compact = false }: { usage: Partial<CostAggregate>; compact?: boolean }) {
  const input = inputTokens(usage)
  const cached = usage.cache_read_tokens ?? 0
  const writes = usage.cache_write_tokens ?? 0
  const fresh = Math.max(0, input - cached - writes)
  const share = input > 0 ? `${(cached / input * 100).toFixed(1)}%` : '—'
  return <section className="rounded-lg border border-border bg-muted/20 p-3 text-xs">
    <div className="mb-2 flex items-center justify-between gap-2">
      <h4 className="font-semibold text-foreground">{compact ? 'Tokens' : 'Input and cache'}</h4>
      <span className="text-muted-foreground">{input > 0 ? `${share} cached` : 'Cache —'}</span>
    </div>
    <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {[
        ['Total input', formatTokens(input)],
        ['Fresh input', formatTokens(fresh)],
        [compact ? 'Cached input' : 'Cached input (read)', formatTokens(cached)],
        ['Output', formatTokens(usage.completion_tokens ?? 0)],
        ...(writes > 0 ? [['Cache writes', formatTokens(writes)]] : []),
      ].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd className="mt-1 font-mono font-semibold text-foreground">{value}</dd></div>)}
    </dl>
  </section>
}
