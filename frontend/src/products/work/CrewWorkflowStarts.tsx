import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { schedulerApi } from '../../api/scheduler'
import { workflowWebhooksApi, apiTriggerURL, type WorkflowAPITrigger } from '../../api/workflowWebhooks'
import type { ScheduledJob, ScheduledJobRun } from '../../services/api-types'
import { DetailLine, PageCard, PageRow, RowButton, RunDot, relativeTime } from '../../components/automation/AutomationKit'
import { Switch } from '../../components/ui/Switch'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useCapabilitiesStore } from '../../stores/useCapabilitiesStore'

function failure(cause: unknown, fallback: string): string {
  if (axios.isAxiosError(cause) && typeof cause.response?.data === 'string' && cause.response.data.trim()) return cause.response.data.trim()
  return cause instanceof Error && cause.message ? cause.message : fallback
}

const runState = (status: ScheduledJobRun['status']): 'running' | 'finished' | 'stopped' | 'idle' =>
  status === 'running' ? 'running' : status === 'success' ? 'finished' : status === 'waiting_for_capacity' || status === 'waiting_for_workflow' ? 'idle' : 'stopped'

/**
 * The workflow page's Starts tab (owner-approved mockup, 19 Nov): what sets this workflow off, its schedules and its
 * webhooks, each with what it starts and when it last did. Every line is the server's; adding or changing one is a
 * chat with the Crew's agent. On the Mac webhooks cannot be reached, so that card says where they work instead.
 */
export function CrewWorkflowStarts({ workspacePath, workflowName, onAsk, onOpenRun }: {
  workspacePath: string
  workflowName: string
  onAsk: (message: string) => void | Promise<void>
  /** Opens one run (its folder name) on the Runs tab. */
  onOpenRun: (runFolder: string) => void
}) {
  const canWrite = useCanWriteWorkflow(workspacePath)
  const onMac = useCapabilitiesStore(state => state.capabilities?.local_mode ?? false)
  const [jobs, setJobs] = useState<ScheduledJob[] | null>(null)
  const [hooks, setHooks] = useState<WorkflowAPITrigger[] | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [openId, setOpenId] = useState<string | null>(null)
  const [calls, setCalls] = useState<Record<string, ScheduledJobRun[] | 'loading' | 'error'>>({})
  const [copied, setCopied] = useState('')

  const refresh = useCallback(async () => {
    setError('')
    try {
      const [listed, webhooks] = await Promise.all([
        schedulerApi.listJobs({ entity_type: 'workflow', workspace_path: workspacePath, limit: 200 }),
        onMac ? Promise.resolve(null) : workflowWebhooksApi.list(workspacePath),
      ])
      setJobs(listed.jobs ?? [])
      setHooks(webhooks ? webhooks.triggers.filter(trigger => !trigger.kind) : [])
    } catch (cause) {
      setError(failure(cause, 'Could not load what starts this workflow. Try again.'))
      setJobs(current => current ?? [])
      setHooks(current => current ?? [])
    }
  }, [workspacePath, onMac])
  useEffect(() => { setJobs(null); setHooks(null); setOpenId(null); setCalls({}); void refresh() }, [refresh])

  async function change(action: () => Promise<unknown>, fallback: string) {
    if (busy) return
    setBusy(true); setError('')
    try { await action(); await refresh() } catch (cause) { setError(failure(cause, fallback)) } finally { setBusy(false) }
  }
  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); setCopied('Address copied') } catch { setError('Could not copy. Open the webhook and copy the address shown.') }
  }
  function toggleHook(id: string) {
    const next = openId === id ? null : id
    setOpenId(next)
    if (!next || calls[id] === 'loading' || Array.isArray(calls[id])) return
    setCalls(current => ({ ...current, [id]: 'loading' }))
    schedulerApi.getJobRuns(id, 10).then(result => setCalls(current => ({ ...current, [id]: result.runs ?? [] })))
      .catch(() => setCalls(current => ({ ...current, [id]: 'error' })))
  }

  const loading = jobs === null || hooks === null
  const schedules = (jobs ?? []).filter(job => !job.kind && job.schedule_type !== 'webhook')
  const lastCall = (id: string) => (jobs ?? []).find(job => job.id === id)?.last_run_at
  const ask = (message: string) => () => void onAsk(`${message}\n\nWorkflow: ${workflowName}`)

  return <div className="space-y-4">
    {error && <p role="alert" className="rounded-xl border border-destructive/30 p-3 text-sm text-destructive">{error} <button type="button" className="underline" onClick={() => void refresh()}>Try again</button></p>}
    <PageCard title="Schedules" testId="workflow-starts-schedules"
      hint={loading ? 'Loading…' : schedules.length ? `${schedules.length} set up · they start this workflow by themselves` : 'Nothing starts this workflow on a timer yet.'}
      action={canWrite ? <RowButton onClick={ask('Add a schedule that starts this workflow. Ask me which task and when, then set it up.')}>Add schedule</RowButton> : undefined}>
      {schedules.length > 0 && schedules.map(job => <PageRow key={job.id} testId={`start-schedule-${job.id}`}
        lead={<Switch aria-label={`${job.enabled ? 'Pause' : 'Turn on'} ${job.name}`} checked={job.enabled} disabled={busy || !canWrite}
          onCheckedChange={enabled => void change(() => enabled ? schedulerApi.enableJob(job.id) : schedulerApi.disableJob(job.id), 'Could not change this schedule. Try again.')} />}
        title={job.name}
        lines={[job.starts, [job.status_word, job.next_run_at && job.enabled ? `next ${new Date(job.next_run_at).toLocaleString()}` : ''].filter(Boolean).join(' · ')]}
        aside={job.last_run_at ? `Last ran ${relativeTime(job.last_run_at)}` : 'Not run yet'}
        open={openId === job.id} onToggle={() => setOpenId(openId === job.id ? null : job.id)}>
        {job.last_result && <DetailLine label="Last run">{job.last_result}</DetailLine>}
        {job.changed_by && <DetailLine label="Changed">{job.changed_by}</DetailLine>}
        {canWrite && <p className="flex gap-3 pt-1">
          <button type="button" className="text-primary hover:underline" onClick={ask(`Change the schedule “${job.name}” (id ${job.id}). I want: `)}>Change</button>
          <button type="button" disabled={busy} className="text-destructive hover:underline disabled:opacity-50" onClick={() => void change(() => schedulerApi.deleteJob(job.id), 'Could not delete this schedule. Try again.')}>Delete</button>
        </p>}
      </PageRow>)}
    </PageCard>

    <PageCard title="Webhooks" testId="workflow-starts-webhooks"
      hint={onMac ? 'An address another app calls to start this workflow.' : loading ? 'Loading…' : (hooks ?? []).length ? `${(hooks ?? []).length} · another app calls the address to start this workflow` : 'No webhook yet. Another app (a shop, a form) can call one to start this workflow.'}
      action={!onMac && canWrite ? <RowButton onClick={ask('Add a webhook that starts this workflow. Ask me which task it should start and what will call it, then set it up.')}>Add webhook</RowButton> : undefined}>
      {(onMac || (hooks ?? []).length > 0 || copied) ? <>{onMac ? <div className="flex items-center gap-3 px-4 py-3">
        <p className="min-w-0 flex-1 text-xs text-muted-foreground">Webhooks work once this workflow is online on the Team, because other apps need an address they can reach.</p>
        <RowButton onClick={ask('Move this workflow online to the Team, with its schedules, so it can have webhooks.')}>Move it online</RowButton>
      </div> : (hooks ?? []).map(hook => {
        const last = lastCall(hook.id)
        const list = calls[hook.id]
        return <PageRow key={hook.id} testId={`start-webhook-${hook.id}`}
          lead={<Switch aria-label={`${hook.enabled ? 'Pause' : 'Turn on'} ${hook.name}`} checked={hook.enabled} disabled={busy || !canWrite}
            onCheckedChange={enabled => void change(() => workflowWebhooksApi.save({ ...hook, enabled, workspace_path: workspacePath }, hook.id), 'Could not change this webhook. Try again.')} />}
          title={hook.name}
          lines={[hook.starts, [hook.enabled ? 'On' : 'Paused', last ? `last called ${relativeTime(last)}` : 'never called yet'].join(' · ')]}
          action={<RowButton onClick={() => void copy(apiTriggerURL(hook.path))}>Copy address</RowButton>}
          open={openId === hook.id} onToggle={() => toggleHook(hook.id)}>
          <DetailLine label="Address"><code className="break-all select-all">{apiTriggerURL(hook.path)}</code></DetailLine>
          <p className="pt-1 font-semibold">Recent calls</p>
          {list === 'loading' && <p className="text-muted-foreground">Loading…</p>}
          {list === 'error' && <p className="text-muted-foreground">Could not load the calls. Close and open this webhook to try again.</p>}
          {Array.isArray(list) && list.length === 0 && <p className="text-muted-foreground">Not called yet. When another app calls the address, the call and the run it started show here.</p>}
          {Array.isArray(list) && list.map(call => {
            const folder = call.run_folder?.split('/').filter(Boolean).pop()
            return <p key={call.id} className="flex min-w-0 items-center gap-2">
              <RunDot state={runState(call.status)} />
              <span className="min-w-0 flex-1 truncate text-muted-foreground">{new Date(call.webhook?.received_at || call.started_at).toLocaleString()}{call.error ? ` · ${call.error}` : ''}</span>
              {folder && <button type="button" className="shrink-0 text-primary hover:underline" onClick={() => onOpenRun(folder)}>Open run</button>}
            </p>
          })}
          {canWrite && <p className="flex gap-3 pt-1">
            <button type="button" className="text-primary hover:underline" onClick={ask(`Change the webhook “${hook.name}” (id ${hook.id}). I want: `)}>Change</button>
            <button type="button" disabled={busy} className="text-destructive hover:underline disabled:opacity-50" onClick={() => void change(() => workflowWebhooksApi.delete(workspacePath, hook.id), 'Could not delete this webhook. Try again.')}>Delete</button>
          </p>}
        </PageRow>
      })}
      {copied && <p role="status" className="px-4 py-2 text-xs text-muted-foreground">{copied}</p>}</> : null}
    </PageCard>
  </div>
}
