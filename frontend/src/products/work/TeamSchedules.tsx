import { useCallback, useEffect, useState } from 'react'
import { CalendarClock } from 'lucide-react'
import { shortAgo } from '../../components/chat/AgentIcon'
import { schedulerApi } from '../../api/scheduler'
import type { ScheduledJob } from '../../services/api-types'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

/** Schedules the Team shares (#3282, owner 19 Nov): optional both ways. Sharing puts one repeating message schedule in
 * the Team's shared folder as <folder>/schedules/<id>.json: only its name, when it runs, its timezone and its message
 * (never an account, a secret or a workflow). Add creates it in the person's own Crew, switched off until they confirm
 * the time; the server checks it like any schedule they create. Later edits do not change copies already added. */

type SharedFile = { path: string; hash: string; author?: string; mine?: boolean; modified_at?: string }
type ScheduleFile = { kind: 'schedule'; name: string; description?: string; cron_expression: string; timezone: string; message: string; when?: string }
type SharedSchedule = SharedFile & { file: ScheduleFile }
const route = (path: string, hash = '') => `/file?path=${encodeURIComponent(path)}&base=${encodeURIComponent(hash)}&transfer=${encodeURIComponent(crypto.randomUUID())}`

/** A Crew message schedule that repeats: the only kind that is shared (no workflow, one message, no calendar dates). */
const shareable = (job: ScheduledJob, project: string) => job.entity_type === 'product' && job.id.startsWith(`product-project:work:${project}:`) && !job.target_workflow_id
  && job.kind !== 'condition' && job.schedule_type === 'cron' && !!job.cron_expression && job.messages?.length === 1 && !!job.messages[0]?.trim()

const DAYS: { value: string; label: string }[] = [{ value: '*', label: 'Every day' }, { value: '1-5', label: 'Weekdays' }, ...['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'].map((label, value) => ({ value: String(value), label: `Every ${label}` }))]
/** The plain "days and time" form of a cron line, when it is one the form can show; anything else keeps its saved timing. */
function simpleWhen(cron: string): { days: string; time: string } | null {
  const match = /^(\d{1,2}) (\d{1,2}) \* \* (\*|\d|1-5)$/.exec(cron.trim())
  if (!match || !DAYS.some(day => day.value === match[3])) return null
  return { days: match[3], time: `${match[2].padStart(2, '0')}:${match[1].padStart(2, '0')}` }
}
const cronOf = (days: string, time: string) => { const [h, m] = time.split(':'); return `${Number(m)} ${Number(h)} * * ${days}` }

/** `folder` is the Team's first shared folder; no shared folder means nothing to show here. */
export function TeamSchedules({ target, folder }: { target: TeamTarget; folder?: string }) {
  const [shared, setShared] = useState<SharedSchedule[]>([])
  const [mine, setMine] = useState<ScheduledJob[]>([])
  const [pick, setPick] = useState('')
  const [adding, setAdding] = useState<SharedSchedule | null>(null)
  const [days, setDays] = useState('*')
  const [time, setTime] = useState('09:00')
  const [zone, setZone] = useState('')
  const [message, setMessage] = useState('')
  const [added, setAdded] = useState<{ id: string; name: string; on: boolean } | null>(null)
  const [busy, setBusy] = useState(false)
  const [line, setLine] = useState('')
  const dir = folder === undefined ? '' : `${folder === '.' ? '' : `${folder}/`}schedules/`
  const myZone = Intl.DateTimeFormat().resolvedOptions().timeZone
  const load = useCallback(async () => {
    if (folder === undefined) return
    try {
      const data = await teamRequest<{ files?: SharedFile[] }>(target, '/files')
      const files = (data.files ?? []).filter(file => file.path.startsWith(dir) && file.path.endsWith('.json')).slice(0, 40)
      const read = await Promise.all(files.map(async file => {
        try {
          const parsed = JSON.parse(await teamRequest<string>(target, `/file?path=${encodeURIComponent(file.path)}`, 'GET', undefined, true)) as Partial<ScheduleFile>
          return parsed.kind === 'schedule' && parsed.name?.trim() && parsed.cron_expression && parsed.message?.trim() ? { ...file, file: parsed as ScheduleFile } : null
        } catch { return null }
      }))
      setShared(read.filter((item): item is SharedSchedule => !!item))
    } catch { setShared([]) }
    try { setMine((await schedulerApi.listJobs({ entity_type: 'product', limit: 200 })).jobs.filter(job => shareable(job, target.project))) } catch { setMine([]) }
  }, [target.project, target.connection, target.folderId, dir]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    void load()
    window.addEventListener('agentworks:team-updated', load)
    return () => window.removeEventListener('agentworks:team-updated', load)
  }, [load])
  if (folder === undefined) return null
  const run = async (work: () => Promise<string>) => {
    setBusy(true)
    try { setLine(await work()) } catch (e) { setLine(teamFailure(e).message) } finally { setBusy(false); void load() }
  }
  const choice = mine.find(job => job.id === pick) ?? mine[0]
  const share = () => run(async () => {
    if (!choice) return ''
    const file: ScheduleFile = { kind: 'schedule', name: choice.name, ...(choice.description ? { description: choice.description } : {}), cron_expression: choice.cron_expression, timezone: choice.timezone, message: choice.messages![0], ...(choice.timing_summary ? { when: choice.timing_summary } : {}) }
    // Sharing the same schedule again updates your own file; someone else's schedule with the same name stays theirs.
    const own = shared.find(item => item.mine && item.file.name.toLowerCase() === choice.name.toLowerCase())
    await teamRequest(target, route(own?.path ?? `${dir}${crypto.randomUUID()}.json`, own?.hash), 'PUT', JSON.stringify(file, null, 2), true)
    return `Shared “${choice.name}” with the Team. Teammates can add it if they want.`
  })
  const open = (item: SharedSchedule) => {
    const simple = simpleWhen(item.file.cron_expression)
    setAdding(item); setAdded(null); setLine('')
    setDays(simple?.days ?? '*'); setTime(simple?.time ?? '09:00'); setZone(myZone || item.file.timezone); setMessage(item.file.message)
  }
  const add = () => adding && run(async () => {
    const simple = simpleWhen(adding.file.cron_expression)
    try {
      const job = await schedulerApi.createJob({
        name: adding.file.name, ...(adding.file.description ? { description: adding.file.description } : {}), entity_type: 'product', product_profile_id: 'work', product_project_id: target.project,
        messages: [message.trim()], cron_expression: simple ? cronOf(days, time) : adding.file.cron_expression, timezone: zone || adding.file.timezone, enabled: false,
      })
      setAdded({ id: job.id, name: adding.file.name, on: false }); setAdding(null)
      return ''
    } catch (e) {
      // The server says why in plain words.
      const why = (e as { response?: { data?: unknown } })?.response?.data
      return typeof why === 'string' && why.trim() ? why.trim() : 'Could not add that schedule. Try again.'
    }
  })
  const turnOn = () => added && run(async () => { await schedulerApi.enableJob(added.id); setAdded({ ...added, on: true }); return '' })
  const stop = (item: SharedSchedule) => run(async () => {
    await teamRequest(target, route(item.path, item.hash), 'DELETE')
    return `Stopped sharing “${item.file.name}”. Copies teammates already added stay with them.`
  })
  const zones = [...new Set([myZone, adding?.file.timezone].filter((zoneName): zoneName is string => !!zoneName))]
  return <section aria-label="Shared schedules" className="space-y-1.5">
    <h4 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Schedules <span className="font-normal normal-case tracking-normal">· share a routine; teammates add it only if they want it</span></h4>
    {shared.length > 0 && <ul className="space-y-1.5">{shared.map(item => {
      const simple = simpleWhen(item.file.cron_expression)
      return <li key={item.path} className="rounded-xl border border-border bg-muted/20 px-3 py-2">
        <div className="flex items-center gap-3">
          <CalendarClock className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="min-w-0 flex-1"><span className="block truncate font-medium">{item.file.name}</span><span className="block text-[11px] text-muted-foreground">{[item.file.when || item.file.cron_expression, item.mine ? 'Shared by you' : item.author && `Shared by ${item.author}`, shortAgo(item.modified_at)].filter(Boolean).join(' · ')}</span></span>
          {!item.mine && <button type="button" disabled={busy} onClick={() => open(item)} className="rounded-lg border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50" aria-label={`Add ${item.file.name} to my schedules`}>Add to my schedules</button>}
          {item.mine && <button type="button" disabled={busy} onClick={() => void stop(item)} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50" aria-label={`Stop sharing ${item.file.name}`}>Stop sharing</button>}
        </div>
        {adding?.path === item.path && <div className="mt-2 space-y-2 border-t border-border pt-2 text-xs">
          <p className="text-muted-foreground">Created in your Crew, switched off, until you confirm the time.</p>
          {simple ? <div className="grid grid-cols-2 gap-2">
            <label className="grid gap-0.5 text-muted-foreground">Days<select aria-label="Days" value={days} onChange={event => setDays(event.target.value)} className="rounded border border-input bg-background px-2 py-1 text-foreground">{DAYS.map(day => <option key={day.value} value={day.value}>{day.label}</option>)}</select></label>
            <label className="grid gap-0.5 text-muted-foreground">Time<input aria-label="Time" type="time" value={time} onChange={event => setTime(event.target.value)} className="rounded border border-input bg-background px-2 py-1 text-foreground" /></label>
          </div> : <p className="text-muted-foreground">Runs {item.file.when || item.file.cron_expression}. You can change the time in Automations after adding it.</p>}
          <label className="grid gap-0.5 text-muted-foreground">Timezone<select aria-label="Timezone" value={zone} onChange={event => setZone(event.target.value)} className="rounded border border-input bg-background px-2 py-1 text-foreground">{zones.map(zoneName => <option key={zoneName} value={zoneName}>{zoneName === myZone ? `Your timezone (${zoneName})` : zoneName}</option>)}</select></label>
          <label className="grid gap-0.5 text-muted-foreground">Message sent to your agent<textarea aria-label="Message" value={message} onChange={event => setMessage(event.target.value)} className="min-h-14 w-full resize-y rounded border border-input bg-background px-2 py-1 text-foreground" /></label>
          <div className="flex justify-end gap-2">
            <button type="button" disabled={busy} onClick={() => setAdding(null)} className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted disabled:opacity-50">Cancel</button>
            <button type="button" disabled={busy || !message.trim() || (!!simple && !time)} onClick={() => void add()} className="rounded-lg bg-primary px-2.5 py-1 font-semibold text-primary-foreground hover:opacity-90 disabled:opacity-50">Add, switched off</button>
          </div>
        </div>}
      </li>
    })}</ul>}
    {!shared.length && <p className="text-xs text-muted-foreground">No shared schedules yet. Share one of yours and teammates can add it if they want.</p>}
    {mine.length > 0 && <div className="flex flex-wrap items-center gap-2 text-xs">
      <label className="flex items-center gap-2"><span className="text-muted-foreground">Share one of your schedules</span>
        <select aria-label="Schedule to share" value={choice?.id ?? ''} disabled={busy} onChange={event => setPick(event.target.value)} className="max-w-48 rounded border border-input bg-background px-2 py-1">{mine.map(job => <option key={job.id} value={job.id}>{job.name}</option>)}</select>
      </label>
      <button type="button" disabled={busy || !choice} onClick={() => void share()} className="rounded-lg border border-border px-2.5 py-1 hover:bg-muted disabled:opacity-50">Share with the Team</button>
    </div>}
    {added && <p role="status" className="flex flex-wrap items-center gap-2 text-xs text-emerald-600">
      <span>{added.on ? `“${added.name}” is on.` : `Added “${added.name}” to your schedules, switched off.`}</span>
      {!added.on && <button type="button" disabled={busy} onClick={() => void turnOn()} className="rounded-lg border border-border px-2.5 py-1 text-foreground hover:bg-muted disabled:opacity-50">Turn it on</button>}
    </p>}
    {line && <p role="status" className="text-xs text-muted-foreground">{line}</p>}
  </section>
}
