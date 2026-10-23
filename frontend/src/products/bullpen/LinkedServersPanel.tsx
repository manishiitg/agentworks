import { useCallback, useEffect, useState } from 'react'
import api from '../../services/api'
import { Button } from '../../components/ui/Button'

type LinkedServer = { id: string; name: string; server: string; state: string; waiting: number }

export function LinkedServersPanel() {
  const [servers, setServers] = useState<LinkedServer[]>([])
  const [name, setName] = useState('team')
  const [server, setServer] = useState('')
  const [approval, setApproval] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const refresh = useCallback(async () => {
    try { setServers((await api.get<{ servers: LinkedServer[] }>('/api/agentbridge/servers')).data.servers); setError('') }
    catch { setError('Linked servers could not be loaded. Try refreshing.') }
  }, [])
  useEffect(() => { void refresh() }, [refresh])
  async function change(action: 'connect' | 'disconnect', serverName: string) {
    setBusy(true); setError(''); setApproval('')
    try {
      const result = await api.post<{ approval_url?: string }>('/api/agentbridge/servers', { action, name: serverName, ...(action === 'connect' ? { server } : {}) })
      if (result.data.approval_url) setApproval(result.data.approval_url)
      else await refresh()
    } catch (reason) {
      const response = reason as { response?: { data?: { error?: { message?: string } | string } } }
      const detail = response.response?.data?.error
      setError(typeof detail === 'string' ? detail : detail?.message || 'The connection could not be changed. Try again.')
    } finally { setBusy(false) }
  }
  return <details className="rounded-xl border border-border p-4 text-sm">
    <summary className="cursor-pointer font-medium">Linked servers{servers.length ? ` · ${servers.length}` : ''}</summary>
    <p className="mt-3 text-muted-foreground">Keep your agents connected here and exchange messages through your account on another AgentWorks server. Send to name@team after linking a server called team.</p>
    <div className="mt-3 space-y-3">{servers.map(item => <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border p-3">
      <div className="min-w-0"><p className="font-medium">{item.name}</p><p className="break-all text-xs text-muted-foreground">{item.server}</p><p className="mt-1 text-xs">{item.state}{item.waiting ? ` · ${item.waiting} messages waiting` : ''}</p></div>
      <Button variant="outline" size="sm" disabled={busy} onClick={() => void change('disconnect', item.name)}>Disconnect</Button>
    </div>)}</div>
    <form className="mt-4 flex flex-wrap items-end gap-3" onSubmit={event => { event.preventDefault(); void change('connect', name) }}>
      <label className="space-y-1">Server name<input required value={name} onChange={event => setName(event.target.value)} placeholder="team" className="block rounded-md border border-border bg-background px-3 py-2" /></label>
      <label className="min-w-60 flex-1 space-y-1">Server web address<input required type="url" value={server} onChange={event => setServer(event.target.value)} placeholder="https://agents.example.com" className="block w-full rounded-md border border-border bg-background px-3 py-2" /></label>
      <Button type="submit" disabled={busy}>{busy ? 'Connecting…' : 'Link server'}</Button>
      <Button type="button" variant="outline" disabled={busy} onClick={() => void refresh()}>Refresh</Button>
    </form>
    {approval && <p role="status" className="mt-3"><a className="font-medium text-primary underline" href={approval} target="_blank" rel="noreferrer">Approve the messaging connection on the other server</a>, then refresh this list.</p>}
    {error && <p role="alert" className="mt-3 text-destructive">{error}</p>}
  </details>
}
