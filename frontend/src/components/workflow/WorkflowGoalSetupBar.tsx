import { useCallback, useEffect, useState } from 'react'
import { CheckCircle2, Circle, Loader2, RefreshCw, Target, X } from 'lucide-react'
import { workflowManifestApi, type WorkflowGoalSetupStatus } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { goalSetupChatMessage } from '../../utils/goalSetupChat'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

const REFRESH_MS = 30_000

const ACTION_LABEL: Record<string, string> = {
  goal: 'Set the goal in chat',
  plan: 'Design the plan in chat',
  metrics: 'Add metrics in chat',
  dashboard: 'Design the dashboard in chat',
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

  const start = async () => {
    if (!next || busy) return
    setBusy(true)
    try {
      await sendWorkspacePaneMessageToChat({ workspacePath, message: goalSetupChatMessage(next.command) })
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
    <section className="shrink-0 border-b border-border bg-muted/30 px-4 py-2.5" aria-label="Goal setup">
      <div className="flex flex-wrap items-center gap-2">
        <Target className="h-4 w-4 shrink-0 text-primary" />
        <span className="text-sm font-semibold text-foreground">Goal setup</span>
        <span className="text-xs text-muted-foreground">Optional</span>
        <ol className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
          {status.checks.map(check => (
            <li key={check.id} className={`flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs ${check.done ? 'border-emerald-500/40 text-emerald-700 dark:text-emerald-300' : check.id === next?.id ? 'border-primary/50 text-foreground' : 'border-border text-muted-foreground'}`}>
              {check.done ? <CheckCircle2 className="h-3 w-3" /> : <Circle className="h-3 w-3" />}
              {check.label}
            </li>
          ))}
        </ol>
        <button type="button" onClick={() => { void refresh() }} disabled={loading} aria-label="Refresh goal setup" className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
        </button>
        {next ? (
          <button type="button" onClick={() => { void start() }} disabled={busy} className="shrink-0 rounded-md bg-primary px-2.5 py-1.5 text-xs font-semibold text-primary-foreground hover:bg-primary/90 disabled:opacity-60">
            {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : ACTION_LABEL[next.id] ?? 'Set up in chat'}
          </button>
        ) : null}
        <button type="button" onClick={() => { void dismiss() }} disabled={busy} title="Goals are optional. Hide goal setup for this automation." aria-label="Dismiss goal setup" className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50">
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    </section>
  )
}
