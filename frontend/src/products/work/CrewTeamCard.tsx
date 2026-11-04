import { useEffect, useState } from 'react'
import { ChevronRight, Folder, Users, X } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { Switch } from '../../components/ui/Switch'
import { Button } from '../../components/ui/Button'
import { fillTeamSetupDraft } from '../../utils/agentPurposeDraft'
import { saveTeam, switchTeam, teamError, useProjectTeam, type Team } from './teamApi'

type Person = { id: string; username: string; email?: string }

export function CrewTeamCard({ projectId }: { projectId: string }) {
  const { team, target, error: loadError, refresh } = useProjectTeam(projectId)
  const [people, setPeople] = useState<Person[]>([])
  const [adding, setAdding] = useState(false)
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [switchNote, setSwitchNote] = useState('')
  const [picking, setPicking] = useState(false)
  const [parent, setParent] = useState('')
  const [listing, setListing] = useState<{ folders: string[]; path: string; files: number; bytes: number }>()
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
    void desktopRequest<{ folders: string[]; path: string; files: number; bytes: number }>(`/${encodeURIComponent(target.connection)}/crew-teams/${encodeURIComponent(target.localProject)}/folders?path=${encodeURIComponent(parent)}`)
      .then(data => { if (active) setListing(data) }).catch(e => { if (active) setError(teamError(e)) })
    return () => { active = false }
  }, [picking, parent, target?.connection, target?.localProject])
  if (!team || !target || !team.can_manage) return loadError ? <p className="text-xs text-muted-foreground">{loadError}</p> : null
  const name = (id: string) => people.find(p => p.id === id)?.username || id
  const save = async (next: Team) => {
    setBusy(true); setError('')
    try { await saveTeam(target, next); refresh() } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const turn = async (on: boolean) => {
    setBusy(true); setError(''); setSwitchNote('')
    try {
      const result = await switchTeam(target, on)
      const notes = [result.folder_created ? 'Made an empty team/ folder as the shared folder.' : '',
        result.shared?.length ? `Shared with your team: ${result.shared.join(', ')}.` : '',
        result.needs_purpose?.length ? `Set a purpose and icon for ${result.needs_purpose.join(', ')} to share ${result.needs_purpose.length === 1 ? 'it' : 'them'} (Set purpose below).` : '',
        result.note || '']
      setSwitchNote(notes.filter(Boolean).join(' '))
      refresh()
    } catch (e) { setError(teamError(e)) } finally { setBusy(false) }
  }
  const askText = 'Ask chat to set up your team: people, shared folder and which agents are shared'
  return <section aria-label="Team" className="mt-4 space-y-3 rounded-xl border border-border px-4 py-3">
    <div className="flex items-center gap-2"><Users className="h-4 w-4" /><h3 className="text-sm font-semibold">Team</h3><span className="ml-auto text-xs text-muted-foreground">{team.on && team.people.length ? `${team.people.length} people` : 'Only you'}</span><button type="button" title={askText} aria-label={askText} onClick={() => fillTeamSetupDraft(projectId)} className="rounded-full border border-border px-2.5 py-0.5 text-xs hover:bg-muted">Ask</button><Switch aria-label="Team" disabled={busy || !target.localProject} checked={team.on} onCheckedChange={on => void turn(on)} /></div>
    {switchNote && <p className="text-xs text-muted-foreground">{switchNote}</p>}
    {!team.on && <p className="text-xs text-muted-foreground">Off: only you. Turn it on to work with people: every agent with a purpose is shared with your team, and an empty team/ folder becomes the shared folder if you have not chosen one.</p>}
    {team.on && <>
    <p className="text-xs text-muted-foreground">Choose who works with you. Each agent's sharing can be changed below.</p>
    {team.people.map(person => <div key={person.user_id} className="flex flex-wrap items-center gap-2 text-xs">
      <span className="min-w-20 flex-1 truncate">{name(person.user_id)}</span>
      <label className="flex items-center gap-2"><Switch aria-label={`${name(person.user_id)} edits shared folder`} disabled={busy || !team.folder.path} checked={person.can_edit_shared_folder} onCheckedChange={on => void save({ ...team, people: team.people.map(p => p.user_id === person.user_id ? { ...p, can_edit_shared_folder: on } : p) })} />Edits shared folder</label>
      <button type="button" disabled={busy} aria-label={`Remove ${name(person.user_id)} from team`} onClick={() => void save({ ...team, people: team.people.filter(p => p.user_id !== person.user_id) })} className="rounded p-1 hover:bg-muted"><X className="h-3 w-3" /></button>
    </div>)}
    <button type="button" disabled={busy} onClick={() => setAdding(!adding)} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Person</button>
    {adding && <div className="rounded-xl border border-border bg-popover">
      <input aria-label="Find a teammate" placeholder="Search people" className="w-full border-b border-border bg-transparent px-3 py-2 text-xs" value={search} onChange={e => setSearch(e.target.value)} />
      <div className="max-h-48 overflow-auto">{people.filter(p => p.id !== team.owner_id && !team.people.some(member => member.user_id === p.id) && `${p.username} ${p.email || ''}`.toLowerCase().includes(search.toLowerCase())).map(p => <button key={p.id} type="button" disabled={busy} onClick={() => { void save({ ...team, people: [...team.people, { user_id: p.id, can_edit_shared_folder: false }] }); setAdding(false) }} className="block w-full px-3 py-2 text-left text-xs hover:bg-muted">{p.username}</button>)}</div>
    </div>}
    <div className="border-t border-border pt-3">
      <div className="flex items-center gap-2 text-xs"><Folder className="h-4 w-4 text-amber-500" /><span className="flex-1">{team.folder.path ? `${team.folder.path}/` : 'Shared folder'}</span><button type="button" disabled={busy || !target.localProject} onClick={() => { setPicking(!picking); setParent('') }} className="text-primary hover:underline">{team.folder.path ? 'Change' : 'Choose folder'}</button></div>
      {team.folder.path && <p className="mt-1.5 text-[11px] text-muted-foreground">{team.folder.path}/ is kept on the server so your team can sync it.{team.server_data ? ` ${(team.server_data.size_bytes / (1 << 20)).toFixed(1)} MB.` : ''}</p>}
      {picking && <div className="mt-3 space-y-2 rounded-lg bg-muted/40 p-3 text-xs">
        <div className="flex items-center gap-2"><button type="button" onClick={() => setParent(parent.split('/').slice(0, -1).join('/'))} className="text-primary">{parent ? '← Up' : 'Project'}</button><span>{parent}</span></div>
        {listing?.folders.map(folder => <button key={folder} type="button" onClick={() => setParent(folder)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted"><Folder className="h-3.5 w-3.5" />{folder.split('/').pop()}<ChevronRight className="ml-auto h-3 w-3" /></button>)}
        {parent && listing?.path === parent && <><p>{listing.files} files · {(listing.bytes / (1 << 20)).toFixed(1)} MB</p><p>{parent}/ will be kept on the server so your team can sync it. Private state, credentials and generated folders are skipped.</p><Button size="sm" disabled={busy} onClick={() => { void save({ ...team, folder: { path: parent, label: parent.split('/').pop() || parent } }); setPicking(false) }}>Use this folder</Button></>}
      </div>}
    </div>
    </>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </section>
}
