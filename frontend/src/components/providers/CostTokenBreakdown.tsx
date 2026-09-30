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
    <p className="mt-3 text-muted-foreground">{share} of input was read from cache. Fresh input{writes > 0 ? ', cache reads and cache writes' : ' and cache reads'} add up to total input; output is separate.</p>
    {!compact && <p className="mt-1 text-muted-foreground">Cache reuses a matching input prefix. New history, tool results and outputs still require processing. Identical user messages do not guarantee identical full inputs.</p>}
  </section>
}
