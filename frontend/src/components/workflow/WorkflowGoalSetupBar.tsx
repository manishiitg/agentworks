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

// One line under each step: what the user gets from it.
const STEP_CAPTION: Record<string, string> = {
  goal: 'What success looks like',
  plan: 'The steps to get there',
  metrics: 'How progress is measured',
  dashboard: 'Where you see it',
}

// Initial setup for an automation, like a Crew template's setup bar: goal,
// then plan, metrics and dashboard, each done in the Builder chat. Goals are optional,
// so it can be dismissed; the server stops showing it once the automation has
// run.
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
  const doneCount = steps.filter(step => step.done).length
  const goalDone = steps.find(step => step.id === 'goal')?.done ?? false

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

  return (
    <section
      className="relative shrink-0 overflow-hidden border-b border-border bg-gradient-to-r from-primary/[0.07] via-background to-background px-4 py-3"
      aria-label="Goal setup"
    >
      <span aria-hidden className="absolute inset-y-0 left-0 w-0.5 bg-primary/60" />
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        {/* What this is, and why it matters. */}
        <div className="flex min-w-[15rem] flex-1 items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary ring-1 ring-primary/20">
            <Target className="h-4 w-4" />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              <span>Goal setup</span>
              <span className="rounded-full bg-muted px-1.5 py-px text-[10px] normal-case tracking-normal">Optional</span>
            </div>
            <p className="mt-0.5 text-sm font-semibold leading-snug text-foreground">
              {goalDone ? 'Goal set. Finish setting it up' : 'Give this automation a goal'}
            </p>
            <p className="text-xs leading-snug text-muted-foreground">
              It plans the work, tracks the numbers and keeps chasing the goal.
            </p>
            {playbook ? (
              <button
                type="button"
                onClick={() => useWorkflowStore.getState().openWorkspaceView('playbooks')}
                title={playbook.done ? 'Installed playbooks guide this setup. Open Playbooks.' : 'Optional: start from a playbook. Open Playbooks.'}
                className={`mt-1 inline-flex items-center gap-1 text-xs hover:underline ${playbook.done ? 'text-emerald-600 dark:text-emerald-400' : 'text-primary'}`}
              >
                <BookMarked className="h-3 w-3" />
                {playbook.done ? `Playbook: ${playbooks.map(p => p.title).join(', ')}` : 'Playbook (optional)'}
              </button>
            ) : null}
          </div>
        </div>

        {/* The steps, in order. */}
        <ol className="flex items-start" aria-label="Setup steps">
          {steps.map((step, index) => {
            const current = step.id === next?.id
            const clickable = current && !busy
            return (
              <li key={step.id} className="flex items-start">
                {index > 0 ? (
                  <span aria-hidden className={`mt-3 h-px w-5 sm:w-8 ${steps[index - 1].done ? 'bg-emerald-500/60' : 'bg-border'}`} />
                ) : null}
                <button
                  type="button"
                  disabled={!clickable}
                  onClick={() => { void startStep(step) }}
                  aria-current={current ? 'step' : undefined}
                  title={current ? ACTION_LABEL[step.id] : step.done ? `${step.label} is done` : `${step.label} comes next`}
                  className={`group flex w-16 flex-col items-center gap-1 rounded-md px-1 text-center sm:w-24 ${clickable ? 'cursor-pointer' : 'cursor-default'}`}
                >
                  <span
                    className={`flex h-6 w-6 items-center justify-center rounded-full text-[11px] font-semibold transition-colors ${
                      step.done
                        ? 'bg-emerald-500 text-white'
                        : current
                          ? 'bg-primary text-primary-foreground ring-4 ring-primary/20 group-hover:ring-primary/35'
                          : 'border border-border bg-background text-muted-foreground'
                    }`}
                  >
                    {step.done ? <Check className="h-3.5 w-3.5" strokeWidth={3} /> : index + 1}
                  </span>
                  <span className={`text-xs font-medium leading-tight ${step.done || current ? 'text-foreground' : 'text-muted-foreground'}`}>{step.label}</span>
                  <span className="hidden text-[10px] leading-tight text-muted-foreground lg:block">{STEP_CAPTION[step.id]}</span>
                </button>
              </li>
            )
          })}
        </ol>

        {/* Progress and the one thing to do next. */}
        <div className="flex shrink-0 items-center gap-3">
          <div className="hidden w-20 flex-col gap-1 sm:flex" aria-label={`${doneCount} of ${steps.length} done`}>
            <span className="text-[11px] text-muted-foreground">{doneCount} of {steps.length} done</span>
            <span className="h-1 overflow-hidden rounded-full bg-muted">
              <span className="block h-full rounded-full bg-primary transition-all" style={{ width: `${steps.length ? (doneCount / steps.length) * 100 : 0}%` }} />
            </span>
          </div>
          {next ? (
            <button
              type="button"
              onClick={() => { void startStep(next) }}
              disabled={busy}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-xs font-semibold text-primary-foreground shadow-sm hover:bg-primary/90 disabled:opacity-60"
            >
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <>{ACTION_LABEL[next.id] ?? 'Set up in chat'}<ArrowRight className="h-3.5 w-3.5" /></>}
            </button>
          ) : null}
          <div className="flex items-center">
            <button type="button" onClick={() => { void refresh() }} disabled={loading} title="Check setup again" aria-label="Refresh goal setup" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            </button>
            <button type="button" onClick={() => { void dismiss() }} disabled={busy} title="Goals are optional. Hide goal setup for this automation." aria-label="Dismiss goal setup" className="rounded-md px-2 py-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
              Skip
            </button>
          </div>
        </div>
      </div>
    </section>
  )
}
