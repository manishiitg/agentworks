import { useEffect, useState } from 'react'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { timeAgo } from '../../components/scheduler/scheduleRuns/helpers'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

// What a Team is doing now and got done (#3900). The server builds every line, count and grouping; this only shows them.
export type TeamOverviewCounts = { chats: number; files: number; commits: number; decisions: number; tokens: number }
export type TeamOverviewNow = { agent: string; person: string; title?: string; team?: string }
export type TeamOverviewItem = { kind: string; person: string; line: string; at: string }
export type TeamOverviewPerson = { user_id: string; person: string; network?: string; working: number; now: TeamOverviewNow[]; teams: string[]; counts: TeamOverviewCounts; done?: string; last?: TeamOverviewItem | null }
export type TeamOverview = {
  period: 'day' | 'week'; label: string; now: TeamOverviewNow[]; now_more?: number; quiet?: boolean; counts: TeamOverviewCounts; done?: string; last?: TeamOverviewItem | null
  chats?: { title?: string; agent: string; person: string; messages?: number; at: string }[]
  files?: { person: string; folder: string; line: string; files: number; saves: number; paths?: { path: string; saves: number }[]; at: string }[]
  commits?: { title: string; author: string; repo?: string; at: string }[]
  decisions?: TeamOverviewItem[]
  tokens?: { person: string; tokens: number; agents?: { agent: string; tokens: number }[] }[]
  people: TeamOverviewPerson[]
}

export const tokensLabel = (n: number) => n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M` : n >= 1_000 ? `${Math.floor(n / 1_000)}K` : String(n)
export const nowLine = (n: TeamOverviewNow, withPerson = true) => `${n.agent}${withPerson && n.person !== 'You' ? ` (${n.person})` : ''}${n.team ? ` · ${n.team}` : ''}${n.title ? ` · “${n.title}”` : ''}`

const box = 'rounded-lg border border-border p-3'
const head = 'mb-1 flex items-baseline justify-between text-xs font-semibold'
const sub = 'text-[11px] text-muted-foreground'
const row = 'flex gap-2 border-t border-border/50 py-1.5 text-xs first:border-t-0'

function Toggle<T extends string>({ value, options, onChange, label }: { value: T; options: [T, string][]; onChange: (v: T) => void; label: string }) {
  return <div role="radiogroup" aria-label={label} className="inline-flex overflow-hidden rounded-md border border-border text-[11px]">
    {options.map(([v, text]) => <button key={v} type="button" role="radio" aria-checked={value === v} onClick={() => onChange(v)} className={`px-2 py-1 ${value === v ? 'bg-foreground text-background' : 'text-muted-foreground hover:bg-muted'}`}>{text}</button>)}
  </div>
}

export function PersonCard({ p, multi }: { p: TeamOverviewPerson; multi?: boolean }) {
  return <div className={box}>
    <div className={head}><span>{p.person}{multi && p.network ? <span className="ml-1 font-normal text-muted-foreground">· {p.network}</span> : null}</span><small className="font-normal text-muted-foreground">{p.working ? `${p.working} working` : 'not working now'}</small></div>
    {p.now.map((n, i) => <p key={i} className="truncate text-xs" title={nowLine(n, false)}><span className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-emerald-500" />{nowLine(n, false)}</p>)}
    <p className={sub}>{p.done || 'Nothing done in this period.'}</p>
    {p.last && <p className={`truncate ${sub}`} title={p.last.line}>last: {p.last.line} · {timeAgo(p.last.at)}</p>}
  </div>
}

export function TeamOverviewPanel({ target }: { target: TeamTarget }) {
  const [period, setPeriod] = useState<'day' | 'week'>('day')
  const [view, setView] = useState<'team' | 'person'>('team')
  const [data, setData] = useState<TeamOverview>()
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  useLiveRefetch(() => setRefresh(v => v + 1), { kinds: ['sessions', 'crew_panel'], fallbackMs: 60000, safetyMs: 60000, minIntervalMs: 15000, enabled: true })
  useEffect(() => {
    const identity = captureChatIdentity()
    let gone = false
    teamRequest<TeamOverview>(target, `/overview?period=${period}`).then(out => { if (!gone && isChatIdentityCurrent(identity)) { setData(out); setError('') } })
      .catch(e => { if (!gone && isChatIdentityCurrent(identity)) setError(teamFailure(e).message || 'The overview could not be loaded. Try again.') })
    return () => { gone = true }
  }, [target, period, refresh])
  if (error && !data) return <p role="alert" className="text-xs text-muted-foreground">{error}</p>
  if (!data) return <p role="status" className="text-sm text-muted-foreground">Loading overview…</p>
  const top = data.tokens?.[0]?.tokens || 1
  const idle = data.people.filter(p => !p.working)
  return <section aria-label="Overview" className="space-y-3">
    <div className="flex flex-wrap items-center gap-2">
      <Toggle label="Period" value={period} onChange={setPeriod} options={[['day', 'Last 24 hours'], ['week', 'Last 7 days']]} />
      <Toggle label="Group" value={view} onChange={setView} options={[['team', 'By Team'], ['person', 'By person']]} />
      {data.done && <span className={sub}>{data.done}</span>}
    </div>
    {view === 'person' ? <div className="grid gap-3 sm:grid-cols-2">{data.people.map(p => <PersonCard key={p.user_id} p={p} />)}</div> :
      <div className="grid gap-3 sm:grid-cols-2">
        <div className={box}><div className={head}>Happening now<small className="font-normal text-muted-foreground">{data.now.length} working</small></div>
          {data.now.map((n, i) => <div key={i} className={row}><span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" /><div className="min-w-0"><b>{n.person}</b> · {n.agent}{n.title && <p className={`truncate ${sub}`}>“{n.title}”</p>}</div></div>)}
          {idle.map(p => <div key={p.user_id} className={row}><span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-muted-foreground/30" /><div className="min-w-0"><b>{p.person}</b><p className={sub}>not working now</p></div></div>)}
        </div>
        <div className={box}><div className={head}>Chats<small className="font-normal text-muted-foreground">{data.counts.chats}</small></div>
          {data.chats?.length ? data.chats.map((c, i) => <div key={i} className={row}><div className="min-w-0 flex-1">{c.title ? `“${c.title}”` : <span className="text-muted-foreground">A chat</span>}<p className={sub}>{c.agent} · {c.person}{c.messages ? ` · ${c.messages} messages` : ''}</p></div><time className={sub}>{timeAgo(c.at)}</time></div>) : <p className={sub}>No chats in this period.</p>}
        </div>
        <div className={box}><div className={head}>Files changed<small className="font-normal text-muted-foreground">{data.counts.files} files</small></div>
          {data.files?.length ? data.files.map((f, i) => <details key={i} className={row}><summary className="flex min-w-0 flex-1 cursor-pointer list-none gap-2"><div className="min-w-0 flex-1"><b>{f.person}</b> {f.line}{f.saves > f.files && <p className={sub}>{f.saves} saves</p>}</div><time className={sub}>{timeAgo(f.at)}</time></summary>
            <div className="mt-1 space-y-0.5 pl-2">{f.paths?.map(p => <p key={p.path} className={`truncate ${sub}`}>{p.path}{p.saves > 1 ? ` · ${p.saves} saves` : ''}</p>)}{f.files > (f.paths?.length || 0) && <p className={sub}>+{f.files - (f.paths?.length || 0)} more</p>}</div></details>) : <p className={sub}>No Team file changes in this period.</p>}
        </div>
        <div className={box}><div className={head}>Commits<small className="font-normal text-muted-foreground">{data.counts.commits}</small></div>
          {data.commits?.length ? data.commits.map((c, i) => <div key={i} className={row}><div className="min-w-0 flex-1 truncate">{c.title}<p className={sub}>{c.author}{c.repo ? ` · ${c.repo}` : ''}</p></div><time className={sub}>{timeAgo(c.at)}</time></div>) : <p className={sub}>No commits in the project folder in this period.</p>}
        </div>
        <div className={box}><div className={head}>Tokens used<small className="font-normal text-muted-foreground">{tokensLabel(data.counts.tokens)}</small></div>
          {data.tokens?.length ? data.tokens.map((t, i) => <div key={i} className={row}><div className="min-w-0 flex-1">{t.person} · {tokensLabel(t.tokens)}<div className="mt-1 h-1.5 rounded bg-muted"><div className="h-1.5 rounded bg-primary" style={{ width: `${Math.max(2, Math.round(t.tokens / top * 100))}%` }} /></div>{t.agents?.length ? <p className={`truncate ${sub}`}>{t.agents.map(a => `${a.agent} ${tokensLabel(a.tokens)}`).join(' · ')}</p> : null}</div></div>) : <p className={sub}>No tokens recorded for this project in this period.</p>}
        </div>
        <div className={box}><div className={head}>Decisions<small className="font-normal text-muted-foreground">{data.counts.decisions}</small></div>
          {data.decisions?.length ? data.decisions.map((d, i) => <div key={i} className={row}><div className="min-w-0 flex-1">{d.line}<p className={sub}>{d.person}</p></div><time className={sub}>{timeAgo(d.at)}</time></div>) : <p className={sub}>No decisions in this period.</p>}
        </div>
      </div>}
  </section>
}
