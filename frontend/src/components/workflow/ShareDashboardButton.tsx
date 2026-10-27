import { useCallback, useEffect, useRef, useState } from 'react'
import { Bot, Link2, MessageSquare, Share as ShareIcon, Users, X } from 'lucide-react'
import { createPortal } from 'react-dom'
import api, { authApi } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { WorkspaceContextRow as Row } from '../workspace/WorkspaceContextCard'

type Person = { id: string; email?: string; username?: string }
type ShareState = {
  mode: 'everyone' | 'people' | 'private'; people?: Person[]; can_share: boolean
  accounts?: { kind: string; name: string; use: string }[]
  agents?: { chat_id: string; name: string; purpose: string; selected: boolean }[]
}
type OwnersState = { owner?: Person; co_owners: Person[]; can_manage: boolean }
const who = (person: Person) => person.email || person.username || person.id
const audience = (state: ShareState) => state.mode === 'everyone' ? 'Everyone' : state.mode === 'people' ? 'Chosen people' : 'Only owners'
const errorText = (cause: unknown, fallback: string) => {
  const data = (cause as { response?: { data?: { error?: { message?: string } | string; message?: string } } })?.response?.data
  return (typeof data?.error === 'string' ? data.error : data?.error?.message) || data?.message || fallback
}
export const ACCESS_CHAT_REQUEST = 'Help me change access to this project. Show who can build it, who can use it and which existing agents are shared, then ask what I want to change.'

/** One Access overview. Changes use the project's visible chat and its existing server tools. */
export function ShareDashboardButton({ workspacePath, label = 'Share', variant = 'button', onOpen, onAsk, tabId }: {
  workspacePath: string; label?: string; variant?: 'button' | 'row' | 'panel'; onOpen?: () => void
  onAsk?: (message: string) => void | Promise<unknown>; tabId?: string
}) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const streaming = useChatStore(state => tabId ? state.chatTabs[tabId]?.isStreaming === true : false)
  const scope = `${server}:${user}:${workspacePath}`
  const [snapshot, setSnapshot] = useState<{ scope: string; state: ShareState } | null>(null)
  const state = snapshot?.scope === scope ? snapshot.state : null
  const [ownerSnapshot, setOwnerSnapshot] = useState<{ scope: string; rows: { person: Person; role: string }[]; crew?: OwnersState } | null>(null)
  const owners = ownerSnapshot?.scope === scope ? ownerSnapshot.rows : []
  const crewOwners = ownerSnapshot?.scope === scope ? ownerSnapshot.crew : undefined
  // Goals and Flows (Workflow/...) keep their owners/editors view; Crews get the simple People list.
  const isCrew = !/(?:^|\/)Workflow\//i.test(workspacePath)
  const [role, setRole] = useState<'use' | 'owner'>('use')
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const [ownersError, setOwnersError] = useState('')
  const [asking, setAsking] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [pickedPeople, setPickedPeople] = useState(false)
  const [email, setEmail] = useState('')
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
    if (!expanded || !state?.can_share) return
    const identity = captureChatIdentity()
    let disposed = false
    setOwnerSnapshot(null); setOwnersError('')
    const load = async () => {
      if (/(?:^|\/)Workflow\//i.test(workspacePath)) {
        const access = await authApi.getWorkflowAccess(workspacePath)
        return [...access.owners.map(person => ({ person, role: 'Owner' })), ...access.editors.map(person => ({ person, role: 'Builder' }))]
      }
      const { data } = await api.post<OwnersState>('/api/dashboards', { workspace: workspacePath, action: 'owners' })
      return { crew: data, rows: [...(data.owner ? [{ person: data.owner, role: 'Main owner' }] : []), ...(data.co_owners || []).map(person => ({ person, role: 'Owner' }))] }
    }
    void load().then(loaded => { if (!disposed && isChatIdentityCurrent(identity)) setOwnerSnapshot(Array.isArray(loaded) ? { scope, rows: loaded } : { scope, ...loaded }) })
      .catch(() => { if (!disposed && isChatIdentityCurrent(identity)) setOwnersError('Could not load the owners. Try again.') })
    return () => { disposed = true }
  }, [expanded, state?.can_share, workspacePath, user, server, scope, streaming, attempt])
  useEffect(() => {
    if (!open) return
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); close() } }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [open, close])
  const ask = async (message: string) => {
    if (!onAsk || asking) return
    setAsking(true); setError('')
    try { await onAsk(message); if (!isPanel) close() }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not open this project chat.') }
    finally { setAsking(false) }
  }
  if (!state) return isPanel ? <div className="text-sm">{error ? <p role="alert">{error} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p> : <p role="status" className="text-muted-foreground">Loading access…</p>}</div> : null
  if (!state.can_share) return isPanel ? <p className="text-sm text-muted-foreground">Ask a project owner to change access.</p> : null
  if (variant === 'row') return <Row icon={Users} detail={audience(state)} onClick={() => onOpen?.()}>{label}</Row>
  const selected = (state.agents || []).filter(agent => agent.selected)
  const emails = (state.people || []).map(person => person.email).filter((value): value is string => !!value)
  const mode = pickedPeople ? 'people' : state.mode
  /** The server decides: it takes the mode and the full list of people and answers with the saved state. */
  const putShare = async (next: ShareState['mode'], list?: string[]) => {
    const { data } = await api.post<ShareState & { unknown_emails?: string[] }>('/api/dashboards', { workspace: workspacePath, action: 'share', mode: next, ...(list ? { emails: list } : {}) })
    setSnapshot({ scope, state: data }); setPickedPeople(false); setEmail('')
    if (data.unknown_emails?.length) setNotice(`No account for ${data.unknown_emails.join(', ')}.`)
  }
  const run = async (work: () => Promise<void>, fallback = 'Could not change access.') => {
    setBusy(true); setError(''); setNotice('')
    try { await work() } catch (cause) { setError(errorText(cause, fallback)) }
    finally { setBusy(false) }
  }
  const save = (next: ShareState['mode'], list?: string[]) => run(() => putShare(next, list))
  const choose = (next: ShareState['mode']) => {
    if (next === 'people') { if (emails.length) void save('people', emails); else setPickedPeople(true) } else void save(next)
  }
  const add = () => {
    const value = email.trim().toLowerCase()
    if (!value) return
    if (emails.includes(value)) { setEmail(''); return }
    void save('people', [...emails, value])
  }
  const remove = (value: string) => {
    const rest = emails.filter(item => item !== value)
    void save(rest.length ? 'people' : 'private', rest.length ? rest : undefined)
  }
  const copyLink = async () => {
    setError(''); setNotice('')
    try {
      const { data } = await api.post<{ dashboards?: { dashboard_id?: string; short_url?: string; url?: string }[] }>('/api/dashboards', { workspace: workspacePath, action: 'list' })
      const rows = (data.dashboards || []).filter(row => row.short_url || row.url)
      const row = rows.find(item => item.dashboard_id === 'overview') || rows[0]
      const link = row?.short_url || row?.url
      if (!link) { setError('There is no published dashboard to link to yet.'); return }
      await navigator.clipboard.writeText(link)
      setNotice('Link copied. People still need access to open it.')
    } catch (cause) { setError(errorText(cause, 'Could not copy the link.')) }
  }
  // Crew access (owner 2026-10-27): one People list with Owner / Can use, and one company-wide choice. "Can use" people are
  // the Run share's list; only the company-wide choice switches between everyone and named people, so removing one person
  // never changes anyone else's access. Owner changes use the same server operation as the chat tool.
  const companyWide = state.mode === 'everyone'
  const shareList = (list: string[]) => putShare(list.length ? 'people' : 'private', list.length ? list : undefined)
  const refreshShare = async () => {
    const { data } = await api.post<ShareState>('/api/dashboards', { workspace: workspacePath, action: 'share_status' })
    setSnapshot({ scope, state: data })
  }
  const changeOwners = async (action: 'owner_add' | 'owner_remove', value: string) => {
    const { data } = await api.post<OwnersState & { note?: string }>('/api/dashboards', { workspace: workspacePath, action, emails: [value] })
    setOwnerSnapshot({ scope, crew: data, rows: [] })
    if (data.note) setNotice(data.note)
  }
  // Adding an owner also takes them off the Can use list on the server, so the share is re-read afterwards.
  const makeOwner = async (value: string) => { await changeOwners('owner_add', value); await refreshShare() }
  const makeUser = async (value: string) => {
    await changeOwners('owner_remove', value)
    if (!companyWide && !emails.includes(value)) await shareList([...emails, value])
  }
  const addPerson = () => {
    const value = email.trim().toLowerCase()
    if (!value) return
    if (role === 'owner') { void run(async () => { await makeOwner(value); setEmail('') }); return }
    if (companyWide) { setNotice('Everyone in the company can already use it.'); setEmail(''); return }
    if (emails.includes(value)) { setEmail(''); return }
    void run(() => shareList([...emails, value]))
  }
  const avatar = (person: Person) => <span aria-hidden="true" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">{who(person).slice(0, 2).toUpperCase()}</span>
  const roleSelect = (person: Person, current: 'owner' | 'use') => person.email && crewOwners?.can_manage
    ? <select aria-label={`Access for ${who(person)}`} value={current} disabled={busy} onChange={event => { const value = person.email as string; void run(() => event.target.value === 'owner' ? makeOwner(value) : makeUser(value)) }} className="h-8 shrink-0 rounded-md border border-border bg-background px-1.5 text-xs">
        <option value="owner">Owner</option><option value="use">Can use</option>
      </select>
    : <span className="shrink-0 text-xs text-muted-foreground">{current === 'owner' ? 'Owner' : 'Can use'}</span>
  const removeButton = (person: Person, remove: (value: string) => Promise<void>) => person.email && crewOwners?.can_manage !== false
    ? <button type="button" aria-label={`Remove ${who(person)}`} disabled={busy} onClick={() => void run(() => remove(person.email as string))} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>
    : null
  const crewContent = <div className="space-y-6 text-sm">
    <section aria-label="People" className="space-y-3">
      <h3 className="font-semibold">People</h3>
      <ul aria-label="People with access" className="space-y-2">
        {crewOwners?.owner && <li className="flex min-w-0 items-center gap-3">{avatar(crewOwners.owner)}<span className="min-w-0 flex-1 truncate" title={who(crewOwners.owner)}>{who(crewOwners.owner)}</span><span className="shrink-0 text-xs text-muted-foreground">Owner (created this)</span></li>}
        {(crewOwners?.co_owners || []).map(person => <li key={`owner-${person.id}`} className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{roleSelect(person, 'owner')}{removeButton(person, value => changeOwners('owner_remove', value))}</li>)}
        {!companyWide && (state.people || []).map(person => <li key={`use-${person.id}`} className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{roleSelect(person, 'use')}{removeButton(person, value => shareList(emails.filter(item => item !== value)))}</li>)}
      </ul>
      {ownersError && <p role="alert" className="text-xs text-destructive">{ownersError} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
      <form onSubmit={event => { event.preventDefault(); addPerson() }} className="flex flex-wrap gap-2">
        <input type="email" aria-label="Add a person by email" placeholder="Add a person by email" value={email} disabled={busy} onChange={event => setEmail(event.target.value)} className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm" />
        <select aria-label="Access for the new person" value={role} disabled={busy} onChange={event => setRole(event.target.value as 'use' | 'owner')} className="h-9 rounded-md border border-border bg-background px-2 text-sm">
          <option value="use">Can use</option>{crewOwners?.can_manage && <option value="owner">Owner</option>}
        </select>
        <button type="button" onClick={addPerson} disabled={busy || !email.trim()} className="h-9 rounded-md border border-border px-3 text-sm hover:bg-muted disabled:opacity-50">Add</button>
      </form>
      <p className="text-xs text-muted-foreground">Owner: build and change it. Can use: use the dashboard and ask for work.</p>
    </section>
    <section aria-label="Anyone else in the company" className="space-y-2 border-t border-border pt-4">
      <label htmlFor="company-access" className="font-semibold">Anyone else in the company</label>
      <div className="flex flex-wrap items-center gap-2">
        <select id="company-access" value={companyWide ? 'use' : 'none'} disabled={busy} onChange={event => void run(() => event.target.value === 'use' ? putShare('everyone') : shareList(emails))} className="h-9 min-w-0 rounded-md border border-border bg-background px-2 text-sm">
          <option value="none">Can't access</option>
          <option value="use">Can use</option>
        </select>
        <button type="button" onClick={() => void copyLink()} className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3 text-sm hover:bg-muted"><Link2 aria-hidden="true" className="h-4 w-4" />Copy link</button>
      </div>
      <p className="text-xs text-muted-foreground">The link opens the dashboard as a user, also for you, so you can try it the way they will.</p>
    </section>
    {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    <div className="space-y-1 border-t border-border pt-3">
      {state.agents && <details><summary className="cursor-pointer py-1 text-sm">Agents others can message <span className="text-muted-foreground">({selected.length})</span></summary>
        <div className="mt-2 space-y-2">{selected.map(agent => <div key={agent.chat_id} className="flex items-start gap-2.5"><Bot aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><div className="min-w-0"><p className="break-words font-medium">{agent.name}</p>{agent.purpose && <p className="text-xs text-muted-foreground">{agent.purpose}</p>}</div></div>)}
          {!selected.length && <p className="text-xs text-muted-foreground">None yet. Ask in chat to share one of this Crew's agents.</p>}</div></details>}
      {!!state.accounts?.length && <details><summary className="cursor-pointer py-1 text-sm">Accounts used</summary><ul className="mt-2 space-y-2 text-xs">{state.accounts.map((account, i) => <li key={i}><span className="font-medium">{account.name}</span><span className="block text-muted-foreground">{account.use}</span></li>)}</ul></details>}
      {onAsk && <button type="button" disabled={asking} onClick={() => void ask(ACCESS_CHAT_REQUEST)} className="inline-flex items-center gap-1.5 py-1 text-xs text-muted-foreground hover:text-foreground disabled:opacity-50"><MessageSquare aria-hidden="true" className="h-3.5 w-3.5" />Or change access in chat</button>}
    </div>
  </div>
  const content = isCrew ? crewContent : <div className="space-y-6 text-sm">
    <section aria-label="Who can build" className="space-y-3">
      <h3 className="font-semibold">Who can build</h3>
      <ul aria-label="Current owners" className="space-y-3">{owners.map(({ person, role }) => <li key={person.id} className="flex min-w-0 items-center gap-3">
        <span aria-hidden="true" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">{who(person).slice(0, 2).toUpperCase()}</span>
        <span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span><span className="shrink-0 text-xs text-muted-foreground">{role}</span>
      </li>)}</ul>
      {ownersError && <p role="alert" className="text-xs text-destructive">{ownersError} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
      <p className="text-xs text-muted-foreground">Owners share the project and its history. Each controls their own chat tabs.</p>
    </section>
    <section aria-label="Who can use" className="space-y-3 border-t border-border pt-4">
      <h3 className="font-semibold">Who can use</h3>
      <div className="flex flex-wrap items-center gap-2">
        <label className="sr-only" htmlFor="general-access">General access</label>
        <select id="general-access" value={mode} disabled={busy} onChange={event => choose(event.target.value as ShareState['mode'])} className="h-9 min-w-0 rounded-md border border-border bg-background px-2 text-sm">
          <option value="private">Only owners</option>
          <option value="people">Chosen people</option>
          <option value="everyone">Everyone</option>
        </select>
        <button type="button" onClick={() => void copyLink()} className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3 text-sm hover:bg-muted"><Link2 aria-hidden="true" className="h-4 w-4" />Copy link</button>
      </div>
      {mode === 'people' && <div className="space-y-2">
        <ul aria-label="People with access" className="space-y-1">{(state.people || []).map(person => <li key={person.id} className="flex min-w-0 items-center gap-2 text-sm">
          <span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>
          {person.email && <button type="button" aria-label={`Remove ${who(person)}`} disabled={busy} onClick={() => remove(person.email as string)} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>}
        </li>)}</ul>
        <form onSubmit={event => { event.preventDefault(); add() }} className="flex gap-2">
          <input type="email" aria-label="Add a person by email" placeholder="Add a person by email" value={email} disabled={busy} onChange={event => setEmail(event.target.value)} className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm" />
          <button type="button" onClick={add} disabled={busy || !email.trim()} className="h-9 rounded-md border border-border px-3 text-sm hover:bg-muted disabled:opacity-50">Add</button>
        </form>
      </div>}
      {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}
      <p className="text-xs text-muted-foreground">People you share with can use the dashboard and ask for work from the website, their Crew or MCP. The link opens the dashboard as a user, also for you, so you can try it the way they will.</p>
    </section>
    {state.agents && <section aria-label="Shared agents" className="space-y-3 border-t border-border pt-4">
      <h3 className="font-semibold">Shared agents <span className="font-normal text-muted-foreground">{selected.length}</span></h3>
      {selected.map(agent => <div key={agent.chat_id} className="flex items-start gap-2.5"><Bot aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" /><div className="min-w-0"><p className="break-words font-medium">{agent.name}</p>{agent.purpose && <p className="text-xs text-muted-foreground">{agent.purpose}</p>}</div></div>)}
      {!selected.length && <p className="text-xs text-muted-foreground">No agents shared yet.</p>}
      {!!selected.length && <p className="text-xs text-muted-foreground">People can read messages from sharing onward and ask these agents for work.</p>}
    </section>}
    {!!state.accounts?.length && <details className="border-t border-border pt-4"><summary className="cursor-pointer text-xs text-muted-foreground">Accounts used for this work</summary><ul className="mt-3 space-y-2 text-xs">{state.accounts.map((account, i) => <li key={i}><span className="font-medium">{account.name}</span><span className="block text-muted-foreground">{account.use}</span></li>)}</ul></details>}
    <div className="space-y-2 border-t border-border pt-4">{onAsk ? <button type="button" disabled={asking} onClick={() => void ask(ACCESS_CHAT_REQUEST)} className="inline-flex min-h-9 items-center gap-2 rounded-md border border-border px-3 text-sm hover:bg-muted disabled:opacity-50"><MessageSquare aria-hidden="true" className="h-4 w-4" />Change access in chat</button> : <p className="text-xs text-muted-foreground">Open the project chat to change access.</p>}
      <p className="text-xs text-muted-foreground">Ask to add an owner, share with someone, or share an existing agent and what it helps with.</p>
    </div>{error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
  if (isPanel) return <div className="w-full max-w-2xl">{content}</div>
  return <>
    <button ref={trigger} type="button" aria-label={label} aria-expanded={open} onClick={() => setOpen(value => !value)} className="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs font-medium hover:bg-muted md:h-auto"><ShareIcon aria-hidden="true" className="h-3.5 w-3.5" />{label}</button>
    {open && createPortal(<div ref={node => node?.focus()} tabIndex={-1} role="dialog" aria-label="Access" className="fixed right-4 top-4 z-[100] flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-xl border border-border bg-background shadow-xl outline-none sm:top-16 sm:max-h-[calc(100dvh-5rem)]"><div className="flex shrink-0 items-center justify-between border-b border-border p-3"><h2 className="text-sm font-semibold">Access</h2><button type="button" onClick={close} className="rounded px-2 py-1 text-xs hover:bg-muted">Close</button></div><div data-share-dialog-body className="min-h-0 overflow-y-auto overscroll-contain p-4">{content}</div></div>, document.body)}
  </>
}
