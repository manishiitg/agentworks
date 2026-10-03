import { GmailInboundPanel } from './bots/GmailInboundPanel'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import axios from 'axios'
import { Copy, GitBranch, ShieldCheck, Webhook, Zap } from 'lucide-react'
import { workflowWebhooksApi, apiTriggerURL, type APITriggerOptions, type WorkflowAPITrigger } from '../../api/workflowWebhooks'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'

const emptyOptions: APITriggerOptions = { triggers: [], routes: [], groups: [] }
const buttonClass = 'rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50'

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : 'Unable to update API triggers'
}

export default function WorkflowAPITriggersView({ workspacePath, onViewRuns, deliveryHistory, headerAction, hideHeader = false, refreshToken = 0, onCounts, onAsk }: { workspacePath: string | null; onViewRuns?: () => void; deliveryHistory?: ReactNode; headerAction?: React.ReactNode; hideHeader?: boolean; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void; onAsk?: (message: string) => void | Promise<void> }) {
  const canWrite = useCanWriteWorkflow(workspacePath)
  const [options, setOptions] = useState<APITriggerOptions>(emptyOptions)
  const [issued, setIssued] = useState<WorkflowAPITrigger | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState('')
  const requestGeneration = useRef(0)

  const refresh = useCallback(async () => {
    if (!workspacePath) return
    const generation = ++requestGeneration.current
    try {
      const result = await workflowWebhooksApi.list(workspacePath)
      if (generation === requestGeneration.current) setOptions(result)
    } catch (cause) {
      if (generation === requestGeneration.current) setError(errorMessage(cause))
    }
  }, [workspacePath])

  const cancelPendingRequests = useCallback(() => { requestGeneration.current++ }, [])

  useEffect(() => {
    setOptions(emptyOptions); setIssued(null); setError(''); setCopied('')
    void refresh()
    return cancelPendingRequests
  }, [refresh, cancelPendingRequests])

  useEffect(() => {
    if (refreshToken) void refresh()
  }, [refreshToken, refresh])

  const activeTriggers = options.triggers.filter(trigger => trigger.enabled).length
  useEffect(() => {
    onCounts?.({ active: activeTriggers, paused: options.triggers.length - activeTriggers })
  }, [onCounts, activeTriggers, options.triggers.length])

  const afterSave = async () => {
    if (!workspacePath) return
    await refresh()
    await useWorkflowManifestStore.getState().refreshWorkflows()
  }

  const save = async (value: WorkflowAPITrigger, rotate = false) => {
    if (!workspacePath || !canWrite || busy) return
    setBusy(true); setError(''); setCopied('')
    try {
      const result = await workflowWebhooksApi.save({ ...value, workspace_path: workspacePath, rotate_secret: rotate }, value.id)
      if (result.secret) setIssued(result)
      await afterSave()
    } catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }

  const remove = async (id: string) => {
    if (!workspacePath || !canWrite || busy) return
    setBusy(true); setError('')
    try {
      await workflowWebhooksApi.delete(workspacePath, id)
      if (issued?.id === id) setIssued(null)
      await afterSave()
    } catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }

  const copy = async (text: string, label: string) => {
    try { await navigator.clipboard.writeText(text); setCopied(label) }
    catch { setError('Clipboard access failed. Select and copy the displayed value.') }
  }

  if (!workspacePath) return <p className="p-4 text-sm text-muted-foreground">Select a workflow to configure API triggers.</p>
  return (
    <div className="flex h-full min-h-0 w-full max-w-none flex-col bg-background">
      {!hideHeader && <WorkspaceViewHeader
        icon={Webhook}
        title="Triggers"
        context={<span className="rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">{options.triggers.length}</span>}
        subtitle="External events that start this workflow."
        actions={<>
          {headerAction}
          <WorkspaceViewIconButton label="Refresh webhooks" onClick={() => { setError(''); void refresh() }} />
        </>}
        below={<div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
          <span><span className="mr-1.5 inline-block h-1.5 w-1.5 rounded-full bg-emerald-500" />{activeTriggers} active</span>
          <span>{options.triggers.length - activeTriggers} paused</span>
          {!deliveryHistory && <button type="button" className="text-foreground underline underline-offset-2" onClick={() => onViewRuns ? onViewRuns() : useWorkflowStore.getState().openWorkspaceView('schedules')}>View delivery history</button>}
        </div>}
      />}
      <div className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto">
      <div className="space-y-4 p-4 sm:p-6">
      <div className="flex min-w-0 max-w-full items-start gap-2 rounded-lg bg-muted/35 px-3 py-2.5 text-xs text-muted-foreground">
        <Zap className="mt-0.5 h-3.5 w-3.5 shrink-0" />
        <p className="min-w-0 [overflow-wrap:anywhere]">Create a webhook or change its routing by asking Builder. Each webhook accepts up to four deliveries at once; additional deliveries receive a retry response.</p>
      </div>
      <GmailInboundPanel workspacePath={workspacePath} refreshToken={refreshToken} onAsk={onAsk} />
      {error && <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{error}</p>}
      {issued?.secret && (
        <section className="space-y-3 rounded-lg border border-primary/30 bg-primary/5 p-3">
          <h3 className="text-sm font-medium">Secret for {issued.name}</h3>
          <p className="text-xs text-muted-foreground">Copy this secret now. It is only shown when created or rotated. Rotation replaces the previous secret.</p>
          <code className="block break-all rounded bg-background p-2 text-xs select-all">{issued.secret}</code>
          <div className="flex gap-2"><button type="button" className={buttonClass} onClick={() => void copy(issued.secret!, 'Secret copied')}>Copy secret</button><button type="button" className={buttonClass} onClick={() => setIssued(null)}>Dismiss secret</button></div>
          {issued.auth_mode === 'github' ? <p className="text-xs text-muted-foreground">In GitHub webhook settings, paste the endpoint into Payload URL, choose application/json, paste this value into Secret, and select the events to deliver. Setup pings do not start runs.</p> : <pre className="overflow-x-auto rounded bg-background p-2 text-xs">{`curl -X POST '${apiTriggerURL(issued.path)}' \\\n  -H 'Authorization: Bearer YOUR_TRIGGER_SECRET' \\\n  -H 'Content-Type: application/json' \\\n  -H 'Idempotency-Key: delivery-123' \\\n  -d '{"event":"example","data":{}}'`}</pre>}
        </section>
      )}
      <div className="space-y-3">
        {options.triggers.filter(trigger => !trigger.kind).map(trigger => {
          const routeSelections = trigger.route_selections || {}
          const groupNames = trigger.group_names || []
          const concurrency = trigger.max_concurrency || 4
          return <section key={trigger.id} className="min-w-0 max-w-full overflow-hidden rounded-xl border border-border bg-card">
            <div className="flex items-start justify-between gap-3 px-4 py-3">
              <div className="min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <h3 className="truncate text-sm font-semibold" title={trigger.name}>{trigger.name}</h3>
                  <span className={`shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium ${trigger.enabled ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300' : 'bg-muted text-muted-foreground'}`}>{trigger.enabled ? 'Active' : 'Paused'}</span>
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">POST endpoint · up to {concurrency} concurrent deliveries</p>
              </div>
              {canWrite && <button type="button" disabled={busy} className={buttonClass} onClick={() => void save({ ...trigger, enabled: !trigger.enabled })}>{trigger.enabled ? 'Pause' : 'Enable'}</button>}
            </div>
            <div className="border-y border-border bg-muted/20 px-4 py-2.5">
              <div className="flex min-w-0 items-center gap-2 overflow-hidden">
                <code className="block min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-xs text-foreground" title={apiTriggerURL(trigger.path)}>{apiTriggerURL(trigger.path)}</code>
                <button type="button" aria-label={`Copy endpoint for ${trigger.name}`} title="Copy endpoint" className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => void copy(apiTriggerURL(trigger.path), 'Endpoint copied')}><Copy size={14} /></button>
              </div>
            </div>
            <div className="grid gap-3 px-4 py-3 text-xs sm:grid-cols-2">
              <div className="flex min-w-0 items-start gap-2"><GitBranch className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" /><div className="min-w-0"><p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Starts</p><ul className="mt-1 space-y-1">{trigger.step_id && <li>Step only: {options.steps?.find(step => step.step_id === trigger.step_id)?.title || trigger.step_id}</li>}{!trigger.step_id && Object.keys(routeSelections).length === 0 && <li>Full workflow</li>}{Object.entries(routeSelections).map(([stepId, routeId]) => {
              const route = (options.routes || []).find(option => option.step_id === stepId && option.route_id === routeId)
              return <li key={stepId}>{route ? `${route.step_title} → ${route.route_name || routeId}` : `${stepId} → ${routeId} (route unavailable)`}</li>
            })}</ul></div></div>
              <div className="flex min-w-0 items-start gap-2"><ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" /><div className="min-w-0"><p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Security and access</p><p className="mt-1">{trigger.auth_mode === 'github' ? 'GitHub signature' : 'Bearer token'}</p><p className="mt-0.5 truncate text-muted-foreground" title={groupNames.join(', ')}>{groupNames.length ? `Groups: ${groupNames.join(', ')}` : 'Default access'}</p></div></div>
            </div>
            {trigger.payload_mappings && <details className="border-t border-border px-4 py-2.5 text-xs">
              <summary className="cursor-pointer text-muted-foreground hover:text-foreground">Payload mappings</summary>
              <ul className="mt-2 space-y-1 text-muted-foreground">
                {trigger.payload_mappings.group && <li>Payload {trigger.payload_mappings.group.source} → group ({Object.entries(trigger.payload_mappings.group.values || {}).map(([value, group]) => `${value} → ${group}`).join(', ')})</li>}
                {Object.entries(trigger.payload_mappings.routes || {}).map(([stepId, mapping]) => <li key={stepId}>Payload {mapping.source} → {stepId} branch ({Object.entries(mapping.values || {}).map(([value, route]) => `${value} → ${route}`).join(', ')})</li>)}
                {trigger.payload_mappings.step && <li>Payload {trigger.payload_mappings.step.source} → single step ({Object.entries(trigger.payload_mappings.step.values || {}).map(([value, step]) => `${value} → ${step}`).join(', ')})</li>}
              </ul>
            </details>}
            {canWrite && <div className="flex flex-wrap items-center gap-2 border-t border-border px-4 py-2.5">
              <button type="button" disabled={busy} className={buttonClass} onClick={() => void save(trigger, true)}>Rotate secret</button>
              <button type="button" disabled={busy} className="ml-auto rounded-md px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50" onClick={() => void remove(trigger.id)}>Remove</button>
            </div>}
          </section>
        })}
        {options.triggers.filter(trigger => !trigger.kind).length === 0 && <p className="rounded-lg border border-dashed border-border p-5 text-center text-sm text-muted-foreground">No webhooks configured. Ask the workflow builder chat to create one.</p>}
      </div>
      {options.triggers.some(trigger => trigger.kind && trigger.kind !== 'gmail') && <p className="text-xs text-muted-foreground">Functions (typed calls from Crews, workflows and MCP/CLI) are under Automation → Functions.</p>}
      {copied && <p role="status" className="text-xs text-muted-foreground">{copied}</p>}
      {options.route_error && <p className="text-xs text-muted-foreground">{options.route_error}</p>}
      <details className="rounded-lg border border-border px-3 py-2.5 text-xs text-muted-foreground">
        <summary className="cursor-pointer text-foreground">Delivery requirements</summary>
        <p className="mt-2 leading-relaxed">Use a server URL reachable by the caller. Send JSON up to 1 MiB. A successful delivery returns 202 with a run ID. When all four delivery slots are busy, the endpoint returns 503 with Retry-After. Reuse Idempotency-Key or GitHub’s delivery ID when retrying.</p>
      </details>
      {deliveryHistory && <div className="min-w-0 max-w-full overflow-hidden rounded-lg border border-border">{deliveryHistory}</div>}
      </div>
      </div>
    </div>
  )
}
