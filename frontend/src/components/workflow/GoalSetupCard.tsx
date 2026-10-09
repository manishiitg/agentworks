import { ArrowRight, BookMarked, Check, Loader2, RefreshCw, Target } from 'lucide-react'
import type { WorkflowGoalSetupCheck, WorkflowGoalSetupStatus } from '../../services/api'

const GOAL_SETUP_ACTION_LABEL: Record<string, string> = {
  goal: 'Set the goal in chat',
  plan: 'Design the plan in chat',
  metrics: 'Add metrics in chat',
  dashboard: 'Design the dashboard in chat',
  pulse: 'Turn on Pulse',
}

// Hover text for each step: what the user gets from it.
const STEP_HINT: Record<string, string> = {
  goal: 'What success looks like',
  plan: 'The steps to get there',
  metrics: 'How progress is measured',
  dashboard: 'Where you see it',
  pulse: 'Someone owns the goal from here',
}

export interface GoalSetupCardProps {
  status: WorkflowGoalSetupStatus
  busy?: boolean
  loading?: boolean
  onStart: (step: WorkflowGoalSetupCheck) => void
  onOpenPlaybooks: () => void
  onDismiss: () => void
  onRefresh: () => void
}

// The goal setup card, drawn from a status alone (WorkflowGoalSetupBar loads
// it). Before a goal is set it is a short card that says what a goal buys;
// after that it is one slim line so it does not crowd the chat.
export function GoalSetupCard({ status, busy = false, loading = false, onStart, onOpenPlaybooks, onDismiss, onRefresh }: GoalSetupCardProps) {
  const next = status.next
  const steps = status.checks.filter(check => check.id !== 'playbook')
  const playbook = status.checks.find(check => check.id === 'playbook')
  const playbooks = status.playbooks ?? []
  const goalDone = steps.find(step => step.id === 'goal')?.done ?? false
  const doneCount = steps.filter(step => step.done).length
  const nextIndex = next ? steps.findIndex(step => step.id === next.id) : -1

  const refreshButton = (
    <button type="button" onClick={onRefresh} disabled={loading} title="Check setup again" aria-label="Refresh goal setup" className="shrink-0 rounded-md p-1 text-muted-foreground/70 hover:bg-muted hover:text-foreground disabled:opacity-50">
      <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
    </button>
  )

  const primaryAction = next ? (
    <button
      type="button"
      onClick={() => onStart(next)}
      disabled={busy}
      className="inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-primary px-3.5 py-2 text-xs font-semibold text-primary-foreground shadow-sm shadow-primary/30 transition hover:bg-primary/90 disabled:opacity-60"
    >
      {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <>{GOAL_SETUP_ACTION_LABEL[next.id] ?? 'Set up in chat'}<ArrowRight className="h-3.5 w-3.5" /></>}
    </button>
  ) : null

  // Segmented progress: one segment per step, labels underneath.
  const progress = (compact: boolean) => (
    <ol className={`grid min-w-0 gap-1.5 ${compact ? 'w-24' : 'w-full max-w-md'}`} style={{ gridTemplateColumns: `repeat(${steps.length}, minmax(0, 1fr))` }} aria-label="Setup steps">
      {steps.map((step, index) => {
        const current = index === nextIndex
        const clickable = current && !busy
        return (
          <li key={step.id} className="min-w-0">
            <button
              type="button"
              disabled={!clickable}
              onClick={() => onStart(step)}
              aria-current={current ? 'step' : undefined}
              title={`${step.label}: ${STEP_HINT[step.id] ?? ''}${step.done ? ' (done)' : ''}`}
              className={`group flex w-full flex-col gap-1 text-left ${clickable ? 'cursor-pointer' : 'cursor-default'}`}
            >
              <span className={`block h-1 w-full rounded-full transition-colors ${step.done ? 'bg-emerald-500' : current ? 'bg-primary group-hover:bg-primary/80' : 'bg-muted-foreground/20'}`} />
              {compact ? <span className="sr-only">{step.label}</span> : (
                <span className={`flex items-center gap-1 truncate text-[11px] ${step.done ? 'text-emerald-600 dark:text-emerald-400' : current ? 'font-semibold text-foreground' : 'text-muted-foreground'}`}>
                  {step.done ? <Check className="h-3 w-3 shrink-0" strokeWidth={3} /> : null}
                  {step.label}
                </span>
              )}
            </button>
          </li>
        )
      })}
    </ol>
  )

  // Goal set: one slim line.
  if (goalDone) {
    return (
      <section className="shrink-0 border-b border-border px-4 py-2" aria-label="Goal setup">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span className="flex shrink-0 items-center gap-1.5 text-xs font-medium text-foreground">
            <span className="flex h-5 w-5 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-500">
              <Check className="h-3 w-3" strokeWidth={3} />
            </span>
            Goal set
          </span>
          <span title={`${doneCount} of ${steps.length} done`}>{progress(true)}</span>
          <span className="text-xs text-muted-foreground">
            {next ? <>Next: <span className="text-foreground">{next.label}</span></> : 'All set'}
          </span>
          <div className="ml-auto flex items-center gap-1">
            {primaryAction}
            <button type="button" onClick={onDismiss} disabled={busy} title="Hide goal setup for this automation." aria-label="Dismiss goal setup" className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
              Hide
            </button>
            {refreshButton}
          </div>
        </div>
      </section>
    )
  }

  return (
    <section className="shrink-0 border-b border-border px-3 py-3 [container-type:inline-size]" aria-label="Goal setup">
      <div className="relative overflow-hidden rounded-xl border border-primary/25 bg-gradient-to-br from-primary/[0.12] via-primary/[0.04] to-transparent p-4">
        <div aria-hidden className="pointer-events-none absolute -right-10 -top-16 h-44 w-44 rounded-full bg-primary/10 blur-3xl" />
        {/* Narrow: actions in one row under the text. Wide (by the card's own
            width, not the window's): actions stacked on the right. */}
        <div className="relative flex flex-col gap-3 [@container(min-width:760px)]:flex-row [@container(min-width:760px)]:items-center [@container(min-width:760px)]:gap-6">
          <div className="flex min-w-0 flex-1 items-start gap-3.5">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-primary to-primary/60 text-primary-foreground shadow-lg shadow-primary/25">
              <Target className="h-5 w-5" />
            </div>
            <div className="min-w-0 flex-1">
              <p className="text-[11px] font-medium uppercase tracking-wider text-primary">Goal setup · optional</p>
              <h3 className="mt-0.5 text-[15px] font-semibold leading-snug text-foreground">Give this automation a goal</h3>
              <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                It plans the work, tracks the numbers and keeps chasing the goal. Not every automation needs one.
              </p>
              <div className="mt-3">{progress(false)}</div>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-1.5 pl-[3.375rem] [@container(min-width:760px)]:flex-col [@container(min-width:760px)]:items-end [@container(min-width:760px)]:pl-0">
            {primaryAction}
            <div className="flex items-center gap-1">
              {playbook ? (
                <button
                  type="button"
                  onClick={onOpenPlaybooks}
                  title={playbook.done ? 'Installed playbooks guide this setup. Open Playbooks.' : 'Start from a ready-made playbook. Open Playbooks.'}
                  className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium hover:bg-muted ${playbook.done ? 'text-emerald-600 dark:text-emerald-400' : 'text-foreground'}`}
                >
                  <BookMarked className="h-3.5 w-3.5" />
                  {playbook.done ? `Playbook: ${playbooks.map(p => p.title).join(', ')}` : 'Start from a playbook'}
                </button>
              ) : null}
              <span aria-hidden className="text-muted-foreground/40">·</span>
              <button
                type="button"
                onClick={onDismiss}
                disabled={busy}
                title="This automation runs without a goal. Hides goal setup; you can still add a goal later from chat."
                aria-label="Dismiss goal setup"
                className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
              >
                No goal needed
              </button>
              {refreshButton}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
