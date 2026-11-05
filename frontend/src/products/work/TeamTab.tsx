import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronRight, Folder, MoreHorizontal, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { fillAskFromMyAgentDraft, fillTeamSetupDraft } from '../../utils/agentPurposeDraft'
import { openAppPage } from '../../utils/appNavigation'
import { activateTab } from '../../utils/activateTab'
import { changeTeamPeople, saveTeam, switchTeam, teamAgentForTab, teamError, teamRequest, useTeamTab, type Team, type TeamTabAgent, type TeamTabShare, type TeamTabState, type TeamTabView, type TeamTarget } from './teamApi'
import { TeamSyncPanel } from './TeamSyncPanel'
import { CrewWhereRow } from './CrewWorksElsewhere'
import { LentCard } from './LentCard'

type Person = { id: string; username: string; email?: string }
// A size read from this Mac's own Crew folder; whether it is too big to share is the server's answer.
type Listing = { folders: string[]; path: string; files: number; bytes: number; too_big?: boolean; too_big_line?: string }

const mb = (bytes: number) => `${(bytes / (1 << 20)).toFixed(bytes < 10 << 20 ? 1 : 0)} MB`
const sizeLine = (size?: { files: number; bytes: number }) => size ? `${size.files.toLocaleString()} ${size.files === 1 ? 'file' : 'files'} · ${mb(size.bytes)}` : ''
const card = 'space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm'
const shortName = (name: string) => name.split('@')[0] || name

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
 * the Mac app and on the website, for a Mac Crew and a Crew on the server. The server's /tab reply writes every row
 * and says what this person may change; this only shows it and sends their choices. Only what is physically on the
 * owner's Mac (its Mac online switch, picking its local folders and their sizes) differs, and the server words it.
 */
export function TeamTab({ projectId, macSwitch, onTurnOn }: { projectId: string; macSwitch?: ReactNode; onTurnOn?: () => void }) {
  const state = useTeamTab(projectId)
  const { view, target, error } = state
  // The Mac app's own Crew before Mac online pairs it: the Team is off, and turning it on turns Mac online on too.
  if (state.unbound) return <section aria-label="Team" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Team</h3>
      <span className="text-xs text-muted-foreground">Only you</span>
      {onTurnOn && <Switch aria-label="Team" checked={false} onCheckedChange={on => { if (on) onTurnOn() }} />}
    </div>
    <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people; it also turns on Mac online.</p>
  </section>
  if (!view || !target) return error ? <p role="alert" className="text-xs text-muted-foreground">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading Team…</p>
  const { team } = view
  return <div className="space-y-3">
    {team.on && <p className="px-1 text-[13px] text-muted-foreground">{view.intro}</p>}
    <PeopleCard view={view} target={target} projectId={projectId} />
    {team.on && <>
      <CrewWhereRow projectId={projectId} />
      <SharesCard view={view} target={target} />
      <AgentsCard state={state} view={view} target={target} projectId={projectId} />
      {team.lent && <LentCard lent={team.lent} target={target} />}
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

/** The Team switch and its people (owner first, by account); + Find a teammate searches the company directory. Adding
 * and removing send one change; the server keeps the record. */
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
  const people = async (change: { add: string } | { remove: string }) => {
    setBusy(true); setError('')
    try { await changeTeamPeople(target, change) } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const turn = async (on: boolean) => {
    setBusy(true); setError(''); setNote('')
    try {
      // The owner's own Mac app switches through its Mac, which applies the record at once.
      const result = await switchTeam(target, on, Boolean(view.mac?.yours && target.localProject))
      setNote(result.folder_created ? 'Made an empty team/ folder to share.' : '')
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const askText = 'Ask chat to set up your team: people, what it shares and which agents are shared'
  const known = new Set(view.people.map(person => person.user_id))
  return <section aria-label="Team" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Team</h3>
      <span className="text-xs text-muted-foreground">{team.on && view.people.length > 1 ? `${view.people.length} people` : 'Only you'}</span>
      <button type="button" title={askText} aria-label={askText} onClick={() => fillTeamSetupDraft(projectId)} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted">Ask</button>
      {view.can_switch ? <Switch aria-label="Team" disabled={busy} checked={team.on} onCheckedChange={on => void turn(on)} />
        : <span className="text-xs text-muted-foreground">{team.on ? 'On' : 'Off'}</span>}
    </div>
    {note && <p className="text-xs text-muted-foreground">{note}</p>}
    {!team.on && <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people.</p>}
    {team.on && <div className="divide-y divide-border">
      {view.people.map(person => <div key={person.user_id} className="flex items-center gap-2 py-1.5">
        <span aria-hidden="true" className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-full bg-muted text-[11px] font-semibold">{person.name.slice(0, 1).toUpperCase()}</span>
        <span className="min-w-0 flex-1 truncate">{person.name}{person.note && <span className="text-muted-foreground"> · {person.note}</span>}</span>
        {person.active_label && <span className="shrink-0 text-xs text-muted-foreground"><Dot on={person.active} />{person.active_label}</span>}
        {person.can_remove && <button type="button" disabled={busy} aria-label={`Remove ${person.name}`} title="Remove" onClick={() => void people({ remove: person.user_id })} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>}
      </div>)}
    </div>}
    {team.on && team.can_manage && <>
      <button type="button" disabled={busy} onClick={() => setAdding(!adding)} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Find a teammate</button>
      {adding && <div className="rounded-xl border border-border bg-popover">
        <input aria-label="Find a teammate" placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-2 text-xs" value={search} onChange={e => setSearch(e.target.value)} />
        <div className="max-h-48 overflow-auto">{directory.filter(p => !known.has(p.id) && `${p.username} ${p.email || ''}`.toLowerCase().includes(search.toLowerCase())).map(p => <button key={p.id} type="button" disabled={busy} onClick={() => { void people({ add: p.id }); setAdding(false) }} className="block w-full px-3 py-2 text-left text-xs hover:bg-muted">{shortName(p.username)}</button>)}</div>
      </div>}
    </>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

function FolderRow({ folder, target, server, canRemove, busy, onRemove }: { folder: string; target: TeamTarget; server?: TeamTabShare; canRemove: boolean; busy: boolean; onRemove: () => void }) {
  // On the owner's Mac its own folder's size; elsewhere what the server keeps of it.
  const size = useShareSize(target.connection, target.localProject, `path=${encodeURIComponent(folder)}`) || server
  return <div className="flex items-center gap-2 rounded-lg border border-border px-2 py-1.5 text-xs">
    <Folder className="h-3.5 w-3.5 shrink-0 text-amber-500" /><span className="min-w-0 flex-1 truncate">{folder}/</span>
    <span className="text-muted-foreground">{sizeLine(size)}</span>
    {canRemove && <button type="button" disabled={busy} aria-label={`Stop sharing ${folder}/`} title="Stop sharing this folder" onClick={onRemove} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>}
  </div>
}

/** What the team shares: Folders or the Whole Crew, changed from anywhere by whoever manages the Team (can_choose).
 * Picking a new folder reads the Crew's own folder, so for a Mac Crew it is in its owner's Mac app (mac_only). */
function SharesCard({ view, target }: { view: TeamTabView; target: TeamTarget }) {
  const { team } = view
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<Listing>()
  const onMac = Boolean(target.connection && target.localProject && view.mac?.yours)
  const choose = view.can_choose
  const pick = choose && onMac
  useEffect(() => {
    if (!picking || !pick) return
    let active = true
    void desktopRequest<Listing>(`/${encodeURIComponent(target.connection!)}/crew-teams/${encodeURIComponent(target.localProject!)}/folders?path=${encodeURIComponent(parent)}`)
      .then(data => { if (active) setListing(data) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [picking, parent, pick, target.connection, target.localProject])
  const localWhole = useShareSize(onMac ? target.connection : undefined, target.localProject, choose ? 'whole=1' : undefined)
  const whole = localWhole || view.whole
  const tooBig = Boolean(localWhole?.too_big)
  const folders = team.folder.folders || []
  const save = async (next: Team) => {
    setBusy(true); setError('')
    try { await saveTeam(target, next) } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  return <section aria-label="What the team shares" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">What the team shares</h3>
      {!choose ? <span className="text-xs text-muted-foreground">{team.folder.whole ? 'Whole Crew' : 'Folders'}</span>
        : <div role="radiogroup" aria-label="What the team shares" className="flex overflow-hidden rounded-md border border-border text-xs">
        {([[false, 'Folders'], [true, 'Whole Crew']] as const).map(([value, label]) => {
          const on = Boolean(team.folder.whole) === value
          return <button key={label} type="button" role="radio" aria-checked={on} disabled={busy || (value && tooBig)}
            onClick={() => { if (!on) void save({ ...team, folder: value ? { whole: true } : { folders: [] } }) }}
            className={`px-2.5 py-0.5 disabled:cursor-not-allowed ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted disabled:opacity-50'}`}>{label}</button>
        })}
      </div>}
    </div>
    {team.folder.whole ? <p className="text-xs text-muted-foreground">The whole <b className="text-foreground">{team.name}</b> folder{whole?.files ? `: ${sizeLine(whole)}` : ''}. Everyone in the Team reads and edits it.</p>
      : <div className="space-y-1.5">
        {folders.map(folder => <FolderRow key={folder} folder={folder} target={onMac ? target : { project: target.project }} server={view.shares?.find(share => share.path === folder)} canRemove={choose} busy={busy}
          onRemove={() => void save({ ...team, folder: { folders: folders.filter(f => f !== folder) } })} />)}
        {!folders.length && <p className="text-xs text-muted-foreground">Nothing is shared yet{choose ? ': add a folder, or share the Whole Crew' : ''}.</p>}
        {pick && <button type="button" disabled={busy} onClick={() => { setPicking(!picking); setParent('') }} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Share this folder</button>}
        {choose && !pick && view.mac_only && <p className="text-xs text-muted-foreground">{view.mac_only}</p>}
        {pick && tooBig && localWhole?.too_big_line && <p className="text-xs text-amber-600">The Whole Crew ({sizeLine(localWhole)}): {localWhole.too_big_line}</p>}
      </div>}
    {picking && pick && !team.folder.whole && <div className="space-y-2 rounded-lg bg-muted/40 p-3 text-xs">
      <div className="flex items-center gap-2"><button type="button" onClick={() => setParent(parent.split('/').slice(0, -1).join('/'))} className="text-primary">{parent ? '← Up' : 'Project'}</button><span>{parent}</span></div>
      {listing?.folders.map(folder => <button key={folder} type="button" onClick={() => setParent(folder)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted"><Folder className="h-3.5 w-3.5" />{folder.split('/').pop()}<ChevronRight className="ml-auto h-3 w-3" /></button>)}
      {parent && listing?.path === parent && <><p>{sizeLine(listing)}</p><Button size="sm" disabled={busy || folders.includes(parent)} onClick={() => { void save({ ...team, folder: { folders: [...folders, parent] } }); setPicking(false) }}>Share this folder</Button></>}
    </div>}
    <p className="text-xs text-muted-foreground">Not shared: your chats and memory, app files, passwords and keys.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

/** Every agent in the Team, as the server lists it: the person's own with In team / Only me, everyone else's as
 * theirs to change, and All agents in team for whoever may switch the Team. The same rows in the Mac app and on the
 * website. */
function AgentsCard({ state, view, target, projectId }: { state: TeamTabState; view: TeamTabView; target: TeamTarget; projectId: string }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const chooseAll = async (on: boolean) => {
    setBusy(true); setError('')
    try { await saveTeam(target, { ...view.team, agents_only_me: !on }) } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const choose = async (key: string, inTeam: boolean) => {
    setBusy(true); setError('')
    try { await teamRequest({ project: target.project, connection: target.connection }, '/agent-visibility', 'POST', { agent_key: key, in_team: inTeam }); window.dispatchEvent(new CustomEvent('agentworks:team-updated')) }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  return <section aria-label="Agents" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Agents</h3>
      {view.can_switch && <label className="flex items-center gap-2 text-[13px]"><span className="font-medium">All agents in team</span><Switch aria-label="All agents in team" checked={view.all_in_team} disabled={busy} onCheckedChange={on => void chooseAll(on)} /></label>}
    </div>
    {view.can_switch && <p className="text-xs text-muted-foreground">New agents join too, unless their person chose Only me.</p>}
    {view.agents.map(agent => <div key={agent.key} className="flex items-center gap-2 border-t border-border pt-2 text-[13px]">
      <span className="min-w-0 flex-1 truncate"><b>{agent.name}</b> <span className="text-muted-foreground">· {agent.detail}</span></span>
      {agent.place === 'mac' && <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">Mac</span>}
      {agent.can_change
        ? <div role="radiogroup" aria-label={`${agent.name} in the team`} className="flex shrink-0 overflow-hidden rounded-md border border-border text-xs">
          {([[true, 'In team'], [false, 'Only me']] as const).map(([value, label]) => {
            const on = agent.in_team === value
            return <button key={label} type="button" role="radio" aria-checked={on} disabled={busy} onClick={() => { if (!on) void choose(agent.key, value) }}
              className={`px-2 py-0.5 ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>
          })}
        </div>
        : <span className="shrink-0 text-xs text-muted-foreground">{agent.fixed}</span>}
      <AgentMenu agent={agent} state={state} projectId={projectId} />
    </div>)}
    {!view.agents.length && <p className="text-xs text-muted-foreground">No agents in the team yet.</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

/** ⋯ for one agent: open its tab here, or ask it from your own agent when it is someone else's. */
function AgentMenu({ agent, state, projectId }: { agent: TeamTabAgent; state: TeamTabState; projectId: string }) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [open])
  const tab = open ? Object.values(useChatStore.getState().chatTabs).find(item => item.metadata?.agentProfileProjectId === projectId && !item.metadata?.isViewOnly
    && teamAgentForTab(state, item.metadata?.agentProfileConversationKey, item.metadata?.agentProfileIsMainChat !== false, item.metadata?.agentProfileTabCanWrite !== false)?.key === agent.key) : undefined
  const ask = !agent.can_change && agent.address
  const item = 'block w-full rounded-lg px-2.5 py-1.5 text-left text-xs hover:bg-muted'
  return <div ref={box} className="relative shrink-0">
    <button type="button" aria-label={`More for ${agent.name}`} aria-expanded={open} onClick={() => setOpen(value => !value)} className="grid h-6 w-6 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"><MoreHorizontal className="h-3.5 w-3.5" /></button>
    {open && <div className="absolute right-0 top-full z-50 mt-1 w-44 rounded-xl border border-border bg-popover p-1 shadow-lg">
      {tab && <button type="button" className={item} onClick={() => { activateTab(tab.tabId); setOpen(false) }}>Open its chat</button>}
      {ask && <button type="button" className={item} onClick={() => { if (!fillAskFromMyAgentDraft(projectId, agent.owner_name, agent.name, agent.address)) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error'); setOpen(false) }}>Ask from my agent</button>}
      {!tab && !ask && <p className="px-2.5 py-1.5 text-xs text-muted-foreground">Its chat is not open here.</p>}
    </div>}
  </div>
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
