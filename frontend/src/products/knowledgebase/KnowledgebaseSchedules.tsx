import { useCallback, useEffect, useState } from 'react'
import { CalendarClock, Play } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../../components/ui/Button'
import { Switch } from '../../components/ui/Switch'
import { Textarea } from '../../components/ui/Textarea'

// Brain's own schedules (PLAT-618): Organize Brain, per person: on/off, how often, what each run does, run now.
const JOB_ID = 'product:knowledgebase:organize'
const CADENCES = [
  { hours: 24, label: 'Every day' },
  { hours: 72, label: 'Every 3 days' },
  { hours: 168, label: 'Weekly' },
  { hours: 336, label: 'Every 2 weeks' },
  { hours: 720, label: 'Every 30 days' },
]

interface BrainScheduleJob {
  id: string
  name: string
  description?: string
  enabled: boolean
  cadence_hours?: number
  messages?: string[]
  last_run_at?: string
  next_run_at?: string
  last_status?: string
  last_error?: string
  deferred_reason?: string
}

function when(value?: string) { return value ? new Date(value).toLocaleString() : '—' }

export function KnowledgebaseSchedules() {
  const [job, setJob] = useState<BrainScheduleJob | null>(null)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const path = `/api/scheduler/jobs/${encodeURIComponent(JOB_ID)}`
  const show = useCallback((next: BrainScheduleJob) => { setJob(next); setMessage(next.messages?.[0] ?? '') }, [])
  useEffect(() => {
    const controller = new AbortController()
    api.get<BrainScheduleJob>(path, { signal: controller.signal }).then(response => show(response.data))
      .catch(failure => { if (!controller.signal.aborted) setError(failure?.response?.data || 'Could not load Brain schedules.') })
    return () => controller.abort()
  }, [path, show])
  async function act(action: string, body: Record<string, unknown> = {}, done = '') {
    setBusy(true); setError(''); setNotice('')
    try {
      const response = await api.post(`${path}/${action}`, body)
      if (action === 'trigger') setNotice(done)
      else { show(response.data as BrainScheduleJob); if (done) setNotice(done) }
    } catch (failure) {
      const data = (failure as { response?: { data?: unknown } }).response?.data
      setError(typeof data === 'string' ? data : 'The change was not saved.')
    } finally { setBusy(false) }
  }
  if (!job) return <div className="p-4 text-sm text-muted-foreground">{error ? <p role="alert" className="text-destructive">{String(error)}</p> : 'Loading schedules…'}</div>
  const cadence = job.cadence_hours ?? 168
  return <div className="space-y-4 p-4 text-sm" data-testid="knowledgebase-schedules">
    <section className="space-y-3 rounded-lg border border-border p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="flex items-center gap-2 font-semibold"><CalendarClock className="h-4 w-4 text-primary" />{job.name}</h3>
          <p className="mt-1 text-xs text-muted-foreground">Runs in your Brain chat, as you: only folders you can edit change.</p>
        </div>
        <Switch aria-label="Organize Brain on schedule" checked={job.enabled} disabled={busy} onCheckedChange={on => void act(on ? 'enable' : 'disable')} />
      </div>
      <label className="flex items-center justify-between gap-3">
        <span className="text-muted-foreground">How often</span>
        <select aria-label="How often" value={cadence} disabled={busy} className="rounded-md border border-border bg-background px-2 py-1"
          onChange={event => void act('cadence', { cadence_hours: Number(event.target.value) })}>
          {!CADENCES.some(option => option.hours === cadence) && <option value={cadence}>Every {cadence}h</option>}
          {CADENCES.map(option => <option key={option.hours} value={option.hours}>{option.label}</option>)}
        </select>
      </label>
      <div className="space-y-1.5">
        <span className="text-muted-foreground">What each run does</span>
        <Textarea aria-label="What each run does" value={message} disabled={busy} rows={4} onChange={event => setMessage(event.target.value)}
          placeholder="For example: organize Engineering by teams and merge duplicates; only propose changes." />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => void act('message', { message: '' }, 'Back to the default.')}>Use default</Button>
          <Button size="sm" disabled={busy || message === (job.messages?.[0] ?? '')} onClick={() => void act('message', { message }, 'Saved.')}>Save</Button>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-muted-foreground">
        <span>Last run</span><span>{when(job.last_run_at)}{job.last_status ? ` · ${job.last_status}` : ''}</span>
        <span>Next run</span><span>{job.enabled ? when(job.next_run_at) : 'Off'}{job.deferred_reason ? ` · ${job.deferred_reason}` : ''}</span>
      </div>
      {job.last_error && <p className="text-xs text-destructive">{job.last_error}</p>}
      <div className="flex items-center justify-between gap-2">
        <span role="status" className="text-xs text-muted-foreground">{notice}</span>
        <Button variant="outline" size="sm" disabled={busy} onClick={() => void act('trigger', {}, 'Started: follow it in your Brain chat.')}><Play className="h-3.5 w-3.5" />Run now</Button>
      </div>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </section>
  </div>
}
