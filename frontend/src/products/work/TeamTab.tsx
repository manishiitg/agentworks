import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronRight, Folder, MoreHorizontal, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { fillAccessAskDraft, fillAskFromMyAgentDraft, fillWhenOthersAskDraft, type AccessAskCard } from '../../utils/agentPurposeDraft'
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

const askTitle = 'Ask your AI agent how to set this up'

/** The small Ask pill at the top right of every Access/Team card (owner, 5 Nov): opens the viewer's own Main with the
 * card's request in its chat box, not sent. */
export function AskButton({ projectId, card, agentName }: { projectId: string; card: AccessAskCard; agentName?: string }) {
  // An agent's details ask about that agent's "When others ask" instead of the card's request.
  const fill = () => agentName === undefined ? fillAccessAskDraft(projectId, card) : fillWhenOthersAskDraft(projectId, agentName)
  return <button type="button" title={askTitle} aria-label={agentName === undefined ? askTitle : `${askTitle}: ${agentName}`} className="shrink-0 rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted"
    onClick={() => { if (!fill()) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>{agentName === undefined ? 'Ask' : 'Ask AI to set this up'}</button>
}

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
      <SharesCard view={view} target={target} projectId={projectId} />
      <AgentsCard state={state} view={view} target={target} projectId={projectId} />
      {team.lent && <LentCard lent={team.lent} target={target} ask={<AskButton projectId={projectId} card="lent" />} />}
      {view.mac && <section aria-label="Mac online" className={card}>
        <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">Mac online</h3><AskButton projectId={projectId} card="mac" /></div>
        {view.mac.yours && macSwitch
          ? <label className="flex items-center justify-between gap-3"><span className="text-[12.5px]">{view.mac.line}</span>{macSwitch}</label>
          : <p className="text-[12.5px]"><Dot on={view.mac.on} />{view.mac.line} · <span className="text-muted-foreground">{view.mac.note}</span></p>}
      </section>}
      {team.folder_access && <section aria-label="Sync" className={card}>
        <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">Sync</h3><AskButton projectId={projectId} card="sync" /></div>
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
  const known = new Set(view.people.map(person => person.user_id))
  return <section aria-label="Team" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Team</h3>
      <span className="text-xs text-muted-foreground">{team.on && view.people.length > 1 ? `${view.people.length} people` : 'Only you'}</span>
      <AskButton projectId={projectId} card="team" />
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

/** What the team shares: Folders or the Whole Crew. The Crew's owner switches it and stops sharing a folder
 * (can_choose); anyone in the Team adds a folder by name (can_add_folder), which the server makes when missing. The
 * owner's Mac app also picks from the Mac's folders. */
function SharesCard({ view, target, projectId }: { view: TeamTabView; target: TeamTarget; projectId: string }) {
  const { team } = view
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<Listing>()
  const [newFolder, setNewFolder] = useState('')
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
      <AskButton projectId={projectId} card="shares" />
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
        {view.can_add_folder && !pick && <form className="flex items-center gap-2" onSubmit={event => {
          event.preventDefault()
          const name = newFolder.trim().replace(/^\/+|\/+$/g, '')
          if (!name || folders.includes(name)) return
          void save({ ...team, folder: { folders: [...folders, name] } }).then(() => setNewFolder(''))
        }}>
          <input aria-label="Folder to share" value={newFolder} onChange={event => setNewFolder(event.target.value)} placeholder="Add a folder to share, such as notes" className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 text-xs" />
          <Button size="sm" type="submit" disabled={busy || !newFolder.trim()}>Add</Button>
        </form>}
        {pick && <button type="button" disabled={busy} onClick={() => { setPicking(!picking); setParent('') }} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Share this folder</button>}
        {view.can_add_folder && !pick && view.mac_only && <p className="text-xs text-muted-foreground">{view.mac_only}</p>}
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
  const [opened, setOpened] = useState('')
  // The viewer's own All my agents in team (owner, 5 Nov): it never changes anyone else's agents.
  const chooseAll = async (on: boolean) => {
    setBusy(true); setError('')
    try { await teamRequest({ project: target.project, connection: target.connection }, '/agent-visibility', 'POST', { agent_key: '*', in_team: on }); window.dispatchEvent(new CustomEvent('agentworks:team-updated')) }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const choose = async (key: string, inTeam: boolean) => {
    setBusy(true); setError('')
    try { await teamRequest({ project: target.project, connection: target.connection }, '/agent-visibility', 'POST', { agent_key: key, in_team: inTeam }); window.dispatchEvent(new CustomEvent('agentworks:team-updated')) }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  return <section aria-label="Agents" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Agents</h3>
      <AskButton projectId={projectId} card="agents" />
    </div>
    {view.can_all_mine && <label className="flex items-center gap-2 text-[13px]"><span className="flex-1 font-medium">All my agents in team</span><Switch aria-label="All my agents in team" checked={view.all_in_team} disabled={busy} onCheckedChange={on => void chooseAll(on)} /></label>}
    {view.can_all_mine && <p className="text-xs text-muted-foreground">Your new agents join too, unless you choose Only me. Main is always in team.</p>}
    {view.agents.map(agent => <div key={agent.key} className="border-t border-border pt-2">
    <div className="flex items-center gap-2 text-[13px]">
      <button type="button" aria-label={`${opened === agent.key ? 'Hide' : 'Show'} details for ${agent.name}`} aria-expanded={opened === agent.key} onClick={() => setOpened(opened === agent.key ? '' : agent.key)} className="grid h-5 w-5 shrink-0 place-items-center rounded text-muted-foreground hover:bg-muted hover:text-foreground">
        <ChevronRight className={`h-3.5 w-3.5 transition-transform ${opened === agent.key ? 'rotate-90' : ''}`} />
      </button>
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
    </div>
    {opened === agent.key && <AgentDetails agent={agent} target={target} projectId={projectId} />}
    </div>)}
    {!view.agents.length && <p className="text-xs text-muted-foreground">No agents in the team yet.</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

/** One agent's details (owner, 5 Nov mockup "When others ask"): what it uses, as the server lists it (the person's own
 * sign-ins marked yours, sent only to them), and what it does when someone else asks. Its own person edits the rule
 * and Answer only / Make changes; everyone else reads the server's summary line. */
function AgentDetails({ agent, target, projectId }: { agent: TeamTabAgent; target: TeamTarget; projectId: string }) {
  const [rule, setRule] = useState(agent.when_others_ask || '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => { setRule(agent.when_others_ask || '') }, [agent.when_others_ask])
  const save = async (change: { when_others_ask?: string; can_make_changes?: boolean }) => {
    setBusy(true); setError('')
    try { await teamRequest({ project: target.project, connection: target.connection }, '/agent-rule', 'POST', { agent_key: agent.key, ...change }); window.dispatchEvent(new CustomEvent('agentworks:team-updated')) }
    catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const label = 'mb-1 mt-2.5 text-xs text-muted-foreground'
  const uses = agent.uses || []
  return <div className="mb-1 ml-7 mt-2 rounded-lg border border-border bg-muted/30 px-3 py-2.5 text-[13px]">
    <div className="flex items-center gap-2">
      <p className="flex-1 text-xs text-muted-foreground">Uses</p>
      {agent.can_edit_rule && <AskButton projectId={projectId} card="agents" agentName={agent.name} />}
    </div>
    <div className="mt-1 flex flex-wrap gap-1.5">
      {uses.map(use => <span key={`${use.kind}:${use.name}`} className="rounded-full border border-border px-2 py-px text-xs">{use.name}{use.personal ? ' · yours' : ''}</span>)}
      {!uses.length && <span className="text-xs text-muted-foreground">No apps or secrets.</span>}
    </div>
    <p className={label}>{agent.can_edit_rule ? 'When someone else asks' : 'When you ask it'}</p>
    <p className="rounded-md bg-muted/60 px-2.5 py-2 text-[12.5px] text-muted-foreground">{agent.when_others_line}</p>
    {agent.can_edit_rule && <>
      <p className={label}>Your extra rule (optional)</p>
      <textarea aria-label={`${agent.name}: your extra rule when others ask`} value={rule} maxLength={1000} disabled={busy} placeholder="e.g. Only answer questions about the website project, not billing."
        onChange={e => setRule(e.target.value)} onBlur={() => { if (rule.trim() !== (agent.when_others_ask || '')) void save({ when_others_ask: rule }) }}
        className="min-h-[58px] w-full rounded-md border border-border bg-background px-2 py-1.5 text-[13px]" />
      <p className="mt-1 text-xs text-muted-foreground">Added only when someone else asks. The agent follows it; it isn't a lock.</p>
      {agent.can_make_changes !== undefined && <>
        <p className={label}>When someone else asks, it can</p>
        <div role="radiogroup" aria-label={`${agent.name} when someone else asks`} className="flex w-max overflow-hidden rounded-md border border-border text-xs">
          {([[false, 'Answer only'], [true, 'Make changes']] as const).map(([value, text]) => {
            const on = agent.can_make_changes === value
            return <button key={text} type="button" role="radio" aria-checked={on} disabled={busy} onClick={() => { if (!on) void save({ can_make_changes: value }) }}
              className={`px-2 py-0.5 ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{text}</button>
          })}
        </div>
      </>}
    </>}
    {error && <p role="alert" className="mt-1 text-xs text-destructive">{error}</p>}
  </div>
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
