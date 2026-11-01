import { useCallback, useEffect, useState } from 'react'
import { Check, Copy, Download, MoreHorizontal, Plus, X } from 'lucide-react'
import api from '../services/api'
import { Button } from './ui/Button'
import ModalPortal from './ui/ModalPortal'
import IconPopover from './ui/IconPopover'
import { TooltipProvider } from './ui/tooltip'
import { useLiveRefetch } from '../hooks/useLiveRefetch'
import { getRuntimeAppName, runtimeBrandingConfig } from '../runtime-branding'
import { extensionVersionOlder } from '../utils/extensionVersion'

export interface SavedBrowserConnection {
  id: string; name: string; browser: string; version?: string; connected: boolean; enabled: boolean
}
interface PairingFailure { reason: string; message: string; extension_version?: string; at: number }
export function openBrowserConnections() { window.dispatchEvent(new Event('open-browser-connections')) }

export interface WorkspaceBrowserChoice {
  label: string
  selected: boolean
  secondary: boolean
  onSelect: () => boolean | void | Promise<boolean | void>
}

interface BrowserConnectionsProps {
  open: boolean
  onClose: () => void
  onConnected?: (browserId: string) => Promise<boolean>
}

export default function BrowserConnectionsDialog(props: BrowserConnectionsProps) {
  return <BrowserConnectionsPanel {...props} inline={false} />
}

// The Browser pane and account dialog share the same account controls and requests.
export function BrowserConnectionsPanel({ open, onClose, onConnected, inline = true, selectedBrowserId, startSetup = false, workspaceBrowser }: BrowserConnectionsProps & { inline?: boolean; selectedBrowserId?: string; startSetup?: boolean; workspaceBrowser?: WorkspaceBrowserChoice }) {
  const [browsers, setBrowsers] = useState<SavedBrowserConnection[]>([])
  const [code, setCode] = useState('')
  const [setupOpen, setSetupOpen] = useState(!inline || startSetup)
  const [step, setStep] = useState<'install' | 'connect'>('install')
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
  useEffect(() => { if (open) { void refresh(); setCode(''); setCopied(false); setStep('install'); setSetupOpen(!inline || startSetup) } }, [open, refresh, inline, startSetup])
  useLiveRefetch(() => { void refresh() }, { kinds: ['browser'], fallbackMs: 2500, safetyMs: 20_000, enabled: open })
  useEffect(() => {
    if (!open || inline) return
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', key); return () => document.removeEventListener('keydown', key)
  }, [open, onClose, inline])
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
  const availableBrowsers = code && !inline ? browsers.filter(browser => browser.connected && browser.enabled) : []
  const finish = async (browserId: string) => {
    if (!onConnected) { onClose(); return }
    setBusy(true)
    try { if (await onConnected(browserId)) { setSetupOpen(false); setCode(''); onClose() } else setError('Could not select this browser. Try again.') }
    catch { setError('Could not select this browser. Try again.') }
    finally { setBusy(false) }
  }

  const selectWorkspace = async () => {
    setBusy(true)
    try { if (await workspaceBrowser?.onSelect() !== false) { setSetupOpen(false); setCode('') } }
    catch { setError('Could not change this project’s browser. Try again.') }
    finally { setBusy(false) }
  }
  const workspaceChoice = workspaceBrowser && <Button size="sm" variant="ghost" className="w-full justify-between" disabled={busy} onClick={() => { void selectWorkspace() }}>{workspaceBrowser.label}{workspaceBrowser.selected && <Check aria-label="Selected" className="h-3.5 w-3.5" />}</Button>
  const chromeBrowser = browsers.find(browser => browser.browser === 'Chrome')
  const savedBrowsers = <ul className={inline ? 'space-y-0.5' : 'space-y-2'} aria-label="Saved browsers">{browsers.map(browser => <li key={browser.id} className={inline ? 'flex min-w-0 items-center gap-1 rounded px-1 py-0.5 hover:bg-muted/40' : 'rounded-md border border-border p-3'}>
    {inline ? <>
      {onConnected && browser.enabled && browser.connected ? <button type="button" aria-label={`Use ${browser.name}`} disabled={busy || selectedBrowserId === browser.id} className="flex min-w-0 flex-1 items-center gap-2 rounded px-1 py-1.5 text-left text-xs focus-visible:outline focus-visible:outline-2 disabled:cursor-default" onClick={() => { void finish(browser.id) }}><span className="truncate">{browser.name}</span>{selectedBrowserId === browser.id && <Check role="img" aria-label="Selected" className="h-3.5 w-3.5 shrink-0" />}</button> : <span className="min-w-0 flex-1 truncate px-1 py-1.5 text-xs">{browser.name}{selectedBrowserId === browser.id && <Check role="img" aria-label="Selected" className="ml-2 inline h-3.5 w-3.5" />}</span>}
      {browser.enabled && !browser.connected && <span className="text-xs text-muted-foreground">Open {browser.name} to reconnect</span>}
      {!browser.enabled && <Button size="xs" variant="ghost" disabled={busy} aria-label={`Connect ${browser.name}`} onClick={() => { setStep('connect'); setSetupOpen(true) }}>Connect</Button>}
      <IconPopover icon={<MoreHorizontal className="h-3.5 w-3.5" />} label={`Options for ${browser.name}`} panelClassName="w-48 !p-1">
        {close => <div className="space-y-1">
          <details className="text-xs"><summary className="cursor-pointer rounded px-2 py-1.5 hover:bg-muted">Connection details</summary><div className="space-y-1 px-2 pb-2 text-muted-foreground"><p>{!browser.enabled ? 'Disconnected' : browser.connected ? 'Connected' : 'Offline'}</p><p>{browser.version ? `Extension ${browser.version}` : 'Extension version unknown'}</p>{extensionVersionOlder(browser.version, latestVersion) && <p>Update available: {latestVersion}</p>}</div></details>
          {workspaceBrowser?.secondary && !workspaceBrowser.selected && chromeBrowser?.id === browser.id && workspaceChoice}
          {browser.enabled && <Button size="sm" variant="ghost" className="w-full justify-start" disabled={busy} onClick={() => { close(); void disconnect(browser.id) }}>Disconnect</Button>}
        </div>}
      </IconPopover>
    </> : <>
      <div className="flex items-center justify-between gap-3"><div className="min-w-0"><p className="truncate text-sm font-medium">{browser.name}</p><p className="text-xs text-muted-foreground">{!browser.enabled ? 'Disconnected' : browser.connected ? 'Connected' : 'Offline'}</p></div>{browser.enabled && <Button size="sm" variant="outline" disabled={busy} onClick={() => { void disconnect(browser.id) }}>Disconnect</Button>}</div>
      {extensionVersionOlder(browser.version, latestVersion) && <p className="mt-1 text-xs text-amber-700 dark:text-amber-300">Update available: {latestVersion}</p>}
      <details className="mt-1 text-xs text-muted-foreground"><summary className="cursor-pointer">Connection details</summary><div className="mt-2"><p>{browser.version ? `Extension ${browser.version}` : 'Extension version unknown'}</p></div></details>
    </>}
  </li>)}</ul>

  const content = <div className={inline ? 'space-y-3' : 'space-y-4 p-4'}>
    {inline && savedBrowsers}
    {inline && (!workspaceBrowser?.secondary || workspaceBrowser.selected) && workspaceChoice}
    {inline && !setupOpen && <div className="flex items-center justify-between gap-1"><Button size="sm" variant="ghost" onClick={() => setSetupOpen(true)}><Plus className="h-3.5 w-3.5" />Add browser</Button>{workspaceBrowser?.secondary && !workspaceBrowser.selected && !chromeBrowser && <IconPopover icon={<MoreHorizontal className="h-3.5 w-3.5" />} label="Browser options" panelClassName="w-48 !p-1">{workspaceChoice}</IconPopover>}</div>}
    {setupOpen && <div className={inline ? 'space-y-3 border-t border-border pt-3' : 'space-y-3'} aria-label="Connect browser">
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
            {!!availableBrowsers.length && <div className="space-y-2"><p className="text-xs text-muted-foreground">Choose a browser to finish.</p>{availableBrowsers.map(browser => <Button key={browser.id} size="sm" disabled={busy} onClick={() => { void finish(browser.id) }}><Check />{onConnected ? `Use ${browser.name}` : `Done · ${browser.name}`}</Button>)}</div>}
            <Button size="sm" variant="ghost" onClick={() => setStep('install')}>Back</Button>
          </div>}
    {inline && <Button size="sm" variant="ghost" onClick={() => { setSetupOpen(false); setCode('') }}>Cancel setup</Button>}
    </div>}
    {!inline && !!browsers.length && <details className="space-y-2 border-t border-border pt-3"><summary className="cursor-pointer text-xs text-muted-foreground">Saved browsers</summary><div className="mt-2">{savedBrowsers}</div></details>}
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
