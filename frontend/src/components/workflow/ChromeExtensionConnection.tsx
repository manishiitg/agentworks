import { useEffect, useRef, useState } from 'react'
import { Check, Copy, Download, Loader2, RefreshCw, ShieldCheck } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { getRuntimeAppName, runtimeBrandingConfig } from '../../runtime-branding'

export interface ChromeExtensionStatus {
  selected: boolean; connected: boolean; tabs: number; workspace?: string; connection_id?: string; tab_titles?: string[]
}
const emptyStatus: ChromeExtensionStatus = { selected: false, connected: false, tabs: 0 }

// Keep the connection alive in the panel's state even when settings is closed.
export function useChromeExtensionConnection(workspacePath: string | null, profileId?: string, readOnly = false) {
  const [status, setStatus] = useState(emptyStatus)
  const [loading, setLoading] = useState(!!workspacePath && !readOnly)
  const [pairing, setPairing] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [checkingError, setCheckingError] = useState('')
  const [copied, setCopied] = useState(false)
  const [manual, setManual] = useState(false)
  const generation = useRef(0)
  const revision = useRef(0)
  const awaiting = useRef<{ previous: string } | null>(null)
  const base = new URL(String(api.defaults.baseURL || window.location.origin), window.location.origin)
  const download = new URL('/api/downloads/chrome-extension.zip', base).href

  useEffect(() => {
    generation.current += 1; revision.current += 1; awaiting.current = null
    setStatus(emptyStatus); setPairing(''); setBusy(false); setError(''); setCheckingError(''); setCopied(false); setManual(false)
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
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 2500)
    return () => { alive = false; window.clearInterval(timer) }
  }, [workspacePath, profileId, readOnly])

  const disconnect = async () => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError('')
    try {
      await api.post('/api/browser/extension', { action: 'disconnect' }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current !== turn) return false
      awaiting.current = null; setStatus(emptyStatus); setPairing(''); setCopied(false); setManual(false)
      return true
    } catch { if (generation.current === turn) setError('Could not disconnect. Your current browser is still selected.'); return false }
    finally { if (generation.current === turn) setBusy(false) }
  }
  const copy = async (reset = false) => {
    const turn = generation.current
    revision.current += 1; setBusy(true); setError(''); setCopied(false)
    try {
      const { data } = await api.post<{ token: string; scope: string }>('/api/browser/extension', { action: reset ? 'reset' : 'pair' }, { params: { workspace_path: workspacePath, profile_id: profileId }, skipSessionContext: true })
      if (generation.current !== turn) return
      const url = new URL('/api/browser/extension/connect', base)
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
      const value = JSON.stringify({ url: url.href, token: data.token, scope: data.scope, brand: getRuntimeAppName(runtimeBrandingConfig()) || 'AgentWorks' })
      awaiting.current = { previous: status.connection_id || '' }
      setPairing(value)
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
  return { status, loading, pairing, busy, error: error || checkingError, copied, manual, setManual, copy, disconnect, download }
}
export type ChromeExtensionController = ReturnType<typeof useChromeExtensionConnection>

export function ChromeExtensionConnection({ connection }: { connection: ChromeExtensionController }) {
  const { status, pairing, busy, error, copied, manual, setManual, copy, download } = connection
  const [installBrowser, setInstallBrowser] = useState(navigator.userAgent.includes('Edg') ? 'edge' : 'chrome')
  const [reconnect, setReconnect] = useState(false)
  const [resetting, setResetting] = useState(false)
  const address = installBrowser === 'edge' ? 'edge://extensions' : 'chrome://extensions'
  const setup = !status.connected || reconnect || !!pairing
  useEffect(() => { if (status.connected && !pairing) { setReconnect(false); setResetting(false) } }, [status.connected, status.connection_id, pairing])

  return <div className="space-y-4">
    <p className="text-xs leading-relaxed text-muted-foreground">Use the tabs you share from your signed-in browser. This connection is private to you in this workspace.</p>
    {status.selected && <div className="rounded-lg border border-border bg-muted/30 p-3">
      <div className="flex items-center justify-between gap-3">
        <span className="flex items-center gap-2 text-sm font-medium"><span className={`h-2 w-2 rounded-full ${status.connected ? 'bg-emerald-500' : 'bg-amber-500'}`} />{status.connected ? 'Connected' : 'Disconnected'}</span>
        {status.connected && <Button size="xs" variant="ghost" disabled={busy} onClick={() => setReconnect(true)}>Reconnect</Button>}
      </div>
      <p className="mt-1 break-words text-xs text-muted-foreground">{status.connected ? status.tabs > 0 ? `${status.tabs} shared ${status.tabs === 1 ? 'tab is' : 'tabs are'} ready for your agent.` : 'Your agent can create its own tabs. Sharing an already-open tab is optional.' : 'Browser actions are paused until you reconnect.'}</p>
    </div>}
    {setup && <ol className="space-y-4">
      <li className="flex gap-3">
        <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border text-xs font-medium text-muted-foreground">1</span>
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="text-sm font-medium">Install the extension</h4><Button asChild size="sm" variant="outline"><a href={download} download><Download />Download ZIP</a></Button></div>
          <details className="text-xs text-muted-foreground">
            <summary className="cursor-pointer hover:text-foreground">Installation instructions</summary>
            <div className="mt-2 space-y-2 rounded-md bg-muted/40 p-3 leading-relaxed">
              <div className="flex gap-1" aria-label="Installation browser">{['chrome', 'edge'].map(browser => <Button key={browser} size="xs" variant={installBrowser === browser ? 'secondary' : 'ghost'} aria-pressed={installBrowser === browser} onClick={() => setInstallBrowser(browser)}>{browser === 'chrome' ? 'Chrome' : 'Microsoft Edge'}</Button>)}</div>
              <p>Unzip the download, then open <code className="select-all text-foreground">{address}</code>.</p>
              <p>Enable <strong>Developer mode</strong>, click <strong>Load unpacked</strong>, and select the unzipped folder. Pin AgentWorks to the toolbar.</p>
            </div>
          </details>
        </div>
      </li>
      <li className="flex gap-3">
        <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border text-xs font-medium text-muted-foreground">2</span>
        <div className="min-w-0 flex-1 space-y-2">
          <h4 className="text-sm font-medium">Connect to this workspace</h4>
          <p className="text-xs leading-relaxed text-muted-foreground">Copy the connection and paste it into the extension once. If your browser is already paired, this connects the project automatically.</p>
          <Button size="sm" disabled={busy} onClick={() => { void copy() }}>{busy ? <Loader2 className="animate-spin" /> : copied ? <Check /> : <Copy />}{busy ? 'Preparing…' : copied ? 'Copied connection' : 'Copy connection'}</Button>
          {pairing && <>
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" />Waiting for your browser…</p>
            <details open={manual} onToggle={event => setManual(event.currentTarget.open)}>
              <summary className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">Copy manually</summary>
              <textarea aria-label="Browser connection code" readOnly value={pairing} rows={3} className="mt-2 w-full resize-none rounded-md border border-border bg-muted/30 p-2 font-mono text-[11px] leading-relaxed" onFocus={event => event.target.select()} />
            </details>
          </>}
          <p className="text-[11px] text-muted-foreground">One stable token works across your Code and Crew projects. Each project keeps its own shared tabs.</p>
        </div>
      </li>
      <li className="flex gap-3">
        <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border text-xs font-medium text-muted-foreground">3</span>
        <div className="min-w-0 space-y-1"><h4 className="text-sm font-medium">Let your agent browse</h4><p className="text-xs leading-relaxed text-muted-foreground">Your agent can create and choose its own project tabs. To use an already-open page, choose <strong>Share this tab</strong> in the extension.</p></div>
      </li>
    </ol>}
    {error && <p role="alert" className="text-xs text-red-600 dark:text-red-300">{error}</p>}
    <p className="flex items-start gap-2 border-t border-border pt-3 text-[11px] leading-relaxed text-muted-foreground"><ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0" />Only shared tabs are accessible. Their contents and screenshots are sent to the platform. Stop access from the extension at any time.</p>
    <details className="text-xs text-muted-foreground">
      <summary className="cursor-pointer hover:text-foreground">Connection code options</summary>
      <div className="mt-2 space-y-2">
        <p>Reset invalidates all saved copies of your account token and disconnects every Code and Crew browser connection.</p>
        {resetting ? <div className="flex flex-wrap gap-2"><Button size="sm" variant="destructive" disabled={busy} onClick={() => { void copy(true); setResetting(false); setReconnect(true) }}>Reset all connections</Button><Button size="sm" variant="ghost" onClick={() => setResetting(false)}>Cancel</Button></div> : <Button size="sm" variant="outline" disabled={busy} onClick={() => setResetting(true)}><RefreshCw />Reset connection code</Button>}
      </div>
    </details>
  </div>
}
