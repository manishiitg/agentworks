import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Download } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { BrowserConnectionsPanel, type SavedBrowserConnection } from '../BrowserConnectionsDialog'
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

export function ChromeExtensionConnection({ connection, additionalBrowsers }: { connection: ChromeExtensionController; additionalBrowsers?: ReactNode }) {
  const { status, error, connect, download } = connection
  const accountBrowser = status.browsers?.find(browser => browser.id === status.browser_id)
  const projectStatus = status.selected ? (status.connected ? (status.tabs ? undefined : 'Ask your agent to open a website.') : accountBrowser?.connected ? 'Connecting this browser…' : undefined) : undefined
  return <div className="space-y-3" aria-label="Choose browser">
    <BrowserConnectionsPanel open onClose={() => {}} onConnected={connect} selectedBrowserId={status.selected ? status.browser_id : undefined} additionalBrowsers={additionalBrowsers} tabTitles={status.connected ? status.tab_titles : undefined} tabCount={status.tabs} projectStatus={projectStatus} />
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
