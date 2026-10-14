import { useCallback, useEffect, useState } from 'react'
import { Users } from 'lucide-react'
import api from '../../services/api'

type ShareState = {
  mode: 'everyone' | 'people' | 'private'
  people?: { id: string; email?: string; username?: string }[]
  unknown_emails?: string[]
  can_share: boolean
}

const OPTIONS: { mode: ShareState['mode']; label: string; hint: string }[] = [
  { mode: 'everyone', label: 'Everyone on this server', hint: 'They can open the dashboard, ask questions and use its buttons.' },
  { mode: 'people', label: 'Only the people I list', hint: 'One email per line. Each needs an account here.' },
  { mode: 'private', label: 'Only me', hint: 'Nobody else can open it.' },
]

/** Share this dashboard (and so its Crew, workflow or Relay). The server decides who may share and what a share gives;
 * the screen only sends the choice and shows the answer. Hidden unless the server says this person can share. */
export function ShareDashboardButton({ workspacePath }: { workspacePath: string }) {
  const [state, setState] = useState<ShareState | null>(null)
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<ShareState['mode']>('private')
  const [emails, setEmails] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')

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
      setMessage(data.unknown_emails?.length ? `Saved. No account for: ${data.unknown_emails.join(', ')}` : 'Saved.')
    } catch (error) {
      const body = (error as { response?: { data?: { error?: string; message?: string; unknown_emails?: string[] } } }).response?.data
      const extra = body?.unknown_emails?.length ? ` No account for: ${body.unknown_emails.join(', ')}` : ''
      setMessage(`${body?.error || body?.message || 'Could not save.'}${extra}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative">
      <button type="button" onClick={() => setOpen(v => !v)} aria-label="Share dashboard" title="Share dashboard" aria-expanded={open}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-background/95 px-2 text-xs text-muted-foreground shadow-sm backdrop-blur-sm transition-colors hover:bg-muted hover:text-foreground">
        <Users className="h-3.5 w-3.5" /> Share
      </button>
      {open && (
        <div role="dialog" aria-label="Share dashboard" className="absolute right-0 mt-1 w-72 space-y-2 rounded-md border border-border bg-background p-3 text-sm shadow-lg">
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
