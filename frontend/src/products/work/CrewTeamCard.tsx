import { useEffect, useState } from 'react'
import { ChevronRight, Folder, Users, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { fillTeamSetupDraft } from '../../utils/agentPurposeDraft'
import { saveTeam, switchTeam, teamError, useProjectTeam, type Team } from './teamApi'

type Person = { id: string; username: string; email?: string }
type Listing = { folders: string[]; path: string; files: number; bytes: number; max_bytes?: number; max_files?: number }

const mb = (bytes: number) => `${(bytes / (1 << 20)).toFixed(bytes < 10 << 20 ? 1 : 0)} MB`
const sizeLine = (size?: Listing) => size ? `${size.files.toLocaleString()} ${size.files === 1 ? 'file' : 'files'} · ${mb(size.bytes)}` : ''

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

function FolderRow({ folder, connection, localProject, busy, onRemove }: { folder: string; connection?: string; localProject?: string; busy: boolean; onRemove: () => void }) {
  const size = useShareSize(connection, localProject, `path=${encodeURIComponent(folder)}`)
  return <div className="flex items-center gap-2 rounded-lg border border-border px-2 py-1.5 text-xs">
    <Folder className="h-3.5 w-3.5 shrink-0 text-amber-500" /><span className="min-w-0 truncate">{folder}/</span>
    <span className="text-muted-foreground">{sizeLine(size)}</span>
    <button type="button" disabled={busy} aria-label={`Stop sharing ${folder}/`} title="Stop sharing this folder" onClick={onRemove} className="ml-auto rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>
  </div>
}

export function CrewTeamCard({ projectId, serverCrew = false }: { projectId: string; serverCrew?: boolean }) {
  const { team, target, error: loadError, refresh } = useProjectTeam(projectId)
  const [people, setPeople] = useState<Person[]>([])
  const [adding, setAdding] = useState(false)
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [switchNote, setSwitchNote] = useState('')
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<Listing>()
  useEffect(() => {
    if (!target) return
    let active = true
    const read = async () => {
      if (!target.connection) return (await api.get<{ users: Person[] }>('/api/users/directory', { skipSessionContext: true })).data
      const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(target.connection)}/api/users/directory`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
      if (!response.ok) throw new Error('The company directory is unavailable.')
      return response.json() as Promise<{ users: Person[] }>
    }
    void read().then(data => { if (active) setPeople(data.users || []) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [target?.connection, target?.project])
  useEffect(() => {
    if (!picking || !target?.connection || !target.localProject) return
    let active = true
    void desktopRequest<Listing>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}/folders?path=${encodeURIComponent(parent)}`)
      .then(data => { if (active) setListing(data) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [picking, parent, target?.connection, target?.localProject])
  // The Whole Crew's size, read before it is chosen (owner, 5 Nov: one plain line, no confirm).
  const whole = useShareSize(target?.connection, target?.localProject, team?.on && team.can_manage ? 'whole=1' : undefined)
  if (!team || !target || !team.can_manage) return loadError ? <p className="text-xs text-muted-foreground">{loadError}</p> : null
  const tooBig = Boolean(whole && (whole.bytes > (whole.max_bytes || Infinity) || whole.files > (whole.max_files || Infinity)))
  const name = (id: string) => people.find(p => p.id === id)?.username || id
  // On the website these choices belong to the source Mac, not this browser's laptop integration.
  if (!target.localProject) return <section aria-label="Team" className="space-y-3 rounded-xl border border-border p-4 text-sm">
    <div className="flex items-center justify-between"><h3 className="font-semibold">{team.name}</h3><span className="text-muted-foreground">{team.on ? 'Team on' : 'Only you'}</span></div>
    {team.on && <>
      {team.people.map(person => <p key={person.user_id}>{name(person.user_id)} <span className="text-muted-foreground">· {person.invited ? 'Invited' : 'Joined'}</span></p>)}
      <div className="border-t border-border pt-3"><h4 className="font-medium">Shared folder</h4><p className="text-muted-foreground">{team.folder.whole ? 'Whole Crew' : team.folder.folders?.join(', ') || 'Nothing shared yet'}</p></div>
    </>}
    <p className="text-xs text-muted-foreground">Change these from the Mac app.</p>
  </section>
  const save = async (next: Team) => {
    setBusy(true); setError('')
    try { await saveTeam(target, next); refresh() } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const turn = async (on: boolean) => {
    setBusy(true); setError(''); setSwitchNote('')
    try {
      const result = await switchTeam(target, on, serverCrew)
      const notes = [result.folder_created ? 'Made an empty team/ folder to share.' : '',
        result.shared?.length ? `Shared with your team: ${result.shared.join(', ')}.` : '',
        result.note || '']
      setSwitchNote(notes.filter(Boolean).join(' '))
      refresh()
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const askText = 'Ask chat to set up your team: people, what it shares and which agents are shared'
  return <section aria-label="Team" className="mt-4 space-y-3 rounded-xl border border-border px-4 py-3">
    <div className="flex items-center gap-2"><Users className="h-4 w-4" /><h3 className="text-sm font-semibold">Team</h3><span className="ml-auto text-xs text-muted-foreground">{team.on && team.people.length ? `${team.people.length} people` : 'Only you'}</span><button type="button" title={askText} aria-label={askText} onClick={() => fillTeamSetupDraft(projectId)} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted">Ask</button><Switch aria-label="Team" disabled={busy || (!serverCrew && !target.localProject)} checked={team.on} onCheckedChange={on => void turn(on)} /></div>
    {switchNote && <p className="text-xs text-muted-foreground">{switchNote}</p>}
    {!team.on && <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work on this Crew with people.</p>}
    {team.on && <>
    <p className="text-xs text-muted-foreground">Choose who works with you. Everyone who joins reads and writes what the team shares; each agent's sharing can be changed below.</p>
    {/* What members' agents in this Crew use of yours (owner, 5 Nov), names only, from the server. */}
    {team.lent && <p className="text-xs text-muted-foreground"><span className="font-medium text-foreground">Lent to the team:</span> {[...team.lent.apps, ...team.lent.secrets.map(name => `${name} (secret)`)].join(', ') || 'nothing yet'}. Their agents in this Crew use these as you; values are never shown.</p>}
    {team.people.map(person => <div key={person.user_id} className="flex flex-wrap items-center gap-2 text-xs">
      <span className="min-w-20 flex-1 truncate">{name(person.user_id)} <span className="text-muted-foreground">· {person.invited ? 'Invited' : 'Joined'}</span></span>
      <button type="button" disabled={busy} aria-label={person.invited ? `Cancel ${name(person.user_id)}'s invite` : `Remove ${name(person.user_id)} from team`} title={person.invited ? 'Cancel invite' : 'Remove from team'} onClick={() => void save({ ...team, people: team.people.filter(p => p.user_id !== person.user_id) })} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>
    </div>)}
    <button type="button" disabled={busy} onClick={() => setAdding(!adding)} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Person</button>
    {adding && <div className="rounded-xl border border-border bg-popover">
      <input aria-label="Find a teammate" placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-2 text-xs" value={search} onChange={e => setSearch(e.target.value)} />
      <div className="max-h-48 overflow-auto">{people.filter(p => p.id !== team.owner_id && !team.people.some(member => member.user_id === p.id) && `${p.username} ${p.email || ''}`.toLowerCase().includes(search.toLowerCase())).map(p => <button key={p.id} type="button" disabled={busy} onClick={() => { void save({ ...team, people: [...team.people, { user_id: p.id }] }); setAdding(false) }} className="block w-full px-3 py-2 text-left text-xs hover:bg-muted">{p.username}</button>)}</div>
    </div>}
    <div className="border-t border-border pt-3 text-xs">
      <p className="font-medium">What the team shares</p>
      <div role="radiogroup" aria-label="What the team shares" className="mb-2 mt-2 flex w-max overflow-hidden rounded-full border border-border">
        {([[false, 'Folders'], [true, 'Whole Crew']] as const).map(([value, label]) => {
          const on = Boolean(team.folder.whole) === value
          return <button key={label} type="button" role="radio" aria-checked={on} disabled={busy || !target.localProject || (value && tooBig)}
            onClick={() => { if (!on) void save({ ...team, folder: value ? { whole: true } : { folders: [] } }) }}
            className={`px-3 py-1 disabled:cursor-not-allowed disabled:opacity-50 ${on ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{label}</button>
        })}
      </div>
      {team.folder.whole ? <div className="space-y-1.5 text-muted-foreground">
        <p>The whole <b className="text-foreground">{team.name}</b> folder{whole ? `: ${sizeLine(whole)}` : ''}. Everyone in the Team reads and edits it.</p>
        <p>Not shared: your chats and memory, app files, <code>node_modules</code>, <code>.git</code> history and caches, and files that look like passwords or keys (you can still choose those in Sync).</p>
        {whole && whole.bytes > 0 && <p className="text-amber-600">First sync sends {mb(whole.bytes)}; you'll see progress under Access.</p>}
        <p>Back to Folders: one click; your last folders come back (or team/). Copies already on teammates' Macs stay there.</p>
      </div> : <div className="space-y-1.5">
        {(team.folder.folders || []).map(folder => <FolderRow key={folder} folder={folder} connection={target.connection} localProject={target.localProject} busy={busy}
          onRemove={() => void save({ ...team, folder: { folders: (team.folder.folders || []).filter(f => f !== folder) } })} />)}
        <button type="button" disabled={busy || !target.localProject} onClick={() => { setPicking(!picking); setParent('') }} className="text-primary hover:underline disabled:opacity-50">+ Add a folder</button>
        <p className="text-muted-foreground">{(team.folder.folders || []).length ? 'Everyone in the Team reads and edits these folders. The rest of this Crew stays on your Mac.' : 'Nothing is shared yet: add a folder, or share the Whole Crew.'}</p>
        {whole && <p className={tooBig ? 'text-amber-600' : 'text-muted-foreground'}>{tooBig ? `The Whole Crew is too big to share (${sizeLine(whole)}; at most 256 MB and 10,000 files).` : `Whole Crew would share ${sizeLine(whole)}.`}</p>}
      </div>}
      {picking && !team.folder.whole && <div className="mt-3 space-y-2 rounded-lg bg-muted/40 p-3">
        <div className="flex items-center gap-2"><button type="button" onClick={() => setParent(parent.split('/').slice(0, -1).join('/'))} className="text-primary">{parent ? '← Up' : 'Project'}</button><span>{parent}</span></div>
        {listing?.folders.map(folder => <button key={folder} type="button" onClick={() => setParent(folder)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted"><Folder className="h-3.5 w-3.5" />{folder.split('/').pop()}<ChevronRight className="ml-auto h-3 w-3" /></button>)}
        {parent && listing?.path === parent && <><p>{sizeLine(listing)}</p><p>{parent}/ will be kept on the server so your team can sync it. Private state, credentials and generated folders are skipped.</p><Button size="sm" disabled={busy || (team.folder.folders || []).includes(parent)} onClick={() => { void save({ ...team, folder: { folders: [...(team.folder.folders || []), parent] } }); setPicking(false) }}>Share this folder</Button></>}
      </div>}
      {team.server_data && <p className="mt-1.5 text-[11px] text-muted-foreground">Kept on the server so your team can sync it: {mb(team.server_data.size_bytes)}.</p>}
    </div>
    </>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}
