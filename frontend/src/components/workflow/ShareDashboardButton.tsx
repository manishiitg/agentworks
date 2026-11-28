import { useCallback, useEffect, useRef, useState } from 'react'
import { Link2, Share as ShareIcon, Users, X } from 'lucide-react'
import { createPortal } from 'react-dom'
import api from '../../services/api'
import { WORKFLOW_REPORT_REFRESH_EVENT } from './reportRefreshEvent'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { folderConnection } from '../../utils/remoteSessions'
import { OnlineOnlyNote } from '../../products/work/useMacCrew'
import { WorkspaceContextRow as Row } from '../workspace/WorkspaceContextCard'
import { PublicLinkControl, type PublicLink } from './PublicLinkControl'
import { ProjectTeamAgents, type ProjectTeamAgent } from './ProjectTeamAgents'

type Person = { id: string; email?: string; username?: string }
/** One account from GET /api/users/directory. */
type DirectoryUser = { id: string; username?: string; email?: string }
type ShareState = {
  mode: 'everyone' | 'people' | 'private'; people?: Person[]; can_share: boolean; share_blocked?: string; needs_dashboard?: boolean
  /** Goals and Flows keep an owner list; a Crew's builders are its Team. */
  has_owners?: boolean
  accounts?: { kind: string; name: string; use: string }[]
  public_links?: PublicLink[]
}
/** users: a project on the server only. Users work in it through chat with its setup and cannot change it; each has extra
 * locked paths besides always_locked. */
type ProjectUser = Person & { locked_paths?: string[] }
/** A Team in the project's Users (#4087): everyone in it works here as a User. */
type ProjectTeam = { team_id: string; name: string; owner: string; people: number; can_remove: boolean; agents?: ProjectTeamAgent[]; members?: { id: string; name: string }[]; new_agents?: 'only_me' | 'whole_team'; project_id?: string }
type OwnersState = { owner?: Person; co_owners: Person[]; can_manage: boolean; users?: ProjectUser[]; always_locked?: string[]; teams?: ProjectTeam[]; team_choices?: ProjectTeam[] }
const who = (person: Person) => person.email || person.username || person.id
// General access, worded like a Drive file (owner, 28 Nov, #4545).
const audience = (state: ShareState) => state.mode === 'everyone' ? 'Anyone at the company' : state.mode === 'people' ? `${state.people?.length || 0} ${state.people?.length === 1 ? 'person' : 'people'} added` : 'Restricted'
const errorText = (cause: unknown, fallback: string) => {
  const data = (cause as { response?: { data?: { error?: { message?: string } | string; message?: string } } })?.response?.data
  return (typeof data?.error === 'string' ? data.error : data?.error?.message) || data?.message || fallback
}

/** One Access overview. Changes use the project's visible chat and its existing server tools. */
export function ShareDashboardButton({ workspacePath, documentPath, label = 'Share', variant = 'button', onOpen, tabId, coOwnersOnly = false }: {
  /** The open dashboard or artifact; Copy link copies its link rather than the project's first dashboard. */
  workspacePath: string; documentPath?: string; label?: string; variant?: 'button' | 'row' | 'panel'; onOpen?: () => void
  /** A server project's Co-owners page (owner, 24 Nov): who builds it, nothing about who uses it or its artifacts. */
  coOwnersOnly?: boolean
  onAsk?: (message: string) => void | Promise<unknown>; tabId?: string
}) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const streaming = useChatStore(state => tabId ? state.chatTabs[tabId]?.isStreaming === true : false)
  // A Crew that lives on the Mac: its dashboard link works once it is online (useMacCrew.tsx).
  const crewProjectId = useChatStore(state => tabId && state.chatTabs[tabId]?.metadata?.agentProfileId === 'work' ? state.chatTabs[tabId]?.metadata?.agentProfileProjectId : undefined)
  const scope = `${server}:${user}:${workspacePath}`
  const [snapshot, setSnapshot] = useState<{ scope: string; state: ShareState } | null>(null)
  const state = snapshot?.scope === scope ? snapshot.state : null
  const [ownerSnapshot, setOwnerSnapshot] = useState<{ scope: string; owners: OwnersState } | null>(null)
    const owners = ownerSnapshot?.scope === scope ? ownerSnapshot.owners : undefined
  // Everyone who can be picked: the server's account list (GET /api/users/directory), the same people as the Team's picker.
  const [directory, setDirectory] = useState<{ scope: string; users: DirectoryUser[] } | null>(null)
  const users = directory?.scope === scope ? directory.users : undefined
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const [ownersError, setOwnersError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [pendingShare, setPendingShare] = useState<{ scope: string; mode: ShareState['mode']; emails?: string[] }>()
  const confirmation = pendingShare?.scope === scope ? pendingShare : undefined
  const trigger = useRef<HTMLButtonElement>(null)
  const isPanel = variant === 'panel'
  const expanded = isPanel || open
  const close = useCallback(() => { setOpen(false); trigger.current?.focus() }, [])
  useEffect(() => {
    const identity = captureChatIdentity()
    let disposed = false
    setSnapshot(null); setError('')
    void api.post<ShareState>('/api/dashboards', { workspace: workspacePath, action: 'share_status' })
      .then(({ data }) => { if (!disposed && isChatIdentityCurrent(identity)) setSnapshot({ scope, state: data }) })
      .catch(() => { if (!disposed && isChatIdentityCurrent(identity)) setError('Could not load access. Try again.') })
    return () => { disposed = true }
  }, [workspacePath, user, server, scope, streaming, attempt])
  useEffect(() => {
    if (!expanded || !state?.can_share || !state.has_owners) return
    const identity = captureChatIdentity()
    let disposed = false
    setOwnerSnapshot(null); setOwnersError('')
    const load = async () => {
      const { data } = await api.post<OwnersState>('/api/dashboards', { workspace: workspacePath, action: 'owners' })
      return data
    }
    void load().then(loaded => { if (!disposed && isChatIdentityCurrent(identity)) setOwnerSnapshot({ scope, owners: loaded }) })
      .catch(() => { if (!disposed && isChatIdentityCurrent(identity)) setOwnersError('Could not load the owners. Try again.') })
    return () => { disposed = true }
  }, [expanded, state?.can_share, state?.has_owners, workspacePath, user, server, scope, streaming, attempt])
  useEffect(() => {
    if (!expanded || !state?.can_share) return
    const identity = captureChatIdentity()
    let disposed = false
    void api.get<{ users?: DirectoryUser[] }>('/api/users/directory', { connection: folderConnection(workspacePath) })
      .then(({ data }) => { if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, users: data.users || [] }) })
      .catch(() => { if (!disposed && isChatIdentityCurrent(identity)) setDirectory({ scope, users: [] }) })
    return () => { disposed = true }
  }, [expanded, state?.can_share, scope])
  useEffect(() => {
    if (!open) return
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); close() } }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [open, close])
  if (!state) return isPanel ? <div className="text-sm">{error ? <p role="alert">{error} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p> : <p role="status" className="text-muted-foreground">Loading access…</p>}</div> : null
  if (!state.can_share) return isPanel ? <p className="text-sm text-muted-foreground">{state.share_blocked || 'Ask a project owner to change access.'}</p> : null
  if (variant === 'row') return <Row icon={Users} detail={audience(state)} onClick={() => onOpen?.()}>{label}</Row>
  const emails = (state.people || []).map(person => person.email).filter((value): value is string => !!value)
  /** The server decides: it takes the mode and the full list of people and answers with the saved state. */
  const putShare = async (next: ShareState['mode'], list?: string[], createDashboard = false) => {
    if (useAuthStore.getState().user?.id !== user || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    const identity = captureChatIdentity()
    try {
      const { data } = await api.post<ShareState & { unknown_emails?: string[] }>('/api/dashboards', { workspace: workspacePath, action: 'share', mode: next, ...(list ? { emails: list } : {}), ...(createDashboard ? { create_dashboard: true } : {}) })
      if (!isChatIdentityCurrent(identity)) return
      setSnapshot({ scope, state: data }); setPendingShare(undefined)
      if (createDashboard) window.dispatchEvent(new CustomEvent(WORKFLOW_REPORT_REFRESH_EVENT, { detail: { workspacePath } }))
      if (data.unknown_emails?.length) setNotice(`No account for ${data.unknown_emails.join(', ')}.`)
    } catch (cause) {
      if (!isChatIdentityCurrent(identity)) return
      if ((cause as { response?: { data?: { dashboard_required?: boolean } } })?.response?.data?.dashboard_required === true) {
        setPendingShare({ scope, mode: next, emails: list }); return
      }
      throw cause
    }
  }
  const run = async (work: () => Promise<void>, fallback = 'Could not change access.') => {
    setBusy(true); setError(''); setNotice('')
    try { await work() } catch (cause) { setError(errorText(cause, fallback)) }
    finally { setBusy(false) }
  }
  const copyLink = async () => {
    setError(''); setNotice('')
    try {
      const { data } = await api.post<{ dashboards?: { dashboard_id?: string; document_path?: string; short_url?: string; url?: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'list' })
      const rows = (data.dashboards || []).filter(row => row.short_url || row.url)
      const row = (documentPath && rows.find(item => item.document_path === documentPath)) || rows.find(item => item.dashboard_id === 'overview') || rows[0]
      const link = row?.short_url || row?.url
      if (!link) { setError('There is no published dashboard to link to yet.'); return }
      await navigator.clipboard.writeText(link)
      setNotice('Link copied. People still need access to open it.')
    } catch (cause) { setError(errorText(cause, 'Could not copy the link.')) }
  }
  // Shared like a Drive file (owner, 28 Nov, #4545): people you add, plus general access (Restricted or anyone at the
  // company with the link); a dashboard's public link is "anyone with the link". The server stores an empty named list as
  // private. Owner changes use the same server operation as chat.
  const companyWide = state.mode === 'everyone'
  const shareList = (list: string[]) => putShare('people', list)
  const addOwner = (value: string) => {
    void run(async () => {
      await changeOwners('owner_add', value)
      const { data } = await api.post<ShareState>('/api/dashboards', { workspace: workspacePath, action: 'share_status' })
      setSnapshot({ scope, state: data })
    })
  }
  const addPerson = (value: string) => {
    if (emails.includes(value)) return
    void run(() => shareList([...emails, value]))
  }
  const changeUsers = async (action: 'user_add' | 'user_remove' | 'user_files' | 'team_add' | 'team_remove', value: string, lockedPaths?: string[]) => {
    const { data } = await api.post<OwnersState & { note?: string }>('/api/dashboards', { workspace: workspacePath, action, emails: [value], locked_paths: lockedPaths })
    setOwnerSnapshot({ scope, owners: data })
    if (data.note) setNotice(data.note)
  }
  const changeOwners = async (action: 'owner_add' | 'owner_remove', value: string) => {
    const { data } = await api.post<OwnersState & { note?: string }>('/api/dashboards', { workspace: workspacePath, action, emails: [value] })
    setOwnerSnapshot({ scope, owners: data })
    if (data.note) setNotice(data.note)
  }
  const avatar = (person: Person) => <span aria-hidden="true" className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-[11px] font-medium text-primary">{who(person).slice(0, 2).toUpperCase()}</span>
  const removeButton = (person: Person, remove: (value: string) => Promise<void>) => person.email
    ? <button type="button" aria-label={`Remove ${who(person)}`} disabled={busy} onClick={() => void run(() => remove(person.email as string))} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>
    : null
  // Pick a person from the server's accounts, like the Team's "+ Add person" (owner, 25 Nov): no typed email, so no
  // browser autofill. People already added and the owner are left out.
  const taken = new Set([user, owners?.owner?.id, ...(owners?.co_owners || []).map(person => person.id), ...(owners?.users || []).map(person => person.id), ...(state.people || []).map(person => person.id)].filter(Boolean))
  const personPicker = (exclude: Set<string | undefined>, pick: (email: string) => void, label: string) => {
    if (!users) return <p role="status" className="text-xs text-muted-foreground">Loading people…</p>
    const options = users.filter(person => person.email && !exclude.has(person.id))
    if (!users.some(person => person.id !== user)) return <p className="text-xs text-muted-foreground">Nobody else has signed in to this server yet.</p>
    if (!options.length) return <p className="text-xs text-muted-foreground">Everyone on this server is already added.</p>
    return <select aria-label={label} value="" disabled={busy} onChange={event => { if (event.target.value) pick(event.target.value) }}
      className="h-9 w-full rounded-md border border-dashed border-border bg-background px-2 text-sm text-primary">
      <option value="">+ Add person</option>
      {options.map(person => <option key={person.id} value={person.email}>{person.username && person.username !== person.email ? `${person.username} (${person.email})` : person.email}</option>)}
    </select>
  }
  const peopleContent = <div className="space-y-6 text-sm">
    {!coOwnersOnly && <><OnlineOnlyNote projectId={crewProjectId} />
    <section aria-label="People with access" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-semibold">People with access</h3>
        <button type="button" onClick={() => void copyLink()} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs hover:bg-muted"><Link2 aria-hidden="true" className="h-3.5 w-3.5" />Copy link</button>
      </div>
      {companyWide ? <p className="text-xs text-muted-foreground">Everyone at the company can open it, so there is nobody to add.</p> : <>
        {!!state.people?.length && <ul aria-label="People with access" className="space-y-2">
          {state.people.map(person => <li key={person.id} className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{removeButton(person, value => shareList(emails.filter(item => item !== value)))}</li>)}
        </ul>}
        {personPicker(taken, addPerson, 'Add people')}
      </>}
    </section>
    <section aria-label="General access" className="space-y-2">
      <h3 className="font-semibold">General access</h3>
      <select aria-label="General access" value={companyWide ? 'company' : 'restricted'} disabled={busy}
        onChange={event => void run(() => event.target.value === 'company' ? putShare('everyone') : shareList([]))}
        className="h-9 w-full rounded-md border border-border bg-background px-2 text-sm">
        <option value="restricted">Restricted: only people added</option>
        <option value="company">Anyone at the company with the link</option>
      </select>
      <p className="text-xs text-muted-foreground">{companyWide ? 'They sign in and see the live page, chat with it and use its functions.' : state.has_owners ? 'Only people added, co-owners and Users can open it.' : 'Only people added and its Team can open it.'} Opening it doesn't make them part of the project.</p>
    </section>
    {!!state.public_links?.length && <section aria-label="Anyone with the link" className="space-y-3 border-t border-border pt-4">
      <div><h3 className="font-semibold">Anyone with the link, no sign-in</h3><p className="text-xs text-muted-foreground">A frozen copy of a dashboard that anyone can open. It changes only when you update it.</p></div>
      {state.public_links.map(link => <div key={link.dashboard_id} className="space-y-1">
        <p className="truncate font-medium">{link.title}</p>
        <PublicLinkControl link={link} change={async mode => (await api.post<PublicLink>('/api/dashboards', { workspace: workspacePath, action: 'public_link', mode, dashboard_id: link.dashboard_id })).data} />
      </div>)}
    </section>}
    {confirmation && <section role="region" aria-label="Create a dashboard to share" className="space-y-3 rounded-lg border border-border p-3">
      <h3 className="font-semibold">Create a dashboard for this project?</h3>
      <p className="text-xs text-muted-foreground">People will open it to chat with this project and use its published functions.</p>
      <p className="text-xs">Share with: {confirmation.mode === 'everyone' ? 'Everyone at the company' : confirmation.emails?.join(', ')}</p>
      <div className="flex flex-wrap gap-2">
        <button type="button" disabled={busy} onClick={() => void run(() => putShare(confirmation.mode, confirmation.emails, true))} className="rounded-md bg-primary px-3 py-2 text-xs text-primary-foreground disabled:opacity-50">Create dashboard and share</button>
        <button type="button" disabled={busy} onClick={() => setPendingShare(undefined)} className="rounded-md border border-border px-3 py-2 text-xs hover:bg-muted">Cancel</button>
      </div>
    </section>}
    </>}
    {state.has_owners && owners?.users && <section aria-label="Users" className={`space-y-3${coOwnersOnly ? '' : ' border-t border-border pt-4'}`}>
      <div><h3 className="font-semibold">Users</h3><p className="text-xs text-muted-foreground">Work in it through chat with your setup. They can't change it.</p></div>
      {owners.users.length > 0 && <ul aria-label="Users" className="space-y-3">
        {owners.users.map(person => <li key={person.id} className="space-y-2">
          <div className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{owners.can_manage && removeButton(person, value => changeUsers('user_remove', value))}</div>
          {owners.can_manage && person.email && <ProjectUserFiles person={person} alwaysLocked={owners.always_locked || []} busy={busy} change={paths => { void run(() => changeUsers('user_files', person.email as string, paths)) }} />}
        </li>)}
      </ul>}
      {!!owners.teams?.length && <ul aria-label="Teams" className="space-y-2">
        {owners.teams.map(team => <li key={team.team_id} className="space-y-1.5"><div className="flex min-w-0 items-center gap-2">
          <span aria-hidden="true" className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary"><Users className="h-3.5 w-3.5" /></span>
          <span className="min-w-0 flex-1"><span className="block truncate">{team.name} <span className="text-xs text-muted-foreground">Team</span></span><span className="block truncate text-xs text-muted-foreground">{team.owner}'s Team · everyone in it · {team.people} {team.people === 1 ? 'person' : 'people'}</span></span>
          {team.can_remove && <button type="button" aria-label={`Remove ${team.name}`} disabled={busy} onClick={() => void run(() => changeUsers('team_remove', team.team_id))} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>}
        </div>
        <ProjectTeamAgents teamId={team.team_id} projectId={team.project_id} connection={folderConnection(workspacePath)} newAgents={team.new_agents} agents={team.agents || []} members={team.members || []} busy={busy} run={run} changed={() => setAttempt(n => n + 1)} />
        </li>)}
      </ul>}
      {owners.can_manage && personPicker(taken, value => { void run(() => changeUsers('user_add', value)) }, 'Add a user')}
      {!!owners.team_choices?.length && <select aria-label="Add a Team" value="" disabled={busy} onChange={event => { const id = event.target.value; if (id) void run(() => changeUsers('team_add', id)) }}
        className="h-9 w-full rounded-md border border-dashed border-border bg-background px-2 text-sm text-primary">
        <option value="">+ Add a Team you're in</option>
        {owners.team_choices.map(team => <option key={team.team_id} value={team.team_id}>{team.name} · {team.owner}'s Team · {team.people} {team.people === 1 ? 'person' : 'people'}</option>)}
      </select>}
    </section>}
    {state.has_owners && <section aria-label="Co-owners" className={`space-y-3${coOwnersOnly && !owners?.users ? '' : ' border-t border-border pt-4'}`}>
      <div><h3 className="font-semibold">Co-owners</h3><p className="text-xs text-muted-foreground">Can build and change it, like you.</p></div>
      <ul aria-label="Owners" className="space-y-2">
        {owners?.owner && <li className="flex min-w-0 items-center gap-2">{avatar(owners.owner)}<span className="min-w-0 flex-1 truncate" title={who(owners.owner)}>{who(owners.owner)}</span><span className="shrink-0 text-xs text-muted-foreground">Created it</span></li>}
        {(owners?.co_owners || []).map(person => <li key={person.id} className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{owners?.can_manage && removeButton(person, value => changeOwners('owner_remove', value))}</li>)}
      </ul>
      {ownersError && <p role="alert" className="text-xs text-destructive">{ownersError} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
      {owners?.can_manage && personPicker(taken, addOwner, 'Add a co-owner')}
    </section>}
    {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {/* Which agents others can message is each agent's In team / Only me, in the Team tab's one agent list. */}
    {!coOwnersOnly && !!state.accounts?.length && <details className="border-t border-border pt-3 text-xs"><summary className="cursor-pointer py-1 text-muted-foreground">Details</summary>
      <div className="mt-2 space-y-3">
        {!!state.accounts?.length && <div><p className="font-medium">Accounts used</p><ul className="mt-1 space-y-1">{state.accounts.map((account, i) => <li key={i}>{account.name}<span className="block text-muted-foreground">{account.use}</span></li>)}</ul></div>}
      </div>
    </details>}
  </div>
  const content = peopleContent
  if (isPanel) return <div className="w-full max-w-2xl">{content}</div>
  return <>
    <button ref={trigger} type="button" aria-label={label} aria-expanded={open} onClick={() => setOpen(value => !value)} className="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs font-medium hover:bg-muted md:h-auto"><ShareIcon aria-hidden="true" className="h-3.5 w-3.5" />{label}</button>
    {open && createPortal(<div ref={node => node?.focus()} tabIndex={-1} role="dialog" aria-label={label} className="fixed right-4 top-4 z-[100] flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-xl border border-border bg-background shadow-xl outline-none sm:top-16 sm:max-h-[calc(100dvh-5rem)]"><div className="flex shrink-0 items-center justify-between border-b border-border p-3"><h2 className="text-sm font-semibold">{label}</h2><button type="button" onClick={close} className="rounded px-2 py-1 text-xs hover:bg-muted">Close</button></div><div data-share-dialog-body className="min-h-0 overflow-y-auto overscroll-contain p-4">{content}</div></div>, document.body)}
  </>
}

/** One User's locked files (a project on the server; the main owner only): always the instructions, skills and
 * settings, plus the files or folders added here. */
function ProjectUserFiles({ person, alwaysLocked, busy, change }: {
  person: ProjectUser; alwaysLocked: string[]; busy: boolean; change: (lockedPaths: string[]) => void
}) {
  const [path, setPath] = useState('')
  const extra = person.locked_paths || []
  const name = person.username || person.email || 'This user'
  return <details className="ml-9 text-xs">
    <summary className="cursor-pointer text-muted-foreground">Locked files{extra.length ? ` · ${extra.length} more` : ''}</summary>
    <div className="mt-1.5 space-y-1.5 rounded-md border border-border p-2">
      <p className="text-muted-foreground">Always: {alwaysLocked.join(', ')}</p>
      {extra.length > 0 && <ul aria-label={`More locked files for ${name}`} className="flex flex-wrap gap-1.5">
        {extra.map(item => <li key={item} className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5">{item}
          <button type="button" aria-label={`Unlock ${item}`} disabled={busy} onClick={() => change(extra.filter(other => other !== item))} className="text-muted-foreground hover:text-foreground"><X aria-hidden="true" className="h-3 w-3" /></button></li>)}
      </ul>}
      <form className="flex gap-2" onSubmit={event => { event.preventDefault(); const value = path.trim(); if (!value) return; change([...extra, value]); setPath('') }}>
        <input aria-label={`Lock another file or folder for ${name}`} placeholder="briefs/ or docs/plan.md" value={path} disabled={busy} onChange={event => setPath(event.target.value)} className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2" />
        <button type="submit" disabled={busy || !path.trim()} className="h-8 rounded-md border border-border px-2.5 hover:bg-muted disabled:opacity-50">Lock</button>
      </form>
    </div>
  </details>
}
