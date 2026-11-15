import { Avatar, type AvatarFields } from '../../components/ui/Avatar'
import { useEffect, useState } from 'react'
import { ChevronRight, Pencil, Users } from 'lucide-react'

/** Rows are admitted and their message lines filtered by the server, not by this view. */
export type TeamPanelAgent = AvatarFields & {
 connection?: string
  id: string; name: string; person: string; mine: boolean; team_agent?: boolean
  cli?: string; engine?: string; model_id?: string; reasoning_effort?: string
  online: boolean; place: string; activity?: string
  last_message?: string; last_message_at?: string; typing?: boolean
}
/** One of the person's own agents shared with the team, from the Team tab's server rows. */
export type TeamPanelShared = { key: string; name: string; canChange: boolean; chosen?: number }
export type TeamPanelPost = { id: string; kind: string; person: string; agent: string; line: string; at: string }

function saved(key: string) { try { return localStorage.getItem(key) || '' } catch { return '' } }
function remember(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* this visit only */ } }
function useRemembered(scope: string, name: string) {
  const key = `agentworks.team-panel:${scope}:${name}`
  const [state, setState] = useState(() => ({ key, value: saved(key) }))
  const value = state.key === key ? state.value : saved(key)
  return [value, (next: string) => { remember(key, next); setState({ key, value: next }) }] as const
}
function age(at?: string) {
  const elapsed = at ? Math.max(0, Date.now() - Date.parse(at)) : NaN
  return !Number.isFinite(elapsed) ? '' : elapsed < 60_000 ? 'now' : elapsed < 3_600_000 ? `${Math.floor(elapsed / 60_000)}m` : elapsed < 86_400_000 ? `${Math.floor(elapsed / 3_600_000)}h` : `${Math.floor(elapsed / 86_400_000)}d`
}
const agentLine = (row: TeamPanelAgent) => [row.team_agent ? 'Online always · answers when others are away' : `${row.mine ? 'You' : row.person} · ${row.place === 'mac' ? 'Mac' : 'Server'}`, row.cli || row.engine, row.model_id, row.reasoning_effort].filter(Boolean).join(' · ')
const time = (at: string) => Date.parse(at) || 0

/** The same compact Team content in the web and Mac right panel. The scope includes
 * the signed-in person and server, so another account never inherits disclosures. */
export function TeamPanel({ scope, agents, posts, onAsk, onActivity, error, shared = [], onShared }: {
  scope: string; agents: TeamPanelAgent[]; posts: TeamPanelPost[]
  onAsk: (agent: TeamPanelAgent) => void; onActivity: () => void; error?: string
  shared?: TeamPanelShared[]; onShared?: (row: TeamPanelShared) => void
}) {
  const [agentsChoice, setAgentsChoice] = useRemembered(scope, 'agents')
  const [activityChoice, setActivityChoice] = useRemembered(scope, 'activity')
  const [sharedChoice, setSharedChoice] = useRemembered(scope, 'shared')
  const sharedOpen = sharedChoice === 'open'
  const sharedLine = (row: TeamPanelShared) => `Shared with team · ${row.canChange ? 'can make changes' : 'answers only'} · ${row.chosen === undefined ? 'whole team' : `${row.chosen} chosen ${row.chosen === 1 ? 'person' : 'people'}`}`
  const [seen, setSeen] = useRemembered(scope, 'seen')
  const agentsOpen = agentsChoice === 'open', activityOpen = activityChoice === 'open'
  const ordered = [...agents.filter(row => row.team_agent), ...agents.filter(row => !row.team_agent && !row.mine)]
  const visible = ordered
  const hasTeamAgent = ordered.some(row => row.team_agent)
  const offlineLine = (row: TeamPanelAgent) => !row.online && !row.mine && !row.team_agent ? hasTeamAgent ? 'Offline · new asks go to the Team agent' : 'Offline · no Team agent available' : ''
  const latest = [...posts].filter(post => post.kind === 'doing' || post.kind === 'decision').sort((a, b) => time(b.at) - time(a.at))
  const newest = latest[0]?.at || ''
  const newCount = latest.filter(post => time(post.at) > time(seen)).length
  // While reading Activity, newly arrived posts are read too. This stores only a
  // timestamp, never Team content. A closed group does not consume new posts.
  useEffect(() => { if (activityOpen && newest && time(newest) > time(seen)) setSeen(newest) }, [activityOpen, newest, seen, scope]) // eslint-disable-line react-hooks/exhaustive-deps
  const summaryClass = 'flex w-full min-w-0 items-center gap-1.5 rounded px-1 py-1.5 text-left text-[11px] text-muted-foreground hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
  const summary = (name: string, open: boolean, count: string, peek: string, toggle: () => void) => <button type="button" aria-expanded={open} onClick={toggle} className={summaryClass}>
    <span className="font-medium">{name}</span><span className="shrink-0 text-[10px]">{count}</span>
    {!open && <span className="min-w-0 flex-1 truncate text-[10px]">{peek}</span>}
    <ChevronRight aria-hidden="true" className={`ml-auto h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-90' : ''}`} />
  </button>
  return <div aria-label="Team agents and activity" className="space-y-0.5">
    {summary('Agents', agentsOpen, `${ordered.length} · ${ordered.filter(row => row.online).length} online`, ordered.slice(0, 2).map(row => row.name).join(', '), () => setAgentsChoice(agentsOpen ? '' : 'open'))}
    {agentsOpen && <div role="group" aria-label="Team agents" className="space-y-0.5">
      {visible.map(row => <button key={row.id} type="button" onClick={() => onAsk(row)} className="flex w-full items-start gap-2 rounded-md px-1 py-1.5 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span aria-hidden="true" className="relative mt-0.5 grid h-6 w-6 shrink-0 place-items-center rounded-full bg-muted text-[10px] font-medium"><Avatar {...row} name={row.team_agent ? row.name : row.person} className="h-full w-full text-[10px]" /><span className={`absolute -bottom-0.5 -right-0.5 h-2 w-2 rounded-full border-2 border-background ${row.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} /></span>
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline gap-1"><span className="min-w-0 flex-1 truncate text-xs font-medium">{row.name}</span><span className={`shrink-0 text-[10px] ${row.activity === 'running' ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{row.activity === 'running' ? 'Working' : row.activity === 'waiting' ? 'Waiting' : !row.online ? 'Offline' : ''}</span></span>
          <span title={agentLine(row)} className="block truncate text-[10px] text-muted-foreground">{agentLine(row)}</span>
          {offlineLine(row) && <span title={offlineLine(row)} className="block truncate text-[10px] text-muted-foreground">{offlineLine(row)}</span>}
          {row.last_message && <span className="block truncate text-[11px] text-muted-foreground">{row.typing ? <span className="text-emerald-600 dark:text-emerald-400">typing… </span> : age(row.last_message_at) ? <span>{age(row.last_message_at)} · </span> : null}{row.last_message}</span>}
        </span>
      </button>)}
      {!ordered.length && <p className="px-1 py-1 text-[11px] text-muted-foreground">No agents shared with the team yet.</p>}
    </div>}
    {summary('Shared by you', sharedOpen, `${shared.length}`, shared.slice(0, 2).map(row => row.name).join(', ') || 'None yet', () => setSharedChoice(sharedOpen ? '' : 'open'))}
    {sharedOpen && <div role="group" aria-label="Shared by you" className="space-y-0.5">
      {shared.map(row => <button key={row.key} type="button" title={sharedLine(row)} onClick={() => onShared?.(row)} className="flex w-full items-center gap-2 rounded-md px-1 py-1 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span aria-hidden="true" className="relative inline-flex shrink-0"><Users className={`h-3 w-3 ${row.canChange ? 'text-primary' : 'text-muted-foreground'}`} />{row.canChange && <Pencil className="absolute -bottom-1 -right-1 h-2 w-2 text-primary" />}</span>
        <span className="min-w-0 flex-1 truncate text-xs">{row.name}</span>
        <span className="shrink-0 text-[10px] text-muted-foreground">{row.chosen === undefined ? 'Whole team' : `${row.chosen} chosen`}</span>
      </button>)}
      {!shared.length && <p className="px-1 py-1 text-[11px] text-muted-foreground">None of your agents is shared with the team.</p>}
    </div>}
    {summary('Activity', activityOpen, `${newCount} new`, latest[0] ? `${latest[0].agent}: ${latest[0].line}` : 'No recent posts', () => setActivityChoice(activityOpen ? '' : 'open'))}
    {activityOpen && <div role="group" aria-label="Recent Team activity">
      {latest.slice(0, 2).map(post => <div key={post.id} className="flex items-start gap-2 px-1 py-1.5 text-[11px]">
        <span aria-hidden="true" className={`mt-1.5 h-1.5 w-1.5 shrink-0 ${post.kind === 'decision' ? 'rotate-45 bg-blue-500' : 'rounded-full bg-emerald-500'}`} />
        <span className="min-w-0"><span className="block truncate font-medium">{post.person}&apos;s {post.agent}{post.kind === 'decision' ? ' decided' : ''}</span><span className="block break-words text-muted-foreground">{post.line}</span></span>
      </div>)}
      <button type="button" onClick={onActivity} className="px-1 py-1 text-[11px] text-primary hover:underline">See all activity</button>
    </div>}
    {error && <p role="alert" className="px-1 text-[11px] text-destructive">{error}</p>}
  </div>
}
