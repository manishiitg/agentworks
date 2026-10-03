import CostTokenBreakdown, { CostPricingNotice } from '../../providers/CostTokenBreakdown'
import React from 'react'
import type { CostSummary } from '../../../services/api-types'
import { DollarSign, Coins } from 'lucide-react'
import { formatStartedAt } from '../../../utils/duration'
import { formatUSD, formatTokens } from './helpers'
import type { CostsData } from './useCostsData'
import { WorkspaceViewHeader } from '../WorkspaceViewHeader'
import { WorkspaceViewIconButton } from '../WorkspaceViewIconButton'

type CostsHeaderProps = Pick<CostsData, 'overallSummary' | 'aggregateSummary' | 'phaseCostSummary' | 'loading' | 'loadAllCosts'> & {
  scopedCosts?: CostSummary | null
  startedAt?: string | null
  headerAction?: React.ReactNode
}

// Header content only; InspectorShell owns the row wrapper.
// Layout follows the inspector standard (see LogsHeader): title left,
// Ask AI + refresh right-aligned in the title row, view-specific summary in
// a strip below instead of jumbled with the actions.
const CostsHeader: React.FC<CostsHeaderProps> = ({
  startedAt,
  scopedCosts,
  overallSummary,
  aggregateSummary,
  phaseCostSummary,
  loading,
  loadAllCosts,
  headerAction,
}) => (
  <div className="min-w-0 flex-1">
    <WorkspaceViewHeader
      bare
      icon={DollarSign}
      title="Cost Analysis"
      context={startedAt ? (
        <span className="text-xs font-normal text-muted-foreground">{formatStartedAt(startedAt)}</span>
      ) : undefined}
      actions={<>
        {headerAction}
        <WorkspaceViewIconButton label="Refresh costs" onClick={loadAllCosts} disabled={loading} spinning={loading} />
      </>}
      below={overallSummary ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 pt-1 text-xs">
        <div className="font-semibold text-foreground">
          {overallSummary.totalCost === 0 && (scopedCosts?.total.unpriced_call_count ?? 0) > 0 ? 'Not priced' : formatUSD(overallSummary.totalCost)}
        </div>
        <div className="flex items-center gap-1.5 text-muted-foreground">
          <Coins className="w-3.5 h-3.5" />
          {formatTokens(overallSummary.totalInputTokens)} input · {formatTokens(overallSummary.totalOutputTokens)} output
        </div>
        {aggregateSummary && (
          <div className="text-muted-foreground">
            {aggregateSummary.totalRuns} run{aggregateSummary.totalRuns !== 1 ? 's' : ''}
          </div>
        )}
        {aggregateSummary && aggregateSummary.totalToolCost > 0 && (
          <div className="text-muted-foreground">
            LLM {formatUSD(aggregateSummary.totalLLMCost)} | Tools {formatUSD(aggregateSummary.totalToolCost)}
          </div>
        )}
        {phaseCostSummary && (
          <div className="text-muted-foreground">
            Builder {formatUSD(phaseCostSummary.totalCost)}
          </div>
        )}
        {scopedCosts && <div className="w-full"><CostTokenBreakdown usage={scopedCosts.total} /></div>}
        {scopedCosts && <CostPricingNotice usage={scopedCosts.total} className="w-full" />}
        </div>
      ) : undefined}
    />
  </div>
)

export default CostsHeader
