import { useEffect, useState } from 'react'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { timeAgo } from '../../components/scheduler/scheduleRuns/helpers'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

// What a Team is doing now and got done (#3900). The server builds every line, count and grouping; this only shows them.
export type TeamOverviewCounts = { chats: number; files: number; commits: number; decisions: number; tokens: number }
export type TeamOverviewNow = { agent: string; person: string; user_id?: string; title?: string; team?: string }
export type TeamOverviewItem = { kind: string; person: string; user_id?: string; line: string; at: string }
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

const box = 'min-w-0 rounded-lg border border-border/70 p-2.5'
const head = 'mb-1 flex items-baseline justify-between text-[11px] font-semibold text-muted-foreground'
const sub = 'text-[11px] text-muted-foreground'
const shown = 10 // each list shows its newest 10; the count says how many there were
const row = 'flex gap-2 border-t border-border/50 py-1 text-xs first:border-t-0'

/** One Team's chats, files, commits, tokens and decisions in the period, opened from its card on the Teams overview
 * page (owner, 25 Nov: the overview is one page for every Team, not a tab in each Team's panel). */
export function TeamOverviewDetails({ target, period }: { target: TeamTarget; period: 'day' | 'week' }) {
  const [data, setData] = useState<TeamOverview>()
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  useLiveRefetch(() => setRefresh(v => v + 1), { kinds: ['sessions', 'crew_panel'], fallbackMs: 60000, safetyMs: 60000, minIntervalMs: 15000, enabled: true })
  useEffect(() => {
    const identity = captureChatIdentity()
    let gone = false
    teamRequest<TeamOverview>(target, `/overview?period=${period}`).then(out => { if (!gone && isChatIdentityCurrent(identity)) { setData(out); setError('') } })
      .catch(e => { if (!gone && isChatIdentityCurrent(identity)) setError(teamFailure(e).message || 'The details could not be loaded. Try again.') })
    return () => { gone = true }
  }, [target, period, refresh])
  if (error && !data) return <p role="alert" className={sub}>{error}</p>
  if (!data) return <p role="status" className={sub}>Loading details…</p>
  const top = data.tokens?.[0]?.tokens || 1
  const more = (n: number, listed?: number) => n > Math.min(listed || 0, shown) ? <p className={`pt-1 ${sub}`}>+{n - Math.min(listed || 0, shown)} more</p> : null
  return <div className="mt-2 grid gap-2 border-t border-border pt-2 sm:grid-cols-2 xl:grid-cols-4">
    <div className={box}><div className={head}>Chats<small className="font-normal">{data.counts.chats}</small></div>
      {data.chats?.length ? data.chats.slice(0, shown).map((c, i) => <div key={i} className={row}><div className="min-w-0 flex-1 truncate">{c.title ? `“${c.title}”` : <span className="text-muted-foreground">A chat</span>}<p className={`truncate ${sub}`}>{c.agent} · {c.person}{c.messages ? ` · ${c.messages} messages` : ''}</p></div><time className={sub}>{timeAgo(c.at)}</time></div>) : <p className={sub}>No chats.</p>}{more(data.counts.chats, data.chats?.length)}
    </div>
    <div className={box}><div className={head}>Commits<small className="font-normal">{data.counts.commits}</small></div>
      {data.commits?.length ? data.commits.slice(0, shown).map((c, i) => <div key={i} className={row}><div className="min-w-0 flex-1 truncate">{c.title}<p className={`truncate ${sub}`}>{c.author}{c.repo ? ` · ${c.repo}` : ''}</p></div><time className={sub}>{timeAgo(c.at)}</time></div>) : <p className={sub}>No commits.</p>}{more(data.counts.commits, data.commits?.length)}
    </div>
    <div className={box}><div className={head}>Files changed<small className="font-normal">{data.counts.files}</small></div>
      {data.files?.length ? data.files.slice(0, shown).map((f, i) => <details key={i} className={row}><summary className="flex min-w-0 flex-1 cursor-pointer list-none gap-2"><div className="min-w-0 flex-1 truncate"><b>{f.person}</b> {f.line}</div><time className={sub}>{timeAgo(f.at)}</time></summary>
        <div className="mt-1 space-y-0.5 pl-2">{f.paths?.map(p => <p key={p.path} className={`truncate ${sub}`}>{p.path}{p.saves > 1 ? ` · ${p.saves} saves` : ''}</p>)}{f.files > (f.paths?.length || 0) && <p className={sub}>+{f.files - (f.paths?.length || 0)} more</p>}</div></details>) : <p className={sub}>No Team file changes.</p>}{more(data.files?.length || 0, data.files?.length)}
    </div>
    <div className={box}><div className={head}>Tokens<small className="font-normal">{tokensLabel(data.counts.tokens)}</small></div>
      {data.tokens?.length ? data.tokens.map((t, i) => <div key={i} className={row}><div className="min-w-0 flex-1">{t.person} · {tokensLabel(t.tokens)}<div className="mt-1 h-1.5 rounded bg-muted"><div className="h-1.5 rounded bg-primary" style={{ width: `${Math.max(2, Math.round(t.tokens / top * 100))}%` }} /></div>{t.agents?.length ? <p className={`truncate ${sub}`}>{t.agents.map(a => `${a.agent} ${tokensLabel(a.tokens)}`).join(' · ')}</p> : null}</div></div>) : <p className={sub}>No tokens recorded.</p>}
    </div>
    {!!data.decisions?.length && <div className={`${box} sm:col-span-2`}><div className={head}>Decisions<small className="font-normal">{data.counts.decisions}</small></div>
      {data.decisions.map((d, i) => <div key={i} className={row}><div className="min-w-0 flex-1">{d.line}<p className={sub}>{d.person}</p></div><time className={sub}>{timeAgo(d.at)}</time></div>)}
    </div>}
  </div>
}
