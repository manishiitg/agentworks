import { useCallback, useEffect, useRef, useState } from 'react'
import { Bot, Link2, Share as ShareIcon, Users, X } from 'lucide-react'
import { createPortal } from 'react-dom'
import api from '../../services/api'
import { WORKFLOW_REPORT_REFRESH_EVENT } from './reportRefreshEvent'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { WorkspaceContextRow as Row } from '../workspace/WorkspaceContextCard'

type Person = { id: string; email?: string; username?: string }
type ShareState = {
  mode: 'everyone' | 'people' | 'private'; people?: Person[]; can_share: boolean; needs_dashboard?: boolean
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

/** One Access overview. Changes use the project's visible chat and its existing server tools. */
export function ShareDashboardButton({ workspacePath, documentPath, label = 'Share', variant = 'button', onOpen, tabId }: {
  /** The open dashboard or artifact; Copy link copies its link rather than the project's first dashboard. */
  workspacePath: string; documentPath?: string; label?: string; variant?: 'button' | 'row' | 'panel'; onOpen?: () => void
  onAsk?: (message: string) => void | Promise<unknown>; tabId?: string
}) {
  const user = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const streaming = useChatStore(state => tabId ? state.chatTabs[tabId]?.isStreaming === true : false)
  const scope = `${server}:${user}:${workspacePath}`
  const [snapshot, setSnapshot] = useState<{ scope: string; state: ShareState } | null>(null)
  const state = snapshot?.scope === scope ? snapshot.state : null
  const [ownerSnapshot, setOwnerSnapshot] = useState<{ scope: string; owners: OwnersState } | null>(null)
    const owners = ownerSnapshot?.scope === scope ? ownerSnapshot.owners : undefined
  const [ownerEmail, setOwnerEmail] = useState('')
  const [choosing, setChoosing] = useState(false)
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const [ownersError, setOwnersError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [email, setEmail] = useState('')
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
    if (!expanded || !state?.can_share) return
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
  }, [expanded, state?.can_share, workspacePath, user, server, scope, streaming, attempt])
  useEffect(() => {
    if (!open) return
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); close() } }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [open, close])
  if (!state) return isPanel ? <div className="text-sm">{error ? <p role="alert">{error} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p> : <p role="status" className="text-muted-foreground">Loading access…</p>}</div> : null
  if (!state.can_share) return isPanel ? <p className="text-sm text-muted-foreground">Ask a project owner to change access.</p> : null
  if (variant === 'row') return <Row icon={Users} detail={audience(state)} onClick={() => onOpen?.()}>{label}</Row>
  const selected = (state.agents || []).filter(agent => agent.selected)
  const emails = (state.people || []).map(person => person.email).filter((value): value is string => !!value)
  /** The server decides: it takes the mode and the full list of people and answers with the saved state. */
  const putShare = async (next: ShareState['mode'], list?: string[], createDashboard = false) => {
    if (useAuthStore.getState().user?.id !== user || useWorkspaceConnectionStore.getState().activeWorkspaceId !== server) return
    const identity = captureChatIdentity()
    try {
      const { data } = await api.post<ShareState & { unknown_emails?: string[] }>('/api/dashboards', { workspace: workspacePath, action: 'share', mode: next, ...(list ? { emails: list } : {}), ...(createDashboard ? { create_dashboard: true } : {}) })
      if (!isChatIdentityCurrent(identity)) return
      setSnapshot({ scope, state: data }); setEmail(''); setPendingShare(undefined)
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
  // Two groups (owner 2026-11-02): co-owners build and change it; people it is shared with use it (dashboard, chat,
  // functions, their own AI apps over MCP). The share choice is Only me / Chosen people / Everyone; the server stores an
  // empty named list as private, so Only me clears the named list. Owner changes use the same server operation as chat.
  const companyWide = state.mode === 'everyone'
  const shareList = (list: string[]) => putShare('people', list)
  const choice: 'private' | 'people' | 'everyone' = companyWide ? 'everyone' : state.mode === 'people' || choosing ? 'people' : 'private'
  const addOwner = () => {
    const value = ownerEmail.trim().toLowerCase()
    if (!value) return
    void run(async () => {
      await changeOwners('owner_add', value)
      const { data } = await api.post<ShareState>('/api/dashboards', { workspace: workspacePath, action: 'share_status' })
      setSnapshot({ scope, state: data }); setOwnerEmail('')
    })
  }
  const addPerson = () => {
    const value = email.trim().toLowerCase()
    if (!value) return
    if (emails.includes(value)) { setEmail(''); return }
    void run(() => shareList([...emails, value]))
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
  const emailForm = (value: string, setValue: (next: string) => void, submit: () => void, label: string) => <form onSubmit={event => { event.preventDefault(); submit() }} className="flex gap-2">
    <input type="email" aria-label={label} placeholder="name@company.com" value={value} disabled={busy} onChange={event => setValue(event.target.value)} className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm" />
    <button type="submit" disabled={busy || !value.trim()} className="h-9 rounded-md border border-border px-3 text-sm hover:bg-muted disabled:opacity-50">Add</button>
  </form>
  const choices = [['private', 'Only me'], ['people', 'Chosen people'], ['everyone', 'Everyone']] as const
  const peopleContent = <div className="space-y-6 text-sm">
    <section aria-label="Shared with" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-semibold">Shared with</h3>
        <button type="button" onClick={() => void copyLink()} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs hover:bg-muted"><Link2 aria-hidden="true" className="h-3.5 w-3.5" />Copy dashboard link</button>
      </div>
      <div role="radiogroup" aria-label="Who it is shared with" className="inline-flex rounded-lg border border-border p-0.5">
        {choices.map(([value, text]) => <button key={value} type="button" role="radio" aria-checked={choice === value} disabled={busy}
          onClick={() => { setChoosing(value === 'people'); void run(() => value === 'everyone' ? putShare('everyone') : shareList(value === 'people' ? emails : [])) }}
          className={`rounded-md px-3 py-1.5 text-xs ${choice === value ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:text-foreground'}`}>{text}</button>)}
      </div>
      <p className="text-xs text-muted-foreground">{choice === 'everyone' ? 'Everyone at the company can use it.' : choice === 'people' ? 'These people can use it.' : 'Only you and co-owners can use it.'} Using means opening its dashboard, chatting with it and reaching it from their own AI apps.</p>
      {choice === 'people' && <>
        <ul aria-label="People it is shared with" className="space-y-2">
          {(state.people || []).map(person => <li key={person.id} className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{removeButton(person, value => shareList(emails.filter(item => item !== value)))}</li>)}
        </ul>
        {emailForm(email, setEmail, addPerson, 'Share with a person by email')}
      </>}
    </section>
    {confirmation && <section role="region" aria-label="Create a dashboard to share" className="space-y-3 rounded-lg border border-border p-3">
      <h3 className="font-semibold">Create a dashboard for this Crew?</h3>
      <p className="text-xs text-muted-foreground">People will open it to chat with this Crew and use its published functions.</p>
      <p className="text-xs">Share with: {confirmation.mode === 'everyone' ? 'Everyone at the company' : confirmation.emails?.join(', ')}</p>
      <div className="flex flex-wrap gap-2">
        <button type="button" disabled={busy} onClick={() => void run(() => putShare(confirmation.mode, confirmation.emails, true))} className="rounded-md bg-primary px-3 py-2 text-xs text-primary-foreground disabled:opacity-50">Create dashboard and share</button>
        <button type="button" disabled={busy} onClick={() => setPendingShare(undefined)} className="rounded-md border border-border px-3 py-2 text-xs hover:bg-muted">Cancel</button>
      </div>
    </section>}
    <section aria-label="Co-owners" className="space-y-3 border-t border-border pt-4">
      <div><h3 className="font-semibold">Co-owners</h3><p className="text-xs text-muted-foreground">Can build and change it, like you.</p></div>
      <ul aria-label="Owners" className="space-y-2">
        {owners?.owner && <li className="flex min-w-0 items-center gap-2">{avatar(owners.owner)}<span className="min-w-0 flex-1 truncate" title={who(owners.owner)}>{who(owners.owner)}</span><span className="shrink-0 text-xs text-muted-foreground">Created it</span></li>}
        {(owners?.co_owners || []).map(person => <li key={person.id} className="flex min-w-0 items-center gap-2">{avatar(person)}<span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span>{owners?.can_manage && removeButton(person, value => changeOwners('owner_remove', value))}</li>)}
      </ul>
      {ownersError && <p role="alert" className="text-xs text-destructive">{ownersError} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
      {owners?.can_manage && emailForm(ownerEmail, setOwnerEmail, addOwner, 'Add a co-owner by email')}
    </section>
    {notice && <p role="status" className="text-xs text-muted-foreground">{notice}</p>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {(state.agents || !!state.accounts?.length) && <details className="border-t border-border pt-3 text-xs"><summary className="cursor-pointer py-1 text-muted-foreground">Details</summary>
      <div className="mt-2 space-y-3">
        {state.agents && <div><p className="font-medium">Agents others can message ({selected.length})</p>{selected.map(agent => <div key={agent.chat_id} className="mt-1 flex items-start gap-2"><Bot aria-hidden="true" className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span>{agent.name}{agent.purpose && <span className="block text-muted-foreground">{agent.purpose}</span>}</span></div>)}{!selected.length && <p className="text-muted-foreground">None yet. Ask in chat to share one of this Crew's agents.</p>}</div>}
        {!!state.accounts?.length && <div><p className="font-medium">Accounts used</p><ul className="mt-1 space-y-1">{state.accounts.map((account, i) => <li key={i}>{account.name}<span className="block text-muted-foreground">{account.use}</span></li>)}</ul></div>}
      </div>
    </details>}
  </div>
  const content = peopleContent
  if (isPanel) return <div className="w-full max-w-2xl">{content}</div>
  return <>
    <button ref={trigger} type="button" aria-label={label} aria-expanded={open} onClick={() => setOpen(value => !value)} className="inline-flex h-10 shrink-0 items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs font-medium hover:bg-muted md:h-auto"><ShareIcon aria-hidden="true" className="h-3.5 w-3.5" />{label}</button>
    {open && createPortal(<div ref={node => node?.focus()} tabIndex={-1} role="dialog" aria-label="Access" className="fixed right-4 top-4 z-[100] flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-xl border border-border bg-background shadow-xl outline-none sm:top-16 sm:max-h-[calc(100dvh-5rem)]"><div className="flex shrink-0 items-center justify-between border-b border-border p-3"><h2 className="text-sm font-semibold">Access</h2><button type="button" onClick={close} className="rounded px-2 py-1 text-xs hover:bg-muted">Close</button></div><div data-share-dialog-body className="min-h-0 overflow-y-auto overscroll-contain p-4">{content}</div></div>, document.body)}
  </>
}
