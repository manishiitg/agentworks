import { useCallback, useEffect, useRef, useState } from 'react'
import { agentApi } from '../../services/api'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'

interface Receipt { name: string; provider?: string; model?: unknown; output?: unknown; tools?: {name: string; args?: unknown; result?: unknown; error?: string}[] }
interface Step { id: string; name: string; dbos_step_id?: number; status: string; checkpoint_reused?: boolean; started_at?: number; completed_at?: number; output?: unknown; error?: string; provider?: string; model?: unknown; tools?: Receipt['tools']; agent_calls?: Receipt[] }
interface Trace { version: number; durability?: string; execution_model?: string; workflow_id?: string; attempt_number?: number; status: string; error?: string; calls: Step[] }
interface Event { timestamp: number; level: string; message: string; workflow_id: string; step_id?: number; attempt_number: number }
const value = (data: unknown) => <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-muted/40 p-3 text-xs">{JSON.stringify(data, null, 2)}</pre>
const uncertainCall = 'Uncertain platform call requires reconciliation; automatic retry is disabled'
function ExecutionError({ message }: { message: string }) {
  const reconciliation = message.includes(uncertainCall)
  const summary = reconciliation ? uncertainCall : message.split('\n')[0]
  return <div className="mt-2 text-destructive">
    <p role="alert" className="text-sm">{summary}</p>
    {reconciliation && <p className="mt-1 text-xs">The agent may have performed tool actions before the crash. Check those actions before retrying this run.</p>}
    {message !== summary && <details className="mt-2"><summary className="cursor-pointer text-xs">Full error details</summary><pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words text-xs">{message}</pre></details>}
  </div>
}

/** DBOS is the source of step state; platform receipts provide agent/tool detail. */
export default function DBOSExecutionTimeline({ workspacePath, runFolder, onAvailable }: {
  workspacePath: string; runFolder: string | null; onAvailable?: (available: boolean) => void
}) {
  const [trace, setTrace] = useState<Trace | null>(null)
  const [events, setEvents] = useState<Event[]>([])
  const [error, setError] = useState('')
  const active = useRef(true)
  useEffect(() => { active.current = true; return () => { active.current = false } }, [])
  const refresh = useCallback(async () => {
    if (!runFolder) return
    const root = `${workspacePath}/runs/${runFolder}`
    try {
      const response = await agentApi.getPlannerFileContent(`${root}/relay_trace.json`)
      if (!active.current) return
      if (!response.success || !response.data) throw new Error(response.error || 'No execution history yet')
      const parsed: Trace = typeof response.data.content === 'string' ? JSON.parse(response.data.content) : response.data.content
      const available = parsed.durability === 'dbos' && parsed.version === 1 && Array.isArray(parsed.calls)
      onAvailable?.(available)
      setTrace(available ? parsed : null); setError('')
      if (parsed.execution_model === 'native-dbos') {
        try {
          const log = await agentApi.getPlannerFileContent(`${root}/dbos_events.jsonl`)
          if (!active.current) return
          const lines = String(log.data?.content || '').trim().split('\n').filter(Boolean).slice(-200)
          setEvents(lines.flatMap(line => { try { return [JSON.parse(line) as Event] } catch { return [] } }))
        } catch { setEvents([]) }
      }
    } catch (cause) {
      if (!active.current) return
      const missing = (cause as { response?: { status?: number } }).response?.status === 404
      if (missing) onAvailable?.(false)
      setError(missing ? '' : cause instanceof Error ? cause.message : 'Could not load DBOS history')
    }
  }, [workspacePath, runFolder, onAvailable])
  useEffect(() => { setTrace(null); setEvents([]); setError(''); void refresh() }, [refresh])
  useLiveRefetch(refresh, { kinds: ['plan', 'sessions', 'schedules'], workflow: workspacePath, fallbackMs: 3000, safetyMs: 2000, minIntervalMs: 500 })
  if (!runFolder) return <p className="p-5 text-sm text-muted-foreground">No runs yet.</p>
  if (error) return <p role="alert" className="p-5 text-sm text-destructive">{error}</p>
  if (!trace) return <p className="p-5 text-sm text-muted-foreground">Loading execution history…</p>
  return <section aria-label="DBOS execution history" className="space-y-4 p-4">
    <header className="space-y-1"><h2 className="text-sm font-semibold">{trace.execution_model === 'native-dbos' ? 'DBOS workflow' : 'DBOS recovery'}</h2>
      <p className="text-xs text-muted-foreground">{trace.error?.includes(uncertainCall) ? 'Needs reconciliation' : trace.status} · Attempt {trace.attempt_number || 1} · {trace.calls.filter(step => step.checkpoint_reused).length} checkpoints reused</p>
      {trace.workflow_id && <p className="break-all font-mono text-xs">Workflow ID: {trace.workflow_id}</p>}
      {trace.execution_model === 'native-dbos' && <p className="text-xs text-muted-foreground">Step status and timing come from DBOS history. Agent and tool activity is linked by step ID.</p>}
    </header>
    {trace.error && <ExecutionError message={trace.error} />}
    <ol className="space-y-3">{trace.calls.map(step => <li key={step.id} className="rounded-lg border border-border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2"><strong className="text-sm">{step.name}</strong><span className="text-xs">{step.checkpoint_reused ? 'Reused checkpoint' : step.status}</span></div>
      <p className="mt-1 text-xs text-muted-foreground">{step.dbos_step_id !== undefined ? `DBOS step ${step.dbos_step_id}` : step.id}{typeof step.started_at === 'number' && typeof step.completed_at === 'number' ? ` · ${Math.max(0, step.completed_at - step.started_at).toFixed(2)}s` : ''}{step.provider ? ` · ${step.provider}` : ''}</p>
      {step.error && <ExecutionError message={step.error} />}
      {step.output !== undefined && <details className="mt-2"><summary className="cursor-pointer text-xs">Step output</summary>{value(step.output)}</details>}
      {step.agent_calls?.map((receipt, index) => <details key={index} className="mt-2"><summary className="cursor-pointer text-xs">Agent: {receipt.name}</summary>{value({ provider: receipt.provider, model: receipt.model, output: receipt.output })}</details>)}
      {step.tools?.map((tool, index) => <details key={index} className="mt-2"><summary className="cursor-pointer text-xs">Tool: {tool.name}</summary>{value(tool)}</details>)}
    </li>)}</ol>
    <details><summary className="cursor-pointer text-sm font-medium">DBOS logs · {events.length} recent events</summary>
      {events.length ? <ol className="mt-3 space-y-2 font-mono text-xs">{events.slice().reverse().map((event, index) => <li key={index} className="break-words"><span className="text-muted-foreground">{new Date(event.timestamp * 1000).toLocaleTimeString()} · {event.level} · Attempt {event.attempt_number}{event.step_id != null ? ` · Step ${event.step_id}` : ''}</span>{event.level === 'ERROR' ? <ExecutionError message={event.message} /> : <p className="whitespace-pre-wrap">{event.message}</p>}</li>)}</ol> : <p className="mt-2 text-xs text-muted-foreground">No DBOS log events recorded.</p>}
    </details>
  </section>
}
