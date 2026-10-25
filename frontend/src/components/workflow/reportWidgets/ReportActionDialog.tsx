import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { X } from 'lucide-react'
import api from '../../../services/api'
import { useAuthStore } from '../../../stores/useAuthStore'
import ModalPortal from '../../ui/ModalPortal'
import { actionArgs, buildActionFields, type ActionField } from './reportActionForm'
import type { ActionRequest } from './reportActionButtons'

type FunctionInfo = { name: string; description?: string; input_schema?: unknown; can_call?: boolean }
type CallState = { status?: string; call_id?: string; answer?: unknown; result?: unknown; error?: unknown; pending_inputs?: unknown[] }

const text = (value: unknown): string => typeof value === 'string' ? value : value === undefined || value === null ? '' : JSON.stringify(value, null, 2)
const terminal = (status?: string) => status === 'completed' || status === 'failed'
const waiting = (call: CallState) => Boolean(call.pending_inputs && call.pending_inputs.length)

/** Native form for one published function of the dashboard's own project. The
 * server resolves the project from the dashboard record; this component only
 * names the function. Submit runs it as the signed-in person. */
export function ReportActionDialog({ resourceId, request, theme, asUser = false, onClose }: { resourceId: string; request: ActionRequest; theme?: { vars: Record<string, string>; mode?: string }; asUser?: boolean; onClose: () => void }) {
  const user = useAuthStore(state => state.user)
  const dialog = useRef<HTMLDialogElement>(null)
  const alive = useRef(true)
  const submission = useRef(crypto.randomUUID())
  const [info, setInfo] = useState<FunctionInfo | null>(null)
  const [fields, setFields] = useState<ActionField[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [call, setCall] = useState<CallState | null>(null)
  const [running, setRunning] = useState(false)
  const base = `/api/dashboards/${encodeURIComponent(resourceId)}`

  useEffect(() => {
    alive.current = true
    dialog.current?.showModal()
    void (async () => {
      try {
        const { data } = await api.get<{ functions?: FunctionInfo[] }>(`${base}/functions`)
        if (!alive.current) return
        const found = (data.functions ?? []).find(fn => fn.name === request.name)
        if (!found) { setLoadError(`This dashboard's project has no function named ${request.name} that you can use.`); return }
        setInfo(found)
        setFields(buildActionFields(found.input_schema, request.defaults))
      } catch (reason) {
        if (alive.current) setLoadError(reason instanceof Error ? reason.message : 'Could not load the function.')
      }
    })()
    return () => { alive.current = false }
  }, [base, request])

  const setField = (name: string, value: string | boolean) => setFields(current => current.map(f => f.name === name ? { ...f, value } : f))

  const submit = async () => {
    const built = actionArgs(fields)
    if (!built.ok) { setFormError(built.error); return }
    setFormError(null); setCall(null); setRunning(true)
    try {
      const started = await api.post<CallState>(`${base}/functions/call`, { function: request.name, args: built.args, submission_id: submission.current, ...(asUser ? { as_user: true } : {}) })
      let state = started.data
      if (alive.current) setCall(state)
      while (alive.current && state.call_id && !terminal(state.status) && !waiting(state)) {
        const next = await api.get<CallState>(`${base}/function-calls/${encodeURIComponent(state.call_id)}`, { params: { wait_seconds: 20 } })
        state = next.data
        if (alive.current) setCall(state)
      }
    } catch (reason) {
      const body = (reason as { response?: { data?: { error?: { message?: string }; message?: string } } })?.response?.data
      if (alive.current) setCall({ status: 'failed', error: body?.error?.message || body?.message || (reason instanceof Error ? reason.message : 'The call could not be started.') })
    } finally {
      if (alive.current) setRunning(false)
    }
  }
  const again = () => { submission.current = crypto.randomUUID(); setCall(null); setFormError(null) }
  const runsAs = useMemo(() => user?.username || user?.email || 'you', [user])
  const finished = Boolean(call && (terminal(call.status) || waiting(call)))
  const shown = call ? text(call.answer) || text(call.result) : ''
  const statusLine = !call ? '' : terminal(call.status) ? (call.status === 'failed' ? 'Failed' : 'Done') : waiting(call) ? 'Waiting for an answer in the app' : 'Running…'

  return <ModalPortal>
    <dialog ref={dialog} aria-label={request.label} data-testid="report-action-dialog" style={theme?.vars as CSSProperties | undefined} onCancel={onClose} className={`${theme?.mode === 'light' ? 'force-light ' : theme?.mode === 'dark' ? 'dark ' : ''}m-auto max-h-[85vh] w-[calc(100%_-_2rem)] max-w-xl overflow-y-auto rounded-xl border border-border bg-background p-4 text-foreground shadow-xl backdrop:bg-black/50`}>
      <header className="mb-3 flex items-start justify-between gap-3">
        <div><h2 className="text-base font-semibold">{request.label}</h2><p className="text-xs text-muted-foreground">Runs as {runsAs}</p></div>
        <button type="button" aria-label="Close" onClick={onClose}><X className="h-4 w-4" /></button>
      </header>
      {loadError ? <p role="alert" className="text-sm text-destructive">{loadError}</p> : !info ? <p role="status" className="text-sm">Loading…</p> : <>
        {info.description && <p className="mb-3 whitespace-pre-wrap text-sm text-muted-foreground">{info.description}</p>}
        {info.can_call === false && <><p role="alert" className="text-sm text-destructive">You are not on this function's list of people who can run it. Ask the owner of this project to add you.</p><div className="mt-3 flex justify-end"><button type="button" className="rounded-md border border-border px-3 py-1 text-sm" onClick={onClose}>Close</button></div></>}
        {info.can_call !== false && !call && <form className="space-y-3" onSubmit={event => { event.preventDefault(); void submit() }}>
          {fields.map(field => <label key={field.name} className="block text-sm">
            <span className="font-medium">{field.label}{field.required && <span aria-hidden="true"> *</span>}</span>
            {field.description && <span className="block text-xs text-muted-foreground">{field.description}</span>}
            {field.kind === 'boolean'
              ? <input type="checkbox" className="mt-1" checked={field.value === true} onChange={event => setField(field.name, event.target.checked)} />
              : field.kind === 'choice'
                ? <select className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1" value={String(field.value)} onChange={event => setField(field.name, event.target.value)}>
                    {!field.required && <option value="">(not set)</option>}
                    {field.choices?.map(choice => <option key={choice} value={choice}>{choice}</option>)}
                  </select>
                : field.kind === 'json' || (field.kind === 'text' && String(field.value).length > 80)
                  ? <textarea className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1 font-mono text-xs" rows={4} value={String(field.value)} onChange={event => setField(field.name, event.target.value)} />
                  : <input className="mt-1 w-full rounded-md border border-border bg-background px-2 py-1" inputMode={field.kind === 'text' ? undefined : 'decimal'} value={String(field.value)} onChange={event => setField(field.name, event.target.value)} />}
          </label>)}
          {formError && <p role="alert" className="text-sm text-destructive">{formError}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" className="rounded-md border border-border px-3 py-1 text-sm" onClick={onClose}>Cancel</button>
            <button type="submit" disabled={running} className="rounded-md bg-primary px-3 py-1 text-sm text-primary-foreground disabled:opacity-60">Run</button>
          </div>
        </form>}
        {info.can_call !== false && call && <div className="space-y-2 text-sm" data-testid="report-action-result">
          <p role="status" className="text-xs text-muted-foreground">{statusLine}{call.call_id ? ` · call ${call.call_id}` : ''}</p>
          {text(call.error) && <p role="alert" className="whitespace-pre-wrap text-destructive">{text(call.error)}</p>}
          {shown && <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-md border border-border p-2 text-xs">{shown}</pre>}
          {finished && <div className="flex justify-end gap-2"><button type="button" className="rounded-md border border-border px-3 py-1" onClick={again}>Run again</button><button type="button" className="rounded-md border border-border px-3 py-1" onClick={onClose}>Close</button></div>}
        </div>}
      </>}
    </dialog>
  </ModalPortal>
}
