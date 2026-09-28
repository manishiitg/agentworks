import { useEffect, useState } from 'react'
import { AlertCircle, Loader2, Trash2, X } from 'lucide-react'
import { agentApi } from '../../services/api'
import type { CodeShareRole, CodeSharesResponse } from '../../services/api-types'

const ROLE_LABELS: Record<CodeShareRole, string> = {
  viewer: 'Viewer — read files',
  editor: 'Editor — also run the agent',
  co_owner: 'Co-owner — also manage sharing',
}

type Row = { user: string; role: CodeShareRole }

// Sharing for one Code workspace. The list lives on the server
// (config/code-shares.json); only the owner and co-owners may change it.
export function CodeShareDialog({ projectId, projectTitle, onClose }: {
  projectId: string
  projectTitle: string
  onClose: () => void
}) {
  const [state, setState] = useState<CodeSharesResponse | null>(null)
  const [rows, setRows] = useState<Row[]>([])
  const [newUser, setNewUser] = useState('')
  const [newRole, setNewRole] = useState<CodeShareRole>('viewer')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void agentApi.getCodeShares(projectId).then(response => {
      if (cancelled) return
      setState(response)
      setRows(response.grants.map(grant => ({ user: grant.username || grant.user_id, role: grant.role })))
    }).catch(cause => {
      if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load sharing.')
    })
    return () => { cancelled = true }
  }, [projectId])

  const canManage = state?.role === 'owner' || state?.role === 'co_owner'

  const save = async (next: Row[]) => {
    setSaving(true)
    setError(null)
    try {
      const response = await agentApi.putCodeShares(projectId, next)
      setState(response)
      setRows(response.grants.map(grant => ({ user: grant.username || grant.user_id, role: grant.role })))
      return true
    } catch (cause) {
      const message = (cause as { response?: { data?: { error?: string } } })?.response?.data?.error
      setError(message || (cause instanceof Error ? cause.message : 'Could not save sharing.'))
      return false
    } finally {
      setSaving(false)
    }
  }

  const add = async () => {
    const user = newUser.trim()
    if (!user) return
    const next = [...rows.filter(row => row.user !== user), { user, role: newRole }]
    if (await save(next)) setNewUser('')
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/65 p-3 backdrop-blur-sm" role="presentation">
      <div role="dialog" aria-modal="true" aria-labelledby="code-share-title" className="w-full max-w-lg rounded-2xl border border-border bg-background p-5 shadow-2xl">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 id="code-share-title" className="truncate text-lg font-semibold text-foreground">Share “{projectTitle}”</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Only the people below can see this workspace. It stays in {state?.owner_username || 'the owner'}’s files; removing someone takes effect at once.
            </p>
          </div>
          <button type="button" onClick={onClose} aria-label="Close" className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground">
            <X className="h-4 w-4" />
          </button>
        </div>
        {error ? <p className="mt-3 flex items-center gap-1.5 text-sm text-destructive"><AlertCircle className="h-4 w-4 shrink-0" />{error}</p> : null}
        {!state && !error ? (
          <p className="mt-4 text-sm text-muted-foreground"><Loader2 className="mr-2 inline h-4 w-4 animate-spin" />Loading…</p>
        ) : null}
        {state ? (
          <ul className="mt-4 divide-y divide-border rounded-lg border border-border">
            <li className="flex items-center justify-between gap-2 px-3 py-2 text-sm">
              <span className="truncate font-medium text-foreground">{state.owner_username || state.owner_id}</span>
              <span className="text-xs text-muted-foreground">Owner</span>
            </li>
            {rows.map(row => (
              <li key={row.user} className="flex items-center justify-between gap-2 px-3 py-2 text-sm">
                <span className="min-w-0 truncate text-foreground">{row.user}</span>
                <span className="flex shrink-0 items-center gap-1">
                  {canManage ? (
                    <select
                      aria-label={`Role for ${row.user}`}
                      value={row.role}
                      disabled={saving}
                      onChange={event => { void save(rows.map(item => item.user === row.user ? { ...item, role: event.target.value as CodeShareRole } : item)) }}
                      className="rounded-md border border-border bg-background px-2 py-1 text-xs"
                    >
                      {(Object.keys(ROLE_LABELS) as CodeShareRole[]).map(role => <option key={role} value={role}>{ROLE_LABELS[role]}</option>)}
                    </select>
                  ) : <span className="text-xs text-muted-foreground">{ROLE_LABELS[row.role]}</span>}
                  {canManage ? (
                    <button
                      type="button"
                      aria-label={`Remove ${row.user}`}
                      disabled={saving}
                      onClick={() => { void save(rows.filter(item => item.user !== row.user)) }}
                      className="rounded p-1.5 text-muted-foreground hover:bg-red-100 hover:text-red-600 disabled:opacity-50 dark:hover:bg-red-950/40"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
        {canManage ? (
          <form
            className="mt-4 flex flex-wrap items-center gap-2"
            onSubmit={event => { event.preventDefault(); void add() }}
          >
            <input
              value={newUser}
              onChange={event => setNewUser(event.target.value)}
              disabled={saving}
              placeholder="Username or email"
              aria-label="Person to share with"
              className="min-w-0 flex-1 basis-40 rounded-md border border-border bg-background px-3 py-1.5 text-sm"
            />
            <select
              aria-label="Role for the new person"
              value={newRole}
              disabled={saving}
              onChange={event => setNewRole(event.target.value as CodeShareRole)}
              className="rounded-md border border-border bg-background px-2 py-1.5 text-xs"
            >
              {(Object.keys(ROLE_LABELS) as CodeShareRole[]).map(role => <option key={role} value={role}>{ROLE_LABELS[role]}</option>)}
            </select>
            <button type="submit" disabled={!newUser.trim() || saving} className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">
              {saving ? 'Saving…' : 'Share'}
            </button>
          </form>
        ) : state ? <p className="mt-4 text-xs text-muted-foreground">Only the owner or a co-owner can change who has access.</p> : null}
        <p className="mt-4 text-xs text-muted-foreground">Admins on this server can view every Code workspace, read-only.</p>
      </div>
    </div>
  )
}
