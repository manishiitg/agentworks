import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { workflowWebhooksApi, type APITriggerOptions, type RelayReleasesResponse, type WorkflowAPITrigger } from '../../api/workflowWebhooks'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { getApiBaseUrl } from '../../services/api'

const emptyOptions: APITriggerOptions = { triggers: [], routes: [], groups: [] }
const buttonClass = 'rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50'

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : 'Unable to load functions'
}

/**
 * A workflow's functions: how other Crews, workflows and MCP/CLI tools call it.
 * The built-in ask reaches the workflow's Run-mode assistant; typed functions
 * are function triggers (a route plus required inputs set as run variables).
 */
export default function WorkflowFunctionsView({ workspacePath, relayMode = false, relayWorkflowID, refreshToken = 0, onCounts, onAsk }: {
  workspacePath: string
  relayMode?: boolean
  relayWorkflowID?: string
  refreshToken?: number
  onCounts?: (counts: { functions: number; running: number }) => void
  /** Route a request to the workflow's Builder chat. */
  onAsk?: (message: string) => void | Promise<void>
}) {
  const canWrite = useCanWriteWorkflow(workspacePath)
  const [options, setOptions] = useState<APITriggerOptions>(emptyOptions)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [releases, setReleases] = useState<RelayReleasesResponse | null>(null)

  const refresh = useCallback(async () => {
    try { setOptions(await workflowWebhooksApi.list(workspacePath)); setError('') }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setLoaded(true) }
  }, [workspacePath])

  useEffect(() => { setOptions(emptyOptions); setLoaded(false); void refresh() }, [refresh])
  useEffect(() => { if (refreshToken) void refresh() }, [refreshToken, refresh])
  const refreshReleases = useCallback(() => {
    if (!relayMode || !relayWorkflowID) { setReleases(null); return }
    void workflowWebhooksApi.relayReleases(relayWorkflowID).then(setReleases).catch(() => setReleases(null))
  }, [relayMode, relayWorkflowID])
  useEffect(() => { refreshReleases() }, [refreshReleases, refreshToken])
  useLiveRefetch(refreshReleases, { kinds: ['plan'], workflow: workspacePath, fallbackMs: 30_000, enabled: relayMode && !!relayWorkflowID })

  const functions = options.triggers.filter(trigger => trigger.kind === 'function' && trigger.function)
  const callerLinks = options.triggers.filter(trigger => trigger.kind === 'internal')
  useEffect(() => { onCounts?.({ functions: functions.length + (relayMode ? 0 : 1), running: 0 }) }, [onCounts, functions.length, relayMode])

  const save = async (trigger: WorkflowAPITrigger) => {
    if (!canWrite || busy) return
    setBusy(true); setError('')
    try { await workflowWebhooksApi.save({ ...trigger, workspace_path: workspacePath }, trigger.id); await refresh() }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }
  const remove = async (id: string) => {
    if (!canWrite || busy) return
    setBusy(true); setError('')
    try { await workflowWebhooksApi.delete(workspacePath, id); await refresh() }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }
  const routeLabel = (trigger: WorkflowAPITrigger) => {
    const selections = Object.entries(trigger.route_selections || {})
    if (selections.length === 0) return 'the full workflow'
    return selections.map(([stepId, routeId]) => {
      const route = (options.routes || []).find(option => option.step_id === stepId && option.route_id === routeId)
      return route ? `${route.step_title} → ${route.route_name || routeId}` : `${stepId} → ${routeId}`
    }).join(', ')
  }

  return <div className="h-full min-w-0 w-full max-w-none overflow-x-hidden overflow-y-auto bg-background">
    <div className="space-y-4 p-4" data-testid="workflow-functions">
      <p className="text-xs leading-relaxed text-muted-foreground">{relayMode ? 'Publish a Relay version before calling its function triggers with a JSON INPUT object. An external access token with runs:execute can start a published run and poll its durable run ID. Builder edits stay in the draft until the next publish.' : "Functions are how other Crews, workflows and MCP/CLI tools call this workflow. Callers are identified by the platform: no URL or secret. A typed function sets its inputs as the run's variables and refuses a call missing a required input before anything runs."}</p>
      {relayMode && <section className="rounded-lg border border-border p-3 text-xs" data-testid="relay-release-status">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div><span className="font-medium">API release</span> <span className="text-muted-foreground">{releases?.active_version ? `Published ${releases.active_version}` : 'Draft only'}</span></div>
          {canWrite && onAsk && <div className="flex gap-1.5">
            <button type="button" className={buttonClass} onClick={() => void onAsk('Test the current Relay draft with a sample INPUT JSON object. Ask me for the JSON if I have not provided it, run the complete graph, and report the actual final JSON or error.')}>Test draft in chat</button>
            <button type="button" className={buttonClass} onClick={() => void onAsk('Validate the current Relay draft and publish it as a new API version. Report the version and content hash returned by publish_relay.')}>Publish in chat</button>
          </div>}
        </div>
        {releases?.releases.length ? <p className="mt-2 text-muted-foreground">Available versions: {releases.releases.map(item => item.version).join(', ')}. API calls use the active version unless you send a version.</p> : <p className="mt-2 text-muted-foreground">Publish the tested graph to make it callable through the Relay API. You can keep editing the draft afterward.</p>}
      </section>}
      {error && <p role="alert" className="rounded-md border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
      <div className="min-w-0 space-y-2">
        {functions.map(trigger => {
          const fn = trigger.function!
          return <section key={trigger.id} data-testid={`workflow-function-${fn.name}`} className="min-w-0 space-y-1.5 rounded-lg border border-border p-3 text-xs">
            <div className="flex items-center justify-between gap-2">
              <h3 className="truncate font-mono text-sm font-medium">{fn.name}</h3>
              <span className="shrink-0 text-[11px] text-muted-foreground">{trigger.enabled ? 'Active' : 'Paused'}</span>
            </div>
            {fn.description && <p className="text-muted-foreground">{fn.description}</p>}
            <p><span className="font-medium">Inputs</span> <span className="text-muted-foreground">{(fn.inputs || []).length === 0 ? 'none' : (fn.inputs || []).map(input => `${input.name}: ${input.type || 'string'}${input.required ? '' : '?'}`).join(', ')}</span></p>
            {relayMode && relayWorkflowID && <details className="rounded bg-muted/40 p-2">
              <summary className="cursor-pointer">External API request</summary>
              <pre className="mt-2 overflow-x-auto whitespace-pre-wrap text-[11px]">{`POST ${getApiBaseUrl()}/api/relays/${relayWorkflowID}/runs\nAuthorization: Bearer <access token>\nContent-Type: application/json\n\n${JSON.stringify({ function: fn.name, ...(releases?.active_version ? { version: releases.active_version } : {}), input: { example: 'value' }, idempotency_key: 'unique-request-id' }, null, 2)}\n\nOmit version to use the active release. Poll GET ${getApiBaseUrl()}/api/relays/${relayWorkflowID}/runs/<run_id>.`}</pre>
            </details>}
            <p className="text-muted-foreground">Runs {relayMode ? 'the Relay graph' : routeLabel(trigger)}{(trigger.group_names || []).length ? ` · groups ${trigger.group_names.join(', ')}` : ''}{fn.allowed_callers?.length ? ` · only ${fn.allowed_callers.map(caller => caller.id).join(', ')}` : ` · anyone who can run this ${relayMode ? 'Relay' : 'workflow'}`}</p>
            {canWrite && <div className="flex flex-wrap justify-end gap-1.5 pt-1">
              {onAsk && <button type="button" disabled={busy} className={buttonClass} onClick={() => void onAsk(`I want to change the workflow function "${fn.name}". Show me its current definition (route, inputs, who may call) and ask me what to change.`)}>Edit in chat</button>}
              <button type="button" disabled={busy} className={buttonClass} onClick={() => void save({ ...trigger, enabled: !trigger.enabled })}>{trigger.enabled ? 'Pause' : 'Enable'}</button>
              <button type="button" disabled={busy} className="rounded-md px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50" onClick={() => void remove(trigger.id)}>Remove</button>
            </div>}
          </section>
        })}
        {!relayMode && <section data-testid="workflow-function-ask" className="min-w-0 space-y-1.5 rounded-lg border border-border p-3 text-xs">
          <div className="flex items-center justify-between gap-2">
            <h3 className="truncate font-mono text-sm font-medium">ask</h3>
            <span className="shrink-0 text-[11px] text-muted-foreground">Built in</span>
          </div>
          <p className="text-muted-foreground">Free text to this workflow's assistant (Run mode). Each caller gets one continuing thread, shown in Chats as "Asked by …". It answers questions and starts runs with the right variables; it can't edit the workflow, so requested changes arrive as suggestions in Human decisions.</p>
        </section>}
        {loaded && functions.length === 0 && <div className="rounded-lg border border-dashed border-border p-4 text-center text-xs text-muted-foreground">
          <p>{relayMode ? 'No Relay trigger yet. Add a function with a required INPUT object to call this graph.' : "No typed functions yet. Expose a route with required inputs so callers can't run it without them."}</p>
          {canWrite && onAsk && <button type="button" className={`${buttonClass} mt-2`} onClick={() => void onAsk(relayMode ? 'Add an API function trigger to this Relay with one required INPUT object argument. Show me the name and input contract, then create it.' : 'Expose one of this workflow\'s routes as a typed function other Crews can call. List the routes and declared variables, suggest a function name and which inputs should be required, and create it once I confirm.')}>Add a function in chat</button>}
        </div>}
      </div>
      {!relayMode && callerLinks.length > 0 && <p className="text-[11px] text-muted-foreground">Older per-caller links (kept for existing callers): {callerLinks.map(trigger => trigger.name).join(', ')}</p>}
    </div>
  </div>
}
