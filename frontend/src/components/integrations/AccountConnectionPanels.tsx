import { useCallback, useEffect, useRef, useState } from 'react'
import { Github, MessageCircle, MonitorCog } from 'lucide-react'
import api from '../../services/api'
import type { WhatsAppStatus } from '../../services/api-types'
import type { ComputerUseStatus } from '../../products/work/ComputerUsePanel'
import { Button } from '../ui/Button'

/** Caller-scoped reads only; no inherited chat/project context or idle polling. */
function useAccountSnapshot<T>(path: string) {
  const [data, setData] = useState<T>()
  const [error, setError] = useState(false)
  const [revision, setRevision] = useState(0)
  const refresh = useCallback(() => setRevision(value => value + 1), [])
  useEffect(() => {
    const abort = new AbortController()
    setError(false)
    void api.get<T>(path, { signal: abort.signal, skipSessionContext: true }).then(result => {
      if (!abort.signal.aborted) setData(result.data)
    }).catch(() => { if (!abort.signal.aborted) setError(true) })
    return () => abort.abort()
  }, [path, revision])
  useEffect(() => { window.addEventListener('focus', refresh); return () => window.removeEventListener('focus', refresh) }, [refresh])
  return { data, error, refresh }
}

export function AccountWhatsAppPanel() {
  const { data, error, refresh } = useAccountSnapshot<WhatsAppStatus>('/api/whatsapp/status')
  const [pairing, setPairing] = useState(false)
  const [qr, setQr] = useState('')
  const [qrError, setQrError] = useState(false)
  const [revision, setRevision] = useState(0)
  const qrOwner = useRef('')
  const phone = data?.devices?.[0] ?? data
  // Only an explicitly opened pairing flow refreshes while waiting for the scan.
  useEffect(() => {
    if (!pairing || phone?.paired) return
    const timer = window.setInterval(refresh, 3000)
    return () => window.clearInterval(timer)
  }, [pairing, phone?.paired, refresh])
  useEffect(() => {
    if (!pairing || phone?.paired) { setQr(''); return }
    const abort = new AbortController()
    setQrError(false)
    void api.get<Blob>('/api/whatsapp/pair', { params: { size: 320 }, responseType: 'blob', signal: abort.signal, skipSessionContext: true }).then(result => {
      if (abort.signal.aborted) return
      if (qrOwner.current) URL.revokeObjectURL(qrOwner.current)
      qrOwner.current = URL.createObjectURL(result.data); setQr(qrOwner.current)
    }).catch(() => { if (!abort.signal.aborted) setQrError(true) })
    return () => abort.abort()
  }, [pairing, phone?.paired, data?.qr_expires_at, revision])
  useEffect(() => () => { if (qrOwner.current) URL.revokeObjectURL(qrOwner.current) }, [])
  return <section aria-label="WhatsApp account" className="space-y-3 rounded-lg border border-border p-4">
    <div className="flex items-center gap-2"><MessageCircle className="h-4 w-4 text-muted-foreground" /><h3 className="flex-1 text-sm font-medium">WhatsApp</h3><Button variant="ghost" size="xs" onClick={refresh}>Refresh</Button></div>
    {error ? <p role="alert" className="text-xs text-destructive">Could not check WhatsApp. Try Refresh.</p> : !data ? <p role="status" className="text-xs text-muted-foreground">Loading WhatsApp…</p> : <>
      <p role="status" className="text-sm">{!data.enabled ? 'Not available on this server' : phone?.paired ? phone.connected ? 'Connected' : 'Linked · currently offline' : 'Not connected'}{phone?.own_jid && <span className="ml-2 text-xs text-muted-foreground">{phone.own_jid.split('@')[0].split(':')[0]}</span>}</p>
      <p className="text-xs text-muted-foreground">Your linked phone is account-wide. Each Crew keeps its own messaging setup.</p>
      {data.enabled && !phone?.paired && <Button size="sm" variant="outline" onClick={() => setPairing(value => !value)}>{pairing ? 'Close pairing' : 'Connect WhatsApp'}</Button>}
      {pairing && !phone?.paired && <div className="space-y-2"><p className="text-xs">In WhatsApp, open Linked Devices → Link a device, then scan this code.</p>{qrError ? <p role="alert" className="text-xs">Could not load the pairing code. <button type="button" className="underline" onClick={() => setRevision(value => value + 1)}>Try again</button></p> : qr ? <img src={qr} width={256} height={256} alt="WhatsApp pairing code" className="rounded bg-white p-2" /> : <p role="status" className="text-xs text-muted-foreground">Loading pairing code…</p>}</div>}
    </>}
  </section>
}

export function AccountGitHubPanel() {
  const { data, error, refresh } = useAccountSnapshot<{ connections: { id: string; login: string; source: string }[] }>('/api/vault/github?view=accounts')
  return <section aria-label="GitHub accounts" className="space-y-3 rounded-lg border border-border p-4"><div className="flex items-center gap-2"><Github className="h-4 w-4 text-muted-foreground" /><h3 className="flex-1 text-sm font-medium">GitHub</h3><Button size="xs" variant="ghost" onClick={refresh}>Refresh</Button></div>
    {error ? <p role="alert" className="text-xs text-destructive">Could not load your GitHub accounts. Update this server if needed, then Refresh.</p> : !data ? <p role="status" className="text-xs text-muted-foreground">Loading GitHub accounts…</p> : data.connections.length ? <ul className="divide-y divide-border">{data.connections.map(connection => <li key={connection.id} className="flex items-center gap-2 py-2 text-sm"><span className="min-w-0 flex-1 truncate">{connection.login}</span><span className="text-xs text-muted-foreground">Saved · {connection.source === 'pat' ? 'Token' : connection.source === 'gh' ? 'GitHub CLI' : 'Browser sign-in'}</span></li>)}</ul> : <p className="text-xs text-muted-foreground">No saved GitHub accounts.</p>}
    <p className="text-xs text-muted-foreground">Add or change a GitHub account in a Crew’s Connections. Saved accounts are reusable; each Crew chooses its own.</p>
  </section>
}

export function AccountComputerUsePanel() {
  const { data, error, refresh } = useAccountSnapshot<ComputerUseStatus>('/api/computer-use/status')
  const permissions = [['App control', data?.permissions?.data?.accessibility?.state], ['Screenshots', data?.permissions?.data?.screen_recording?.state]] as const
  return <section aria-label="Computer use setup" className="space-y-3 rounded-lg border border-border p-4"><div className="flex items-center gap-2"><MonitorCog className="h-4 w-4 text-muted-foreground" /><h3 className="flex-1 text-sm font-medium">Computer use</h3><Button size="xs" variant="ghost" onClick={refresh}>Refresh</Button></div>
    {error ? <p className="text-xs text-muted-foreground">Check Computer use in your local AgentWorks app. If you are already there, update the backend and try Refresh.</p> : !data ? <p role="status" className="text-xs text-muted-foreground">Checking this computer…</p> : <><p role="status" className="text-sm">{data.active ? 'Desktop action running' : !data.installed ? 'Needs setup' : 'Installed'}</p>{data.installed && data.platform === 'darwin' && <dl className="space-y-1 text-xs">{permissions.map(([label, state]) => <div key={label} className="flex items-center justify-between"><dt>{label}</dt><dd className="text-muted-foreground">{state === 'granted' ? 'Allowed' : state === 'denied' ? 'Not allowed' : state === 'not_required' ? 'Not needed' : 'Could not check'}</dd></div>)}</dl>}{data.permission_setup_note && <p className="text-xs text-muted-foreground">{data.permission_setup_note}</p>}</>}
    <p className="text-xs text-muted-foreground">Install, grant permissions and enable access in a Crew’s Computer use settings. There is no account-wide enable switch.</p>
  </section>
}
