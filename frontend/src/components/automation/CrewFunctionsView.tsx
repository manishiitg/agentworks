import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import { crewFunctionsApi, type CrewFunction, type CrewFunctionCall, type CrewFunctionSchema } from '../../api/crewFunctions'
import { productWebhooksApi, type ProductAPITrigger, type ProductTriggerScope } from '../../api/productWebhooks'

const buttonClass = 'rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50'

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : 'Unable to load functions'
}

function schemaType(schema?: CrewFunctionSchema): string {
  if (!schema?.type) return 'any'
  if (schema.enum?.length) return schema.enum.map(String).join(' | ')
  if (schema.type === 'array') return `${schemaType(schema.items)}[]`
  return schema.type
}

/** One line per input or result field: name, type, required. */
export function schemaFields(schema?: CrewFunctionSchema): { name: string; type: string; required: boolean }[] {
  const required = new Set(schema?.required ?? [])
  return Object.entries(schema?.properties ?? {}).map(([name, child]) => ({ name, type: schemaType(child), required: required.has(name) }))
}

function formatTime(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const CALLER_KIND: Record<string, string> = { crew: 'Crew', workflow: 'Workflow', user: 'External connection' }

const STATUS_LABEL: Record<string, string> = { queued: 'Queued', running: 'Running', completed: 'Done', failed: 'Failed' }

function FieldList({ label, schema }: { label: string; schema?: CrewFunctionSchema }) {
  const fields = schemaFields(schema)
  return <div className="min-w-0 text-xs">
    <span className="font-medium text-foreground">{label}</span>{' '}
    {fields.length === 0
      ? <span className="text-muted-foreground">{schema?.type ? schemaType(schema) : label === 'Returns' ? 'final message and output files' : 'none'}</span>
      : <span className="text-muted-foreground">{fields.map(field => `${field.name}: ${field.type}${field.required ? '' : '?'}`).join(', ')}</span>}
  </div>
}

export default function CrewFunctionsView({ scope, refreshToken = 0, onCounts, onEdit }: {
  scope: ProductTriggerScope
  refreshToken?: number
  onCounts?: (counts: { functions: number; running: number }) => void
  /** Route an edit request to the Crew chat. */
  onEdit?: (message: string) => void | Promise<void>
}) {
  const [functions, setFunctions] = useState<CrewFunction[]>([])
  const [calls, setCalls] = useState<CrewFunctionCall[]>([])
  // Caller bindings: one per Crew, workflow or external connection that has
  // called this Crew; each function invocation has its own isolated execution.
  const [callers, setCallers] = useState<ProductAPITrigger[]>([])
  const [expanded, setExpanded] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const data = await crewFunctionsApi.list(scope)
      setFunctions((data.functions ?? []).filter(fn => !fn.implicit))
      setCalls(data.calls ?? [])
      const triggers = await productWebhooksApi.list(scope).then(result => result.triggers).catch(() => [] as ProductAPITrigger[])
      setCallers(triggers.filter(trigger => trigger.kind === 'internal'))
      setError('')
    } catch (cause) { setError(errorMessage(cause)) }
    finally { setLoaded(true) }
  }, [scope])

  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => { if (refreshToken) void refresh() }, [refreshToken, refresh])

  const running = calls.filter(call => call.status === 'queued' || call.status === 'running').length
  useEffect(() => { onCounts?.({ functions: functions.length, running }) }, [onCounts, functions.length, running])

  const remove = async (name: string) => {
    if (busy) return
    setBusy(true); setError('')
    try { await crewFunctionsApi.delete(scope, name); await refresh() }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }

  const disconnect = async (id: string) => {
    if (busy) return
    setBusy(true); setError('')
    try { await productWebhooksApi.delete(scope, id); await refresh() }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }

  return <div className="h-full min-w-0 w-full max-w-none overflow-x-hidden overflow-y-auto bg-background">
    <div className="space-y-4 p-4">
      <p className="text-xs leading-relaxed text-muted-foreground">Functions are internal triggers with checked inputs. Each call runs in a fresh isolated chat and has its own output folder. Calls run in parallel within the running-call limit. Agent conversations use messaging separately. Ask the Crew chat to add or change a function.</p>
      {error && <p role="alert" className="rounded-md border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
      <div className="min-w-0 max-w-full space-y-2">
        {functions.map(fn => <section key={fn.name} data-testid={`crew-function-${fn.name}`} className="min-w-0 max-w-full space-y-2 overflow-hidden rounded-lg border border-border p-3">
          <div className="flex items-center justify-between gap-2">
            <h3 className="truncate font-mono text-sm font-medium">{fn.name}</h3>
            <span className="shrink-0 text-[11px] text-muted-foreground">{fn.implicit ? 'Built in' : fn.created_by ? `By ${fn.created_by}` : ''}{!fn.implicit && fn.updated_at ? ` · ${formatTime(fn.updated_at)}` : ''}</span>
          </div>
          {fn.description && <p className="line-clamp-2 text-xs text-muted-foreground" title={fn.description}>{fn.description}</p>}
          <FieldList label="Inputs" schema={fn.input_schema} />
          <FieldList label="Returns" schema={fn.result_schema} />
          {!fn.implicit && <div className="flex flex-wrap justify-end gap-1.5">
            {onEdit && <button type="button" disabled={busy} className={buttonClass} onClick={() => void onEdit(`I want to update the Crew function "${fn.name}". Show me its current definition and ask me what to change.`)}>Edit in chat</button>}
            <button type="button" disabled={busy} className={buttonClass} onClick={() => void remove(fn.name)}>Remove</button>
          </div>}
        </section>)}
        {loaded && functions.length === 0 && <p className="rounded-lg border border-dashed border-border p-5 text-center text-sm text-muted-foreground">No functions yet.</p>}
      </div>

      <section className="space-y-2" data-testid="crew-function-callers">
        <h3 className="text-sm font-medium">Callers</h3>
        {callers.length === 0 && <p className="rounded-lg border border-dashed border-border p-4 text-center text-xs text-muted-foreground">Nothing has called this Crew yet.</p>}
        {callers.map(caller => <div key={caller.id} className="flex min-w-0 items-center justify-between gap-2 rounded-lg border border-border p-2 text-xs">
          <span className="min-w-0 truncate">{caller.name}<span className="text-muted-foreground"> · {CALLER_KIND[caller.caller?.type ?? ''] ?? 'Caller'}{caller.enabled ? '' : ' · disabled'}</span></span>
          <button type="button" disabled={busy} className={buttonClass} title="Remove this caller's binding; its next call creates a new one" onClick={() => void disconnect(caller.id)}>Disconnect</button>
        </div>)}
        {callers.length > 0 && <p className="text-[11px] text-muted-foreground">Each accepted function call starts a separate execution; calls do not continue these bindings’ chats.</p>}
      </section>

      <section className="space-y-2">
        <h3 className="text-sm font-medium">Recent calls</h3>
        {calls.length === 0 && <p className="rounded-lg border border-dashed border-border p-4 text-center text-xs text-muted-foreground">No recent function calls.</p>}
        {calls.map(call => {
          const open = expanded === call.call_id
          return <div key={call.call_id} data-testid={`crew-function-call-${call.call_id}`} className="min-w-0 rounded-lg border border-border">
            <button type="button" aria-expanded={open} className="flex w-full min-w-0 items-center justify-between gap-2 p-2 text-left text-xs hover:bg-muted/50" onClick={() => setExpanded(open ? null : call.call_id)}>
              <span className="min-w-0 truncate"><span className="font-mono">{call.function}</span> <span className="text-muted-foreground">from {call.caller_label || call.caller_kind}</span></span>
              <span className={`shrink-0 ${call.status === 'failed' ? 'text-destructive' : 'text-muted-foreground'}`}>{STATUS_LABEL[call.status] ?? call.status}</span>
            </button>
            {call.latest_progress && !open && <p className="truncate px-2 pb-2 text-[11px] text-muted-foreground">{call.latest_progress.message}</p>}
            {open && <div className="space-y-2 border-t border-border p-2 text-xs">
              <p className="text-muted-foreground">Started {formatTime(call.started_at)}{call.finished_at ? ` · finished ${formatTime(call.finished_at)}` : ''}</p>
              {(call.progress ?? []).length > 0 && <ul className="space-y-1">{call.progress!.map((entry, index) => <li key={index} className="text-muted-foreground">{formatTime(entry.at)} — {entry.message}{typeof entry.percent === 'number' ? ` (${entry.percent}%)` : ''}</li>)}</ul>}
              {call.error && <p className="text-destructive">{call.error}</p>}
              {call.answer && <p className="whitespace-pre-wrap break-words">{call.answer}</p>}
              {!!call.files?.length && <ul className="space-y-1" aria-label="Output files">{call.files.map(file => <li key={file.name} className="flex flex-wrap items-center gap-2"><code>{file.name}</code><span className="text-muted-foreground">{file.size.toLocaleString()} bytes · {file.mime_type}</span></li>)}</ul>}
              {call.isolated_execution && <p className="text-muted-foreground">Isolated execution · <code>{call.call_id}</code></p>}
              {call.result !== undefined && call.result !== null && <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded bg-muted/40 p-2">{JSON.stringify(call.result, null, 2)}</pre>}
            </div>}
          </div>
        })}
      </section>
    </div>
  </div>
}
