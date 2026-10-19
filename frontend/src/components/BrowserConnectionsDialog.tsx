import { useCallback, useEffect, useState } from 'react'
import { Copy, Download, X } from 'lucide-react'
import api from '../services/api'
import { Button } from './ui/Button'
import ModalPortal from './ui/ModalPortal'
import { useLiveRefetch } from '../hooks/useLiveRefetch'
import { getRuntimeAppName, runtimeBrandingConfig } from '../runtime-branding'
import { extensionVersionOlder } from '../utils/extensionVersion'

export interface SavedBrowserConnection {
  id: string; name: string; browser: string; version?: string; connected: boolean; enabled: boolean
}
interface PairingFailure { reason: string; message: string; extension_version?: string; at: number }
export function openBrowserConnections() { window.dispatchEvent(new Event('open-browser-connections')) }

export default function BrowserConnectionsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [browsers, setBrowsers] = useState<SavedBrowserConnection[]>([])
  const [code, setCode] = useState('')
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [latestVersion, setLatestVersion] = useState('')
  const [pairingFailure, setPairingFailure] = useState<PairingFailure | null>(null)
  const base = new URL(String(api.defaults.baseURL || window.location.origin), window.location.origin)
  const refresh = useCallback(async () => {
    try {
      const { data } = await api.get('/api/browser/extension', { skipSessionContext: true })
      setBrowsers(data.browsers || []); setLatestVersion(data.latest_extension_version || '')
      setPairingFailure(data.pairing_error || null); setError('')
    }
    catch { setError('Could not check your browsers. Try again shortly.') }
  }, [])
  useEffect(() => { if (open) { void refresh(); setCode(''); setCopied(false) } }, [open, refresh])
  useLiveRefetch(() => { void refresh() }, { kinds: ['browser'], fallbackMs: 2500, safetyMs: 20_000, enabled: open })
  useEffect(() => {
    if (!open) return
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', key); return () => document.removeEventListener('keydown', key)
  }, [open, onClose])
  if (!open) return null
  const connect = async () => {
    setBusy(true); setError(''); setCopied(false)
    try {
      const { data } = await api.post('/api/browser/extension', { action: 'copy' }, { skipSessionContext: true })
      setPairingFailure(null)
      const url = new URL('/api/browser/extension/connect', base); url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
      const value = JSON.stringify({ url: url.href, token: data.token, brand: getRuntimeAppName(runtimeBrandingConfig()) || 'AgentWorks' })
      setCode(value)
      try { await navigator.clipboard.writeText(value); setCopied(true) } catch { /* Manual copy remains available. */ }
    } catch { setError('Could not prepare your browser connection. Try again.') }
    finally { setBusy(false) }
  }
  const disconnect = async (id: string) => {
    setBusy(true); setError('')
    try { await api.post('/api/browser/extension', { action: 'disconnect', browser_id: id }, { skipSessionContext: true }); await refresh() }
    catch { setError('Could not disconnect this browser. Try again.') }
    finally { setBusy(false) }
  }
  return <ModalPortal><div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
    <section role="dialog" aria-modal="true" aria-labelledby="browser-connections-title" className="w-full max-w-lg max-h-[85vh] overflow-y-auto rounded-lg border border-border bg-background shadow-xl" onClick={e => e.stopPropagation()}>
      <header className="flex items-center justify-between border-b border-border p-4"><h2 id="browser-connections-title" className="text-lg font-semibold">Browser connections</h2><button onClick={onClose} aria-label="Close browser connections"><X className="h-4 w-4" /></button></header>
      <div className="space-y-4 p-4"><p className="text-sm text-muted-foreground">Connect Chrome, Edge, or both to your account once. Choose a saved browser in a project's Browser settings.</p>
        <ul className="space-y-2">{browsers.map(browser => <li key={browser.id} className="flex items-center justify-between gap-3 rounded-md border border-border p-3"><div><p className="font-medium">{browser.name}</p><p className="text-xs text-muted-foreground">{browser.connected ? 'Connected' : 'Offline'}{browser.version ? ` · Extension ${browser.version}` : ' · Extension version unknown'}</p>{extensionVersionOlder(browser.version, latestVersion) && <p className="text-xs text-amber-700 dark:text-amber-300">Update available: {latestVersion}</p>}</div>{browser.enabled && <Button size="sm" variant="outline" disabled={busy} onClick={() => { void disconnect(browser.id) }}>Disconnect</Button>}</li>)}</ul>
        {!browsers.length && <p className="text-sm text-muted-foreground">No browsers connected yet.</p>}
        <div className="space-y-2 border-t border-border pt-3"><h3 className="font-medium">Connect a browser</h3><p className="text-sm text-muted-foreground">Install the extension in each browser you want to use. Paste your connection code into its popup once; it reconnects automatically.</p>
          <div className="flex flex-wrap gap-2"><Button asChild variant="outline" size="sm"><a href={new URL('/api/downloads/chrome-extension.zip', base).href} download><Download />Download extension{latestVersion ? ` ${latestVersion}` : ''}</a></Button><Button size="sm" disabled={busy} onClick={() => { void connect() }}><Copy />{copied ? 'Copied connection code' : 'Copy connection code'}</Button></div>
          <p className="text-xs text-muted-foreground">Open chrome://extensions or edge://extensions, enable Developer mode, and load the unzipped folder.</p>
          {code && <><p className="text-sm">Paste this into the extension, then choose Connect browser.</p><textarea aria-label="Account browser connection code" readOnly value={code} onFocus={e => e.currentTarget.select()} className="w-full rounded-md border border-border bg-muted/30 p-2 text-xs" rows={3} /></>}
        </div>
        {pairingFailure && <div role="alert" className="space-y-1 rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm"><p className="font-medium">{pairingFailure.reason === 'extension_outdated' ? 'Update your browser extension' : 'Browser could not connect'}</p><p>{pairingFailure.message}</p>{pairingFailure.extension_version && <p className="text-xs text-muted-foreground">Installed extension: {pairingFailure.extension_version}{latestVersion ? ` · Download: ${latestVersion}` : ''}</p>}</div>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
    </section>
  </div></ModalPortal>
}
