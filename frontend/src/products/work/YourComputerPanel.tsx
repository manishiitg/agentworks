import { useEffect, useState, type ReactNode } from 'react'
import { CheckCircle2, CircleDashed, Folder, Laptop, RefreshCw, Terminal, TriangleAlert } from 'lucide-react'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { Button } from '../../components/ui/Button'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { useLocalFileDevices } from './codeLocalFiles'

type Binding = { connection_id?: string; peer_server?: string; peer_project_id: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' | 'local' | 'download' }
type SyncStatus = { binding: Binding | null; auto?: { state: string; message?: string } }
type LaptopSettings = { device_id: string; folders: { id: string; path: string; access: 'read' | 'write' }[]; commands: boolean }

/** In the Mac app a Crew is either online on a network or only on this Mac; on a server the page is its laptop access. */
export const crewOnlineMode = () => hasDesktopConnections()
// Most people have one network, so its name is shown only when there are several.
const networkName = (binding: Binding | null | undefined, connections: { id: string; name: string }[]) =>
  connections.filter(item => item.id !== 'local').length > 1 ? connections.find(item => item.id === binding?.connection_id)?.name || '' : ''

/** The one-line answer for the main menu row: Online (with a small spinner while it syncs) or Off. */
export function useYourComputerSummary(enabled: boolean, projectId?: string, profileId?: string): ReactNode {
  const connections = useConnectedWorkStore(state => state.connections)
  const [summary, setSummary] = useState<ReactNode>()
  useEffect(() => {
    setSummary(undefined)
    if (!enabled || !projectId) return
    const abort = new AbortController(), options = { signal: abort.signal, skipSessionContext: true }
    const load = () => {
      const request: Promise<ReactNode> = crewOnlineMode()
        ? api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, options).then(({ data }) => {
          if (!data.binding) return 'Off'
          const name = networkName(data.binding, connections)
          if (data.auto?.state === 'syncing') return <span className="inline-flex items-center gap-1"><RefreshCw aria-label="Syncing" className="h-3 w-3 animate-spin" />{name || 'Syncing'}</span>
          if (data.binding.pending_action || (data.auto?.state && !['synced', 'idle'].includes(data.auto.state))) return <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400"><TriangleAlert className="h-3 w-3" />{name || 'On'}</span>
          return <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400"><CheckCircle2 className="h-3 w-3" />{name || 'On'}</span>
        })
        : api.get<LaptopSettings>(laptopSettingsEndpoint(undefined, profileId, projectId), options).then(({ data }) => data.device_id ? 'On' : 'Off')
      void request.then(value => { if (!abort.signal.aborted) setSummary(value) }).catch(() => {})
    }
    load()
    const timer = crewOnlineMode() ? window.setInterval(load, 4000) : undefined
    return () => { window.clearInterval(timer); abort.abort() }
  }, [enabled, projectId, profileId, connections])
  return summary
}

export function YourComputerPanel({ projectId, sessionId }: { projectId: string; sessionId?: string; canEdit: boolean }) {
  return <div className="max-w-xl">{crewOnlineMode() ? <OnlineCard projectId={projectId} /> : <LaptopCard sessionId={sessionId} />}</div>
}

function Switch({ on, disabled, label, onChange }: { on: boolean; disabled?: boolean; label: string; onChange: () => void }) {
  return <button type="button" role="switch" aria-checked={on} aria-label={label} disabled={disabled} onClick={onChange}
    className={`relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50 ${on ? 'bg-emerald-500' : 'bg-muted-foreground/30'}`}>
    <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all ${on ? 'left-[22px]' : 'left-0.5'}`} />
  </button>
}

function OnlineCard({ projectId }: { projectId: string }) {
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [choice, setChoice] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId, reload])
  const binding = status?.binding
  const network = networkName(binding, connections)
  const target = binding?.connection_id || choice || (connections.length === 1 ? connections[0].id : '')
  const run = async (action: 'update' | 'server' | 'local' | 'download') => {
    setBusy(true); setError('')
    try { await desktopRequest(`/${encodeURIComponent(target)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action }) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It did not finish. Your work is kept.') }
    finally { setBusy(false); setReload(value => value + 1) }
  }
  const problem = error || (status?.auto?.state && !['syncing', 'synced', 'idle'].includes(status.auto.state) ? status.auto.message || '' : '')
  const syncing = busy || status?.auto?.state === 'syncing'
  return <section aria-label="Online" className="space-y-3">
    <div className="flex items-center gap-3">
      <div className="min-w-0 flex-1">
        <p role="status" className="flex items-center gap-2 text-sm font-semibold">
          {syncing ? <RefreshCw aria-label="Syncing" className="h-4 w-4 animate-spin text-muted-foreground" /> : binding ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <CircleDashed className="h-4 w-4 text-muted-foreground" />}
          {!status ? 'Checking…' : binding ? (network ? `Online on ${network}` : 'Online') : 'Only on this Mac'}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">{binding ? 'Use it from the website or your phone. It works here or on the network, with this project’s folder and terminal on this Mac.' : 'Turn on to use this Crew from anywhere.'}</p>
      </div>
      {/* Turning a Crew offline arrives with the server's stop-syncing step; until then the switch only turns it on. */}
      <Switch on={Boolean(binding)} disabled={!status || busy || Boolean(binding) || !target} label="Online" onChange={() => void run('update')} />
    </div>
    {!binding && connections.length > 1 && <select aria-label="Network" value={choice} onChange={event => setChoice(event.target.value)} className="w-full rounded-md border border-border bg-background p-2 text-sm">
      <option value="">Choose a network</option>
      {connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
    </select>}
        {binding?.pending_action && !busy && <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last update was interrupted.</span><Button size="sm" variant="outline" onClick={() => void run(binding.pending_action!)}>Finish</Button></div>}
    {problem && !binding?.pending_action && !syncing && <div className="space-y-1">
      <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last update didn’t finish. Your work is kept.</span>{binding && <Button size="sm" variant="outline" onClick={() => void run('update')}>Try again</Button>}</div>
      <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Details</summary><p className="mt-1 break-all">{problem}</p></details>
    </div>}
    {binding?.peer_server && <a className="inline-block text-xs underline" href={`${binding.peer_server}/crew/projects/${encodeURIComponent(binding.peer_project_id)}`} target="_blank" rel="noopener noreferrer">{network ? `Open on ${network}` : 'Open on the website'}</a>}
  </section>
}

function LaptopCard({ sessionId }: { sessionId?: string }) {
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const endpoint = laptopSettingsEndpoint(undefined, tab?.metadata?.agentProfileId, tab?.metadata?.agentProfileProjectId)
  const { devices } = useLocalFileDevices(Boolean(endpoint), tab?.metadata?.agentProfileWorkspace)
  const [settings, setSettings] = useState<LaptopSettings | null>(null)
  useEffect(() => {
    let active = true
    const load = () => { if (endpoint) void api.get<LaptopSettings>(endpoint, { skipSessionContext: true }).then(({ data }) => { if (active) setSettings(data) }).catch(() => {}) }
    load()
    window.addEventListener('crew-laptop-settings-changed', load)
    return () => { active = false; window.removeEventListener('crew-laptop-settings-changed', load) }
  }, [endpoint])
  const online = Boolean(settings?.device_id && devices.some(item => item.device_id === settings.device_id))
  const summary = !settings ? 'Checking…' : !settings.device_id ? 'Off' : `${settings.device_id} · ${online ? 'online' : 'offline'}`
  return <Card icon={<Laptop className="h-4 w-4 text-muted-foreground" />} title="Laptop access" status={summary} tone={!settings?.device_id ? 'off' : online ? 'ok' : 'warn'} details={<CodeLocalFilesSettings sessionId={sessionId} />}>
    {settings?.device_id && <ul className="space-y-1 text-xs">
      {settings.folders.length === 0 && <li className="text-muted-foreground">No folders shared</li>}
      {settings.folders.map(folder => <li key={folder.id || folder.path} className="flex items-center gap-2"><Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1 break-all">{folder.path}</span><span className="text-muted-foreground">{folder.access === 'write' ? 'Read & write' : 'Read'}</span></li>)}
      <li className="flex items-center gap-2"><Terminal className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span className="flex-1">Terminal commands</span><span className={settings.commands ? 'font-medium text-amber-600' : 'text-muted-foreground'}>{settings.commands ? 'On' : 'Off'}</span></li>
    </ul>}
  </Card>
}

function Card({ icon, title, status, tone, children, details }: { icon: ReactNode; title: string; status: string; tone: 'ok' | 'off' | 'warn'; children?: ReactNode; details: ReactNode }) {
  const mark = tone === 'ok' ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : tone === 'warn' ? <TriangleAlert className="h-4 w-4 text-amber-500" /> : <CircleDashed className="h-4 w-4 text-muted-foreground" />
  return <section aria-label={title} className="rounded-lg border border-border">
    <div className="space-y-2 p-4">
      <div className="flex items-center gap-2 text-sm font-semibold">{icon}{title}</div>
      <p role="status" className="flex items-center gap-2 text-sm">{mark}{status}</p>
      {children}
    </div>
    <details className="border-t border-border">
      <summary className="cursor-pointer px-4 py-2 text-xs text-muted-foreground">Change</summary>
      <div className="px-4 pb-4">{details}</div>
    </details>
  </section>
}
