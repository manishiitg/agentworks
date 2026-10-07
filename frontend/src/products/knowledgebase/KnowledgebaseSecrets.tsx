import { useEffect, useState } from 'react'
import { KeyRound, Trash2 } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../../components/ui/Button'

// Brain's own secrets (PLAT-633), not Vault's platform secrets: names only, managed by people who own the whole Brain.
export function KnowledgebaseSecrets() {
  const [names, setNames] = useState<string[] | null>(null)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const failed = (failure: unknown, fallback: string) => {
    const data = (failure as { response?: { data?: unknown } }).response?.data
    setError(typeof data === 'string' && data ? data : fallback)
  }
  useEffect(() => {
    const controller = new AbortController()
    api.get<{ secrets: string[] }>('/api/knowledgebase/secrets', { signal: controller.signal }).then(response => setNames(response.data.secrets ?? []))
      .catch(failure => { if (!controller.signal.aborted) { setNames([]); failed(failure, "Could not load Brain's secrets.") } })
    return () => controller.abort()
  }, [])
  async function save() {
    setBusy(true); setError('')
    try {
      const response = await api.put<{ secrets: string[] }>('/api/knowledgebase/secrets', { name: name.trim(), value })
      setNames(response.data.secrets ?? []); setName(''); setValue('')
    } catch (failure) { failed(failure, 'The secret was not saved.') } finally { setBusy(false) }
  }
  async function remove(secret: string) {
    setBusy(true); setError('')
    try {
      const response = await api.delete<{ secrets: string[] }>(`/api/knowledgebase/secrets/${encodeURIComponent(secret)}`)
      setNames(response.data.secrets ?? [])
    } catch (failure) { failed(failure, 'The secret was not removed.') } finally { setBusy(false) }
  }
  return <div className="space-y-4 p-4 text-sm" data-testid="knowledgebase-secrets">
    <div>
      <h3 className="font-semibold">Brain secrets</h3>
      <p className="mt-1 text-xs text-muted-foreground">Brain's own secrets, such as the Git backup token. Platform secrets stay in Vault. Values are never shown.</p>
    </div>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {names === null ? <p className="text-muted-foreground">Loading…</p> : names.length === 0
      ? <p className="text-muted-foreground">No secrets yet.</p>
      : <ul className="divide-y divide-border rounded-lg border border-border">{names.map(secret => <li key={secret} className="flex items-center gap-3 px-3 py-2">
          <KeyRound className="h-4 w-4 text-muted-foreground" /><code className="min-w-0 flex-1 truncate">{secret}</code>
          <Button variant="ghost" size="icon" className="h-7 w-7" aria-label={`Remove ${secret}`} disabled={busy} onClick={() => void remove(secret)}><Trash2 className="h-3.5 w-3.5" /></Button>
        </li>)}</ul>}
    <form className="space-y-2 rounded-lg border border-border p-3" onSubmit={event => { event.preventDefault(); void save() }}>
      <p className="text-xs font-medium">Add or replace a secret</p>
      <input aria-label="Secret name" value={name} onChange={event => setName(event.target.value)} placeholder="BRAIN_GITHUB_PAT" className="w-full rounded-md border border-border bg-background px-2 py-1.5 font-mono text-xs" />
      <input aria-label="Secret value" type="password" autoComplete="off" value={value} onChange={event => setValue(event.target.value)} placeholder="Value" className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-xs" />
      <div className="flex justify-end"><Button size="sm" type="submit" disabled={busy || !name.trim() || !value}>Save</Button></div>
    </form>
  </div>
}
