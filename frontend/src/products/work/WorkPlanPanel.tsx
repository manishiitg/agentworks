import { Route } from 'lucide-react'
import { usePlanData } from '../../components/workflow/hooks/usePlanData'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'

export function WorkPlanPanel({ workspacePath, onAsk }: { workspacePath: string; onAsk: (message: string) => Promise<unknown> }) {
  const { plan, loading, error, refresh } = usePlanData(workspacePath)
  const steps = plan?.steps ?? []

  return <div className="flex h-full min-h-0 flex-col">
    <WorkspaceViewHeader
      icon={Route}
      title="Plan"
      subtitle="The saved steps for this Crew project"
      actions={<WorkspaceViewActions
        workspacePath={workspacePath}
        message="Explain this Crew project's plan and help me revise it if needed."
        onAsk={async message => { await onAsk(message) }}
        onRefresh={() => { void refresh() }}
        refreshing={loading}
        refreshLabel="Refresh plan"
      />}
    />
    <div className="min-h-0 flex-1 overflow-y-auto p-4">
      {loading && !plan ? <p className="text-sm text-muted-foreground">Loading plan…</p>
        : error ? <p className="text-sm text-destructive">Could not load the plan: {error}</p>
        : steps.length === 0 ? <p className="text-sm text-muted-foreground">No plan has been saved for this Crew member yet.</p>
        : <ol className="mx-auto max-w-3xl space-y-3">
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
