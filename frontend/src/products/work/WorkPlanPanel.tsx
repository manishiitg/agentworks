import { RefreshCw, Route } from 'lucide-react'
import { PlanEmptyState } from '../../components/workflow/PlanEmptyState'
import { usePlanData } from '../../components/workflow/hooks/usePlanData'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

export function WorkPlanPanel({ workspacePath, onAsk, onCreatePlan }: { workspacePath: string; onAsk: (message: string) => Promise<unknown>; onCreatePlan: () => void }) {
  const { plan, loading, error, refresh } = usePlanData(workspacePath)
  useLiveRefetch(() => { void refresh() }, {
    kinds: ['plan'],
    workflow: workspacePath,
    fallbackMs: 30_000,
    minIntervalMs: 500,
  })
  const steps = plan?.steps ?? []

  return <div className="flex h-full min-h-0 flex-col">
    <WorkspaceViewHeader
      icon={Route}
      title="Plan"
      actions={<WorkspaceViewActions
        workspacePath={workspacePath}
        message="Explain this Crew project's plan and help me revise it if needed."
        onAsk={async message => { await onAsk(message) }}
        onRefresh={() => { void refresh() }}
        refreshing={loading}
        refreshLabel="Refresh plan"
      />}
    />
    <div className="min-h-0 flex-1 overflow-y-auto">
      {loading && !plan ? <p className="p-4 text-sm text-muted-foreground">Loading plan…</p>
        : error ? <p className="p-4 text-sm text-destructive">Could not load the plan: {error}</p>
        : steps.length === 0 ? <PlanEmptyState
          kind="plan"
          title="Plan this Crew project"
          description="Work with Crew in chat to define the steps. The saved plan will appear here when it is ready."
          actionLabel="Draft a plan in chat"
          onAction={onCreatePlan}
          secondaryActions={<button type="button" onClick={() => { void refresh() }} disabled={loading} className="inline-flex h-8 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"><RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />Refresh plan</button>}
        />
        : <ol className="mx-auto max-w-3xl space-y-3 p-4">
          {steps.map((step, index) => <li key={step.id || index} className="rounded-lg border border-border bg-card p-4">
            <div className="flex items-start gap-3">
              <span className="grid h-6 w-6 shrink-0 place-items-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">{index + 1}</span>
              <div className="min-w-0">
                <h3 className="text-sm font-semibold text-foreground">{step.title}</h3>
                {step.description && <p className="mt-1 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">{step.description}</p>}
                {step.success_criteria && <p className="mt-2 text-xs text-muted-foreground"><span className="font-medium text-foreground">Done when:</span> {step.success_criteria}</p>}
              </div>
            </div>
          </li>)}
        </ol>}
    </div>
  </div>
}
