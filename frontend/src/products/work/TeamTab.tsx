import { useEffect, useState, type ReactNode } from 'react'
import { ChevronRight, Folder, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { fillTeamSetupDraft } from '../../utils/agentPurposeDraft'
import { openAppPage } from '../../utils/appNavigation'
import { saveTeam, switchTeam, teamError, teamRequest, useTeamTab, type Team, type TeamTabView, type TeamTarget } from './teamApi'
import { MacSharedAgents } from './MacSharedAgents'
import { TeamSyncPanel } from './TeamSyncPanel'
import { CrewWhereRow } from './CrewWorksElsewhere'

type Person = { id: string; username: string; email?: string }
type Listing = { folders: string[]; path: string; files: number; bytes: number; max_bytes?: number; max_files?: number }

const mb = (bytes: number) => `${(bytes / (1 << 20)).toFixed(bytes < 10 << 20 ? 1 : 0)} MB`
const sizeLine = (size?: Listing) => size ? `${size.files.toLocaleString()} ${size.files === 1 ? 'file' : 'files'} · ${mb(size.bytes)}` : ''
const card = 'space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm'

/** One read of this Mac's Crew folder: a folder's size (path) or the Whole Crew's (whole), by the sync's own rules. */
function useShareSize(connection?: string, localProject?: string, query?: string) {
  const [size, setSize] = useState<Listing>()
  useEffect(() => {
    setSize(undefined)
    if (!connection || !localProject || query === undefined) return
    let active = true
    void desktopRequest<Listing>(`/${encodeURIComponent(connection)}/crew-teams/${encodeURIComponent(localProject)}/folders?${query}`)
      .then(data => { if (active) setSize(data) }).catch(() => {})
    return () => { active = false }
  }, [connection, localProject, query])
  return size
}

/**
 * The Team tab (owner, 5 Nov, approved mockup "Single Team Tab"): the same tab for the owner and every teammate, in
 * the Mac app and on the website, for a Mac Crew and a Crew on the server. The server's reply (useTeamTab) writes
 * every row and says what this person may change; this only shows it and sends their choices. In the owner's Mac app
 * the Access page passes its Mac online switch, and their own agents are listed from the Mac itself.
 */
export function TeamTab({ projectId, connection, macSwitch }: { projectId: string; connection?: string; macSwitch?: ReactNode }) {
  const { view, target, error } = useTeamTab(projectId)
  if (!view || !target) return error ? <p role="alert" className="text-xs text-muted-foreground">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading Team…</p>
  const { team } = view
  return <div className="space-y-3">
    {team.on && <p className="px-1 text-[13px] text-muted-foreground">{view.intro}</p>}
    <PeopleCard view={view} target={target} projectId={projectId} />
    {team.on && <>
      <CrewWhereRow projectId={projectId} />
      <SharesCard view={view} target={target} />
      <AgentsCard view={view} target={target} projectId={projectId} connection={connection} />
      {team.lent && <section aria-label="Lent to the team" className={card}>
        <h3 className="font-semibold">Lent to the team</h3>
        <p className="text-[12.5px] text-muted-foreground">{[team.lent.apps.length ? `Apps: ${team.lent.apps.join(', ')}` : '', team.lent.secrets.length ? `Secrets: ${team.lent.secrets.join(', ')}` : ''].filter(Boolean).join(' · ') || 'Nothing yet'}</p>
      </section>}
      {view.mac && <section aria-label="Mac online" className={card}>
        <h3 className="font-semibold">Mac online</h3>
        {view.mac.yours && macSwitch
          ? <label className="flex items-center justify-between gap-3"><span className="text-[12.5px]">{view.mac.line}</span>{macSwitch}</label>
          : <p className="text-[12.5px]"><Dot on={view.mac.on} />{view.mac.line} · <span className="text-muted-foreground">{view.mac.note}</span></p>}
      </section>}
      {team.folder_access && <section aria-label="Sync" className={card}>
        <h3 className="font-semibold">Sync</h3>
        <TeamSyncPanel target={target} name={team.name} projectId={projectId} server={view.sync} />
      </section>}
      {view.can_leave && <LeaveTeam target={target} />}
    </>}
  </div>
}

function Dot({ on }: { on: boolean }) {
  return <span aria-hidden="true" className={`mr-1.5 inline-block h-1.5 w-1.5 rounded-full align-middle ${on ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
}

const changed = () => window.dispatchEvent(new CustomEvent('agentworks:team-updated'))

/** The Team switch and its people (owner first, by account); + Find a teammate searches the company directory. */
function PeopleCard({ view, target, projectId }: { view: TeamTabView; target: TeamTarget; projectId: string }) {
  const { team } = view
  const [directory, setDirectory] = useState<Person[]>([])
  const [adding, setAdding] = useState(false)
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [note, setNote] = useState('')
  useEffect(() => {
    if (!adding) return
    let active = true
    const read = async () => {
      if (!target.connection) return (await api.get<{ users: Person[] }>('/api/users/directory', { skipSessionContext: true })).data
      const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(target.connection)}/api/users/directory`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
      if (!response.ok) throw new Error('The company directory is unavailable.')
      return response.json() as Promise<{ users: Person[] }>
    }
    void read().then(data => { if (active) setDirectory(data.users || []) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [adding, target.connection, target.project])
  const save = async (next: Team) => {
    setBusy(true); setError('')
    try { await saveTeam(target, next) } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  // The switch works where the server says it can: on the server, or for a Mac Crew in its owner's Mac app.
  const canSwitch = team.can_manage && (view.switch === 'server' || Boolean(target.localProject && view.mac?.yours))
  const turn = async (on: boolean) => {
    setBusy(true); setError(''); setNote('')
    try {
      const result = await switchTeam(target, on, view.switch === 'server')
      setNote([result.folder_created ? 'Made an empty team/ folder to share.' : '', result.shared?.length ? `Shared with your team: ${result.shared.join(', ')}.` : '', result.note || ''].filter(Boolean).join(' '))
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const askText = 'Ask chat to set up your team: people, what it shares and which agents are shared'
  const known = new Set([team.owner_id, ...team.people.map(p => p.user_id)])
  return <section aria-label="Team" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Team</h3>
      <span className="text-xs text-muted-foreground">{team.on && view.people.length > 1 ? `${view.people.length} people` : 'Only you'}</span>
      <button type="button" title={askText} aria-label={askText} onClick={() => fillTeamSetupDraft(projectId)} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted">Ask</button>
      <Switch aria-label="Team" disabled={busy || !canSwitch} checked={team.on} onCheckedChange={on => void turn(on)} />
    </div>
    {note && <p className="text-xs text-muted-foreground">{note}</p>}
    {!team.on && <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people.</p>}
    {team.on && <div className="divide-y divide-border">
      {view.people.map(person => <div key={person.user_id} className="flex items-center gap-2 py-1.5">
        <span aria-hidden="true" className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-full bg-muted text-[11px] font-semibold">{person.name.slice(0, 1).toUpperCase()}</span>
        <span className="min-w-0 flex-1 truncate">{person.name}{person.note && <span className="text-muted-foreground"> · {person.note}</span>}</span>
        {person.active_label && <span className="shrink-0 text-xs text-muted-foreground"><Dot on={person.active} />{person.active_label}</span>}
        {person.can_remove && <button type="button" disabled={busy} aria-label={`Remove ${person.name}`} title="Remove" onClick={() => void save({ ...team, people: team.people.filter(p => p.user_id !== person.user_id) })} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>}
      </div>)}
    </div>}
    {team.on && team.can_manage && <>
      <button type="button" disabled={busy} onClick={() => setAdding(!adding)} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Find a teammate</button>
      {adding && <div className="rounded-xl border border-border bg-popover">
        <input aria-label="Find a teammate" placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-2 text-xs" value={search} onChange={e => setSearch(e.target.value)} />
        <div className="max-h-48 overflow-auto">{directory.filter(p => !known.has(p.id) && `${p.username} ${p.email || ''}`.toLowerCase().includes(search.toLowerCase())).map(p => <button key={p.id} type="button" disabled={busy} onClick={() => { void save({ ...team, people: [...team.people, { user_id: p.id }] }); setAdding(false) }} className="block w-full px-3 py-2 text-left text-xs hover:bg-muted">{p.username}</button>)}</div>
      </div>}
    </>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

function FolderRow({ folder, target, canRemove, busy, onRemove }: { folder: string; target: TeamTarget; canRemove: boolean; busy: boolean; onRemove: () => void }) {
  const size = useShareSize(target.connection, target.localProject, `path=${encodeURIComponent(folder)}`)
  return <div className="flex items-center gap-2 rounded-lg border border-border px-2 py-1.5 text-xs">
    <Folder className="h-3.5 w-3.5 shrink-0 text-amber-500" /><span className="min-w-0 flex-1 truncate">{folder}/</span>
    <span className="text-muted-foreground">{sizeLine(size)}</span>
    {canRemove && <button type="button" disabled={busy} aria-label={`Stop sharing ${folder}/`} title="Stop sharing this folder" onClick={onRemove} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>}
  </div>
}

/** What the team shares: Folders or the Whole Crew. Choosing and sizing folders reads this Mac's own Crew folder, so
 * those choices are made in the Mac app; anyone who manages the Team can stop sharing a folder from anywhere. */
function SharesCard({ view, target }: { view: TeamTabView; target: TeamTarget }) {
  const { team } = view
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<Listing>()
  const onMac = Boolean(target.connection && target.localProject)
  useEffect(() => {
    if (!picking || !onMac) return
    let active = true
    void desktopRequest<Listing>(`/${encodeURIComponent(target.connection!)}/crew-teams/${encodeURIComponent(target.localProject!)}/folders?path=${encodeURIComponent(parent)}`)
      .then(data => { if (active) setListing(data) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [picking, parent, onMac, target.connection, target.localProject])
  const whole = useShareSize(target.connection, target.localProject, team.can_manage ? 'whole=1' : undefined)
  const tooBig = Boolean(whole && (whole.bytes > (whole.max_bytes || Infinity) || whole.files > (whole.max_files || Infinity)))
  const folders = team.folder.folders || []
  const save = async (next: Team) => {
    setBusy(true); setError('')
    try { await saveTeam(target, next) } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const choose = team.can_manage && onMac
  return <section aria-label="What the team shares" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">What the team shares</h3>
      <div role="radiogroup" aria-label="What the team shares" className="flex overflow-hidden rounded-md border border-border text-xs">
        {([[false, 'Folders'], [true, 'Whole Crew']] as const).map(([value, label]) => {
          const on = Boolean(team.folder.whole) === value
          return <button key={label} type="button" role="radio" aria-checked={on} disabled={busy || !choose || (value && tooBig)}
            onClick={() => { if (!on) void save({ ...team, folder: value ? { whole: true } : { folders: [] } }) }}
            className={`px-2.5 py-0.5 disabled:cursor-not-allowed ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted disabled:opacity-50'}`}>{label}</button>
        })}
      </div>
    </div>
    {team.folder.whole ? <p className="text-xs text-muted-foreground">The whole <b className="text-foreground">{team.name}</b> folder{whole ? `: ${sizeLine(whole)}` : ''}. Everyone in the Team reads and edits it.</p>
      : <div className="space-y-1.5">
        {folders.map(folder => <FolderRow key={folder} folder={folder} target={target} canRemove={team.can_manage} busy={busy}
          onRemove={() => void save({ ...team, folder: { folders: folders.filter(f => f !== folder) } })} />)}
        {!folders.length && <p className="text-xs text-muted-foreground">Nothing is shared yet{choose ? ': add a folder, or share the Whole Crew' : ''}.</p>}
        {choose && <button type="button" disabled={busy} onClick={() => { setPicking(!picking); setParent('') }} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Share this folder</button>}
        {choose && whole && tooBig && <p className="text-xs text-amber-600">The Whole Crew is too big to share ({sizeLine(whole)}; at most 256 MB and 10,000 files).</p>}
      </div>}
    {picking && choose && !team.folder.whole && <div className="space-y-2 rounded-lg bg-muted/40 p-3 text-xs">
      <div className="flex items-center gap-2"><button type="button" onClick={() => setParent(parent.split('/').slice(0, -1).join('/'))} className="text-primary">{parent ? '← Up' : 'Project'}</button><span>{parent}</span></div>
      {listing?.folders.map(folder => <button key={folder} type="button" onClick={() => setParent(folder)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted"><Folder className="h-3.5 w-3.5" />{folder.split('/').pop()}<ChevronRight className="ml-auto h-3 w-3" /></button>)}
      {parent && listing?.path === parent && <><p>{sizeLine(listing)}</p><Button size="sm" disabled={busy || folders.includes(parent)} onClick={() => { void save({ ...team, folder: { folders: [...folders, parent] } }); setPicking(false) }}>Share this folder</Button></>}
    </div>}
    <p className="text-xs text-muted-foreground">Not shared: your chats and memory, app files, passwords and keys.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

/** Every agent in the Team, as the server lists it: the person's own ones with In team / Only me, everyone else's as
 * theirs to change. A Mac Crew owner's own agents are listed from their Mac in the Mac app (MacSharedAgents). */
function AgentsCard({ view, target, projectId, connection }: { view: TeamTabView; target: TeamTarget; projectId: string; connection?: string }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const fromMac = Boolean(connection && view.mac?.yours)
  const rows = view.agents.filter(agent => !(fromMac && agent.chosen_on_mac))
  const choose = async (key: string, inTeam: boolean) => {
    setBusy(true); setError('')
    try { await teamRequest({ project: target.project, connection: target.connection }, '/agent-visibility', 'POST', { agent_key: key, in_team: inTeam }); changed() }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  return <section aria-label="Agents" className={card}>
    {!fromMac && <h3 className="font-semibold">Agents</h3>}
    {fromMac && connection && <MacSharedAgents projectId={projectId} connection={connection} />}
    {rows.map(agent => <div key={agent.key} className="flex items-center gap-2 border-t border-border pt-2 text-[13px]">
      <span className="min-w-0 flex-1 truncate"><b>{agent.name}</b> <span className="text-muted-foreground">· {agent.detail}</span></span>
      {agent.can_change
        ? <select aria-label={`${agent.name} in the team`} value={agent.in_team ? 'team' : 'me'} disabled={busy} onChange={e => void choose(agent.key, e.target.value === 'team')} className="rounded-md border border-border bg-background px-1.5 py-0.5 text-xs"><option value="team">In team</option><option value="me">Only me</option></select>
        : <span className="shrink-0 text-xs text-muted-foreground">{agent.fixed}</span>}
    </div>)}
    {!rows.length && !fromMac && <p className="text-xs text-muted-foreground">No agents in the team yet.</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

function LeaveTeam({ target }: { target: TeamTarget }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const leave = async () => {
    setBusy(true); setError('')
    try { await teamRequest({ project: target.project, connection: target.connection }, '/leave', 'POST', {}); openAppPage('/home') }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  return <div className="space-y-1">
    <Button variant="outline" disabled={busy} onClick={() => void leave()}>Leave Team</Button>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}
