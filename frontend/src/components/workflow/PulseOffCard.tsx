import { ArrowRight, Brain, CalendarClock, Check, MessageSquare, SearchCheck, ShieldCheck, Target, Wrench } from 'lucide-react'
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

const PULSE_TILES = [
  { icon: Target, title: 'Checks the goal', text: 'Every day: is it measured, is it moving, did a run fail.' },
  { icon: Wrench, title: 'Improves the workflow', text: 'Asks the Builder to fix a failing step, set up measurement or try an experiment.' },
  { icon: Brain, title: 'Learns', text: 'Remembers what it tried and what worked, and builds on it.' },
]

/** The Pulse tab when Pulse is off, or on without a soul.md: the owner manages
 * the workflow. Pulse is introduced first (what it is, how it helps), then
 * what runs today. */
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
    <div data-testid="pulse-off-card" className="mx-auto max-w-3xl space-y-4">
      <section className="rounded-xl border border-primary/25 bg-gradient-to-b from-primary/10 to-transparent px-4 py-6 text-center sm:px-8">
        <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-primary/15 ring-4 ring-primary/10">
          <PulseIcon className="h-6 w-6 text-primary" aria-hidden="true" />
        </div>
        <h2 className="text-base font-semibold text-foreground">{pulseOn ? 'Pulse is waiting for a goal' : 'Let an agent own this workflow’s goal'}</h2>
        <p className="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
          Pulse watches the goal written in soul.md every day and gets the Builder to improve the workflow, like a teammate who looks after it.
        </p>
        <div className="mt-4">
          {hasSoul ? (
            !pulseOn && (
              <button type="button" onClick={onTurnOn} disabled={saving} className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow-sm hover:bg-primary/90 disabled:opacity-50">
                Turn on Pulse
              </button>
            )
          ) : (
            <span className="inline-block rounded-md border border-border bg-background px-3 py-1.5 text-xs text-muted-foreground">Write the goal in soul.md first: ask the Builder chat.</span>
          )}
        </div>
      </section>

      <section className="grid gap-3 sm:grid-cols-3">
        {PULSE_TILES.map(({ icon: Icon, title, text }) => (
          <div key={title} className="rounded-lg border border-border bg-background p-3">
            <Icon className="mb-2 h-4 w-4 text-primary" aria-hidden="true" />
            <div className="text-xs font-semibold text-foreground">{title}</div>
            <div className="mt-0.5 text-xs text-muted-foreground">{text}</div>
          </div>
        ))}
      </section>

      <section className="rounded-lg border border-border bg-background p-3">
        <div className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">How it works</div>
        <div className="flex flex-wrap items-center gap-2 text-xs text-foreground">
          <span className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2 py-1 font-medium text-primary"><PulseIcon className="h-3.5 w-3.5" aria-hidden="true" />Pulse</span>
          <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" /><span className="text-muted-foreground">asks</span>
          <span className="inline-flex items-center gap-1 rounded-full bg-secondary px-2 py-1 font-medium"><MessageSquare className="h-3.5 w-3.5" aria-hidden="true" />Builder</span>
          <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" /><span className="text-muted-foreground">changes</span>
          <span className="inline-flex items-center gap-1 rounded-full bg-secondary px-2 py-1 font-medium"><Check className="h-3.5 w-3.5" aria-hidden="true" />Workflow</span>
        </div>
        <div className="mt-2 flex items-start gap-1.5 text-xs text-muted-foreground">
          <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />
          <span>Pulse only reads. The Builder makes changes within the permission levels you set; anything big comes to you under Needs you. Talk to it any time with #pulse in the Builder chat, or turn it off.</span>
        </div>
      </section>

      <section className="rounded-lg border border-border bg-background">
        <div className="flex items-center justify-between border-b border-border px-3 py-2">
          <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Today you manage this workflow</div>
          <button type="button" onClick={openSchedules} className="text-xs font-medium text-primary hover:underline">Edit</button>
        </div>
        <ul className="divide-y divide-border text-xs">
          {schedules.length === 0 && (
            <li className="flex items-center gap-2 px-3 py-2 text-muted-foreground"><CalendarClock className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />No schedules: runs start only when you start them.</li>
          )}
          {schedules.map((schedule, index) => (
            <li key={`${schedule.name}-${index}`} className={`flex items-center gap-2 px-3 py-2 ${schedule.enabled ? 'text-foreground' : 'text-muted-foreground'}`}>
              <CalendarClock className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
              <span className="min-w-0 flex-1 truncate"><span className="font-medium">{schedule.name || 'Schedule'}</span> · {scheduleWhen(schedule)}{schedule.enabled ? '' : ' · paused'}</span>
              {schedule.type !== 'webhook' && <span className="shrink-0 text-muted-foreground">after: {afterRunText(schedule.after_run)}</span>}
            </li>
          ))}
          <li className="flex items-center gap-2 px-3 py-2 text-foreground">
            <MessageSquare className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 flex-1">Runs from a chat</span>
            <span className="shrink-0 text-muted-foreground">after: {afterRunText(runSetup?.manual_after_run)}</span>
          </li>
          <li className="flex items-center gap-2 px-3 py-2 text-foreground">
            <SearchCheck className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span className="min-w-0 flex-1">Workflow Review checks a changed plan before each run</span>
            <span className="shrink-0 text-muted-foreground">{reviewedAt ? `last ${timeAgo(reviewedAt)}` : 'not run yet'}</span>
          </li>
        </ul>
      </section>
    </div>
  )
}
