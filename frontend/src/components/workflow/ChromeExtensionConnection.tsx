import { useEffect, useRef, useState } from 'react'
import { Download, Loader2 } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openBrowserConnections, type SavedBrowserConnection } from '../BrowserConnectionsDialog'
import { getRuntimeAppName, runtimeBrandingConfig } from '../../runtime-branding'

export interface ChromeExtensionStatus {
  selected: boolean; connected: boolean; account_connected?: boolean; tabs: number; workspace?: string; connection_id?: string; tab_titles?: string[]
  browser_id?: string; browsers?: SavedBrowserConnection[]; message?: string; extension_version?: string; latest_extension_version?: string
}

/** True when dotted version a is older than b; unknown versions are not "older". */
export function extensionVersionOlder(a?: string, b?: string): boolean {
  if (!a || !b) return false
  const left = a.split('.').map(Number), right = b.split('.').map(Number)
  for (let i = 0; i < Math.max(left.length, right.length); i++) {
    const x = left[i] || 0, y = right[i] || 0
    if (x !== y) return x < y
  }
  return false
}
const emptyStatus: ChromeExtensionStatus = { selected: false, connected: false, tabs: 0 }

// Keep the connection alive in the panel's state even when settings is closed.
export function useChromeExtensionConnection(workspacePath: string | null, profileId?: string, readOnly = false) {
  const [status, setStatus] = useState(emptyStatus)
  const [loading, setLoading] = useState(!!workspacePath && !readOnly)
  const [pairing, setPairing] = useState('')
  const [connectionCode, setConnectionCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [checkingError, setCheckingError] = useState('')
  const [copied, setCopied] = useState(false)
  const [manual, setManual] = useState(false)
  const generation = useRef(0)
  const revision = useRef(0)
  const awaiting = useRef<{ previous: string } | null>(null)
  const refreshRef = useRef<() => void>(() => {})
  const base = new URL(String(api.defaults.baseURL || window.location.origin), window.location.origin)
  const download = new URL('/api/downloads/chrome-extension.zip', base).href

  useEffect(() => {
    generation.current += 1; revision.current += 1; awaiting.current = null
    setStatus(emptyStatus); setPairing(''); setConnectionCode(''); setBusy(false); setError(''); setCheckingError(''); setCopied(false); setManual(false)
    setLoading(!!workspacePath && !readOnly)
    if (!workspacePath || readOnly) return
    let alive = true
    let refreshing = false
    async function refresh() {
      if (refreshing) return
      refreshing = true
      const mutation = revision.current
      try {
        const { data } = await api.get<ChromeExtensionStatus>('/api/browser/extension', { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
        if (alive && mutation === revision.current) {
          setStatus(data); setLoading(false); setCheckingError('')
          if (data.connected && data.connection_id && awaiting.current && data.connection_id !== awaiting.current.previous) {
            awaiting.current = null; setPairing(''); setCopied(false); setManual(false)
          }
        }
      } catch {
        if (alive && mutation === revision.current) { setLoading(false); setCheckingError('Could not check the browser connection. Try again shortly.') }
      } finally { refreshing = false }
    }
    refreshRef.current = () => { void refresh() }
    void refresh()
    return () => { alive = false; refreshRef.current = () => {} }
  }, [workspacePath, profileId, readOnly])
  // The server announces a browser change on the live feed; the old 2.5 s poll only runs while the feed is down.
  useLiveRefetch(() => refreshRef.current(), { kinds: ['browser'], fallbackMs: 2500, safetyMs: 20_000, minIntervalMs: 500, enabled: !!workspacePath && !readOnly })

  const connect = async (browserId?: string) => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError('')
    try {
      const { data } = await api.post<ChromeExtensionStatus>('/api/browser/extension', { action: 'connect', ...(browserId ? { browser_id: browserId } : {}) }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current === turn) setStatus(data)
    } catch { if (generation.current === turn) setError('Could not use that browser for this project. Choose a connected browser and try again.') }
    finally { if (generation.current === turn) setBusy(false) }
  }
  const disconnect = async () => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError('')
    try {
      await api.post('/api/browser/extension', { action: 'disconnect' }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current !== turn) return false
      awaiting.current = null; setStatus(emptyStatus); setPairing(''); setConnectionCode(''); setCopied(false); setManual(false)
      return true
    } catch { if (generation.current === turn) setError('Could not disconnect. Your current browser is still selected.'); return false }
    finally { if (generation.current === turn) setBusy(false) }
  }
  const copy = async (reset = false, copyOnly = false) => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError(''); setCopied(false)
    try {
      const { data } = await api.post<{ token: string; scope: string }>('/api/browser/extension', { action: reset ? 'reset' : copyOnly ? 'copy' : 'pair' }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current !== turn) return
      const url = new URL('/api/browser/extension/connect', base)
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
      const value = JSON.stringify({ url: url.href, token: data.token, scope: data.scope, brand: getRuntimeAppName(runtimeBrandingConfig()) || 'AgentWorks' })
      setConnectionCode(value)
      if (!copyOnly) {
        awaiting.current = { previous: status.connection_id || '' }
        setPairing(value)
      }
      if (reset) setStatus(current => ({ ...current, connected: false, tabs: 0 }))
      try {
        await navigator.clipboard.writeText(value)
        if (generation.current === turn) setCopied(true)
      } catch {
        if (generation.current === turn) { setManual(true); setError('Clipboard unavailable. Select and copy the connection below.') }
      }
    } catch { if (generation.current === turn) setError('Could not create the connection. Please try again.') }
    finally { if (generation.current === turn) setBusy(false) }
  }
  return { status, loading, pairing, connectionCode, busy, error: error || checkingError, copied, manual, setManual, copy, connect, disconnect, download }
}
export type ChromeExtensionController = ReturnType<typeof useChromeExtensionConnection>

export function ChromeExtensionConnection({ connection }: { connection: ChromeExtensionController }) {
  const { status, busy, error, connect, download } = connection
  const [chosenBrowser, setChosenBrowser] = useState('')
  return <div className="space-y-4">
    <p className="text-xs text-muted-foreground">Connect browsers to your account once, then use them across your projects.</p>
    <Button size="sm" variant="outline" onClick={openBrowserConnections}>Manage browser connections</Button>
    {!!status.browsers?.filter(b => b.enabled).length ? <div className="space-y-2">
      <label className="block text-xs font-medium" htmlFor="project-saved-browser">Browser for this project</label>
      <select id="project-saved-browser" value={chosenBrowser || status.browser_id || ''} onChange={e => setChosenBrowser(e.target.value)} className="w-full rounded-md border border-border bg-background p-2 text-sm">
        <option value="">Most recently connected browser</option>
        {status.browsers.filter(b => b.enabled).map(b => <option key={b.id} value={b.id}>{b.name} · {b.connected ? 'Connected' : 'Offline'}</option>)}
      </select>
      <Button size="sm" disabled={busy} onClick={() => { void connect(chosenBrowser || status.browser_id) }}>{busy && <Loader2 />}Use this browser</Button>
    </div> : !status.account_connected && <p className="text-sm text-muted-foreground">Connect Chrome or Edge from Browser connections above. No workflow is needed.</p>}
    {status.selected && <div className="space-y-1 rounded-md border border-border p-3">
      <p className="text-sm font-medium">{status.connected ? 'Connected' : 'Offline'}{status.browser_id ? ` · ${status.browsers?.find(b => b.id === status.browser_id)?.name || 'Saved browser'}` : ''}</p>
      <p className="text-xs text-muted-foreground">{status.message || (status.connected ? status.tabs ? `${status.tabs} shared tabs are ready.` : 'Your agent can create its own tabs. Sharing an existing tab is optional.' : 'Open your selected browser to reconnect, or choose another browser.')}</p>
      {status.connected && <ExtensionVersionLine status={status} download={download} />}
    </div>}
    {status.account_connected && !status.browsers?.length && <><p className="text-xs text-muted-foreground">Reload the updated extension to list Chrome and Edge separately. Your existing connection remains available.</p><Button size="sm" disabled={busy} onClick={() => { void connect() }}>Use connected browser</Button></>}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}

function ExtensionVersionLine({ status, download }: { status: ChromeExtensionStatus; download: string }) {
  const installed = status.extension_version, latest = status.latest_extension_version
  const outdated = extensionVersionOlder(installed, latest)
  if (!installed) return <p className="mt-1 text-xs text-muted-foreground">Browser Bridge version unknown{latest ? ` · latest is ${latest}` : ''}</p>
  if (!outdated) return <p className="mt-1 text-xs text-muted-foreground">Browser Bridge {installed} · up to date</p>
  return <div className="mt-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-2 text-xs">
    <p className="font-medium text-amber-700 dark:text-amber-300">Browser Bridge {installed} · update to {latest}</p>
    <p className="mt-1 leading-relaxed text-muted-foreground">Download it, replace the files in your unpacked extension folder, then click reload on Browser Bridge in your browser's extensions page.</p>
    <Button asChild size="xs" variant="outline" className="mt-2"><a href={download} download><Download />Download {latest}</a></Button>
  </div>
}
