import { useEffect, useRef, useState } from 'react'
import { Download, Loader2 } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { openBrowserConnections, type SavedBrowserConnection } from '../BrowserConnectionsDialog'
import { extensionVersionOlder } from '../../utils/extensionVersion'
export { extensionVersionOlder } from '../../utils/extensionVersion'

export interface ChromeExtensionStatus {
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

  const select = async (action: 'connect' | 'disconnect', browserId?: string) => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError('')
    try {
      const { data } = await api.post<ChromeExtensionStatus>('/api/browser/extension', { action, ...(browserId ? { browser_id: browserId } : {}) }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current !== turn) return false
      setStatus(data)
      return true
    } catch {
      if (generation.current === turn) setError('Could not change this project’s browser. Your previous choice is still selected.')
      return false
    } finally { if (generation.current === turn) setBusy(false) }
  }
  return { status, loading, busy, error: error || checkingError, connect: (browserId?: string) => select('connect', browserId), disconnect: () => select('disconnect'), download }
}
export type ChromeExtensionController = ReturnType<typeof useChromeExtensionConnection>

export function ChromeExtensionConnection({ connection, workspaceBrowser }: { connection: ChromeExtensionController; workspaceBrowser?: { label: string; selected: boolean; onSelect: () => void | Promise<void> } }) {
  const { status, busy, error, connect, download } = connection
  const [chosenBrowser, setChosenBrowser] = useState('')
  const choice = chosenBrowser || (workspaceBrowser?.selected ? 'workspace-browser' : status.browser_id || '')
  return <div className="space-y-2">
    <div className="flex flex-wrap items-center gap-2">
      <label className="text-xs text-muted-foreground" htmlFor="project-saved-browser">Browser</label>
      <select id="project-saved-browser" value={choice} disabled={busy} onChange={e => setChosenBrowser(e.target.value)} className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-xs">
        {workspaceBrowser && <option value="workspace-browser">{workspaceBrowser.label}</option>}
        {!status.browser_id && !workspaceBrowser?.selected && <option value="" disabled>Choose a browser</option>}
        {status.browsers?.filter(b => b.enabled).map(b => <option key={b.id} value={b.id}>{b.name} · {b.connected ? 'Connected' : 'Offline'}</option>)}
      </select>
      {choice !== (workspaceBrowser?.selected ? 'workspace-browser' : status.browser_id || '') && <Button size="sm" disabled={busy || !choice} onClick={() => { if (choice === 'workspace-browser' && workspaceBrowser) { void Promise.resolve(workspaceBrowser.onSelect()).then(() => setChosenBrowser('')) } else { void connect(choice).then(saved => { if (saved) setChosenBrowser('') }) } }}>{busy && <Loader2 />}Use browser</Button>}
      <Button size="sm" variant="ghost" onClick={openBrowserConnections}>Connections</Button>
    </div>
    {!status.selected && !!status.browsers?.some(b => b.connected) && <p className="text-xs text-muted-foreground">Chrome or Edge is connected to your account. Choose a saved browser above to use it here.</p>}
    {status.selected && status.connected && <ExtensionVersionLine status={status} download={download} />}
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
