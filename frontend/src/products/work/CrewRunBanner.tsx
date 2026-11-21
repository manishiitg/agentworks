import { X } from 'lucide-react'
import { Spinner } from '../../components/automation/AutomationKit'
import type { RunFolderInfo, RunPythonStep } from '../../services/api-types'

const SHELL = 'flex items-start gap-2 rounded-xl border px-4 py-3 text-sm'

/**
 * What is happening with the workflow's run, in one banner at the top of its page: starting, running (which task and
 * which step), or how the run that just ended finished. The ended banner stays for a while and can be dismissed.
 */
export function CrewRunBanner({ state, run, task, steps, onDismiss }: {
  state: 'starting' | 'running' | 'ended'
  /** The run: the one running, or the one that ended. */
  run?: RunFolderInfo
  task?: string
  /** The run's recorded steps, in the order they started. */
  steps: RunPythonStep[]
  onDismiss?: () => void
}) {
  if (state === 'starting') return <div role="status" data-testid="run-banner" data-state="starting" className={`${SHELL} items-center border-amber-400/60 bg-amber-400/10`}>
    <Spinner className="h-4 w-4" /><span><span className="font-semibold">Starting the run…</span> It shows here as soon as it begins.</span>
  </div>
  if (state === 'running') {
    const step = steps.find(item => item.status === 'running')
    const at = step ? steps.indexOf(step) + 1 : 0
    return <div role="status" data-testid="run-banner" data-state="running" className={`${SHELL} items-center border-amber-400/60 bg-amber-400/10`}>
      <Spinner className="h-4 w-4" />
      <span><span className="font-semibold">Running now</span>{task ? ` · task: ${task}` : ''}{step ? ` · step ${at}: ${step.name}` : ''}{steps.length ? ` · ${steps.filter(item => item.status === 'completed' || item.status === 'finished').length} done` : ''}</span>
    </div>
  }
  const status = run?.metadata?.status ?? ''
  const failed = status === 'failed' || status === 'error'
  const ok = status === 'completed' || status === 'finished'
  const failedStep = steps.find(item => item.status === 'failed')
  const took = run?.metadata?.duration_ms ? ` · took ${Math.max(1, Math.round(run.metadata.duration_ms / 1000))}s` : ''
  const tone = failed ? 'border-red-500/50 bg-red-500/5' : ok ? 'border-emerald-500/40 bg-emerald-500/5' : 'border-border bg-muted/30'
  return <div role="status" data-testid="run-banner" data-state="ended" data-status={status} className={`${SHELL} ${tone}`}>
    <span className="min-w-0 flex-1">
      <span className={`font-semibold ${failed ? 'text-red-600 dark:text-red-400' : ok ? 'text-emerald-600 dark:text-emerald-400' : ''}`}>{ok ? 'Run finished' : failed ? 'Run failed' : `Run ended (${status || 'unknown'})`}</span>
      {task ? ` · task: ${task}` : ''}{took}
      {failed && failedStep && <span className="block text-xs text-red-600 dark:text-red-400">Stopped at step: {failedStep.name}{failedStep.detail ? `. ${failedStep.detail}` : ''}</span>}
    </span>
    {onDismiss && <button type="button" aria-label="Dismiss" title="Dismiss" onClick={onDismiss} className="shrink-0 text-muted-foreground hover:text-foreground"><X aria-hidden="true" className="h-4 w-4" /></button>}
  </div>
}
