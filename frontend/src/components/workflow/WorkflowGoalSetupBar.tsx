import { useCallback, useEffect, useState } from 'react'
import { ArrowRight, BookMarked, Check, Loader2, RefreshCw, Target } from 'lucide-react'
import { workflowManifestApi, type WorkflowGoalSetupCheck, type WorkflowGoalSetupStatus } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { goalSetupChatMessage } from '../../utils/goalSetupChat'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

const REFRESH_MS = 30_000

const ACTION_LABEL: Record<string, string> = {
  goal: 'Set the goal in chat',
  plan: 'Design the plan in chat',
  metrics: 'Add metrics in chat',
  dashboard: 'Design the dashboard in chat',
}

// Hover text for each step: what the user gets from it.
const STEP_HINT: Record<string, string> = {
  goal: 'What success looks like',
  plan: 'The steps to get there',
  metrics: 'How progress is measured',
  dashboard: 'Where you see it',
}

// Initial setup for an automation, like a Crew template's setup bar: goal,
// then plan, metrics and dashboard, each done in the Builder chat. Goals are
// optional, so it can be dismissed; the server stops showing it once the
// automation has run. Until the goal is set it is a short card; after that it
// collapses to one line so it does not crowd the chat.
export function WorkflowGoalSetupBar({ workspacePath, canEdit }: { workspacePath: string | null | undefined; canEdit: boolean }) {
  const [status, setStatus] = useState<WorkflowGoalSetupStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)

  const refresh = useCallback(async () => {
    if (!workspacePath) {
      setStatus(null)
      return
    }
    setLoading(true)
    try {
      setStatus(await workflowManifestApi.getGoalSetup(workspacePath))
    } catch {
      setStatus(null)
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => {
    setStatus(null)
    void refresh()
  }, [refresh])

  useEffect(() => {
    if (!status?.show) return
    const timer = window.setInterval(() => { void refresh() }, REFRESH_MS)
    return () => window.clearInterval(timer)
  }, [refresh, status?.show])

  if (!canEdit || !workspacePath || !status?.show) return null
  const next = status.next
  const steps = status.checks.filter(check => check.id !== 'playbook')
  const playbook = status.checks.find(check => check.id === 'playbook')
  const playbooks = status.playbooks ?? []
  const goalDone = steps.find(step => step.id === 'goal')?.done ?? false
  const nextIndex = next ? steps.findIndex(step => step.id === next.id) : -1
  const stepPosition = nextIndex >= 0 ? `Step ${nextIndex + 1} of ${steps.length}` : `${steps.length} of ${steps.length} done`

  const startStep = async (step: WorkflowGoalSetupCheck | undefined) => {
    if (!step || busy) return
    setBusy(true)
    try {
      await sendWorkspacePaneMessageToChat({ workspacePath, message: goalSetupChatMessage(step.command || 'setup-goals', playbooks) })
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not start goal setup in chat.', 'error')
    } finally {
      setBusy(false)
    }
  }

  const dismiss = async () => {
    if (busy) return
    setBusy(true)
    try {
      setStatus(await workflowManifestApi.dismissGoalSetup(workspacePath))
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not dismiss goal setup.', 'error')
    } finally {
      setBusy(false)
    }
  }

  const header = (
    <span className="flex shrink-0 items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
      <Target className="h-3.5 w-3.5 text-primary" />
      Goal setup · optional
    </span>
  )

  const refreshButton = (
    <button type="button" onClick={() => { void refresh() }} disabled={loading} title="Check setup again" aria-label="Refresh goal setup" className="shrink-0 rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
      <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
    </button>
  )

  const stepper = (
    <ol className="flex min-w-0 items-center" aria-label="Setup steps">
      {steps.map((step, index) => {
        const current = step.id === next?.id
        const clickable = current && !busy
        return (
          <li key={step.id} className="flex items-center">
            {index > 0 ? <span aria-hidden className={`mx-1.5 h-px w-4 sm:w-6 ${steps[index - 1].done ? 'bg-emerald-500/60' : 'bg-border'}`} /> : null}
            <button
              type="button"
              disabled={!clickable}
              onClick={() => { void startStep(step) }}
              aria-current={current ? 'step' : undefined}
              title={`${step.label}: ${STEP_HINT[step.id] ?? ''}${step.done ? ' (done)' : ''}`}
              className={`flex items-center gap-1.5 rounded-full py-0.5 pr-1.5 ${clickable ? 'cursor-pointer hover:bg-primary/10' : 'cursor-default'}`}
            >
              <span
                className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[10px] font-semibold ${
                  step.done
                    ? 'bg-emerald-500 text-white'
                    : current
                      ? 'bg-primary text-primary-foreground ring-4 ring-primary/20'
                      : 'border border-border bg-background text-muted-foreground'
                }`}
              >
                {step.done ? <Check className="h-3 w-3" strokeWidth={3} /> : index + 1}
              </span>
              <span className={`text-xs font-medium ${step.done || current ? 'text-foreground' : 'text-muted-foreground'}`}>{step.label}</span>
            </button>
          </li>
        )
      })}
    </ol>
  )

  const primaryAction = next ? (
    <button
      type="button"
      onClick={() => { void startStep(next) }}
      disabled={busy}
      className="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground shadow-sm hover:bg-primary/90 disabled:opacity-60"
    >
      {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <>{ACTION_LABEL[next.id] ?? 'Set up in chat'}<ArrowRight className="h-3.5 w-3.5" /></>}
    </button>
  ) : null

  const playbookAction = playbook ? (
    <button
      type="button"
      onClick={() => useWorkflowStore.getState().openWorkspaceView('playbooks')}
      title={playbook.done ? 'Installed playbooks guide this setup. Open Playbooks.' : 'Start from a ready-made playbook. Open Playbooks.'}
      className={`inline-flex shrink-0 items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium hover:bg-muted ${playbook.done ? 'border-emerald-500/40 text-emerald-700 dark:text-emerald-300' : 'border-border bg-background text-foreground'}`}
    >
      <BookMarked className="h-3.5 w-3.5" />
      {playbook.done ? `Playbook: ${playbooks.map(p => p.title).join(', ')}` : 'Start from a playbook'}
    </button>
  ) : null

  const dismissAction = (
    <button
      type="button"
      onClick={() => { void dismiss() }}
      disabled={busy}
      title={goalDone ? 'Hide goal setup for this automation.' : 'This automation runs without a goal. Hides goal setup; you can still add a goal later from chat.'}
      aria-label="Dismiss goal setup"
      className="shrink-0 rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
    >
      {goalDone ? 'Hide' : 'No goal needed'}
    </button>
  )

  // Goal set: one line, so it does not sit tall above every chat.
  if (goalDone) {
    return (
      <section className="shrink-0 border-b border-border bg-primary/[0.04] px-4 py-2" aria-label="Goal setup">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {header}
          {stepper}
          <span className="text-[11px] text-muted-foreground">{stepPosition}</span>
          <div className="ml-auto flex items-center gap-1.5">
            {primaryAction}
            {dismissAction}
            {refreshButton}
          </div>
        </div>
      </section>
    )
  }

  return (
    <section className="relative shrink-0 border-b border-border bg-gradient-to-r from-primary/[0.07] via-background to-background px-4 py-3" aria-label="Goal setup">
      <span aria-hidden className="absolute inset-y-0 left-0 w-0.5 bg-primary/60" />
      <div className="flex items-center justify-between gap-2">
        {header}
        {refreshButton}
      </div>
      <p className="mt-1 text-sm font-semibold leading-snug text-foreground">Give this automation a goal</p>
      <p className="text-xs leading-snug text-muted-foreground">
        It plans the work, tracks the numbers and keeps chasing the goal. Not every automation needs one.
      </p>
      <div className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex min-w-0 items-center gap-3">
          {stepper}
          <span className="shrink-0 text-[11px] text-muted-foreground">{stepPosition}</span>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          {primaryAction}
          {playbookAction}
          {dismissAction}
        </div>
      </div>
    </section>
  )
}
