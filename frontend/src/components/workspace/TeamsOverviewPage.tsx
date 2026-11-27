import { useState } from 'react'
import { Users } from 'lucide-react'
import { WorkspacePageHeader } from './WorkspacePageHeader'
import { Avatar } from '../ui/Avatar'
import { timeAgo } from '../scheduler/scheduleRuns/helpers'
import { openCrewTeam, useTeamCrews, type AdminPerson, type MonitorSource } from './TeamMonitor'
import type { TeamLiveView } from './TeamLiveAgents'
import { TeamOverviewDetails, type TeamOverviewPerson } from '../../products/work/TeamOverview'

// The Teams overview (owner, 25 Nov, mockup "Teams overview page"): every Team and person on every connected server
// on one page, opened from the Team menu's "Open overview". The server words every line, count and status.
type Card = { key: string; source: MonitorSource; crewId: string; live: TeamLiveView; networkId: string }

const capital = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)
const short = (at?: string) => at ? timeAgo(at).replace(/ ago$/, '') : ''
const section = 'mb-2 mt-6 text-[10px] font-medium uppercase tracking-wide text-muted-foreground'
const grid = 'grid items-start gap-3 [grid-template-columns:repeat(auto-fill,minmax(20rem,1fr))]'
const card = 'min-w-0 rounded-xl border border-border bg-card p-3'
const sub = 'truncate text-[11px] text-muted-foreground'

function Segmented<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: [T, string][]; onChange: (v: T) => void }) {
  return <span role="radiogroup" aria-label={label} className="inline-flex rounded-md bg-muted p-0.5 text-xs">
    {options.map(([v, text]) => <button key={v} type="button" role="radio" aria-checked={value === v} onClick={() => onChange(v)} className={`rounded px-2.5 py-1 ${value === v ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground'}`}>{text}</button>)}
  </span>
}

function Status({ working, quiet }: { working?: string; quiet?: string }) {
  if (working) return <span className="shrink-0 rounded-full bg-emerald-500/10 px-2 py-px text-[11px] font-medium text-emerald-700 dark:text-emerald-400">{working}</span>
  return quiet ? <span className="shrink-0 text-[11px] text-muted-foreground">{quiet}</span> : null
}

function Face({ name, working }: { name: string; working?: boolean }) {
  return <span className="relative inline-flex shrink-0"><Avatar name={name} className="h-[18px] w-[18px] shrink-0 rounded-full text-[9px]" />{working && <span aria-label="Working" className="absolute -bottom-px -right-px h-1.5 w-1.5 rounded-full border border-card bg-emerald-500" />}</span>
}

function Quiet({ label, names }: { label: string; names: { key: string; name: string; open?: () => void }[] }) {
  if (!names.length) return null
  return <details className="group mt-3"><summary className="cursor-pointer list-none text-xs text-muted-foreground hover:text-foreground">{label} <span className="inline-block transition-transform group-open:rotate-90">›</span></summary>
    <div className="mt-2 flex flex-wrap gap-1.5">{names.map(n => <button key={n.key} type="button" disabled={!n.open} onClick={n.open} className="rounded-full border border-border bg-background px-2.5 py-0.5 text-xs hover:border-muted-foreground">{n.name}</button>)}</div>
  </details>
}

function TeamCard({ c, period }: { c: Card; period: 'day' | 'week' }) {
  const [open, setOpen] = useState(false)
  const o = c.live.overview, work = c.live.work
  const target = { project: c.live.project_id || c.crewId, connection: c.live.connection || c.source.connectionId }
  // An opened card takes the whole row, so its chats, commits, files and tokens have room.
  return <div className={`${card} ${open ? 'col-span-full' : ''}`}>
    <div className="flex min-w-0 items-baseline gap-2"><button type="button" onClick={() => openCrewTeam(c.source.connectionId, c.crewId)} className="min-w-0 flex-1 truncate text-left text-sm font-semibold hover:underline">{c.live.name || 'Team'}</button><Status working={work?.working ? work.label : undefined} quiet={work?.last_worked_label} /></div>
    {o?.done && <p className={sub} title={o.done}>{capital(o.label)}: {o.done}</p>}
    {o?.now.map((n, i) => <p key={i} title={`${n.person} · ${n.agent}`} className="mt-1.5 flex min-w-0 items-center gap-1.5 text-xs"><Face name={n.person} working /><span className="shrink-0 font-medium">{n.person}</span><span className="min-w-0 flex-1 truncate text-muted-foreground">{n.agent}{n.title ? ` — “${n.title}”` : ''}</span><span className="shrink-0 text-[11px] text-muted-foreground">now</span></p>)}
    {o && !o.now.length && o.last && <p className="mt-1.5 flex min-w-0 items-center gap-1.5 text-xs"><Face name={o.last.person} /><span className="shrink-0 font-medium">{o.last.person}</span><span className="min-w-0 flex-1 truncate text-muted-foreground">{o.last.line}</span><span className="shrink-0 text-[11px] text-muted-foreground">{short(o.last.at)}</span></p>}
    <button type="button" aria-expanded={open} onClick={() => setOpen(v => !v)} className="mt-2 text-xs text-emerald-700 dark:text-emerald-400">{o?.now_more ? `+${o.now_more} more working · ` : ''}{open ? 'Hide details' : 'Details ›'}</button>
    {open && <TeamOverviewDetails target={target} period={period} />}
  </div>
}

function PersonCard({ p, multi }: { p: TeamOverviewPerson; multi: boolean }) {
  return <div className={card}>
    <div className="flex min-w-0 items-center gap-2"><Face name={p.person} working={p.working > 0} /><span className="min-w-0 flex-1 truncate text-sm font-semibold">{p.person}{multi && p.network ? <span className="font-normal text-muted-foreground"> · {p.network}</span> : null}</span><Status working={p.working ? 'working' : undefined} quiet={p.last ? timeAgo(p.last.at) : undefined} /></div>
    {(p.done || p.teams.length > 0) && <p className={`ml-6 ${sub}`}>{[p.done, p.teams.join(', ')].filter(Boolean).join(' · ')}</p>}
    {p.now.map((n, i) => <p key={i} className={`ml-6 ${sub}`}>{`${n.agent}${n.title ? ` — “${n.title}”` : ''}${n.team ? ` · ${n.team}` : ''}`}</p>)}
  </div>
}

function OtherCard({ p }: { p: AdminPerson }) {
  return <div className={card}>
    <div className="flex min-w-0 items-center gap-2"><Face name={p.person} working={p.active} /><span className="min-w-0 flex-1 truncate text-sm font-semibold">{p.person}</span><Status working={p.active ? 'active now' : undefined} quiet={p.last ? timeAgo(p.last) : p.online ? 'Mac app open' : undefined} /></div>
    {(p.done || !!p.projects?.length) && <p className={`ml-6 ${sub}`}>{[p.done && `Today: ${p.done}`, (p.projects || []).join(', ')].filter(Boolean).join(' · ')}</p>}
    {p.online && p.last && <p className={`ml-6 ${sub}`}>Mac app open</p>}
    {p.mac && <p className={`ml-6 ${sub}`}>Mac app on the company account: {p.mac}</p>}
  </div>
}

export default function TeamsOverviewPage() {
  const [period, setPeriod] = useState<'day' | 'week'>('day')
  const [byPerson, setByPerson] = useState(false)
  const { sources, notice, people, summary, loading } = useTeamCrews(period)
  const seen = new Set<string>()
  const cards: Card[] = []
  for (const source of sources) for (const crew of source.crews) {
    const live = crew.team_live
    if (!live) continue
    const networkId = (live.network_id || source.networkId).replace(/\/$/, '')
    const key = `${networkId}:${live.project_id || crew.id}`
    if (seen.has(key)) continue
    seen.add(key)
    cards.push({ key, source, crewId: crew.id, live, networkId })
  }
  const newest = (c: Card) => Date.parse(c.live.work?.last_worked_at || '') || 0
  cards.sort((a, b) => Number((b.live.work?.working || 0) > 0) - Number((a.live.work?.working || 0) > 0) || newest(b) - newest(a) || (a.live.name || '').localeCompare(b.live.name || ''))
  const servers = [...new Set(cards.map(c => c.networkId))]
  const multi = servers.length > 1
  const isQuiet = (c: Card) => !!c.live.overview?.quiet && !c.live.work?.working
  const busy = people.filter(p => p.working > 0), earlier = people.filter(p => !p.working && (p.done || p.last)), quietPeople = people.filter(p => !p.working && !p.done && !p.last)
  const others = sources.filter(s => s.others?.length)
  const [lead, ...rest] = summary.split(' · ')
  return <section aria-label="Teams overview" className="flex h-full min-h-0 flex-col bg-background">
    <WorkspacePageHeader page="teams" title="Teams overview" icon={Users} showBack />
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-6xl p-4 sm:p-6">
        <div className="flex flex-wrap items-center gap-2">
          <p className="min-w-0 flex-1 text-sm text-muted-foreground">{summary ? <>{lead.includes('working') ? <b className="font-semibold text-emerald-700 dark:text-emerald-400">{lead}</b> : lead}{rest.map(part => ` · ${part}`).join('')}</> : loading ? 'Loading Teams…' : 'Nothing happening in your Teams.'}</p>
          <Segmented label="Period" value={period} onChange={setPeriod} options={[['day', 'Today'], ['week', 'This week']]} />
          <Segmented label="Group" value={byPerson ? 'person' : 'team'} onChange={v => setByPerson(v === 'person')} options={[['team', 'By Team'], ['person', 'By person']]} />
        </div>
        {notice && <p role="status" className="mt-2 text-xs text-warning">{notice}</p>}
        <div className="-mt-2">
          {!byPerson ? servers.map(id => {
            const group = cards.filter(c => c.networkId === id), quiet = group.filter(isQuiet)
            return <div key={id}>
              {multi && <h2 className={section}>{group[0].source.network || 'This server'}</h2>}
              <div className={grid}>{group.filter(c => !isQuiet(c)).map(c => <TeamCard key={`${c.key}:${period}`} c={c} period={period} />)}</div>
              <Quiet label={`${quiet.length} quiet ${quiet.length === 1 ? 'Team' : 'Teams'}`} names={quiet.map(c => ({ key: c.key, name: `${c.live.name || 'Team'}${c.live.work?.last_worked_label ? ` · ${c.live.work.last_worked_label.replace(/ ago$/, '')}` : ''}`, open: () => openCrewTeam(c.source.connectionId, c.crewId) }))} />
            </div>
          }) : <>
            {busy.length > 0 && <><h2 className={section}>Working now</h2><div className={grid}>{busy.map(p => <PersonCard key={`${p.network || ''}:${p.user_id}`} p={p} multi={multi} />)}</div></>}
            {earlier.length > 0 && <><h2 className={section}>Earlier</h2><div className={grid}>{earlier.map(p => <PersonCard key={`${p.network || ''}:${p.user_id}`} p={p} multi={multi} />)}</div></>}
            <Quiet label={`${quietPeople.length} ${quietPeople.length === 1 ? 'person' : 'people'} quiet ${period === 'week' ? 'this week' : 'today'}`} names={quietPeople.map(p => ({ key: `${p.network || ''}:${p.user_id}`, name: p.person }))} />
            {others.map(source => <div key={`others:${source.connectionId}`}>
              <h2 className={section}>Not in your Teams{multi && source.network ? ` · ${source.network}` : ''} <span className="ml-1 rounded bg-muted px-1 normal-case tracking-normal">admin</span></h2>
              <div className={grid}>{source.others.filter(p => !p.quiet).map(p => <OtherCard key={p.user_id} p={p} />)}</div>
              <Quiet label={`${source.others.filter(p => p.quiet).length} quiet this week`} names={source.others.filter(p => p.quiet).map(p => ({ key: p.user_id, name: p.person }))} />
            </div>)}
          </>}
        </div>
      </div>
    </div>
  </section>
}
