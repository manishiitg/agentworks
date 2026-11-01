import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, Check, ExternalLink, Download, MoreHorizontal, Plus, X } from 'lucide-react'
import api from '../services/api'
import { Button } from './ui/Button'
import ModalPortal from './ui/ModalPortal'
import IconPopover from './ui/IconPopover'
import { TooltipProvider } from './ui/tooltip'
import { useLiveRefetch } from '../hooks/useLiveRefetch'
import { getRuntimeAppName, runtimeBrandingConfig } from '../runtime-branding'
import { prepareSignInWindow } from '../utils/signInWindow'
import { extensionVersionOlder } from '../utils/extensionVersion'

export interface SavedBrowserConnection {
  id: string; name: string; browser: string; version?: string; connected: boolean; enabled: boolean
}
interface PairingFailure { reason: string; message: string; extension_version?: string; at: number }
export function openBrowserConnections() { window.dispatchEvent(new Event('open-browser-connections')) }

interface BrowserConnectionsProps {
  open: boolean
  onClose: () => void
  onConnected?: (browserId: string) => Promise<boolean>
}

export default function BrowserConnectionsDialog(props: BrowserConnectionsProps) {
  return <BrowserConnectionsPanel {...props} inline={false} />
}

// The Browser pane and account dialog share the same account controls and requests.
export function BrowserConnectionsPanel({ open, onClose, onConnected, inline = true, selectedBrowserId, startSetup = false, additionalBrowsers, tabTitles = [], tabCount = 0, projectStatus }: BrowserConnectionsProps & { inline?: boolean; selectedBrowserId?: string; startSetup?: boolean; additionalBrowsers?: ReactNode; tabTitles?: string[]; tabCount?: number; projectStatus?: string }) {
  const [browsers, setBrowsers] = useState<SavedBrowserConnection[]>([])
  const [pairingStarted, setPairingStarted] = useState(false)
  const [setupOpen, setSetupOpen] = useState(!inline || startSetup)
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
  useEffect(() => { if (open) { void refresh(); setPairingStarted(false); setSetupOpen(!inline || startSetup) } }, [open, refresh, inline, startSetup])
  useLiveRefetch(() => { void refresh() }, { kinds: ['browser'], fallbackMs: 2500, safetyMs: 20_000, enabled: open })
  useEffect(() => {
    if (!open || inline) return
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', key); return () => document.removeEventListener('keydown', key)
  }, [open, onClose, inline])
  if (!open) return null
  const connect = async () => {
    const browserWindow = prepareSignInWindow()
    setBusy(true); setError('')
    try {
      const { data } = await api.post('/api/browser/extension', { action: 'pair' }, { skipSessionContext: true })
      const url = new URL('/api/browser/extension/setup', base)
      url.hash = new URLSearchParams({ handoff: data.handoff }).toString()
      if (!browserWindow.open(url.href)) throw new Error('Browser window blocked')
      setPairingFailure(null)
      setPairingStarted(true)
    } catch { browserWindow.close(); setError('Could not open the connection. Allow a new browser tab, then try again.') }
    finally { setBusy(false) }
  }
  const disconnect = async (id: string) => {
    setBusy(true); setError('')
    try { await api.post('/api/browser/extension', { action: 'disconnect', browser_id: id }, { skipSessionContext: true }); await refresh() }
    catch { setError('Could not disconnect this browser. Try again.') }
    finally { setBusy(false) }
  }
  const availableBrowsers = pairingStarted ? browsers.filter(browser => browser.connected && browser.enabled) : []
  const finish = async (browserId: string) => {
    if (!onConnected) { onClose(); return }
    setBusy(true)
    try { if (await onConnected(browserId)) { setSetupOpen(false); setPairingStarted(false); onClose() } else setError('Could not select this browser. Try again.') }
    catch { setError('Could not select this browser. Try again.') }
    finally { setBusy(false) }
  }

  const savedBrowsers = <ul className={inline ? 'space-y-0.5' : 'space-y-2'} aria-label="Saved browsers">{browsers.map(browser => <li key={browser.id} className={inline ? 'min-w-0 rounded px-1 py-0.5' : 'rounded-md border border-border p-3'}>
    {inline ? <>
      <div className="flex min-w-0 items-center gap-1 rounded hover:bg-muted/40">
      {onConnected && browser.enabled && browser.connected ? <button type="button" aria-label={`Use ${browser.name}`} disabled={busy || selectedBrowserId === browser.id} className="flex min-w-0 flex-1 items-center gap-2 rounded px-1 py-1.5 text-left text-xs focus-visible:outline focus-visible:outline-2 disabled:cursor-default" onClick={() => { void finish(browser.id) }}><span className="truncate">{browser.name}</span>{selectedBrowserId === browser.id && <Check role="img" aria-label="Selected" className="h-3.5 w-3.5 shrink-0" />}</button> : <span className="min-w-0 flex-1 truncate px-1 py-1.5 text-xs">{browser.name}{selectedBrowserId === browser.id && <Check role="img" aria-label="Selected" className="ml-2 inline h-3.5 w-3.5" />}</span>}
      {browser.enabled && !browser.connected && <span className="text-xs text-muted-foreground">Open {browser.name} to reconnect</span>}
      {!browser.enabled && <Button size="xs" variant="ghost" disabled={busy} aria-label={`Connect ${browser.name}`} onClick={() => { setSetupOpen(true) }}>Connect</Button>}
      <IconPopover icon={<MoreHorizontal className="h-3.5 w-3.5" />} label={`Options for ${browser.name}`} panelClassName="w-48 !p-1">
        {close => <div className="space-y-1">
          <details className="text-xs"><summary className="cursor-pointer rounded px-2 py-1.5 hover:bg-muted">Connection details</summary><div className="space-y-1 px-2 pb-2 text-muted-foreground"><p>{!browser.enabled ? 'Disconnected' : browser.connected ? 'Connected' : 'Offline'}</p><p>{browser.version ? `Extension ${browser.version}` : 'Extension version unknown'}</p>{extensionVersionOlder(browser.version, latestVersion) && <p>Update available: {latestVersion}</p>}</div></details>
          {browser.enabled && <Button size="sm" variant="ghost" className="w-full justify-start" disabled={busy} onClick={() => { close(); void disconnect(browser.id) }}>Disconnect</Button>}
        </div>}
      </IconPopover>
      </div>
      {selectedBrowserId === browser.id && projectStatus && <p role="status" className="ml-1 mt-1 text-xs text-muted-foreground">{projectStatus}</p>}
      {selectedBrowserId === browser.id && tabTitles.length > 0 && <details open className="ml-4 mt-1 text-xs"><summary className="cursor-pointer py-1 text-muted-foreground">Open tabs ({tabCount || tabTitles.length})</summary><ul className="space-y-1 py-1" aria-label="Project browser tabs">{tabTitles.map((title, index) => <li key={index} title={title} className="truncate px-2 py-1">{title}</li>)}</ul></details>}
    </> : <>
      <div className="flex items-center justify-between gap-3"><div className="min-w-0"><p className="truncate text-sm font-medium">{browser.name}</p><p className="text-xs text-muted-foreground">{!browser.enabled ? 'Disconnected' : browser.connected ? 'Connected' : 'Offline'}</p></div>{browser.enabled && <Button size="sm" variant="outline" disabled={busy} onClick={() => { void disconnect(browser.id) }}>Disconnect</Button>}</div>
      {extensionVersionOlder(browser.version, latestVersion) && <p className="mt-1 text-xs text-amber-700 dark:text-amber-300">Update available: {latestVersion}</p>}
      <details className="mt-1 text-xs text-muted-foreground"><summary className="cursor-pointer">Connection details</summary><div className="mt-2"><p>{browser.version ? `Extension ${browser.version}` : 'Extension version unknown'}</p></div></details>
    </>}
  </li>)}</ul>

  const content = <div className={inline ? 'space-y-3' : 'space-y-4 p-4'}>
    {!setupOpen && <>
      {savedBrowsers}
      {additionalBrowsers}
      <Button size="sm" variant="ghost" onClick={() => setSetupOpen(true)}><Plus className="h-3.5 w-3.5" />Add browser</Button>
    </>}
    {setupOpen && <div className="space-y-4" aria-label="Connect browser">
      <Button size="sm" variant="ghost" onClick={() => { setSetupOpen(false); setPairingStarted(false) }}><ArrowLeft className="h-3.5 w-3.5" />Back</Button>
      <div className="space-y-1"><h3 className="text-sm font-medium">Connect browser</h3><p className="text-xs text-muted-foreground">Install Browser Bridge in Chrome or Edge, then connect this browser.</p></div>
      <div className="flex flex-wrap gap-2">
        <Button asChild variant="outline" size="sm"><a href={new URL('/api/downloads/chrome-extension.zip', base).href} download><Download />Download extension</a></Button>
        <Button size="sm" disabled={busy} onClick={() => { void connect() }}><ExternalLink />Connect browser</Button>
      </div>
      <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Installation help</summary><ol className="ml-4 mt-2 list-decimal space-y-2"><li>Download and unzip Browser Bridge.</li><li>Open chrome://extensions or edge://extensions, enable Developer mode, choose Load unpacked and select the unzipped folder.</li><li>Choose Connect browser here, then open Browser Bridge on its setup tab. Check the account and server and choose Connect browser in the extension.</li></ol></details>
      {pairingStarted && <p role="status" className="text-xs text-muted-foreground">{availableBrowsers.length ? 'Choose a browser to use here.' : 'Open Browser Bridge on the setup tab to finish connecting.'}</p>}
      {!!availableBrowsers.length && <div className="flex flex-wrap gap-2" role="group" aria-label="Choose connected browser">{availableBrowsers.map(browser => <Button key={browser.id} size="sm" variant="outline" disabled={busy} onClick={() => { void finish(browser.id) }}>{selectedBrowserId === browser.id && <Check role="img" aria-label="Selected" />}{onConnected ? `Use ${browser.name}` : `Done · ${browser.name}`}</Button>)}</div>}
    </div>}
        {pairingFailure && <div role="alert" className="space-y-1 rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-sm"><p className="font-medium">{pairingFailure.reason === 'extension_outdated' ? 'Update your browser extension' : 'Browser could not connect'}</p><p>{pairingFailure.message}</p>{pairingFailure.extension_version && <p className="text-xs text-muted-foreground">Installed extension: {pairingFailure.extension_version}{latestVersion ? ` · Download: ${latestVersion}` : ''}</p>}</div>}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </div>
  if (inline) return <TooltipProvider><section aria-label="Browser connections">{content}</section></TooltipProvider>
  return <ModalPortal><div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
    <section role="dialog" aria-modal="true" aria-labelledby="browser-connections-title" className="w-full max-w-lg max-h-[85vh] overflow-y-auto rounded-lg border border-border bg-background shadow-xl" onClick={event => event.stopPropagation()}>
      <header className="flex items-center justify-between border-b border-border p-4"><h2 id="browser-connections-title" className="text-lg font-semibold">Connect browser</h2><button onClick={onClose} aria-label="Close browser connections"><X className="h-4 w-4" /></button></header>
      {content}
    </section>
  </div></ModalPortal>
}
