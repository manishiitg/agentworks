import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react'
import { ChevronRight, Folder, Loader2, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { sendToMain } from '../../utils/agentPurposeDraft'
import { openAppPage } from '../../utils/appNavigation'
import ModalPortal from '../../components/ui/ModalPortal'
import { changeCrewPerson, crewPeople, saveTeam, switchTeam, teamError, teamFailure, teamFolderChange, teamFolders, teamRequest, useTeamTab, type CrewPeopleChange, type CrewPeopleLevelKey, type CrewPeoplePerson, type CrewPeoplePreview, type CrewPeopleView, type Team, type TeamFolderChange, type TeamFolderListing, type TeamTabAgent, type TeamTabView, type TeamTarget } from './teamApi'
import { TeamSyncPanel } from './TeamSyncPanel'
const CrewFunctionsView = lazy(() => import('../../components/automation/CrewFunctionsView'))
import { CrewWhereRow } from './CrewWorksElsewhere'
import { LentCard } from './LentCard'
import { TeamFolderChangeDialog } from './TeamFolderChangeDialog'

type Person = { id: string; username: string; email?: string }

const mb = (bytes: number) => `${(bytes / (1 << 20)).toFixed(bytes < 10 << 20 ? 1 : 0)} MB`
const sizeLine = (size?: { files: number; bytes: number }) => size ? `${size.files.toLocaleString()} ${size.files === 1 ? 'file' : 'files'} · ${mb(size.bytes)}` : ''
export const teamCard = 'space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm'
const card = teamCard
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
    try { await save(); return true } catch (e) { setError(teamFailure(e).message); return false } finally { setPending('') }
  }
  return { pending, error, setError, run }
}

const askTitle = 'Ask your AI agent how to set this up'
// Before Mac online pairs the Mac app's own Crew there is no server reply with the cards' messages yet.
const UNPAIRED_ASKS: Record<string, string> = {
  mac: 'Explain Mac online for this Crew: is it on, and what turning it on lets me do from the website, my phone and my other apps. Help me turn it on.',
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
  if (state.unbound) return <section aria-label="People" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">People</h3>
      <span className="text-xs text-muted-foreground">Only you</span>
      {onTurnOn && <Switch aria-label="Team" checked={false} onCheckedChange={on => { if (on) onTurnOn() }} />}
    </div>
    <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people; it also turns on Mac online.</p>
    <NeverOnlinePeople projectId={projectId} />
  </section>
  if (!view || !target) return error ? <p role="alert" className="text-xs text-muted-foreground">{error}</p> : <p role="status" className="text-sm text-muted-foreground">Loading Team…</p>
  const { team } = view
  return <div className="space-y-3">
    {team.on && <p className="px-1 text-[13px] text-muted-foreground">{view.intro}</p>}
    <PeopleCard view={view} target={target} projectId={projectId} />
    {team.on && <>
      <CrewWhereRow projectId={projectId} />
      <SharesCard view={view} target={target} projectId={projectId} />
      <AgentsCard view={view} target={target} projectId={projectId} />
      <FunctionsCard projectId={projectId} />
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

/** A Crew on the Mac that was never online has no server record, so only the Mac app's own server can say why its
 * people list is closed: one greyed line, the server's. */
function NeverOnlinePeople({ projectId }: { projectId: string }) {
  const [line, setLine] = useState('')
  useEffect(() => {
    let active = true
    void crewPeople({ project: projectId }).then(data => { if (active) setLine(data.blocked || '') }).catch(() => {})
    return () => { active = false }
  }, [projectId])
  return line ? <p className="text-xs text-muted-foreground opacity-80">{line}</p> : null
}

const changeLabel = (to: CrewPeopleChange['level'], from: string) => to === 'none' ? 'Remove' : from === 'none' ? 'Add' : 'Move'

/** The confirm box for one person's change (owner, 6 Nov, same kind as the Whole Crew box): the server's summary, what
 * they gain and lose, and its notes. Nothing is saved until the confirm button. needsDashboard is the server's plain
 * refusal when the Crew has no dashboard yet, with the one button that creates the standard one. */
function PeopleChangeDialog({ ask, saving, error, onCancel, onConfirm }: { ask: { name: string; level: CrewPeopleChange['level']; preview: CrewPeoplePreview; needsDashboard?: string }; saving: boolean; error: string; onCancel: () => void; onConfirm: (createDashboard: boolean) => void }) {
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape' && !saving) onCancel() }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [saving, onCancel])
  const { preview } = ask
  const label = 'mb-1.5 mt-3.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'
  return <ModalPortal>
    <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/50 p-4" onClick={() => { if (!saving) onCancel() }}>
      <div role="dialog" aria-modal="true" aria-label={preview.summary} onClick={event => event.stopPropagation()} className="w-full max-w-md rounded-xl border border-border bg-card p-5 text-sm shadow-md">
        <div className="flex items-start gap-2">
          <h3 className="flex-1 text-[15px] font-semibold">{preview.summary}</h3>
          <button type="button" aria-label="Close" disabled={saving} onClick={onCancel} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        {preview.gains.length > 0 && <>
          <p className={label}>They get</p>
          <ul className="list-disc space-y-1 pl-5 text-[13px]">{preview.gains.map(line => <li key={line}>{line}</li>)}</ul>
        </>}
        {preview.loses.length > 0 && <>
          <p className={label}>They lose</p>
          <ul className="list-disc space-y-1 pl-5 text-[13px]">{preview.loses.map(line => <li key={line}>{line}</li>)}</ul>
        </>}
        {preview.notes.map(line => <p key={line} className="mt-3.5 rounded-lg bg-muted/60 px-3 py-2 text-xs text-muted-foreground">{line}</p>)}
        {ask.needsDashboard && <p role="alert" className="mt-3.5 rounded-lg bg-amber-500/10 px-3 py-2 text-[13px]">{ask.needsDashboard}</p>}
        {error && <p role="alert" className="mt-3.5 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" disabled={saving} onClick={onCancel}>Cancel</Button>
          {ask.needsDashboard
            ? <Button size="sm" disabled={saving} onClick={() => onConfirm(true)}>{saving && <Loader2 aria-label="Saving" className="animate-spin" />}Create a standard dashboard and add them</Button>
            : <Button size="sm" disabled={saving} onClick={() => onConfirm(false)}>{saving && <Loader2 aria-label="Saving" className="animate-spin" />}{changeLabel(ask.level, preview.from)}</Button>}
        </div>
      </div>
    </div>
  </ModalPortal>
}

/** The People card (owner, 6 Nov, approved mockup): the Team switch, and one list of everyone with their level, as the
 * server's /people writes it (Works with me is the Team, Dashboards only is the Crew's Share). Adding someone or moving
 * them between levels always asks first with what they gain or lose; the server keeps both records and every line. */
function PeopleCard({ view, target, projectId }: { view: TeamTabView; target: TeamTarget; projectId: string }) {
  const { team } = view
  const [people, setPeople] = useState<CrewPeopleView>()
  const [directory, setDirectory] = useState<Person[]>([])
  const [adding, setAdding] = useState(false)
  const [search, setSearch] = useState('')
  const [chosen, setChosen] = useState<Person>()
  const [level, setLevel] = useState<CrewPeopleLevelKey>('works')
  const [note, setNote] = useState('')
  const [ask, setAsk] = useState<{ person: string; name: string; level: CrewPeopleChange['level']; preview: CrewPeoplePreview; needsDashboard?: string }>()
  const [askError, setAskError] = useState('')
  const { pending, error, setError, run } = useSave()
  useEffect(() => {
    let active = true
    const load = () => void crewPeople(target).then(data => { if (active) setPeople(data) }).catch(e => { if (active) setError(teamFailure(e).message) })
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [target.project, target.connection]) // eslint-disable-line react-hooks/exhaustive-deps
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
  // One change: first the server says what the person gains and loses; only a yes in the box saves it.
  const propose = (person: string, name: string, to: CrewPeopleChange['level']) => run(`change:${person}`, async () => {
    setNote(''); setAskError('')
    const reply = await changeCrewPerson(target, { person, level: to })
    if (reply.confirm_needed && reply.preview) setAsk({ person, name, level: to, preview: reply.preview })
    else setNote(reply.note || '')
  })
  const apply = (createDashboard: boolean) => run('confirm', async () => {
    if (!ask) return
    setAskError('')
    try {
      const reply = await changeCrewPerson(target, { person: ask.person, level: ask.level, confirm: true, create_dashboard: createDashboard })
      setAsk(undefined); setChosen(undefined); setAdding(false); setSearch(''); setNote(reply.note || '')
    } catch (cause) {
      const failure = teamFailure(cause)
      if (failure.code === 'dashboard_needed') setAsk({ ...ask, needsDashboard: failure.message }); else setAskError(failure.message)
    }
  })
  const turn = (on: boolean) => run('team', async () => {
    setNote('')
    // The owner's own Mac app switches through its Mac, which applies the record at once.
    const result = await switchTeam(target, on, Boolean(view.can_switch && target.localProject))
    setNote(result.folder_created ? 'Made an empty team/ folder to share.' : '')
  })
  const everyone = people ? [people.owner, ...people.people] : []
  const known = new Set(everyone.map(person => person.user_id))
  const levelOf = (key: CrewPeopleLevelKey) => people?.levels.find(item => item.key === key)
  const live = (id: string) => view.people.find(person => person.user_id === id)
  const closed = Boolean(people?.blocked)
  const personRow = (person: CrewPeoplePerson) => {
    const activity = live(person.user_id)
    const options = (person.moves || []).map(move => move === 'none' ? { key: move, label: 'Remove from this Crew', off: false, why: '' } : { key: move, label: levelOf(move)?.label || move, off: levelOf(move)?.available === false, why: levelOf(move)?.why || '' })
    return <div key={person.user_id} className={`flex items-start gap-2 py-2 ${pending === `change:${person.user_id}` ? 'opacity-60' : ''}`}>
      <span aria-hidden="true" className="mt-0.5 grid h-[22px] w-[22px] shrink-0 place-items-center rounded-full bg-muted text-[11px] font-semibold">{person.name.slice(0, 1).toUpperCase()}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate">{person.name}{person.you && <span className="text-muted-foreground"> · you</span>}{person.invited && <span className="text-muted-foreground"> · invited</span>}</span>
        <span className="block text-xs text-muted-foreground">{person.line}</span>
      </span>
      {activity?.active_label && <span className="mt-0.5 shrink-0 text-xs text-muted-foreground"><Dot on={activity.active} />{activity.active_label}</span>}
      {options.length > 0 && !closed
        ? <span className="inline-flex shrink-0 items-center gap-1.5"><Saving on={pending === `change:${person.user_id}`}>{null}</Saving>
          <select aria-label={`${person.name}: how they use this Crew`} disabled={Boolean(pending)} value={person.level} onChange={event => { const next = event.target.value as CrewPeopleChange['level']; if (next !== person.level) void propose(person.user_id, person.name, next) }}
            className="rounded-md border border-border bg-background px-2 py-0.5 text-xs">
            <option value={person.level}>{person.level_label}</option>
            {options.map(option => <option key={option.key} value={option.key} disabled={option.off} title={option.why || undefined}>{option.label}</option>)}
          </select></span>
        : <span className="mt-0.5 shrink-0 text-xs text-muted-foreground">{person.level_label}</span>}
    </div>
  }
  return <section aria-label="People" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">People</h3>
      <span className="text-xs text-muted-foreground">{team.on && view.people.length > 1 ? `${view.people.length} people` : 'Only you'}</span>
      <AskButton projectId={projectId} card="team" />
      {view.can_switch ? <><Saving on={pending === 'team'}>{null}</Saving><Switch aria-label="Team" disabled={Boolean(pending)} checked={team.on} onCheckedChange={on => void turn(on)} /></>
        : <span className="text-xs text-muted-foreground">{team.on ? 'On' : 'Off'}</span>}
    </div>
    {note && <p className="text-xs text-muted-foreground">{note}</p>}
    {!team.on && <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people.</p>}
    {people && <>
      <p className="text-xs text-muted-foreground">{people.intro}</p>
      {people.blocked && <p className="text-xs text-muted-foreground">{people.blocked}</p>}
      <div className={`divide-y divide-border ${closed ? 'opacity-60' : ''}`}>{everyone.map(personRow)}</div>
      {people.put_online && <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground"><span className="min-w-0 flex-1">{people.put_online}</span>
        <Button size="sm" variant="outline" className="h-7 rounded-full px-3" onClick={() => window.dispatchEvent(new CustomEvent('agentworks:open-crew-access', { detail: { projectId, section: 'mac' } }))}>Put it online</Button></div>}
      {people.can_add && !closed && <>
        <span className="inline-flex items-center gap-2"><button type="button" disabled={Boolean(pending)} onClick={() => { setAdding(!adding); setChosen(undefined) }} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Add a person</button></span>
        {adding && !chosen && <div className="rounded-xl border border-border bg-popover">
          <input aria-label="Find a person" placeholder="Find a person…" className="w-full border-b border-border bg-transparent px-3 py-2 text-xs" value={search} onChange={e => setSearch(e.target.value)} />
          {people.add_everyone && people.add_everyone.toLowerCase().includes(search.toLowerCase()) && <button type="button" disabled={Boolean(pending)} onClick={() => void propose('everyone', people.add_everyone || '', 'dashboards')} className="block w-full border-b border-border px-3 py-2 text-left text-xs font-medium hover:bg-muted">{people.add_everyone}</button>}
          <div className="max-h-48 overflow-auto">{directory.filter(p => !known.has(p.id) && `${p.username} ${p.email || ''}`.toLowerCase().includes(search.toLowerCase())).map(p => <button key={p.id} type="button" disabled={Boolean(pending)} onClick={() => { setChosen(p); setLevel(levelOf('works')?.available ? 'works' : 'dashboards') }} className="block w-full px-3 py-2 text-left text-xs hover:bg-muted">{shortName(p.username)}</button>)}</div>
        </div>}
        {adding && chosen && <div role="radiogroup" aria-label={`How ${shortName(chosen.username)} uses this Crew`} className="space-y-2 rounded-xl border border-border p-3 text-xs">
          <p className="font-medium">How should {shortName(chosen.username)} use this Crew?</p>
          {people.levels.map(item => <label key={item.key} className={`flex items-start gap-2 ${item.available ? '' : 'opacity-60'}`}>
            <input type="radio" name={`level-${projectId}`} checked={level === item.key} disabled={!item.available || Boolean(pending)} onChange={() => setLevel(item.key)} className="mt-0.5" />
            <span><span className="block font-medium">{item.label}</span><span className="block text-muted-foreground">{item.available ? item.line : item.why}</span></span>
          </label>)}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="outline" disabled={Boolean(pending)} onClick={() => setChosen(undefined)}>Back</Button>
            <Button size="sm" disabled={Boolean(pending) || !levelOf(level)?.available} onClick={() => void propose(chosen.id, shortName(chosen.username), level)}><Saving on={pending === `change:${chosen.id}`}>{null}</Saving>Continue</Button>
          </div>
        </div>}
      </>}
      {people.gets.length > 0 && <details className="text-xs text-muted-foreground">
        <summary className="cursor-pointer">What each level gets</summary>
        <table className="mt-1.5 w-full text-left"><thead><tr><th className="py-0.5 pr-2 font-medium" /><th className="py-0.5 pr-2 font-medium">Dashboards only</th><th className="py-0.5 font-medium">Works with me</th></tr></thead>
          <tbody>{people.gets.map(row => <tr key={row.label} className="border-t border-border"><td className="py-0.5 pr-2">{row.label}</td><td className="py-0.5 pr-2">{row.dashboards_only}</td><td className="py-0.5">{row.works_with_me}</td></tr>)}</tbody></table>
      </details>}
    </>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {ask && <PeopleChangeDialog ask={ask} saving={pending === 'confirm'} error={askError} onCancel={() => { setAsk(undefined); setAskError('') }} onConfirm={createDashboard => void apply(createDashboard)} />}
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

/** What the team shares: Folders or the Whole Crew. The Crew's owner switches it, after the server's confirm box
 * (owner, 6 Nov: Whole Crew asks first), and stops sharing a folder (can_choose; never the last one, can_remove_folder); anyone in the Team adds one with "+ Share this folder"
 * (can_add_folder), picked from the Crew's folders the server lists where the Crew is. While a Mac Crew's Mac is off
 * the server says so, and the folder is named instead (the server makes it when the Mac is back). */
function SharesCard({ view, target, projectId }: { view: TeamTabView; target: TeamTarget; projectId: string }) {
  const { team } = view
  const { pending, error, setError, run } = useSave()
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<TeamFolderListing>()
  const [name, setName] = useState('')
  const [change, setChange] = useState<TeamFolderChange>()
  const choose = view.can_choose
  useEffect(() => {
    if (!picking) return
    let active = true
    setListing(undefined)
    void teamFolders(target, `path=${encodeURIComponent(parent)}`).then(data => { if (active) setListing(data) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [picking, parent, target.project, target.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  const whole = useFolderSize(target, team.folder.whole ? 'whole=1' : undefined)
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
          return <button key={label} type="button" role="radio" aria-checked={on} disabled={Boolean(pending)}
            onClick={() => { if (!on) void run(`share:${label}`, async () => setChange(await teamFolderChange(target, value))) }}
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
          <p>{sizeLine(listing)}</p>
          <Button size="sm" disabled={Boolean(pending) || folders.includes(parent)} onClick={() => void add(parent)}><Saving on={pending === 'add'}>{null}</Saving>Share this folder</Button>
        </>}
      </>}
    </div>}
    <p className="text-xs text-muted-foreground">Not shared: your chats and memory, app files, passwords and keys.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {change && <TeamFolderChangeDialog change={change} saving={pending === 'confirm'} onCancel={() => setChange(undefined)}
      onConfirm={() => void save('confirm', { ...team, folder: change.whole ? { whole: true } : { folders: [] } }).then(() => setChange(undefined))} />}
  </section>
}

/** What the Team can call (owner, 8 Nov: functions go on Share, not Automations): the same function list the Crew
 * always had, with its owner-only actions, here with the people it is shared with. */
function FunctionsCard({ projectId }: { projectId: string }) {
  return <section aria-label="Functions" className={card}>
    <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">Functions</h3><AskButton projectId={projectId} prompt="Help me add a function to this Crew." /></div>
    <Suspense fallback={<p className="text-xs text-muted-foreground">Loading…</p>}>
      <CrewFunctionsView embedded scope={{ profileId: 'work', projectId }} onEdit={message => { if (!sendToMain(projectId, message)) useChatStore.getState().addToast('Open your own Main tab in this Crew, then ask from there.', 'error') }} />
    </Suspense>
  </section>
}

/** The agent sharing list (owner, 6 Nov, approved mockups): only the viewer's own agents, as the server marks them
 * (mine). Teammates' agents stay in the tab bar. The same rows in the Mac app and on the website. */
function AgentsCard({ view, target, projectId }: { view: TeamTabView; target: TeamTarget; projectId: string }) {
  const { pending, error, run } = useSave()
  const agents = view.agents.filter(agent => agent.mine)
  const visibility = (key: string, inTeam: boolean) => run(`agent:${key}`, async () => {
    await teamRequest({ project: target.project, connection: target.connection }, '/agent-visibility', 'POST', { agent_key: key, in_team: inTeam })
    window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  })
  return <section aria-label="Agents" className={card}>
    <div className="flex items-center gap-2">
      <h3 className="flex-1 font-semibold">Agents</h3>
      <AskButton projectId={projectId} card="agents" />
    </div>
    {view.agents_line && <p className="text-xs text-muted-foreground">{view.agents_line}</p>}
    {agents.map(agent => <AgentRow key={agent.key} agent={agent} target={target} projectId={projectId} switching={pending === `agent:${agent.key}`} busy={Boolean(pending)} onShare={on => void visibility(agent.key, on)} />)}
    {!agents.length && <p className="text-xs text-muted-foreground">No agents in the team yet.</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}

// The status line's colour by the server's tone (crewAlwaysOnPlaceLabel): quiet grey unless something needs attention.
const toneText: Record<string, string> = { wait: 'text-amber-700 dark:text-amber-300', server: 'text-sky-700 dark:text-sky-300' }

/** One of the viewer's own agents (owner, 6 Nov, approved "calmer rows" mock): its name and one "Shared" switch, one
 * quiet status line the server words (place_label, coloured by place_tone only when it needs attention), and, while
 * shared, "Teammates can [Answer only | Make changes]", "Teammates see [Live chat | Chat history | Just the agent]" (its
 * words and choices from the server) and a Rules link. Purpose stays in the agent's own chat. Every
 * save goes to the server (agent-visibility, agent-rule); the server says when a switch cannot go off and why. */
function AgentRow({ agent, target, projectId, switching, busy, onShare }: { agent: TeamTabAgent; target: TeamTarget; projectId: string; switching: boolean; busy: boolean; onShare: (on: boolean) => void }) {
  const [rules, setRules] = useState(false)
  const { pending, error, run } = useSave()
  const save = (key: string, change: { can_make_changes?: boolean; chat_share?: string }) => run(key, async () => {
    await teamRequest({ project: target.project, connection: target.connection }, '/agent-rule', 'POST', { agent_key: agent.key, ...change })
    window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  })
  const locked = !agent.can_change || (agent.in_team && !agent.can_unshare)
  const status = agent.place_label || (!agent.in_team ? agent.not_shared_line : '') || ''
  return <div className="space-y-1 border-t border-border pt-2">
    <div className="flex items-center gap-2 text-[13px]">
      <span className="min-w-0 flex-1 truncate"><b>{agent.name}</b></span>
      <span className="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground" title={locked ? agent.fixed : undefined}>
        Shared<Saving on={switching}>{null}</Saving>
        <Switch aria-label={`${agent.name}: shared`} checked={agent.in_team} disabled={locked || busy} onCheckedChange={onShare} />
      </span>
    </div>
    {status && <p className={`text-xs ${toneText[agent.place_tone || ''] || 'text-muted-foreground'}`}>{status}</p>}
    {agent.in_team && agent.can_edit_rule && <div className="flex flex-wrap items-center gap-2 text-xs">
      {agent.can_make_changes !== undefined && <>
        <label htmlFor={`can-${agent.key}`} className="text-muted-foreground">Teammates can</label>
        <select id={`can-${agent.key}`} value={agent.can_make_changes ? 'change' : 'answer'} disabled={Boolean(pending)}
          onChange={e => void save('can', { can_make_changes: e.target.value === 'change' })}
          className="rounded-md border border-border bg-background px-2 py-0.5 text-xs">
          <option value="answer">Answer only</option>
          <option value="change">Make changes</option>
        </select>
        <Saving on={pending === 'can'}>{null}</Saving>
      </>}
      {agent.chat_share_options && <>
        <label htmlFor={`see-${agent.key}`} className="text-muted-foreground">{agent.chat_share_label}</label>
        <select id={`see-${agent.key}`} value={agent.chat_share} disabled={Boolean(pending)}
          onChange={e => void save('see', { chat_share: e.target.value })}
          className="rounded-md border border-border bg-background px-2 py-0.5 text-xs">
          {agent.chat_share_options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
        <Saving on={pending === 'see'}>{null}</Saving>
      </>}
      <button type="button" aria-expanded={rules} onClick={() => setRules(!rules)} className="text-primary hover:underline">{agent.has_rule ? '1 rule…' : 'Rules…'}</button>
    </div>}
    {agent.in_team && rules && <AgentDetails agent={agent} target={target} projectId={projectId} />}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}

/** One agent's rule when others ask (owner, 5 Nov mockup "When others ask"): what it uses, as the server lists it (the
 * person's own sign-ins marked yours, sent only to them), and what it does when someone else asks. Its own person edits
 * the rule; everyone else reads the server's summary line. */
function AgentDetails({ agent, target, projectId }: { agent: TeamTabAgent; target: TeamTarget; projectId: string }) {
  const [rule, setRule] = useState(agent.when_others_ask || '')
  const { pending, error, run } = useSave()
  useEffect(() => { setRule(agent.when_others_ask || '') }, [agent.when_others_ask])
  const save = (key: string, change: { when_others_ask?: string }) => run(key, async () => {
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
    </>}
    {error && <p role="alert" className="mt-1 text-xs text-destructive">{error}</p>}
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
