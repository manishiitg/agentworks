import { useCallback, useEffect, useRef, useState } from 'react'
import { Bot, MessageSquare, Users } from 'lucide-react'
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
  const [ownerSnapshot, setOwnerSnapshot] = useState<{ scope: string; rows: { person: Person; role: string }[] } | null>(null)
  const owners = ownerSnapshot?.scope === scope ? ownerSnapshot.rows : []
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const [ownersError, setOwnersError] = useState('')
  const [asking, setAsking] = useState(false)
  const [attempt, setAttempt] = useState(0)
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
      return [...(data.owner ? [{ person: data.owner, role: 'Main owner' }] : []), ...(data.co_owners || []).map(person => ({ person, role: 'Owner' }))]
    }
    void load().then(rows => { if (!disposed && isChatIdentityCurrent(identity)) setOwnerSnapshot({ scope, rows }) })
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
  if (variant === 'row') return <Row icon={Users} detail={audience(state)} onClick={onOpen}>{label}</Row>
  const selected = (state.agents || []).filter(agent => agent.selected)
  const content = <div className="space-y-6 text-sm">
    <section aria-label="Who can build" className="space-y-3">
      <h3 className="font-semibold">Who can build</h3>
      <ul aria-label="Current owners" className="space-y-3">{owners.map(({ person, role }) => <li key={person.id} className="flex min-w-0 items-center gap-3">
        <span aria-hidden="true" className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">{who(person).slice(0, 2).toUpperCase()}</span>
        <span className="min-w-0 flex-1 truncate" title={who(person)}>{who(person)}</span><span className="shrink-0 text-xs text-muted-foreground">{role}</span>
      </li>)}</ul>
      {ownersError && <p role="alert" className="text-xs text-destructive">{ownersError} <button type="button" className="underline" onClick={() => setAttempt(n => n + 1)}>Try again</button></p>}
      <p className="text-xs text-muted-foreground">Owners share the project and its history. Each controls their own chat tabs.</p>
    </section>
    <section aria-label="Who can use" className="space-y-2 border-t border-border pt-4">
      <h3 className="font-semibold">Who can use</h3><p>{audience(state)}</p>
      {state.mode === 'people' && <ul className="space-y-1 text-xs text-muted-foreground">{state.people?.map(person => <li key={person.id} className="break-words">{who(person)}</li>)}</ul>}
      <p className="text-xs text-muted-foreground">People you share with can use the dashboard and ask for work from the website, their Crew or MCP.</p>
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
    <button ref={trigger} type="button" aria-label={label} aria-expanded={open} onClick={() => setOpen(value => !value)} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2 text-xs hover:bg-muted"><Users aria-hidden="true" className="h-3.5 w-3.5" />{label}</button>
    {open && createPortal(<div ref={node => node?.focus()} tabIndex={-1} role="dialog" aria-label="Access" className="fixed right-4 top-4 z-[100] flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-xl border border-border bg-background shadow-xl outline-none sm:top-16 sm:max-h-[calc(100dvh-5rem)]"><div className="flex shrink-0 items-center justify-between border-b border-border p-3"><h2 className="text-sm font-semibold">Access</h2><button type="button" onClick={close} className="rounded px-2 py-1 text-xs hover:bg-muted">Close</button></div><div data-share-dialog-body className="min-h-0 overflow-y-auto overscroll-contain p-4">{content}</div></div>, document.body)}
  </>
}
