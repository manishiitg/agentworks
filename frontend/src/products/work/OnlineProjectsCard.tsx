import { useCallback, useEffect, useState } from 'react'
import { Globe, Loader2, X } from 'lucide-react'
import ModalPortal from '../../components/ui/ModalPortal'
import { Button } from '../../components/ui/Button'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { teamFailure, teamRequest, type TeamTarget } from './teamApi'

// Inside the People card (#3797): people and projects are both members, so one card lists them.
const block = 'space-y-2 border-t border-border pt-3 text-sm'

type OnlineProject = { project_id: string; name?: string }
type ProjectChoice = { project_id: string; name: string; owner?: string; available: boolean; why?: string }
type OnlineProjectsView = { projects?: OnlineProject[]; choices?: ProjectChoice[]; can_add: boolean; add_line?: string; max: number }

// What taking a project out does, as decided in #3664 and done by the server (crew_team_projects.go).
const REMOVE_LOSES = ['The Team\'s shared folders and Team instructions, where nobody changed them since the last sync']
const REMOVE_KEEPS = ['Its own files, chats, agents and keys', 'Shared files someone changed since the last sync']

/** The Team's online projects (#3664; inside the People card since #3797): projects on the Team's server that joined as
 * members of their own kind. The server lists them and the person's projects that can join; adding and removing go through its
 * checks, and its refusal is shown as it says it. Also from chat: team_sync project_add / project_remove. */
export function OnlineProjectsCard({ target }: { target: TeamTarget }) {
  const [view, setView] = useState<OnlineProjectsView>()
  const [error, setError] = useState('')
  const [pending, setPending] = useState('')
  const [picking, setPicking] = useState(false)
  const [removing, setRemoving] = useState<OnlineProject>()
  const server = useConnectedWorkStore(state => state.connections.find(connection => connection.id === target.connection)?.name)
  const where = server ? `On ${server}` : 'On this server'
  const load = useCallback(() => {
    void teamRequest<OnlineProjectsView>(target, '/projects').then(data => { setView(data); setError('') }).catch(cause => setError(teamFailure(cause).message))
  }, [target])
  useEffect(load, [load])
  useLiveRefetch(load, { kinds: ['crew_panel'], project: target.project, scopeKey: `${target.connection || ''}:${target.project}:projects`, fallbackMs: 30000, minIntervalMs: 3000 })
  const change = async (action: 'add' | 'remove', project: string) => {
    setPending(`${action}:${project}`); setError('')
    try {
      await teamRequest(target, '/projects', 'POST', { action, project_id: project })
      setPicking(false); setRemoving(undefined); load()
    } catch (cause) { setError(teamFailure(cause).message) } finally { setPending('') }
  }
  if (!view) return error ? <section aria-label="AI teammates" className={block}><h4 className="text-xs font-semibold text-muted-foreground">AI teammates</h4><p role="alert" className="text-xs text-destructive">{error}</p></section> : null
  const projects = view.projects || []
  const full = projects.length >= view.max
  return <section aria-label="AI teammates" className={block}>
    <h4 className="text-xs font-semibold text-muted-foreground">AI teammates {where.toLowerCase()}</h4>
    <p className="text-xs text-muted-foreground">Agents of your projects on the server. They work for the Team around the clock, even when your Mac is off, with the Team's shared folders and instructions; their own files, chats and keys stay theirs.</p>
    {projects.length === 0
      ? <p className="text-xs text-muted-foreground">No AI teammates yet.</p>
      : <div className="divide-y divide-border">{projects.map(project => <div key={project.project_id} className="flex items-center gap-2 py-2">
          <Globe aria-hidden="true" className="h-4 w-4 shrink-0 text-primary" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm">{project.name || 'Project'}</p>
            <p className="text-xs text-muted-foreground">AI teammate · online 24/7 {where.toLowerCase()}</p>
          </div>
          <button type="button" disabled={Boolean(pending)} onClick={() => setRemoving(project)} className="shrink-0 rounded-md border border-border px-2.5 py-1 text-xs hover:bg-muted disabled:opacity-50">Remove</button>
        </div>)}</div>}
    {view.can_add
      ? full ? <p className="text-xs text-muted-foreground">A Team has at most {view.max} AI teammates. Remove one to add another.</p>
        : <button type="button" disabled={Boolean(pending)} onClick={() => setPicking(!picking)} className="rounded-full border border-dashed border-border px-3 py-1 text-xs hover:bg-muted">+ Add an AI teammate</button>
      : view.add_line && <p className="text-xs text-muted-foreground">{view.add_line}</p>}
    {picking && <div className="rounded-xl border border-border bg-popover">
      <div className="border-b border-border px-3 py-2 text-xs text-muted-foreground">
        <p>Your projects {where.toLowerCase()} with AI teammate on</p>
        <p className="mt-1 text-foreground">Everyone in this Team can ask Main and its named agents, and they can make changes in this project.</p>
      </div>
      <div className="max-h-56 overflow-auto">
        {(view.choices || []).length === 0 && <p className="px-3 py-2 text-xs text-muted-foreground">None yet. Turn on AI teammate on a project's Access page on the server, then add it here.</p>}
        {(view.choices || []).map(choice => <button key={choice.project_id} type="button" disabled={!choice.available || Boolean(pending)} onClick={() => void change('add', choice.project_id)}
          className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:bg-muted disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:bg-transparent">
          <span className="min-w-0 flex-1"><span className="block truncate text-sm text-foreground">{choice.name}</span>{choice.why && <span className="block text-muted-foreground">{choice.why}</span>}</span>
          {pending === `add:${choice.project_id}` && <Loader2 aria-label="Adding" className="h-3.5 w-3.5 animate-spin" />}
        </button>)}
      </div>
      {(view.choices || []).length > 0 && <p className="border-t border-border px-3 py-2 text-xs text-muted-foreground">Don't see a project? Turn on AI teammate on its Access page on the server.</p>}
    </div>}
    {error && !removing && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {removing && <RemoveDialog name={removing.name || 'Project'} saving={pending === `remove:${removing.project_id}`} error={error} onCancel={() => { setRemoving(undefined); setError('') }} onConfirm={() => void change('remove', removing.project_id)} />}
  </section>
}

function RemoveDialog({ name, saving, error, onCancel, onConfirm }: { name: string; saving: boolean; error: string; onCancel: () => void; onConfirm: () => void }) {
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape' && !saving) onCancel() }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [saving, onCancel])
  const label = 'mb-1.5 mt-3.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'
  return <ModalPortal>
    <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-overlay/50 p-4" onClick={() => { if (!saving) onCancel() }}>
      <div data-theme-surface="dialog" role="dialog" aria-modal="true" aria-label={`Take ${name} out of the Team`} onClick={event => event.stopPropagation()}
        style={{ width: '28rem', maxWidth: 'calc(100vw - 2rem)', maxHeight: 'calc(100vh - 2rem)' }}
        className="min-w-0 overflow-y-auto break-words rounded-xl border border-border bg-surface-panel p-5 text-sm shadow-md">
        <div className="flex items-start gap-2">
          <h3 className="flex-1 text-[15px] font-semibold">Take {name} out of the Team?</h3>
          <button type="button" aria-label="Close" disabled={saving} onClick={onCancel} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p className={label}>It loses</p>
        <ul className="list-disc space-y-1 pl-5 text-[13px]">{REMOVE_LOSES.map(line => <li key={line}>{line}</li>)}</ul>
        <p className={label}>It keeps</p>
        <ul className="list-disc space-y-1 pl-5 text-[13px]">{REMOVE_KEEPS.map(line => <li key={line}>{line}</li>)}</ul>
        {error && <p role="alert" className="mt-3.5 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" disabled={saving} onClick={onCancel}>Cancel</Button>
          <Button variant="destructive" size="sm" disabled={saving} onClick={onConfirm}>{saving && <Loader2 aria-label="Saving" className="animate-spin" />}Remove</Button>
        </div>
      </div>
    </div>
  </ModalPortal>
}
