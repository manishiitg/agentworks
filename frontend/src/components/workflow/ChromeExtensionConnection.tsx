import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Download, ExternalLink, MoreHorizontal } from 'lucide-react'
import IconPopover from '../ui/IconPopover'
import { prepareSignInWindow } from '../../utils/signInWindow'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { type SavedBrowserConnection } from '../BrowserConnectionsDialog'
export { extensionVersionOlder } from '../../utils/extensionVersion'

export interface ChromeExtensionStatus {
  update_needed?: boolean; update_url?: string
  mode?: 'none' | 'extension' | 'headless' | 'cdp'; allowed_modes?: ('extension' | 'headless' | 'cdp')[]; selected: boolean; connected: boolean; account_connected?: boolean; tabs: number; workspace?: string; connection_id?: string; tab_titles?: string[]
  browser_id?: string; browsers?: SavedBrowserConnection[]; message?: string; extension_version?: string; latest_extension_version?: string
}

const emptyStatus: ChromeExtensionStatus = { selected: false, connected: false, tabs: 0 }

// Account pairing lives in Browser connections. This hook only selects a
// saved browser for the current project and reads its server-owned status.
export function useChromeExtensionConnection(workspacePath: string | null, profileId?: string, readOnly = false) {
  const [status, setStatus] = useState(emptyStatus)
  const [loading, setLoading] = useState(!!workspacePath && !readOnly)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [checkingError, setCheckingError] = useState('')
  const generation = useRef(0)
  const revision = useRef(0)
  const refreshRef = useRef<() => void>(() => {})
  const base = new URL(String(api.defaults.baseURL || window.location.origin), window.location.origin)
  const download = new URL('/api/downloads/chrome-extension.zip', base).href

  useEffect(() => {
    generation.current += 1; revision.current += 1
    setStatus(emptyStatus); setBusy(false); setError(''); setCheckingError('')
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
        if (alive && mutation === revision.current) { setStatus(data); setLoading(false); setCheckingError('') }
      } catch {
        if (alive && mutation === revision.current) { setLoading(false); setCheckingError('Could not check the browser connection. Try again shortly.') }
      } finally { refreshing = false }
    }
    refreshRef.current = () => { void refresh() }
    void refresh()
    return () => { alive = false; refreshRef.current = () => {} }
  }, [workspacePath, profileId, readOnly])
  useLiveRefetch(() => refreshRef.current(), { kinds: ['browser'], fallbackMs: 2500, safetyMs: 20_000, minIntervalMs: 500, enabled: !!workspacePath && !readOnly })

  const select = async (action: 'connect' | 'disconnect' | 'workspace', browserId?: string, mode?: string, port?: number) => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError('')
    try {
      const { data } = await api.post<ChromeExtensionStatus>('/api/browser/extension', { action, ...(browserId ? { browser_id: browserId } : {}), ...(mode ? { mode, port } : {}) }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current !== turn) return false
      setStatus(data)
      return true
    } catch {
      if (generation.current === turn) setError('Could not confirm the browser change. Check the connection and try again.')
      return false
    } finally { if (generation.current === turn) setBusy(false) }
  }
  return { status, loading, busy, error: error || checkingError, connect: (browserId?: string) => select('connect', browserId), disconnect: () => select('disconnect'), selectWorkspace: (mode: string, port?: number) => select('workspace', undefined, mode, port), download }
}
export type ChromeExtensionController = ReturnType<typeof useChromeExtensionConnection>

const ago = (at?: number) => {
  if (!at) return ''
  const minutes = Math.max(1, Math.round((Date.now() - at) / 60000))
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.round(minutes / 60)
  return hours < 48 ? `${hours} h ago` : `${Math.round(hours / 24)} days ago`
}

/**
 * The Browser section (owner approved the mock, 11 Nov): the Chrome extension first and the one default. First time it
 * says exactly what to do; once Chromes are connected each is a row (one picked per Crew, others a click away). The
 * server's allowed modes decide which other ways appear (`otherWays`, `ownCard`); this screen only names them.
 */
export function ChromeExtensionConnection({ connection, mac, otherWays, ownCard }: { connection: ChromeExtensionController; mac: boolean; otherWays?: ReactNode; ownCard?: ReactNode }) {
  const { status, error, connect, disconnect, download, busy } = connection
  const browsers = status.browsers ?? []
  const [pairingStarted, setPairingStarted] = useState(false)
  const [adding, setAdding] = useState(false)
  const [help, setHelp] = useState(false)
  const [pairError, setPairError] = useState('')
  const base = new URL(String(api.defaults.baseURL || window.location.origin), window.location.origin)
  const pair = async () => {
    const browserWindow = prepareSignInWindow()
    setPairError('')
    try {
      const { data } = await api.post('/api/browser/extension', { action: 'pair' }, { skipSessionContext: true })
      const url = new URL('/api/browser/extension/setup', base)
      url.hash = new URLSearchParams({ handoff: data.handoff }).toString()
      if (!browserWindow.open(url.href)) throw new Error('Browser window blocked')
      setPairingStarted(true)
    } catch { browserWindow.close(); setPairError('Could not open the connection. Allow a new browser tab, then try again.') }
  }
  const picked = status.selected ? status.browser_id : undefined
  const anyConnected = browsers.some(browser => browser.enabled && browser.connected)
  const steps = <div className="rounded-lg border border-primary/40 bg-primary/5 p-3" aria-label="Use your own Chrome">
    <div className="flex items-center gap-2"><span className="text-sm font-medium">Use your own Chrome</span><span className="rounded-full border border-primary/40 px-2 text-[11px] text-primary">Recommended</span></div>
    <p className="mt-0.5 text-xs text-muted-foreground">{mac ? 'Agents work in your normal Chrome, with your sign-ins. You see every tab they open.' : 'The extension lets agents on the server work in your Chrome tabs, with your sign-ins.'}</p>
    <ol className="ml-4 mt-2 list-decimal space-y-1 text-xs">
      <li>Add the AgentWorks extension to Chrome</li>
      <li>{mac ? 'Pin it, then click it once (it connects to this Mac)' : 'Click it once and choose this server'}</li>
      <li>{mac ? 'Come back here: it shows Connected' : 'It shows Connected here'}</li>
    </ol>
    <div className="mt-2 flex flex-wrap gap-2">
      <Button asChild size="sm"><a href={download} download><Download />Add to Chrome</a></Button>
      <Button size="sm" variant="outline" disabled={busy} onClick={() => { void pair() }}><ExternalLink />Connect</Button>
      <Button size="sm" variant="ghost" aria-expanded={help} onClick={() => setHelp(value => !value)}>Show me how</Button>
    </div>
    {help && <ol className="ml-4 mt-2 list-decimal space-y-2 text-xs text-muted-foreground"><li>Download and unzip the extension.</li><li>Open chrome://extensions, turn on Developer mode, choose Load unpacked and pick the unzipped folder.</li><li>Choose Connect here, then click the extension on the page that opens and choose Connect.</li></ol>}
    <p role="status" className="mt-2 flex items-center gap-2 border-t border-border pt-2 text-xs text-muted-foreground"><span aria-hidden="true" className={`h-2 w-2 rounded-full ${pairingStarted ? 'bg-amber-500' : 'bg-muted-foreground/60'}`} />{pairingStarted ? 'Waiting for the extension…' : 'Not connected yet'}</p>
  </div>
  return <div className="space-y-3" aria-label="Choose browser">
    {browsers.length > 1 && <p className="px-0.5 text-xs text-muted-foreground">Agents in this Crew use the one you pick.</p>}
    {ownCard}
    {browsers.map(browser => {
      const isPicked = picked === browser.id
      const live = browser.enabled && browser.connected
      const tabs = isPicked && status.connected ? status.tab_titles ?? [] : []
      return <div key={browser.id} className={`rounded-lg border p-3 ${isPicked ? 'border-primary/40 bg-primary/5' : 'border-border'}`} aria-label={browser.name}>
        <div className="flex min-w-0 items-center gap-2">
          {browsers.length > 1 && <span aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 rounded-full border-2 ${isPicked ? 'border-primary bg-primary/70' : 'border-muted-foreground/60'}`} />}
          <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${live ? 'bg-emerald-500' : 'bg-muted-foreground/60'}`} />
          <span className="min-w-0 truncate text-sm font-medium">{browsers.length > 1 || !isPicked ? browser.name : 'Your Chrome'}</span>
          <span className="shrink-0 rounded-full border border-border px-2 text-[11px] text-muted-foreground">extension</span>
          <span className="flex-1" />
          {live && !isPicked && <Button size="xs" variant="outline" disabled={busy} onClick={() => { void connect(browser.id) }}>Use this</Button>}
          <IconPopover icon={<MoreHorizontal className="h-3.5 w-3.5" />} label={`Options for ${browser.name}`} panelClassName="w-44 !p-1">
            {close => <div className="space-y-1">
              {browser.enabled && <Button size="sm" variant="ghost" className="w-full justify-start" disabled={busy} onClick={() => { close(); void disconnect() }}>Disconnect</Button>}
              <Button size="sm" variant="ghost" className="w-full justify-start" disabled={busy} onClick={() => { close(); void pair() }}>Reconnect</Button>
            </div>}
          </IconPopover>
        </div>
        <p className={`mt-0.5 text-xs text-muted-foreground ${browsers.length > 1 ? 'ml-6' : ''}`}>
          {live ? (isPicked ? (browsers.length > 1 ? 'Connected · agents use this one' : 'Connected. Agents in this Crew use it.') : 'Connected')
            : !browser.enabled ? 'Disconnected. Connect it again to use it.'
              : `Not connected${browser.last_connected ? ` · last seen ${ago(browser.last_connected)}` : ''} (that computer is off or Chrome is closed)`}
        </p>
        {isPicked && status.connected && !tabs.length && <p className={`mt-1 text-xs text-muted-foreground ${browsers.length > 1 ? 'ml-6' : ''}`}>Ask your agent to open a website.</p>}
        {tabs.length > 0 && <details open className={`mt-2 text-xs ${browsers.length > 1 ? 'ml-6' : ''}`}><summary className="cursor-pointer text-muted-foreground">Open tabs ({status.tabs || tabs.length})</summary><ul className="mt-1 space-y-1" aria-label="Project browser tabs">{tabs.map((title, index) => <li key={index} title={title} className="truncate rounded bg-muted/40 px-2 py-1">{title}</li>)}</ul></details>}
      </div>
    })}
    {browsers.length === 0 ? steps : adding ? <div className="space-y-2">{steps}<Button size="xs" variant="ghost" onClick={() => setAdding(false)}>Close</Button></div>
      : <button type="button" className="text-left text-xs text-primary hover:underline" onClick={() => setAdding(true)}>＋ Connect another Chrome <span className="text-muted-foreground">(install the extension in that Chrome or profile)</span></button>}
    {!anyConnected && browsers.length > 0 && <p className="text-xs text-muted-foreground">Open Chrome with the extension to connect it.</p>}
    {otherWays && <details className="text-xs"><summary className="cursor-pointer text-muted-foreground">Other ways to connect</summary><div className="mt-2 space-y-2">{otherWays}</div></details>}
    {pairError && <p role="alert" className="text-xs text-destructive">{pairError}</p>}
    {status.update_needed && <ExtensionVersionLine status={status} download={download} />}
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </div>
}

function ExtensionVersionLine({ status, download }: { status: ChromeExtensionStatus; download: string }) {
  const installed = status.extension_version
  const browser = status.browsers?.find(browser => browser.id === status.browser_id)
  const outdated = status.update_needed === true
  const updateURL = status.update_url ? new URL(status.update_url, download).href : download
  if (!outdated) return null
  return <div className="mt-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-2 text-xs">
    <p className="font-medium text-amber-700 dark:text-amber-300">{browser ? `${browser.name} · ` : ''}Update Browser Bridge{installed ? `: ${installed}` : ''}{status.latest_extension_version ? ` → ${status.latest_extension_version}` : ''}</p>
    <p className="mt-1 leading-relaxed text-muted-foreground">Download it, unzip it over your Browser Bridge folder, then press Reload on Browser Bridge in your browser’s Extensions page.</p>
    <Button asChild size="xs" variant="outline" className="mt-2"><a href={updateURL} download><Download />Download Browser Bridge</a></Button>
  </div>
}
