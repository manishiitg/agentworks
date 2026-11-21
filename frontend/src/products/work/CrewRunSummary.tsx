import { formatCost } from '../../components/workflow/costs/helpers'
import type { RunStepCost } from './CrewRunCost'

/**
 * The first lines of an opened run: when it started, how long it took and how it was started, then what it cost.
 * A run that cost nothing says so; a run that cost something shows the total and the steps that cost.
 */
export function CrewRunSummary({ facts, cost, costKnown, steps }: { facts: string[]; cost?: number; costKnown: boolean; steps: RunStepCost[] }) {
  const paid = steps.filter(step => step.cost > 0)
  const costText = !costKnown ? '' : cost && cost > 0 ? `Cost ${formatCost(cost)}` : 'No cost'
  const line = [...facts.filter(Boolean), costText].filter(Boolean).join(' · ')
  return <div className="space-y-1 px-4 py-2" data-testid="run-summary">
    {line && <p className="text-xs text-muted-foreground">{line}</p>}
    {cost !== undefined && cost > 0 && paid.map(step => <p key={step.title} className="flex items-baseline justify-between gap-3 text-xs text-muted-foreground"><span className="min-w-0 truncate">{step.title}</span><span className="shrink-0">{formatCost(step.cost)}</span></p>)}
  </div>
}
