import React from 'react'
import { CalendarClock } from 'lucide-react'
import type { CostSummary } from '../../../services/api-types'
import { newTokens, totalTokens, costAmountLabel } from '../../../utils/costTokens'
import { formatCost } from './helpers'
import { formatTokens } from '../../../utils/formatTokens'

// Spend per project schedule, reminder or trigger (PLAT-702). Rows come from
// the ledger's by_source, which only turns recorded after PLAT-702 carry.
const CostsScheduleSection: React.FC<{ scopedCosts: Pick<CostSummary, 'by_source'> | null }> = ({ scopedCosts }) => {
  const rows = Object.entries(scopedCosts?.by_source || {})
    .map(([id, usage]) => ({ id, usage }))
    .sort((a, b) => newTokens(b.usage) - newTokens(a.usage) || totalTokens(b.usage) - totalTokens(a.usage))
  if (rows.length === 0) return null
  return (
    <section className="rounded-lg border border-border bg-card p-4 shadow-sm">
      <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-foreground">
        <CalendarClock className="h-4 w-4 text-primary" />
        Schedules
      </h3>
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border text-muted-foreground">
              <th className="pb-2 text-left font-medium">Schedule</th>
              <th className="pb-2 text-right font-medium">Runs</th>
              <th className="pb-2 text-right font-medium">Tokens</th>
              <th className="pb-2 text-right font-medium">Cost</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {rows.map(({ id, usage }) => (
              <tr key={id}>
                <td className="max-w-[16rem] truncate py-2 pr-3 font-medium text-foreground">{usage.label || "Scheduled run"}</td>
                <td className="py-2 text-right font-mono text-muted-foreground">{(usage.run_count || 0).toLocaleString()}</td>
                <td className="py-2 text-right font-mono font-medium text-foreground">{formatTokens(newTokens(usage))}</td>
                <td className="py-2 text-right font-mono text-muted-foreground">
                  {costAmountLabel(usage, formatCost)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

export default CostsScheduleSection
