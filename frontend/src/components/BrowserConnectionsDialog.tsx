import { useCallback, useEffect, useState } from 'react'
import { Check, Copy, Download, X } from 'lucide-react'
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

export default function BrowserConnectionsDialog({ open, onClose, onConnected }: { open: boolean; onClose: () => void; onConnected?: (browserId: string) => Promise<boolean> }) {
  const [browsers, setBrowsers] = useState<SavedBrowserConnection[]>([])
  const [code, setCode] = useState('')
  const [step, setStep] = useState<'install' | 'connect'>('install')
  const [connectedAtStart, setConnectedAtStart] = useState<string[]>([])
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
  useEffect(() => { if (open) { void refresh(); setCode(''); setCopied(false); setStep('install') } }, [open, refresh])
  useLiveRefetch(() => { void refresh() }, { kinds: ['browser'], fallbackMs: 2500, safetyMs: 20_000, enabled: open })
  useEffect(() => {
    if (!open) return
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', key); return () => document.removeEventListener('keydown', key)
  }, [open, onClose])
  if (!open) return null
  const connect = async () => {
    setBusy(true); setError(''); setCopied(false)
    setConnectedAtStart(browsers.filter(browser => browser.connected).map(browser => browser.id))
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
  const connectedBrowser = code ? browsers.find(browser => browser.connected && !connectedAtStart.includes(browser.id)) : undefined
  const done = !!connectedBrowser
  const finish = async () => {
    if (!connectedBrowser || !onConnected) { onClose(); return }
    setBusy(true)
    try { if (await onConnected(connectedBrowser.id)) onClose(); else setError('Could not select this browser. Try Done again.') }
    catch { setError('Could not select this browser. Try Done again.') }
    finally { setBusy(false) }
  }
  return <ModalPortal><div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
    <section role="dialog" aria-modal="true" aria-labelledby="browser-connections-title" className="w-full max-w-lg max-h-[85vh] overflow-y-auto rounded-lg border border-border bg-background shadow-xl" onClick={e => e.stopPropagation()}>
      <header className="flex items-center justify-between border-b border-border p-4"><h2 id="browser-connections-title" className="text-lg font-semibold">Connect browser</h2><button onClick={onClose} aria-label="Close browser connections"><X className="h-4 w-4" /></button></header>
      <div className="space-y-4 p-4">
        {done ? <div className="space-y-3 text-center"><Check className="mx-auto h-6 w-6" /><p className="font-medium">Browser connected</p><Button disabled={busy} onClick={() => { void finish() }}>Done</Button></div> : <>
          <p className="text-xs text-muted-foreground">{step === 'install' ? '1. Install extension' : '2. Connect'}</p>
          {step === 'install' ? <div className="space-y-3">
            <p className="text-sm">Add Browser Bridge to Chrome or Edge.</p>
            <Button asChild variant="outline" size="sm"><a href={new URL('/api/downloads/chrome-extension.zip', base).href} download><Download />Download extension{latestVersion ? ` ${latestVersion}` : ''}</a></Button>
            <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Installation steps</summary><p className="mt-2">Unzip the download. Open chrome://extensions or edge://extensions, enable Developer mode, choose Load unpacked and select the unzipped folder.</p></details>
            <Button size="sm" onClick={() => setStep('connect')}>Extension installed · Next</Button>
          </div> : <div className="space-y-3">
            <p className="text-sm">Copy the code, open Browser Bridge in your browser, and paste it there.</p>
            <Button size="sm" disabled={busy} onClick={() => { void connect() }}><Copy />{copied ? 'Code copied' : 'Copy connection code'}</Button>
            {code && <><p role="status" className="text-xs text-muted-foreground">Choose Connect browser in the extension. Waiting for connection…</p><details open={!copied || undefined}><summary className="cursor-pointer text-xs text-muted-foreground">Copy manually</summary><textarea aria-label="Account browser connection code" readOnly value={code} onFocus={e => e.currentTarget.select()} className="mt-2 w-full rounded-md border border-border bg-muted/30 p-2 text-xs" rows={3} /></details></>}
            <Button size="sm" variant="ghost" onClick={() => setStep('install')}>Back</Button>
          </div>}
        </>}
        {!!browsers.length && <details className="space-y-2 border-t border-border pt-3"><summary className="cursor-pointer text-xs text-muted-foreground">Saved browsers</summary>
          <ul className="mt-2 space-y-2">{browsers.map(browser => <li key={browser.id} className="flex items-center justify-between gap-3 rounded-md border border-border p-3"><div><p className="text-sm font-medium">{browser.name}</p><p className="text-xs text-muted-foreground">{browser.connected ? 'Connected' : 'Offline'}</p>{extensionVersionOlder(browser.version, latestVersion) && <p className="text-xs text-amber-700 dark:text-amber-300">Update available: {latestVersion}</p>}<details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Connection details</summary>{browser.version ? `Extension ${browser.version}` : 'Extension version unknown'}</details></div>{browser.enabled && <Button size="sm" variant="outline" disabled={busy} onClick={() => { void disconnect(browser.id) }}>Disconnect</Button>}</li>)}</ul>
        </details>}
        {pairingFailure && <div role="alert" className="space-y-1 rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm"><p className="font-medium">{pairingFailure.reason === 'extension_outdated' ? 'Update your browser extension' : 'Browser could not connect'}</p><p>{pairingFailure.message}</p>{pairingFailure.extension_version && <p className="text-xs text-muted-foreground">Installed extension: {pairingFailure.extension_version}{latestVersion ? ` · Download: ${latestVersion}` : ''}</p>}</div>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
    </section>
  </div></ModalPortal>
}
