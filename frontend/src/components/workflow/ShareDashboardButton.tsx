import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { Users } from 'lucide-react'
import { createPortal } from 'react-dom'
import api from '../../services/api'
import { WorkspaceContextRow as Row } from '../workspace/WorkspaceContextCard'
import { WorkspacePanelActions } from '../workspace/WorkspacePanelHeaderSlots'
import { useWorkspacePanelHosted } from '../workspace/WorkspacePanelNavigation'

type ShareState = {
  mode: 'everyone' | 'people' | 'private'
  people?: { id: string; email?: string; username?: string }[]
  unknown_emails?: string[]
  can_share: boolean
  accounts?: { kind: string; name: string; use: string }[]
  note?: string
  agents?: { chat_id: string; name: string; purpose: string; selected: boolean; can_edit_purpose: boolean; owner_name?: string }[]
}

type OwnersState = {
  owner?: { id: string; email?: string; username?: string }
  co_owners: { id: string; email?: string; username?: string }[]
  can_manage: boolean
}

const who = (p: { id: string; email?: string; username?: string }) => p.email || p.username || p.id

const OPTIONS: { mode: ShareState['mode']; label: string; hint: string }[] = [
  { mode: 'private', label: 'Only me', hint: 'Nobody else can open it.' },
  { mode: 'people', label: 'Chosen people', hint: 'One email per line. Each needs an account here.' },
  { mode: 'everyone', label: 'Everyone', hint: 'Everyone allowed to use this product.' },
]

/** Share this dashboard (and so its Crew, workflow or Relay). The server decides who may share and what a share gives;
 * the screen only sends the choice and shows the answer. Hidden unless the server says this person can share. */
/** variant 'button' is the floating Share button on a dashboard (options in a popup); 'row' is the side-list entry that
 * shows the current choice and calls onOpen; 'panel' is the options themselves, drawn full width in the workspace. */
export function ShareDashboardButton({ workspacePath, label = 'Share', variant = 'button', onOpen, section }: { workspacePath: string; label?: string; variant?: 'button' | 'row' | 'panel'; onOpen?: () => void; section?: 'people' | 'owners' }) {
  const hosted = useWorkspacePanelHosted()
  const [state, setState] = useState<ShareState | null>(null)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<ShareState['mode']>('private')
  const [emails, setEmails] = useState('')
  const [sharedAgents, setSharedAgents] = useState<string[]>([])
  const [agentPurposes, setAgentPurposes] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [owners, setOwners] = useState<OwnersState | null>(null)
  const [ownerEmail, setOwnerEmail] = useState('')
  const [ownersLoading, setOwnersLoading] = useState(false)
  const [ownersError, setOwnersError] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const ownerInputId = useId()
  const shareButton = useRef<HTMLButtonElement>(null)
  const focusPopup = useCallback((node: HTMLDivElement | null) => node?.focus(), [])
  const closePopup = () => { setOpen(false); shareButton.current?.focus() }

  const loadOwners = useCallback(async () => {
    setOwnersLoading(true)
    setOwnersError('')
    try {
      const { data } = await api.post<OwnersState>('/api/dashboards', { workspace: workspacePath, action: 'owners' })
      setOwners(data && Array.isArray(data.co_owners) ? data : null)
    } catch {
      setOwners(null)
      setOwnersError('Could not load the owners. Try again.')
    } finally {
      setOwnersLoading(false)
    }
  }, [workspacePath])

  const changeOwner = async (action: 'owner_add' | 'owner_remove', email: string) => {
    setBusy(true)
    setMessage('')
    try {
      const { data } = await api.post<OwnersState & { note?: string }>('/api/dashboards', { workspace: workspacePath, action, emails: [email] })
      setOwners(data)
      setOwnerEmail('')
      setMessage(data.note || 'Owners saved.')
    } catch (error) {
      const body = (error as { response?: { data?: { error?: string | { message?: string }; message?: string } } }).response?.data
      const detail = typeof body?.error === 'string' ? body.error : body?.error?.message
      setMessage(detail || body?.message || 'Could not change owners. Try again shortly.')
    } finally {
      setBusy(false)
    }
  }

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError('')
    try {
      const { data } = await api.post<ShareState>('/api/dashboards', { workspace: workspacePath, action: 'share_status' })
      setState(data)
      setMode(data.mode)
      setSharedAgents((data.agents || []).filter(agent => agent.selected).map(agent => agent.chat_id))
      setAgentPurposes(Object.fromEntries((data.agents || []).map(agent => [agent.chat_id, agent.purpose])))
      setEmails((data.people ?? []).map(p => p.email || p.username || p.id).join('\n'))
    } catch {
      setState(null)
      setLoadError('Could not load access settings. Try again.')
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => { void load() }, [load])
  const isPanel = variant === 'panel'
  useEffect(() => { if (isPanel) { if (section !== 'people') void loadOwners(); setOpen(true) } }, [isPanel, loadOwners, section])
  if (isPanel && loading && !state) return <p role="status" className="text-sm text-muted-foreground">Loading access settings…</p>
  if (isPanel && loadError && !state) return <div role="alert" className="space-y-2 text-sm"><p>{loadError}</p><button type="button" onClick={() => void load()} className="text-primary underline">Try again</button></div>
  if (!state?.can_share) return isPanel && state ? <p className="text-sm text-muted-foreground">Only the owner can change who this is shared with.</p> : null
  if (variant === 'row') return <Row icon={Users} detail={OPTIONS.find(option => option.mode === state.mode)?.label} onClick={() => onOpen?.()}>{label}</Row>

  const save = async () => {
    setBusy(true)
    setMessage('')
    try {
      const { data } = await api.post<ShareState>('/api/dashboards', {
        workspace: workspacePath,
        action: 'share',
        mode,
        emails: emails.split(/[\s,;]+/).filter(Boolean),
        ...(state.agents ? { shared_agents: sharedAgents, agent_purposes: Object.fromEntries(state.agents.filter(agent => agent.can_edit_purpose && agentPurposes[agent.chat_id] !== agent.purpose).map(agent => [agent.chat_id, agentPurposes[agent.chat_id] || ''])) } : {}),
      })
      setState(data)
      setMessage(data.unknown_emails?.length ? `Saved. No account for: ${data.unknown_emails.join(', ')}` : data.note || 'Saved.')
    } catch (error) {
      const body = (error as { response?: { data?: { error?: string | { message?: string }; message?: string; unknown_emails?: string[] } } }).response?.data
      const extra = body?.unknown_emails?.length ? ` No account for: ${body.unknown_emails.join(', ')}` : ''
      const detail = typeof body?.error === 'string' ? body.error : body?.error?.message
      setMessage(`${detail || body?.message || 'Could not save. Try again shortly.'}${extra}`)
    } finally {
      setBusy(false)
    }
  }

  const content = <>
          {section !== 'owners' && <>
          {isPanel && !hosted && <h3 className="text-base font-semibold">People &amp; access</h3>}
          <p className="text-sm leading-6 text-muted-foreground">People you share with can use the dashboard, published functions and selected agents from the website, their own Crew or an AI connection. Existing file access stays the same.</p>
          {state.accounts && state.accounts.length > 0 && <div className="space-y-3 rounded-lg border border-border p-4" aria-label="Selected accounts">
            <p className="text-xs font-medium">Accounts this project uses</p>
            {state.accounts.map((account, index) => <div key={`${account.kind}:${index}`} className="text-xs">
              <p>{account.kind}: <strong>{account.name}</strong></p>
              <p className="text-muted-foreground">{account.use}</p>
            </div>)}
            <p className="text-xs text-muted-foreground">Choose an available company model account in Project settings → Models, or manage app accounts in Connections. Sharing keeps your current selections.</p>
          </div>}
          {OPTIONS.map(option => (
            <label key={option.mode} className={`flex cursor-pointer items-start gap-3 rounded-lg border p-3 ${mode === option.mode ? 'border-primary/50 bg-primary/5' : 'border-border'}`}>
              <input type="radio" name="share-mode" className="mt-1" checked={mode === option.mode} onChange={() => setMode(option.mode)} />
              <span><span className="font-medium">{option.label}</span><span className="block text-xs text-muted-foreground">{option.hint}</span></span>
            </label>
          ))}
          {mode === 'people' && (
            <textarea value={emails} onChange={event => setEmails(event.target.value)} rows={4} aria-label="Emails to share with" placeholder="name@example.com"
              className="w-full rounded-md border border-border bg-background p-2 text-xs" />
          )}
          {state.agents && <fieldset className="space-y-3 rounded-lg border border-border p-4" disabled={busy}>
            <legend className="px-1 text-sm font-medium">Agents to share</legend>
            <p className="text-xs leading-5 text-muted-foreground">Selected agents use their existing conversation and tools. People in this audience can read messages from sharing onward and send requests as themselves. The tab's owner keeps its controls.</p>
            {state.agents.length === 0 && <p className="text-xs text-muted-foreground">Open a named agent tab to share it here.</p>}
            {state.agents.map(agent => <div key={agent.chat_id} className="space-y-2 border-t border-border pt-3">
              <label className="flex cursor-pointer items-center gap-2 text-sm font-medium"><input type="checkbox" checked={sharedAgents.includes(agent.chat_id)} onChange={event => setSharedAgents(ids => event.target.checked ? [...ids, agent.chat_id] : ids.filter(id => id !== agent.chat_id))} />{agent.name}</label>
              {agent.can_edit_purpose ? <input aria-label={`Purpose of ${agent.name}`} value={agentPurposes[agent.chat_id] || ''} onChange={event => setAgentPurposes(values => ({ ...values, [agent.chat_id]: event.target.value }))} maxLength={300} placeholder="What does this agent help with?" className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm" />
                : <p className="text-xs text-muted-foreground">{agent.purpose || `${agent.owner_name || 'The tab owner'} can add its purpose in chat.`}</p>}
            </div>)}
          </fieldset>}
          </>}
          {section !== 'people' && (section === 'owners' || owners || ownersLoading) && <section className={`space-y-5 ${section === 'owners' ? '' : 'border-t border-border pt-4'}`} aria-label="Owners">
              <div className="space-y-2">{(!hosted || section !== 'owners') && <h3 className="text-base font-semibold">Owners &amp; collaborators</h3>}
              <p className="text-sm leading-6 text-muted-foreground">Owners can edit, share or delete this project and read all Builder chats, including earlier messages. Work runs using the main owner’s account.</p></div>
              {ownersLoading && !owners && <p role="status" className="text-sm text-muted-foreground">Loading owners…</p>}
              {ownersError && <p role="alert" className="text-sm text-destructive">{ownersError} <button type="button" className="underline" onClick={() => void loadOwners()}>Try again</button></p>}
              {owners && <><ul className="divide-y divide-border rounded-lg border border-border" aria-label="Current owners">
                {owners.owner && <li className="flex flex-wrap items-center justify-between gap-2 p-3"><span className="min-w-0 break-words font-medium">{who(owners.owner)}</span><span className="rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground">Main owner</span></li>}
                {owners.co_owners.map(p => (
                  <li key={p.id} className="flex flex-wrap items-center justify-between gap-2 p-3">
                    <span className="min-w-0 break-words">{who(p)}</span>
                    {owners.can_manage ? <button type="button" disabled={busy} onClick={() => void changeOwner('owner_remove', who(p))} className="min-h-8 rounded-md px-2 text-xs text-muted-foreground hover:bg-muted disabled:opacity-50">Remove</button> : <span className="text-xs text-muted-foreground">Owner</span>}
                  </li>
                ))}
              </ul>
              {!owners.co_owners.length && <p className="text-xs text-muted-foreground">No other owners have been added.</p>}
              {owners.can_manage && (
                <div className="space-y-2">
                  <label htmlFor={ownerInputId} className="block text-sm font-medium">Add another owner</label>
                    <div className="flex flex-wrap items-center gap-2"><input id={ownerInputId} value={ownerEmail} onChange={event => setOwnerEmail(event.target.value)} aria-label="Add an owner by email" placeholder="name@example.com" className="h-9 min-w-0 flex-1 basis-52 rounded-md border border-border bg-background px-3 text-sm font-normal" />
                    <button type="button" disabled={busy || !ownerEmail.trim()} onClick={() => void changeOwner('owner_add', ownerEmail.trim())} className="h-9 shrink-0 rounded-md border border-border px-3 text-xs hover:bg-muted disabled:opacity-50">Add owner</button></div>
                  <p className="text-xs text-muted-foreground">Use the email address of someone with an account here.</p>
                </div>
              )}
              </>}
            </section>}
          {message && <p className="text-xs text-muted-foreground" role="status">{message}</p>}
  </>
  const actions = <div className="flex justify-end gap-2">
            {!isPanel && <button type="button" onClick={closePopup} className="rounded-md px-2 py-1 text-xs hover:bg-muted">Close</button>}
            {section !== 'owners' && <WorkspacePanelActions local={!isPanel}><button type="button" disabled={busy} onClick={() => void save()} className="h-8 rounded-md border border-border px-3 text-xs hover:bg-muted disabled:opacity-50">{busy ? 'Saving…' : 'Save'}</button></WorkspacePanelActions>}
          </div>
  if (isPanel) return <div className="w-full max-w-3xl space-y-5 text-sm">{content}{actions}</div>
  return (
    <div className="relative">
      <button ref={shareButton} type="button" onClick={() => { if (!open) void loadOwners(); setOpen(v => !v) }} aria-label={label} title={label} aria-expanded={open}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background/95 px-2 text-xs text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">
        <Users className="h-3.5 w-3.5" /> {label}
      </button>
      {open && createPortal(<div ref={focusPopup} tabIndex={-1} role="dialog" aria-label="Share" onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); closePopup() } }} className="fixed right-4 top-4 z-50 flex max-h-[calc(100dvh-2rem)] w-[min(24rem,calc(100vw-2rem))] flex-col overflow-hidden rounded-lg border border-border bg-background text-sm shadow-xl outline-none sm:top-16 sm:max-h-[calc(100dvh-5rem)]">
        <div data-share-dialog-body className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain p-4">{content}</div>
        <div className="shrink-0 border-t border-border bg-background p-3">{actions}</div>
      </div>, document.body)}
    </div>
  )
}
