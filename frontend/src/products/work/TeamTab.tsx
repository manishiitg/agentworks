import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronRight, Folder, Loader2, MoreHorizontal, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { fillAskFromMyAgentDraft, sendToMain } from '../../utils/agentPurposeDraft'
import { openAppPage } from '../../utils/appNavigation'
import { activateTab } from '../../utils/activateTab'
import { changeTeamPeople, saveTeam, switchTeam, teamError, teamFolders, teamRequest, useTeamTab, type Team, type TeamFolderListing, type TeamTabAgent, type TeamTabState, type TeamTabView, type TeamTarget } from './teamApi'
import { TeamSyncPanel } from './TeamSyncPanel'
import { CrewWhereRow } from './CrewWorksElsewhere'
import { LentCard } from './LentCard'
import { AgentSharingRow, CopySetupLine, teamAgentChatTab } from './AgentSharingRow'

type Person = { id: string; username: string; email?: string }

const mb = (bytes: number) => `${(bytes / (1 << 20)).toFixed(bytes < 10 << 20 ? 1 : 0)} MB`
const sizeLine = (size?: { files: number; bytes: number }) => size ? `${size.files.toLocaleString()} ${size.files === 1 ? 'file' : 'files'} · ${mb(size.bytes)}` : ''
const card = 'space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm'
const shortName = (name: string) => name.split('@')[0] || name

/** The one saving pattern of these cards (owner, 6 Nov): only the clicked control shows it is working. */
export function Saving({ on, children }: { on: boolean; children: ReactNode }) {
  return on ? <Loader2 aria-label="Saving" className="h-3.5 w-3.5 shrink-0 animate-spin text-muted-foreground" /> : <>{children}</>
}

/** Which control of a card is saving (its key), and one call that runs a save under it and keeps its error. */
function useSave() {
  const [pending, setPending] = useState('')
  const [error, setError] = useState('')
  const run = async (key: string, save: () => Promise<unknown>) => {
    setPending(key); setError('')
    try { await save(); return true } catch (e) { setError(teamError(e)); return false } finally { setPending('') }
  }
  return { pending, error, setError, run }
}

const askTitle = 'Ask your AI agent how to set this up'
// Before Mac online pairs the Mac app's own Crew there is no server reply with the cards' messages yet.
const UNPAIRED_ASKS: Record<string, string> = {
  mac: 'Explain Mac online for this Crew: is it on, what can reach my Mac (website, phone, ChatGPT or Claude, AgentBridge), and which of my agents are public on AgentBridge. Help me change it.',
  publish: 'Help me publish this Crew to the server.',
}

/** The small Ask pill on every Access/Team card (owner, 6 Nov): sends the server's message for the card (or the
 * prompt given) to the viewer's own Main, which looks first and changes only when the person says so. */
export function AskButton({ projectId, card, prompt, label = 'Ask' }: { projectId: string; card?: string; prompt?: string; label?: string }) {
  const { view } = useTeamTab(projectId)
  const text = prompt || (card ? view?.asks?.[card] || UNPAIRED_ASKS[card] : '') || ''
  return <button type="button" title={askTitle} aria-label={label === 'Ask' ? askTitle : label} disabled={!text} className="shrink-0 rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted disabled:opacity-50"
    onClick={() => { if (!sendToMain(projectId, text)) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }}>{label}</button>
}

/** One read of the Crew's folders through the server (a size: path=…, or whole=1); undefined until it answers. */
function useFolderSize(target: TeamTarget, query?: string) {
  const [size, setSize] = useState<TeamFolderListing>()
  useEffect(() => {
    setSize(undefined)
    if (query === undefined) return
    let active = true
    void teamFolders(target, query).then(data => { if (active) setSize(data) }).catch(() => {})
    return () => { active = false }
  }, [target.project, target.connection, query]) // eslint-disable-line react-hooks/exhaustive-deps
  return size
}

/**
 * The Team tab (owner, 5 Nov, approved mockup "Single Team Tab"): the same tab for the owner and every teammate, in
 * the Mac app and on the website, for a Mac Crew and a Crew on the server. The server's /tab reply writes every row
 * and says what this person may change, and its /folders reads the Crew's folders where the Crew is; this only shows
 * them and sends the person's choices.
 */
export function TeamTab({ projectId, onTurnOn }: { projectId: string; onTurnOn?: () => void }) {
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
  const [note, setNote] = useState('')
  const { pending, error, setError, run } = useSave()
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
  }, [adding, target.connection, target.project]) // eslint-disable-line react-hooks/exhaustive-deps
  const people = (key: string, change: { add: string } | { remove: string }) => run(key, () => changeTeamPeople(target, change))
  const turn = (on: boolean) => run('team', async () => {
    setNote('')
    // The owner's own Mac app switches through its Mac, which applies the record at once.
    const result = await switchTeam(target, on, Boolean(view.can_switch && target.localProject))
    setNote(result.folder_created ? 'Made an empty team/ folder to share.' : '')
  })
  const known = new Set(view.people.map(person => person.user_id))
  return <section aria-label="Team" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Team</h3>
      <span className="text-xs text-muted-foreground">{team.on && view.people.length > 1 ? `${view.people.length} people` : 'Only you'}</span>
      <AskButton projectId={projectId} card="team" />
      {view.can_switch ? <><Saving on={pending === 'team'}>{null}</Saving><Switch aria-label="Team" disabled={Boolean(pending)} checked={team.on} onCheckedChange={on => void turn(on)} /></>
        : <span className="text-xs text-muted-foreground">{team.on ? 'On' : 'Off'}</span>}
    </div>
    {note && <p className="text-xs text-muted-foreground">{note}</p>}
    {!team.on && <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people.</p>}
    {team.on && <div className="divide-y divide-border">
      {view.people.map(person => <div key={person.user_id} className={`flex items-center gap-2 py-1.5 ${pending === `remove:${person.user_id}` ? 'opacity-60' : ''}`}>
        <span aria-hidden="true" className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-full bg-muted text-[11px] font-semibold">{person.name.slice(0, 1).toUpperCase()}</span>
        <span className="min-w-0 flex-1 truncate">{person.name}{person.note && <span className="text-muted-foreground"> · {person.note}</span>}</span>
        {person.active_label && <span className="shrink-0 text-xs text-muted-foreground"><Dot on={person.active} />{person.active_label}</span>}
        {person.can_remove && <Saving on={pending === `remove:${person.user_id}`}><button type="button" disabled={Boolean(pending)} aria-label={`Remove ${person.name}`} title="Remove" onClick={() => void people(`remove:${person.user_id}`, { remove: person.user_id })} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button></Saving>}
      </div>)}
    </div>}
    {team.on && team.can_manage && <>
      <span className="inline-flex items-center gap-2"><button type="button" disabled={Boolean(pending)} onClick={() => setAdding(!adding)} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Find a teammate</button><Saving on={pending === 'add'}>{null}</Saving></span>
      {adding && <div className="rounded-xl border border-border bg-popover">
        <input aria-label="Find a teammate" placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-2 text-xs" value={search} onChange={e => setSearch(e.target.value)} />
        <div className="max-h-48 overflow-auto">{directory.filter(p => !known.has(p.id) && `${p.username} ${p.email || ''}`.toLowerCase().includes(search.toLowerCase())).map(p => <button key={p.id} type="button" disabled={Boolean(pending)} onClick={() => { void people('add', { add: p.id }); setAdding(false) }} className="block w-full px-3 py-2 text-left text-xs hover:bg-muted">{shortName(p.username)}</button>)}</div>
      </div>}
    </>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

function FolderRow({ folder, target, canRemove, saving, disabled, onRemove }: { folder: string; target: TeamTarget; canRemove: boolean; saving: boolean; disabled: boolean; onRemove: () => void }) {
  const size = useFolderSize(target, `path=${encodeURIComponent(folder)}`)
  return <div className={`flex items-center gap-2 rounded-lg border border-border px-2 py-1.5 text-xs ${saving ? 'opacity-60' : ''}`}>
    <Folder className="h-3.5 w-3.5 shrink-0 text-amber-500" /><span className="min-w-0 flex-1 truncate">{folder}/</span>
    <span className="text-muted-foreground">{sizeLine(size)}</span>
    {canRemove && <Saving on={saving}><button type="button" disabled={disabled} aria-label={`Stop sharing ${folder}/`} title="Stop sharing this folder" onClick={onRemove} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button></Saving>}
  </div>
}

/** What the team shares: Folders or the Whole Crew. The Crew's owner switches it and stops sharing a folder
 * (can_choose; never the last one, can_remove_folder); anyone in the Team adds one with "+ Share this folder"
 * (can_add_folder), picked from the Crew's folders the server lists where the Crew is. While a Mac Crew's Mac is off
 * the server says so, and the folder is named instead (the server makes it when the Mac is back). */
function SharesCard({ view, target, projectId }: { view: TeamTabView; target: TeamTarget; projectId: string }) {
  const { team } = view
  const { pending, error, setError, run } = useSave()
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<TeamFolderListing>()
  const [name, setName] = useState('')
  const choose = view.can_choose
  useEffect(() => {
    if (!picking) return
    let active = true
    setListing(undefined)
    void teamFolders(target, `path=${encodeURIComponent(parent)}`).then(data => { if (active) setListing(data) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [picking, parent, target.project, target.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  const whole = useFolderSize(target, choose || team.folder.whole ? 'whole=1' : undefined)
  const tooBig = Boolean(whole?.too_big)
  const folders = team.folder.folders || []
  const save = (key: string, next: Team) => run(key, () => saveTeam(target, next))
  const add = async (folder: string) => {
    const clean = folder.trim().replace(/^\/+|\/+$/g, '')
    if (!clean || folders.includes(clean)) return
    if (await save('add', { ...team, folder: { folders: [...folders, clean] } })) { setPicking(false); setName('') }
  }
  return <section aria-label="What the team shares" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">What the team shares</h3>
      <AskButton projectId={projectId} card="shares" />
      {!choose ? <span className="text-xs text-muted-foreground">{team.folder.whole ? 'Whole Crew' : 'Folders'}</span>
        : <div role="radiogroup" aria-label="What the team shares" className="flex overflow-hidden rounded-md border border-border text-xs">
        {([[false, 'Folders'], [true, 'Whole Crew']] as const).map(([value, label]) => {
          const on = Boolean(team.folder.whole) === value
          return <button key={label} type="button" role="radio" aria-checked={on} disabled={Boolean(pending) || (value && tooBig)}
            onClick={() => { if (!on) void save(`share:${label}`, { ...team, folder: value ? { whole: true } : { folders: [] } }) }}
            className={`inline-flex items-center gap-1 px-2.5 py-0.5 disabled:cursor-not-allowed ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted disabled:opacity-50'}`}><Saving on={pending === `share:${label}`}>{null}</Saving>{label}</button>
        })}
      </div>}
    </div>
    {team.folder.whole ? <p className="text-xs text-muted-foreground">The whole <b className="text-foreground">{team.name}</b> folder{whole?.files ? `: ${sizeLine(whole)}` : ''}. Everyone in the Team reads and edits it.</p>
      : <div className="space-y-1.5">
        {folders.map(folder => <FolderRow key={folder} folder={folder} target={target} canRemove={view.can_remove_folder} saving={pending === `remove:${folder}`} disabled={Boolean(pending)}
          onRemove={() => void save(`remove:${folder}`, { ...team, folder: { folders: folders.filter(f => f !== folder) } })} />)}
        {!folders.length && <p className="text-xs text-muted-foreground">Nothing is shared yet{choose ? ': add a folder, or share the Whole Crew' : ''}.</p>}
        {view.can_add_folder && <button type="button" disabled={Boolean(pending)} title="Choose a folder of this Crew to share with the Team"
          onClick={() => { setPicking(!picking); setParent('') }} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Share this folder</button>}
        {choose && tooBig && whole?.too_big_line && <p className="text-xs text-amber-600">The Whole Crew ({sizeLine(whole)}): {whole.too_big_line}</p>}
      </div>}
    {picking && !team.folder.whole && <div className="space-y-2 rounded-lg bg-muted/40 p-3 text-xs">
      {listing?.offline ? <>
        <p>{listing.offline}</p>
        <form className="flex items-center gap-2" onSubmit={e => { e.preventDefault(); void add(name) }}>
          <input aria-label="Folder name" placeholder="Folder name" value={name} onChange={e => setName(e.target.value)} className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 text-xs" />
          <Button size="sm" type="submit" disabled={Boolean(pending) || !name.trim()}><Saving on={pending === 'add'}>{null}</Saving>Share this folder</Button>
        </form>
      </> : <>
        <div className="flex items-center gap-2"><button type="button" onClick={() => setParent(parent.split('/').slice(0, -1).join('/'))} className="text-primary">{parent ? '← Up' : 'Project'}</button><span>{parent}</span></div>
        {!listing && <Loader2 aria-label="Loading folders" className="h-3.5 w-3.5 animate-spin text-muted-foreground" />}
        {listing?.folders.map(folder => <button key={folder} type="button" onClick={() => setParent(folder)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted"><Folder className="h-3.5 w-3.5" />{folder.split('/').pop()}<ChevronRight className="ml-auto h-3 w-3" /></button>)}
        {parent && listing?.path === parent && <>
          <p>{sizeLine(listing)}{listing.too_big_line ? ` · ${listing.too_big_line}` : ''}</p>
          <Button size="sm" disabled={Boolean(pending) || folders.includes(parent)} onClick={() => void add(parent)}><Saving on={pending === 'add'}>{null}</Saving>Share this folder</Button>
        </>}
      </>}
    </div>}
    <p className="text-xs text-muted-foreground">Not shared: your chats and memory, app files, passwords and keys.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

/** The one agent sharing list (owner, 6 Nov): every agent in the Team as the server lists it, the person's own with a
 * Shared switch, its purpose and its permissions; everyone else's as theirs to change. The same rows in the Mac app
 * and on the website. */
function AgentsCard({ state, view, target, projectId }: { state: TeamTabState; view: TeamTabView; target: TeamTarget; projectId: string }) {
  const [opened, setOpened] = useState('')
  const { pending, error, run } = useSave()
  const visibility = (key: string, inTeam: boolean) => run(key === '*' ? 'all' : `agent:${key}`, async () => {
    await teamRequest({ project: target.project, connection: target.connection }, '/agent-visibility', 'POST', { agent_key: key, in_team: inTeam })
    window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  })
  return <section aria-label="Agents" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Agents</h3>
      <AskButton projectId={projectId} card="agents" />
    </div>
    {view.can_all_mine && <div className="flex items-center gap-2 text-[13px]"><span className="flex-1 font-medium">Share all my agents</span><Saving on={pending === 'all'}>{null}</Saving><Switch aria-label="Share all my agents" checked={view.all_in_team} disabled={Boolean(pending)} onCheckedChange={on => void visibility('*', on)} /></div>}
    {view.can_all_mine && <p className="text-xs text-muted-foreground">{view.agents_line || 'Your new agents are shared too, unless you turn sharing off for them. Main is always shared.'}</p>}
    {view.agents.map(agent => <div key={agent.key} className="border-t border-border pt-2">
      <div className="flex items-center gap-2 text-[13px]">
        <span className="min-w-0 flex-1 truncate"><b>{agent.name}</b> <span className="text-muted-foreground">· {agent.detail}</span></span>
        {(agent.place_label || agent.place === 'mac') && <span className={`shrink-0 rounded-full px-2 py-0.5 text-[11px] ${agent.place_server ? 'bg-sky-500/10 text-sky-700 dark:text-sky-300' : 'bg-muted text-muted-foreground'}`}>{agent.place_label || 'Mac'}</span>}
        {agent.in_team && <button type="button" aria-expanded={opened === agent.key} onClick={() => setOpened(opened === agent.key ? '' : agent.key)} className="shrink-0 rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted">Permissions</button>}
        {agent.can_change || agent.can_edit_rule
          ? <span className="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground" title={agent.can_change ? undefined : agent.fixed}>
            Shared<Saving on={pending === `agent:${agent.key}`}>{null}</Saving>
            <Switch aria-label={`${agent.name}: shared`} checked={agent.in_team} disabled={!agent.can_change || Boolean(pending)} onCheckedChange={on => void visibility(agent.key, on)} />
          </span>
          : <span className="shrink-0 text-xs text-muted-foreground">{agent.fixed}</span>}
        <AgentMenu agent={agent} state={state} projectId={projectId} />
      </div>
      <AgentSharingRow agent={agent} state={state} projectId={projectId} />
      {agent.in_team && agent.permissions_line && <p className="ml-0.5 text-xs text-muted-foreground">{agent.permissions_line}</p>}
      {opened === agent.key && agent.in_team && <AgentDetails agent={agent} target={target} projectId={projectId} />}
    </div>)}
    {!view.agents.length && <p className="text-xs text-muted-foreground">No agents in the team yet.</p>}
    <CopySetupLine crewName={view.team.name} />
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

/** One agent's permissions (owner, 5 Nov mockup "When others ask"): what it uses, as the server lists it (the person's
 * own sign-ins marked yours, sent only to them), and what it does when someone else asks. Its own person edits the rule
 * and Answer only / Make changes; everyone else reads the server's summary line. */
function AgentDetails({ agent, target, projectId }: { agent: TeamTabAgent; target: TeamTarget; projectId: string }) {
  const [rule, setRule] = useState(agent.when_others_ask || '')
  const { pending, error, run } = useSave()
  useEffect(() => { setRule(agent.when_others_ask || '') }, [agent.when_others_ask])
  const save = (key: string, change: { when_others_ask?: string; can_make_changes?: boolean }) => run(key, async () => {
    await teamRequest({ project: target.project, connection: target.connection }, '/agent-rule', 'POST', { agent_key: agent.key, ...change })
    window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  })
  const label = 'mb-1 mt-2.5 text-xs text-muted-foreground'
  const uses = agent.uses || []
  return <div className="mb-1 mt-2 rounded-lg border border-border bg-muted/30 px-3 py-2.5 text-[13px]">
    <div className="flex items-center gap-2">
      <p className="flex-1 text-xs text-muted-foreground">Uses</p>
      {agent.permissions_prompt && <AskButton projectId={projectId} prompt={agent.permissions_prompt} label="Ask AI to set this up" />}
    </div>
    <div className="mt-1 flex flex-wrap gap-1.5">
      {uses.map(use => <span key={`${use.kind}:${use.name}`} className="rounded-full border border-border px-2 py-px text-xs">{use.name}{use.personal ? ' · yours' : ''}</span>)}
      {!uses.length && <span className="text-xs text-muted-foreground">No apps or secrets.</span>}
    </div>
    <p className={label}>{agent.can_edit_rule ? 'When someone else asks' : 'When you ask it'}</p>
    <p className="rounded-md bg-muted/60 px-2.5 py-2 text-[12.5px] text-muted-foreground">{agent.when_others_line}</p>
    {agent.can_edit_rule && <>
      <p className={`${label} flex items-center gap-1.5`}>Your extra rule (optional)<Saving on={pending === 'rule'}>{null}</Saving></p>
      <textarea aria-label={`${agent.name}: your extra rule when others ask`} value={rule} maxLength={1000} disabled={Boolean(pending)} placeholder="e.g. tester-b may see the website files; everyone else gets answers only."
        onChange={e => setRule(e.target.value)} onBlur={() => { if (rule.trim() !== (agent.when_others_ask || '')) void save('rule', { when_others_ask: rule }) }}
        className="min-h-[58px] w-full rounded-md border border-border bg-background px-2 py-1.5 text-[13px]" />
      <p className="mt-1 text-xs text-muted-foreground">Added only when someone else asks; the agent knows who is asking. It follows it; it isn't a lock.</p>
      {agent.can_make_changes !== undefined && <>
        <p className={label}>When someone else asks, it can</p>
        <div role="radiogroup" aria-label={`${agent.name} when someone else asks`} className="flex w-max overflow-hidden rounded-md border border-border text-xs">
          {([[false, 'Answer only'], [true, 'Make changes']] as const).map(([value, text]) => {
            const on = agent.can_make_changes === value
            return <button key={text} type="button" role="radio" aria-checked={on} disabled={Boolean(pending)} onClick={() => { if (!on) void save(`can:${text}`, { can_make_changes: value }) }}
              className={`inline-flex items-center gap-1 px-2 py-0.5 ${on ? 'bg-primary font-medium text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}><Saving on={pending === `can:${text}`}>{null}</Saving>{text}</button>
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
  const tab = open ? teamAgentChatTab(state, projectId, agent.key) : undefined
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
  const { pending, error, run } = useSave()
  const leave = () => run('leave', async () => {
    await teamRequest({ project: target.project, connection: target.connection }, '/leave', 'POST', {})
    openAppPage('/home')
  })
  return <div className="space-y-1">
    <Button variant="outline" disabled={Boolean(pending)} onClick={() => void leave()}><Saving on={pending === 'leave'}>{null}</Saving>Leave Team</Button>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}
