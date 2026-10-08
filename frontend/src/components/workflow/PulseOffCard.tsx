import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { describeCron } from '../scheduler/scheduleRuns/cron'
import { timeAgo } from '../scheduler/scheduleRuns/helpers'
import { PulseIcon } from './pulseIcon'
import type { ScheduleAfterRun, WorkflowRunSetup } from '../../services/api-types'

function afterRunText(options: ScheduleAfterRun | undefined): string {
  const on = (['backup', 'publish', 'notify'] as const).filter(key => options?.[key])
  return on.length ? on.join(', ') : 'nothing'
}

function scheduleWhen(schedule: WorkflowRunSetup['schedules'][number]): string {
  if (schedule.type === 'calendar') return 'Calendar'
  if (schedule.type === 'webhook') return 'API trigger'
  return schedule.cron_expression ? describeCron(schedule.cron_expression) : 'Cron'
}

/** The Pulse tab when Pulse is off, or on without a soul.md: the owner manages
 * the workflow, so the tab says what still runs and how to hand the goal to Pulse. */
export function PulseOffCard({ pulseOn, hasSoul, runSetup, saving, onTurnOn }: {
  pulseOn: boolean
  hasSoul: boolean
  runSetup: WorkflowRunSetup | null
  saving: boolean
  onTurnOn: () => void
}) {
  const openSchedules = () => useWorkflowStore.getState().openWorkspaceView('schedules')
  const schedules = runSetup?.schedules ?? []
  const review = runSetup?.workflow_review
  const reviewedAt = review?.finished_at || review?.started_at
  return (
    <div data-testid="pulse-off-card" className="mx-auto max-w-2xl space-y-4">
      <div>
        <div className="text-sm font-medium text-foreground">You manage this workflow.</div>
        <div className="text-xs text-muted-foreground">Nothing reviews it or changes it on its own.</div>
      </div>

      <div className="rounded-lg border border-border">
        <div className="border-b border-border px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">What still runs</div>
        <dl className="divide-y divide-border text-xs">
          <div className="grid grid-cols-[8rem_1fr] gap-2 px-3 py-2">
            <dt className="text-muted-foreground">Your schedules</dt>
            <dd className="space-y-0.5">
              {schedules.length === 0 && <div className="text-muted-foreground">No schedules: runs start only when you start them.</div>}
              {schedules.map((schedule, index) => (
                <div key={`${schedule.name}-${index}`} className={schedule.enabled ? 'text-foreground' : 'text-muted-foreground'}>
                  <span className="font-medium">{schedule.name || 'Schedule'}</span> · {scheduleWhen(schedule)}{schedule.enabled ? '' : ' · paused'}
                  {schedule.type !== 'webhook' && <span className="text-muted-foreground"> · after: {afterRunText(schedule.after_run)}</span>}
                </div>
              ))}
            </dd>
          </div>
          <div className="grid grid-cols-[8rem_1fr] gap-2 px-3 py-2">
            <dt className="text-muted-foreground">Runs from a chat</dt>
            <dd className="text-foreground">after: {afterRunText(runSetup?.manual_after_run)}</dd>
          </div>
          <div className="grid grid-cols-[8rem_1fr] gap-2 px-3 py-2">
            <dt className="text-muted-foreground">Before each run</dt>
            <dd className="text-foreground">
              Workflow Review checks a changed plan before it runs
              <span className="text-muted-foreground">{reviewedAt ? ` · last ${timeAgo(reviewedAt)}` : ' · not run yet'}</span>
            </dd>
          </div>
        </dl>
        <div className="border-t border-border px-3 py-2 text-right">
          <button type="button" onClick={openSchedules} className="text-xs font-medium text-primary hover:underline">Edit schedules and after-run options</button>
        </div>
      </div>

      <div className="rounded-lg border border-primary/30 bg-primary/5 p-3">
        <div className="mb-1 flex items-center gap-1.5 text-sm font-medium text-foreground">
          <PulseIcon className="h-4 w-4 text-primary" aria-hidden="true" />
          {pulseOn ? 'Pulse is waiting for a goal' : 'Turn on Pulse'}
        </div>
        <p className="text-xs text-muted-foreground">
          An agent owns the goal in soul.md: it checks the goal daily, finds what to improve and asks the Builder to make the changes.
        </p>
        <div className="mt-2 flex justify-end">
          {hasSoul ? (
            !pulseOn && (
              <button type="button" onClick={onTurnOn} disabled={saving} className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50">
                Turn on Pulse
              </button>
            )
          ) : (
            <span className="text-xs text-muted-foreground">Write the goal in soul.md first: ask the Builder chat.</span>
          )}
        </div>
      </div>
    </div>
  )
}
