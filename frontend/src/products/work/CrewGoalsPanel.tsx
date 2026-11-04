import { useCallback, useEffect, useState } from 'react'
import { Plus, Target } from 'lucide-react'
import { isAxiosError } from 'axios'
import api from '../../services/api'
import type { GoalMetric, GoalMeasurements } from '../../services/api-types'
import { GoalProgress } from '../../components/workflow/GoalProgress'

type Goal = { id: string; name: string; outcome: string; status: 'active' | 'achieved' | 'stopped'; workflow_ids: string[]; metric_ids: string[] }
type GoalData = { goals: Goal[]; measurements: GoalMeasurements; progress: Record<string, { state: string; targets_met: boolean; current?: number; unit?: string }>; can_edit: boolean; workspace_path: string }
type Workflow = { id: string; manifest: { label: string } }
function message(error: unknown) {
  return isAxiosError(error) && typeof error.response?.data === 'string' ? error.response.data : error instanceof Error ? error.message : 'Could not save. Try again.'
}

export function CrewGoalsPanel({ projectId, onAsk }: { projectId: string; onAsk: (message: string) => void }) {
  const [data, setData] = useState<GoalData | null>(null)
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [editing, setEditing] = useState<Goal | null>(null)
  const [recording, setRecording] = useState<Goal | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const refresh = useCallback(async (signal?: AbortSignal) => {
    const response = await api.get<GoalData>('/api/crew-goals', { params: { crew_id: projectId }, signal })
    setData(response.data)
    if (response.data.can_edit) {
      const children = await api.get<{ executables: Workflow[] }>('/api/crew-executables', { params: { crew_id: projectId }, signal })
      setWorkflows(children.data.executables)
    }
  }, [projectId])
  useEffect(() => {
    const controller = new AbortController()
    setData(null); setError(''); setEditing(null); setRecording(null); setWorkflows([])
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(message(e)) })
    return () => controller.abort()
  }, [refresh])
  async function save(body: unknown) {
    setBusy(true); setError('')
    try {
      const response = await api.post<GoalData>('/api/crew-goals', body, { params: { crew_id: projectId } })
      setData(response.data); setEditing(null); setRecording(null)
    } catch (e) { setError(message(e)) }
    finally { setBusy(false) }
  }
  return <div className="mx-auto w-full max-w-3xl space-y-6 p-6">
    <div className="flex items-start justify-between gap-4"><div><h2 className="text-lg font-semibold">Goals</h2><p className="mt-1 text-sm text-muted-foreground">What this Crew is working toward. Track results from your work and workflows.</p></div>{data?.can_edit && <button onClick={() => { setRecording(null); setEditing({ id: crypto.randomUUID(), name: '', outcome: '', status: 'active', workflow_ids: [], metric_ids: [] }) }} className="inline-flex shrink-0 items-center gap-2 rounded-md border px-3 py-2 text-sm"><Plus className="h-4 w-4" />Add goal</button>}</div>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {!data && !error && <p className="text-sm text-muted-foreground">Loading goals…</p>}
    {data && editing && <GoalEditor key={editing.id} goal={editing} metrics={data.measurements.metrics.filter(m => editing.metric_ids.includes(m.id))} workflows={workflows} busy={busy} onCancel={() => setEditing(null)} onSave={(goal, metrics) => void save({ action: 'save', goal, metrics })} />}
    {data && recording && <ObservationEditor key={recording.id} goal={recording} metrics={data.measurements.metrics.filter(m => recording.metric_ids.includes(m.id))} busy={busy} onCancel={() => setRecording(null)} onSave={observations => void save({ action: 'record', observations })} />}
    {data && data.goals.length === 0 && !editing && <div className="rounded-lg border border-dashed p-6 text-sm text-muted-foreground">No goals yet. Add an outcome such as “Reach 20 qualified leads” or “Launch the new website”. A workflow is optional.</div>}
    {data?.goals.map(goal => {
      const metrics = data.measurements.metrics.filter(m => goal.metric_ids.includes(m.id))
      const linked = workflows.filter(w => goal.workflow_ids.includes(w.id))
      return <article key={goal.id} className="space-y-3 rounded-xl border p-5">
        <div className="flex items-start justify-between gap-3"><div className="flex items-center gap-2"><Target className="h-4 w-4 text-muted-foreground" /><h3 className="font-medium">{goal.name}</h3></div><span className="rounded-full bg-muted px-2 py-1 text-xs capitalize">{goal.status}</span></div>
        {goal.outcome && <p className="text-sm text-muted-foreground">{goal.outcome}</p>}
        {data.progress[goal.id]?.current !== undefined && <p className="text-2xl font-semibold tabular-nums">{data.progress[goal.id].current?.toLocaleString()} <span className="text-sm font-normal text-muted-foreground">{data.progress[goal.id].unit}</span></p>}
        <p className="text-sm">{data.progress[goal.id]?.state}</p>
        {metrics.filter(m => m.role === 'primary' && m.target !== undefined).map(m => <p key={m.id} className="text-sm text-muted-foreground">{m.name}: target {m.direction === 'decrease' ? '≤ ' : m.direction === 'increase' ? '≥ ' : ''}{m.target} {m.unit}{m.target_date ? ` by ${m.target_date}` : ''}</p>)}
        {linked.length > 0 && <p className="text-xs text-muted-foreground">Workflows: {linked.map(w => w.manifest.label).join(', ')}</p>}
        <div className="flex flex-wrap gap-4 text-sm">{data.can_edit && <><button className="text-primary" onClick={() => { setRecording(null); setEditing(goal) }}>Edit goal</button>{metrics.length > 0 && <button className="text-primary" onClick={() => { setEditing(null); setRecording(goal) }}>Record update</button>}</>}<button className="text-primary" onClick={() => onAsk(`Review the goal “${goal.name}” in this Crew. Use crew_goals to check its actual measurements and evidence. Explain progress and what to do next.`)}>Discuss in chat</button></div>
        {metrics.length > 0 && <details><summary className="cursor-pointer text-sm text-muted-foreground">Measurements and evidence</summary><div className="mt-4"><GoalProgress impact={{ metrics, observations: data.measurements.observations }} workspacePath={data.workspace_path} onAskBuilder={onAsk} /></div></details>}
      </article>
    })}
  </div>
}

function GoalEditor({ goal, metrics, workflows, busy, onCancel, onSave }: { goal: Goal; metrics: GoalMetric[]; workflows: Workflow[]; busy: boolean; onCancel: () => void; onSave: (goal: Goal, metrics: GoalMetric[]) => void }) {
  const [draft, setDraft] = useState(goal)
  const existing = metrics.find(m => m.role === 'primary')
  const [numeric, setNumeric] = useState(!!existing)
  const [target, setTarget] = useState(existing?.target?.toString() ?? '')
  const [targetDate, setTargetDate] = useState(existing?.target_date ?? '')
  const [unit, setUnit] = useState(existing?.unit ?? '')
  const [source, setSource] = useState(existing?.source ?? '')
  const [direction, setDirection] = useState<GoalMetric['direction']>(existing?.direction ?? 'increase')
  const [freshness, setFreshness] = useState(existing?.freshness_hours ?? 48)
  const input = 'mt-1 w-full rounded-md border bg-background p-2'
  function submit() {
    const next = [...metrics]
    if (numeric) {
      const measurement: GoalMetric = existing ? { ...existing, target: Number(target), target_date: targetDate || undefined, freshness_hours: freshness } : { id: `${goal.id}-progress`, criterion_id: goal.id, name: draft.name, role: 'primary', unit, direction, definition: `Observed ${unit}`, source, window: 'instant', route: '', environment: '', collection_frequency: 'On update', freshness_hours: freshness, target: Number(target), target_date: targetDate || undefined }
      if (existing) next[next.findIndex(m => m.id === existing.id)] = measurement
      else next.push(measurement)
    }
    onSave(draft, next)
  }
  return <form onSubmit={e => { e.preventDefault(); submit() }} className="space-y-4 rounded-xl border p-5">
    <label className="block text-sm">Goal name<input autoFocus required maxLength={160} className={input} value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
    <label className="block text-sm">What would success look like?<textarea maxLength={4000} className={input} value={draft.outcome} onChange={e => setDraft({ ...draft, outcome: e.target.value })} /></label>
    <label className="block text-sm">Status<select className={input} value={draft.status} onChange={e => setDraft({ ...draft, status: e.target.value as Goal['status'] })}><option value="active">Active</option><option value="achieved">Achieved</option><option value="stopped">Stopped</option></select></label>
    <label className="flex items-center gap-2 text-sm"><input type="checkbox" disabled={!!existing} checked={numeric} onChange={e => setNumeric(e.target.checked)} />Track a number</label>
    {numeric && <div className="space-y-3 rounded-lg bg-muted/30 p-4"><div className="grid grid-cols-2 gap-3"><label className="text-sm">Target<input type="number" step="any" required value={target} onChange={e => setTarget(e.target.value)} className={input} /></label><label className="text-sm">Unit<input required readOnly={!!existing} placeholder="qualified leads" value={unit} onChange={e => setUnit(e.target.value)} className={input} /></label></div><label className="block text-sm">Direction<select disabled={!!existing} value={direction} onChange={e => setDirection(e.target.value as GoalMetric['direction'])} className={input}><option value="increase">At least</option><option value="decrease">At most</option><option value="maintain">Exactly</option></select></label><label className="block text-sm">Target date (optional)<input type="date" value={targetDate} onChange={e => setTargetDate(e.target.value)} className={input} /></label><label className="block text-sm">Where will the number come from?<input required readOnly={!!existing} placeholder="CRM qualified leads report" value={source} onChange={e => setSource(e.target.value)} className={input} /></label><label className="block text-sm">Ask for a fresh reading after (hours)<input type="number" min="1" required value={freshness} onChange={e => setFreshness(Number(e.target.value))} className={input} /></label><p className="text-xs text-muted-foreground">Only recorded evidence updates progress. Finishing a workflow does not mark the goal achieved.</p></div>}
    {workflows.length > 0 && <fieldset className="space-y-2"><legend className="mb-2 text-sm">Related workflows or scripts (optional)</legend>{workflows.map(w => <label key={w.id} className="flex items-center gap-2 text-sm"><input type="checkbox" checked={draft.workflow_ids.includes(w.id)} onChange={e => setDraft({ ...draft, workflow_ids: e.target.checked ? [...draft.workflow_ids, w.id] : draft.workflow_ids.filter(id => id !== w.id) })} />{w.manifest.label}</label>)}</fieldset>}
    <div className="flex gap-4"><button disabled={busy} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">{busy ? 'Saving…' : 'Save goal'}</button><button type="button" onClick={onCancel} className="text-sm">Cancel</button></div>
  </form>
}

function ObservationEditor({ goal, metrics, busy, onCancel, onSave }: { goal: Goal; metrics: GoalMetric[]; busy: boolean; onCancel: () => void; onSave: (observations: unknown[]) => void }) {
  const [metricId, setMetricId] = useState(metrics[0].id)
  const [value, setValue] = useState('')
  const [evidence, setEvidence] = useState('')
  const [observed, setObserved] = useState(new Date(Date.now() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 16))
  const [windowStart, setWindowStart] = useState('')
  const [windowEnd, setWindowEnd] = useState('')
  const [collectionId] = useState(() => `manual-${crypto.randomUUID()}`)
  const metric = metrics.find(m => m.id === metricId)!
  const period = !['instant', 'snapshot', 'point_in_time'].includes(metric.window)
  const input = 'mt-1 w-full rounded-md border bg-background p-2'
  return <form className="space-y-4 rounded-xl border p-5" onSubmit={e => { e.preventDefault(); onSave([{ metric: metric.id, criterion_id: metric.criterion_id, unit: metric.unit, route: metric.route, environment: metric.environment, run_id: collectionId, value: Number(value), observed_at: new Date(observed).toISOString(), evidence: [evidence], ...(period ? { window_start: new Date(windowStart).toISOString(), window_end: new Date(windowEnd).toISOString() } : {}) }]) }}>
    <h3 className="font-medium">Record an update · {goal.name}</h3>
    <label className="block text-sm">Measurement<select value={metricId} onChange={e => setMetricId(e.target.value)} className={input}>{metrics.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}</select></label>
    <label className="block text-sm">Value ({metric.unit})<input type="number" step="any" required value={value} onChange={e => setValue(e.target.value)} className={input} /></label>
    <label className="block text-sm">When was this measured?<input type="datetime-local" required value={observed} onChange={e => setObserved(e.target.value)} className={input} /></label>
    {period && <div className="grid grid-cols-2 gap-3"><label className="text-sm">Period start<input type="datetime-local" required value={windowStart} onChange={e => setWindowStart(e.target.value)} className={input} /></label><label className="text-sm">Period end<input type="datetime-local" required value={windowEnd} onChange={e => setWindowEnd(e.target.value)} className={input} /></label></div>}
    <label className="block text-sm">Source or evidence<textarea required placeholder="Report link, file or what you verified" value={evidence} onChange={e => setEvidence(e.target.value)} className={input} /></label>
    <div className="flex gap-4"><button disabled={busy} className="rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground">{busy ? 'Saving…' : 'Record update'}</button><button type="button" onClick={onCancel} className="text-sm">Cancel</button></div>
  </form>
}
