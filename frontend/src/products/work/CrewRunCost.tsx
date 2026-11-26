import { formatCost } from '../../components/workflow/costs/helpers'

export type RunStepCost = { title: string; cost: number }

/**
 * What one run cost, said plainly: the total, and the steps that cost something. A run that made no paid
 * model calls says so, instead of showing nothing.
 */
export function CrewRunCost({ total, known, steps }: { total?: number; known: boolean; steps: RunStepCost[] }) {
  const paid = steps.filter(step => step.cost > 0)
  const headline = !known ? 'Loading…' : total && total > 0 ? formatCost(total) : 'No cost. This run made no paid model calls.'
  return <div className="space-y-1 px-4 py-3" data-testid="run-cost">
    <p className="text-xs font-semibold">Cost<span className="ml-2 font-normal text-muted-foreground">{headline}</span></p>
    {paid.map(step => <p key={step.title} className="flex items-baseline justify-between gap-3 text-xs text-muted-foreground"><span className="min-w-0 truncate">{step.title}</span><span className="shrink-0">{formatCost(step.cost)}</span></p>)}
  </div>
}
