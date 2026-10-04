import { AlertCircle } from 'lucide-react'
import type { CostAggregate } from '../../services/api-types'

/** Short markers distinguish incomplete accounting from a known zero cost. */
export default function CostPricingNotice({ usage }: { usage: CostAggregate }) {
  const missing = usage.missing_usage_call_count ?? 0
  const unpriced = Math.max(0, (usage.unpriced_call_count ?? 0) - missing)
  if (missing === 0 && unpriced === 0) return null
  const label = [missing > 0 ? `${missing.toLocaleString()} missing usage` : '',
    unpriced > 0 ? `${unpriced.toLocaleString()} unpriced` : ''].filter(Boolean).join(' · ')
  return <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
    <AlertCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
    {label}
  </span>
}
