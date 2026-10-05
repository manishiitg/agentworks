import { useEffect, useRef, useState } from 'react'
import api from '../../services/api'
import { Button } from '../ui/Button'

interface Status { selected: boolean; connected: boolean; tabs: number; workspace?: string }
interface Props { workspacePath: string | null; profileId?: string; readOnly?: boolean; onSelectionChange: (selected: boolean) => void }

export function ChromeExtensionConnection({ workspacePath, profileId, readOnly, onSelectionChange }: Props) {
  const [status, setStatus] = useState<Status>({ selected: false, connected: false, tabs: 0 })
  const [open, setOpen] = useState(false)
  const [pairing, setPairing] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const generation = useRef(0)
  const params = { workspace_path: workspacePath, profile_id: profileId }
  const base = new URL(String(api.defaults.baseURL || window.location.origin), window.location.origin)
  const download = new URL('/api/downloads/chrome-extension.zip', base).href

  useEffect(() => {
    generation.current += 1; setBusy(false); setCopied(false)
    setStatus({ selected: false, connected: false, tabs: 0 }); setPairing(''); setError(''); setOpen(false)
    onSelectionChange(false)
    if (!workspacePath || readOnly) return
    let alive = true
    async function refresh() {
      try {
        const { data } = await api.get<Status>('/api/browser/extension', { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
        if (alive) { setStatus(data); onSelectionChange(data.selected) }
      } catch { /* The ordinary browser remains available when the bridge status cannot load. */ }
    }
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 5000)
    return () => { alive = false; window.clearInterval(timer) }
  }, [workspacePath, profileId, readOnly, onSelectionChange])

  if (!workspacePath || readOnly) return null
  const act = async (action: 'pair' | 'disconnect') => {
    const turn = generation.current
    setBusy(true); setError(''); setCopied(false)
    try {
      if (action === 'pair') {
        const { data } = await api.post<{ token: string }>('/api/browser/extension', { action }, { params, skipSessionContext: true })
        if (generation.current !== turn) return
        const url = new URL('/api/browser/extension/connect', base)
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
        setPairing(JSON.stringify({ url: url.href, token: data.token })); setOpen(true)
      } else {
        await api.post('/api/browser/extension', { action }, { params, skipSessionContext: true })
        if (generation.current !== turn) return
        setStatus({ selected: false, connected: false, tabs: 0 }); setPairing(''); setOpen(false); onSelectionChange(false)
      }
    } catch (e) { if (generation.current === turn) setError(e instanceof Error ? e.message : 'Cannot connect Chrome') }
    finally { if (generation.current === turn) setBusy(false) }
  }
  return <div className="shrink-0 border-b border-border px-3 py-2 text-sm">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span>{status.selected ? status.connected ? `Your Chrome · ${status.tabs} shared ${status.tabs === 1 ? 'tab' : 'tabs'}` : 'Your Chrome · disconnected' : 'Use your signed-in Chrome'}</span>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" disabled={busy} onClick={() => { if (open && !status.selected) setOpen(false); else void act('pair') }}>{status.selected ? 'Reconnect' : 'Connect Chrome'}</Button>
        {status.selected && <Button size="sm" variant="outline" disabled={busy} onClick={() => { void act('disconnect') }}>Use workspace browser</Button>}
      </div>
    </div>
    {open && <div className="mt-3 space-y-2">
      <p>Download and unzip the <a className="underline" href={download}>Chrome extension</a>. Open chrome://extensions, enable Developer mode, and choose Load unpacked for the unzipped folder.</p>
      <p>Open the extension, paste this connection, then choose Share current tab. It expires after five minutes.</p>
      <textarea aria-label="Chrome pairing connection" readOnly value={pairing} rows={2} className="w-full rounded border bg-background p-2 text-xs" onFocus={e => e.target.select()} />
      <Button size="sm" variant="outline" onClick={() => { void navigator.clipboard.writeText(pairing).then(() => setCopied(true), () => setError('Select and copy the connection above.')) }}>{copied ? 'Copied' : 'Copy connection'}</Button>
      <p className="text-xs text-muted-foreground">Only shared tabs are available to your agent. Their contents and screenshots are sent to the platform. Stop access from the extension at any time.</p>
    </div>}
    {status.selected && !status.connected && <p className="mt-2 text-xs text-muted-foreground">Browser actions are paused. Reconnect Chrome or choose the workspace browser.</p>}
    {error && <p role="alert" className="mt-2 text-xs text-red-600">{error}</p>}
  </div>
}
