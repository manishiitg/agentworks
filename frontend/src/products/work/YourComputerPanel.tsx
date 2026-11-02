import { useEffect, useState, type ReactNode } from 'react'
import { CheckCircle2, CircleDashed, Folder, Laptop, RefreshCw, Terminal, TriangleAlert } from 'lucide-react'
import api from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { CrewSyncPanel } from './CrewSyncPanel'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { useLocalFileDevices } from './codeLocalFiles'

type SyncStatus = { binding: { connection_id?: string; peer_server?: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: string } | null; auto?: { state: string; message?: string } }
type LaptopSettings = { device_id: string; folders: { id: string; path: string; access: 'read' | 'write' }[]; commands: boolean }

/** Two plain answers first: is this Crew synced with a server, and what can it reach on your computer. Details fold away. */
export function YourComputerPanel({ projectId, sessionId, canEdit }: { projectId: string; sessionId?: string; canEdit: boolean }) {
  return <div className="max-w-xl space-y-4">
    {canEdit && <SyncCard projectId={projectId} />}
    <LaptopCard sessionId={sessionId} />
  </div>
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

function SyncCard({ projectId }: { projectId: string }) {
  const connections = useConnectedWorkStore(state => state.connections)
  const [status, setStatus] = useState<SyncStatus | null>(null)
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId])
  const binding = status?.binding
  const server = connections.find(item => item.id === binding?.connection_id)?.name || (binding?.peer_server ? new URL(binding.peer_server).hostname : 'a server')
  const problem = status?.auto?.state && !['syncing', 'synced', 'idle'].includes(status.auto.state) ? status.auto.message : ''
  const summary = !status ? 'Checking…' : !binding ? 'Only on this computer' : binding.pending_action ? 'A sync was interrupted. Open Change to finish it.' : problem ? `Synced with ${server}, but the last sync needs attention` : status.auto?.state === 'syncing' ? `Syncing with ${server}…` : `Synced with ${server}`
  return <Card icon={<RefreshCw className="h-4 w-4 text-muted-foreground" />} title="Sync with a server" status={summary} tone={!binding ? 'off' : problem || binding.pending_action ? 'warn' : 'ok'} details={<CrewSyncPanel projectId={projectId} />}>
    {binding?.updated_at && <p className="text-xs text-muted-foreground">Last synced {new Date(binding.updated_at).toLocaleString()} · working on {binding.editing === 'local' ? 'this Mac' : 'the server'}</p>}
    {!binding && status && <p className="text-xs text-muted-foreground">Open Change to keep this Crew on a server too.</p>}
  </Card>
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
