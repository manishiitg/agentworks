import React from 'react'
import { CalendarClock } from 'lucide-react'
import type { CostSummary } from '../../../services/api-types'
import { inputTokens, totalTokens } from '../../../utils/costTokens'
import { formatTokens, formatUSD } from './helpers'

// Spend per project schedule, reminder or trigger (PLAT-702). Rows come from
// the ledger's by_source, which only turns recorded after PLAT-702 carry.
const CostsScheduleSection: React.FC<{ scopedCosts: CostSummary | null }> = ({ scopedCosts }) => {
  const rows = Object.entries(scopedCosts?.by_source || {})
    .map(([id, usage]) => ({ id, usage }))
    .sort((a, b) => b.usage.total_cost_usd - a.usage.total_cost_usd || totalTokens(b.usage) - totalTokens(a.usage))
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
              <th className="pb-2 text-right font-medium">Input</th>
              <th className="pb-2 text-right font-medium">Output</th>
              <th className="pb-2 text-right font-medium">Cost</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {rows.map(({ id, usage }) => (
              <tr key={id}>
                <td className="max-w-[16rem] truncate py-2 pr-3 font-medium text-foreground" title={id}>{usage.label || id}</td>
                <td className="py-2 text-right font-mono text-muted-foreground">{(usage.run_count || 0).toLocaleString()}</td>
                <td className="py-2 text-right font-mono text-muted-foreground">{formatTokens(inputTokens(usage))}</td>
                <td className="py-2 text-right font-mono text-muted-foreground">{formatTokens(usage.completion_tokens)}</td>
                <td className="py-2 text-right font-mono font-medium text-foreground">
                  {usage.total_cost_usd === 0 && (usage.unpriced_call_count ?? 0) > 0 ? 'Not priced' : formatUSD(usage.total_cost_usd)}
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
