import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { workflowWebhooksApi, type APITriggerOptions, type WorkflowAPITrigger } from '../../api/workflowWebhooks'
import { useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'

const emptyOptions: APITriggerOptions = { triggers: [], routes: [], input_presets: [] }
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
export default function WorkflowFunctionsView({ workspacePath, refreshToken = 0, onCounts, onAsk, embedded = false }: {
  workspacePath: string
  embedded?: boolean
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

  const refresh = useCallback(async () => {
    try { setOptions(await workflowWebhooksApi.list(workspacePath)); setError('') }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setLoaded(true) }
  }, [workspacePath])

  useEffect(() => { setOptions(emptyOptions); setLoaded(false); void refresh() }, [refresh])
  useEffect(() => { if (refreshToken) void refresh() }, [refreshToken, refresh])

  const functions = options.triggers.filter(trigger => trigger.kind === 'function' && trigger.function)
  const callerLinks = options.triggers.filter(trigger => trigger.kind === 'internal')
  useEffect(() => { onCounts?.({ functions: functions.length + (1), running: 0 }) }, [onCounts, functions.length])

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

  if (embedded && !error && functions.length === 0) return null
  return <div className={`${embedded ? '' : 'h-full'} min-w-0 w-full max-w-none overflow-x-hidden overflow-y-auto bg-background`}>
    <div className="space-y-4 p-4" data-testid="workflow-functions">
      <p className="text-xs text-muted-foreground">{loaded ? `${functions.length} functions · ${functions.filter(item => item.enabled).length} enabled` : 'Loading functions…'}</p>

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
            <p><span className="font-medium">Inputs</span> <span className="text-muted-foreground">{Object.keys(trigger.input_schema?.properties ?? {}).length === 0 ? 'none' : Object.keys(trigger.input_schema?.properties ?? {}).map(input => `${input}: ${trigger.input_schema?.properties?.[input]?.type || 'string'}${trigger.input_schema?.required?.includes(input) ? '' : '?'}`).join(', ')}</span></p>

            <p className="text-muted-foreground">Runs {routeLabel(trigger)}{trigger.input_preset ? ` · saved inputs: ${trigger.input_preset}` : ''}{fn.allowed_callers?.length ? ` · only ${fn.allowed_callers.map(caller => caller.id).join(', ')}` : ` · anyone who can run this ${'workflow'}`}</p>
            {canWrite && !!options.chat_choices?.length && <label className="flex items-center gap-2">Chat<select aria-label={`Chat for ${fn.name}`} disabled={!canWrite || busy} value={trigger.chat_id || 'main'} onChange={event => void save({ ...trigger, chat_id: event.target.value })} className="rounded border border-border bg-background px-2 py-1">{options.chat_choices.map(chat => <option key={chat.id} value={chat.id}>{chat.label}</option>)}</select></label>}
            {canWrite && <div className="flex flex-wrap justify-end gap-1.5 pt-1">
              {onAsk && <button type="button" disabled={busy} className={buttonClass} onClick={() => void onAsk(`I want to change the workflow function "${fn.name}". Show me its current definition (route, inputs, who may call) and ask me what to change.`)}>Edit in chat</button>}
              <button type="button" disabled={busy} className={buttonClass} onClick={() => void save({ ...trigger, enabled: !trigger.enabled })}>{trigger.enabled ? 'Pause' : 'Enable'}</button>
              <button type="button" disabled={busy} className="rounded-md px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50" onClick={() => void remove(trigger.id)}>Remove</button>
            </div>}
          </section>
        })}
        {!embedded && <section data-testid="workflow-function-ask" className="min-w-0 space-y-1.5 rounded-lg border border-border p-3 text-xs">
          <div className="flex items-center justify-between gap-2">
            <h3 className="truncate font-mono text-sm font-medium">ask</h3>
            <span className="shrink-0 text-[11px] text-muted-foreground">Built in</span>
          </div>
          <p className="text-muted-foreground">Questions and permitted runs through the assistant. Each caller keeps a continuing chat; workflow edits stay with the Builder.</p>
        </section>}
        {loaded && functions.length === 0 && <div className="rounded-lg border border-dashed border-border p-4 text-center text-xs text-muted-foreground">
          <p>{"No typed functions yet. Expose a route with required inputs so callers can't run it without them."}</p>
          {canWrite && onAsk && <button type="button" className={`${buttonClass} mt-2`} onClick={() => void onAsk('Expose one of this workflow\'s routes as a typed function other Crews can call. List the routes and declared variables, suggest a function name and which inputs should be required, and create it once I confirm.')}>Add a function in chat</button>}
        </div>}
      </div>
      {callerLinks.length > 0 && <p className="text-[11px] text-muted-foreground">Older per-caller links (kept for existing callers): {callerLinks.map(trigger => trigger.name).join(', ')}</p>}
    </div>
  </div>
}
