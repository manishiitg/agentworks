import React from 'react'
import type { CostSummary } from '../../../services/api-types'
import CostPricingNotice from '../../providers/CostPricingNotice'
import CostTokenBreakdown from '../../providers/CostTokenBreakdown'
import { DollarSign } from 'lucide-react'
import { formatStartedAt } from '../../../utils/duration'
import { formatCost, formatUSD } from './helpers'
import { costAmountLabel } from '../../../utils/costTokens'
import type { CostsData } from './useCostsData'
import { WorkspaceViewHeader } from '../WorkspaceViewHeader'
import { WorkspaceViewIconButton } from '../WorkspaceViewIconButton'
import { formatTokens } from '../../../utils/formatTokens'

type CostsHeaderProps = Pick<CostsData, 'loading' | 'loadAllCosts'> & {
  startedAt?: string | null
  headerAction?: React.ReactNode
}

type CostsOverviewProps = Pick<CostsData, 'overallSummary' | 'aggregateSummary' | 'phaseCostSummary'> & {
  scopedCosts?: CostSummary | null
}

// Only the title/actions stay fixed. Totals and disclosures scroll with content.
export function CostsOverview({ scopedCosts, overallSummary, aggregateSummary, phaseCostSummary }: CostsOverviewProps) {
  if (!overallSummary) return null
  const usage = { ...scopedCosts?.total, total_cost_usd: overallSummary.totalCost }
  return <section aria-label="Cost summary" className="min-w-0 rounded-lg border border-border bg-card p-4">
    <div className="flex flex-wrap items-start justify-between gap-5">
      <div>
        <p className="text-xs text-muted-foreground">Recorded tokens</p>
        <p className="mt-1 text-3xl font-semibold tracking-tight tabular-nums text-foreground" title={(overallSummary.totalInputTokens + overallSummary.totalOutputTokens).toLocaleString()}>
          {formatTokens(overallSummary.totalInputTokens + overallSummary.totalOutputTokens)}
        </p>
        <p className="mt-2 text-xs text-muted-foreground">{formatTokens(overallSummary.totalInputTokens)} input · {formatTokens(overallSummary.totalOutputTokens)} output</p>
      </div>
      <div className="space-y-1 text-left sm:text-right">
        <p className="text-xs text-muted-foreground">Cost · USD</p>
        <p className="text-lg font-medium tabular-nums text-foreground" title={formatUSD(overallSummary.totalCost)}>{costAmountLabel(usage, formatCost)}</p>
        {scopedCosts && <CostPricingNotice usage={scopedCosts.total} />}
      </div>
    </div>
    <details className="mt-3 border-t border-border pt-3 text-xs">
      <summary className="w-fit cursor-pointer font-medium text-muted-foreground hover:text-foreground">Usage details</summary>
      <div className="mt-3 space-y-3">
        <CostTokenBreakdown compact usage={{ input_tokens: overallSummary.totalInputTokens, cache_read_tokens: overallSummary.cacheReadTokens, cache_write_tokens: overallSummary.cacheWriteTokens, completion_tokens: overallSummary.totalOutputTokens }} />
        <details className="text-muted-foreground">
          <summary className="w-fit cursor-pointer hover:text-foreground">How tokens are counted</summary>
          <p className="mt-2">Fresh input, cache reads and cache writes add up to total input; output is separate.</p>
          <p className="mt-1">Cache reuses a matching input prefix. New history, tool results and outputs still require processing. Identical user messages do not guarantee identical full inputs.</p>
        </details>
        {aggregateSummary && <p className="text-muted-foreground">{aggregateSummary.totalRuns} run{aggregateSummary.totalRuns !== 1 ? 's' : ''}</p>}
        {aggregateSummary && aggregateSummary.totalToolCost > 0 && <p className="text-muted-foreground">AI {formatUSD(aggregateSummary.totalLLMCost)} · Tools {formatUSD(aggregateSummary.totalToolCost)}</p>}
        {phaseCostSummary && !scopedCosts && <p className="text-muted-foreground">Builder {formatUSD(phaseCostSummary.totalCost)}</p>}
      </div>
    </details>
  </section>
}

const CostsHeader: React.FC<CostsHeaderProps> = ({ startedAt, loading, loadAllCosts, headerAction }) => (
  <div className="min-w-0 shrink-0">
    <WorkspaceViewHeader
      bare
      icon={DollarSign}
      title="Usage & cost"
      context={startedAt ? <span className="text-xs font-normal text-muted-foreground">{formatStartedAt(startedAt)}</span> : undefined}
      actions={<>
        {headerAction}
        <WorkspaceViewIconButton label="Refresh costs" onClick={loadAllCosts} disabled={loading} spinning={loading} />
      </>}
    />
  </div>
)

export default CostsHeader
