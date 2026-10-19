import { useCallback, useEffect, useState } from 'react'
import { Users } from 'lucide-react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import api from '../../services/api'

type ShareState = {
  mode: 'everyone' | 'people' | 'private'
  people?: { id: string; email?: string; username?: string }[]
  unknown_emails?: string[]
  can_share: boolean
  accounts?: { kind: string; name: string; use: string }[]
  note?: string
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
export function ShareDashboardButton({ workspacePath, label = 'Share', inline = false }: { workspacePath: string; label?: string; inline?: boolean }) {
  const [state, setState] = useState<ShareState | null>(null)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<ShareState['mode']>('private')
  const [emails, setEmails] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [owners, setOwners] = useState<OwnersState | null>(null)
  const [ownerEmail, setOwnerEmail] = useState('')

  const loadOwners = useCallback(async () => {
    try {
      const { data } = await api.post<OwnersState>('/api/dashboards', { workspace: workspacePath, action: 'owners' })
      setOwners(data && Array.isArray(data.co_owners) ? data : null)
    } catch {
      setOwners(null)
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
    try {
      const { data } = await api.post<ShareState>('/api/dashboards', { workspace: workspacePath, action: 'share_status' })
      setState(data)
      setMode(data.mode)
      setEmails((data.people ?? []).map(p => p.email || p.username || p.id).join('\n'))
    } catch {
      setState(null)
    }
  }, [workspacePath])

  useEffect(() => { void load() }, [load])
  useWorkspaceViewTarget('context', target => { if (target === 'share') { void loadOwners(); setOpen(true) } }, inline && state?.can_share === true)
  if (!state?.can_share) return null

  const save = async () => {
    setBusy(true)
    setMessage('')
    try {
      const { data } = await api.post<ShareState>('/api/dashboards', {
        workspace: workspacePath,
        action: 'share',
        mode,
        emails: emails.split(/[\s,;]+/).filter(Boolean),
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

  return (
    <div className="relative">
      <button type="button" onClick={() => { if (!open) void loadOwners(); setOpen(v => !v) }} aria-label={label} title={label} aria-expanded={open}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background/95 px-2 text-xs text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">
        <Users className="h-3.5 w-3.5" /> {label}
      </button>
      {open && (
        <div role="dialog" aria-label="Share" className={`${inline ? 'relative mt-3 w-full' : 'absolute right-0 mt-1 w-72'} space-y-2 rounded-md border border-border bg-background p-3 text-sm shadow-lg`}>
          <p className="text-xs text-muted-foreground">People you share with open its dashboard, can ask it, use its functions and read its files (not secrets).</p>
          {state.accounts && state.accounts.length > 0 && <div className="space-y-1 border-b border-border pb-2" aria-label="Selected accounts">
            <p className="text-xs font-medium">Accounts this project uses</p>
            {state.accounts.map((account, index) => <div key={`${account.kind}:${index}`} className="text-xs">
              <p>{account.kind}: <strong>{account.name}</strong></p>
              <p className="text-muted-foreground">{account.use}</p>
            </div>)}
            <p className="text-xs text-muted-foreground">Choose an available company model account in Project settings → Models, or manage app accounts in Connections. Sharing keeps your current selections.</p>
          </div>}
          {OPTIONS.map(option => (
            <label key={option.mode} className="flex cursor-pointer items-start gap-2">
              <input type="radio" name="share-mode" className="mt-1" checked={mode === option.mode} onChange={() => setMode(option.mode)} />
              <span><span className="font-medium">{option.label}</span><span className="block text-xs text-muted-foreground">{option.hint}</span></span>
            </label>
          ))}
          {mode === 'people' && (
            <textarea value={emails} onChange={event => setEmails(event.target.value)} rows={4} aria-label="Emails to share with" placeholder="name@example.com"
              className="w-full rounded-md border border-border bg-background p-2 text-xs" />
          )}
          {owners && (
            <div className="space-y-1 border-t border-border pt-2" aria-label="Owners">
              <p className="font-medium">Owners</p>
              <p className="text-xs text-muted-foreground">Owners build this together and can share or delete it. All Builders see every Builder chat, including existing history. It still runs as the main owner.</p>
              <ul className="text-xs">
                {owners.owner && <li>{who(owners.owner)} <span className="text-muted-foreground">(main owner)</span></li>}
                {owners.co_owners.map(p => (
                  <li key={p.id} className="flex items-center justify-between">
                    <span>{who(p)}</span>
                    {owners.can_manage && <button type="button" disabled={busy} onClick={() => void changeOwner('owner_remove', who(p))} className="rounded px-1 text-muted-foreground hover:bg-muted">Remove</button>}
                  </li>
                ))}
              </ul>
              {owners.can_manage && (
                <div className="flex gap-1">
                  <input value={ownerEmail} onChange={event => setOwnerEmail(event.target.value)} aria-label="Add an owner by email" placeholder="name@example.com" className="min-w-0 flex-1 rounded-md border border-border bg-background p-1 text-xs" />
                  <button type="button" disabled={busy || !ownerEmail.trim()} onClick={() => void changeOwner('owner_add', ownerEmail.trim())} className="rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">Add</button>
                </div>
              )}
            </div>
          )}
          {message && <p className="text-xs text-muted-foreground" role="status">{message}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setOpen(false)} className="rounded-md px-2 py-1 text-xs hover:bg-muted">Close</button>
            <button type="button" disabled={busy} onClick={() => void save()} className="rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50">{busy ? 'Saving…' : 'Save'}</button>
          </div>
        </div>
      )}
    </div>
  )
}
