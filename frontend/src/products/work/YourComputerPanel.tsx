import { forwardRef, useEffect, useState, type ReactNode } from 'react'
import { CheckCircle2, CircleDashed, Folder, Laptop, RefreshCw, Terminal, TriangleAlert, type LucideIcon, type LucideProps } from 'lucide-react'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { Button } from '../../components/ui/Button'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { ServerProjectLocalOpen } from './ServerProjectLocalOpen'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useLocalFileDevices } from './codeLocalFiles'

type Binding = { connection_id?: string; peer_server?: string; peer_project_id: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' | 'local' | 'download' }
type SyncStatus = { binding: Binding | null }
type Readiness = { fits: boolean; estimated_seconds: number; secrets?: number; measurement: { dependency_files?: string[]; files: number; bytes: number; max_files: number; max_bytes: number; max_file_bytes: number; largest_folders?: { path: string; bytes: number }[]; files_over_limit?: string[] } }
const sizeLabel = (bytes: number) => bytes >= 1 << 30 ? `${(bytes / (1 << 30)).toFixed(1)} GB` : bytes >= 1 << 20 ? `${Math.round(bytes / (1 << 20))} MB` : 'under 1 MB'
type LaptopSettings = { device_id: string; folders: { id: string; path: string; access: 'read' | 'write' }[]; commands: boolean }

/** In the AgentWorks app a Crew lives on this Mac or online (owner, 3 Nov); on a server the page is its laptop access. */
export const crewCloudMode = () => hasDesktopConnections()

// Laptop access (a Crew on a server) keeps its on/off.
export type CloudState = 'mac' | 'online' | 'moving' | 'check' | 'on' | 'off'
/** The menu row's state. The server decides where the Crew lives (its pair's editing side); the row only shows it. */
export function useYourComputerSummary(enabled: boolean, projectId?: string, profileId?: string): CloudState | undefined {
  const [state, setState] = useState<CloudState>()
  useEffect(() => {
    setState(undefined)
    if (!enabled || !projectId) return
    const abort = new AbortController(), options = { signal: abort.signal, skipSessionContext: true }
    const load = () => {
      const request: Promise<CloudState> = crewCloudMode() && getRawActiveWorkspaceId() !== 'local' ? Promise.resolve('online') : crewCloudMode()
        ? api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, options).then(({ data }) => {
          if (!data.binding) return 'mac'
          if (data.binding.pending_action === 'server' || data.binding.pending_action === 'local') return 'check'
          return data.binding.editing === 'server' ? 'online' : 'mac'
        })
        : api.get<LaptopSettings>(laptopSettingsEndpoint(undefined, profileId, projectId), options).then(({ data }) => data.device_id ? 'on' : 'off')
      void request.then(value => { if (!abort.signal.aborted) setState(value) }).catch(() => {})
    }
    load()
    const timer = crewCloudMode() ? window.setInterval(load, 4000) : undefined
    return () => { window.clearInterval(timer); abort.abort() }
  }, [enabled, projectId, profileId])
  return state
}
export const cloudStateLabel: Record<CloudState, string> = { mac: 'On this Mac', online: 'Online', moving: 'Moving…', check: 'Check', on: 'On', off: 'Off' }
/** The row icon in the state's colour: green online, pulsing while it moves, amber to check, plain on this Mac. */
export function cloudStateIcon(Icon: LucideIcon, state?: CloudState): LucideIcon {
  const tone = state === 'online' || state === 'on' ? '!text-emerald-400/90' : state === 'moving' ? '!text-emerald-400/90 animate-pulse' : state === 'check' ? '!text-amber-500' : ''
  if (!tone) return Icon
  // One component per icon and state, so a re-render does not restart the pulse.
  const key = `${Icon.displayName || 'icon'}:${state}`
  let Tinted = tintedIcons.get(key)
  if (!Tinted) {
    Tinted = forwardRef<SVGSVGElement, LucideProps>(({ className, ...props }, ref) => <Icon ref={ref} {...props} className={`${className || ''} ${tone}`} />) as LucideIcon
    Tinted.displayName = 'CloudStateIcon'
    tintedIcons.set(key, Tinted)
  }
  return Tinted
}
const tintedIcons = new Map<string, LucideIcon>()

export function YourComputerPanel({ projectId, sessionId }: { projectId: string; sessionId?: string; canEdit: boolean }) {
  if (!crewCloudMode()) return <div><LaptopCard sessionId={sessionId} /></div>
  // A Crew opened from a connected server already lives online; the AgentWorks app offers to open it here.
  const connection = getRawActiveWorkspaceId()
  if (connection !== 'local') return <section aria-label="Where it lives" className="space-y-3 text-sm">
    <p role="status" className="flex items-center gap-2 font-semibold"><CheckCircle2 className="h-4 w-4 text-emerald-500" />Online</p>
    <ServerProjectLocalOpen projectId={projectId} connection={connection} />
  </section>
  return <div><WhereItLivesCard projectId={projectId} /></div>
}

function WhereItLivesCard({ projectId }: { projectId: string }) {
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [choice, setChoice] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [sharing, setSharing] = useState(false)
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId, reload])
  const binding = status?.binding
  const online = binding?.editing === 'server'
  const unfinished = binding?.pending_action === 'server' || binding?.pending_action === 'local' ? binding.pending_action : undefined
  // Before moving online, the Mac says whether this Crew fits (the server decides the numbers).
  const [readiness, setReadiness] = useState<Readiness>()
  useEffect(() => {
    if (online) return
    const abort = new AbortController()
    void api.get<Readiness>(`/api/crew-sync/${encodeURIComponent(projectId)}/readiness`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setReadiness(data) }).catch(() => {})
    return () => abort.abort()
  }, [projectId, online])
  const tooBig = !online && readiness?.fits === false
  const target = binding?.connection_id || choice || (connections.length === 1 ? connections[0].id : '')
  const link = binding?.peer_server ? `${binding.peer_server}/crew/projects/${encodeURIComponent(binding.peer_project_id)}` : ''
  const run = async (action: 'server' | 'local' | 'relay', via = target) => {
    setBusy(true); setError('')
    try { await desktopRequest(`/${encodeURIComponent(via)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action }); return true }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It did not finish. Your work is kept.'); return false }
    finally { setBusy(false); setReload(value => value + 1) }
  }
  const moveAndShare = async () => {
    setSharing(false)
    if (await run('server') && link) window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')
  }
  return <div className="space-y-5">
    <section aria-label="Where it lives" className="space-y-3">
      <h3 className="text-sm font-semibold">Where it lives</h3>
      <p role="status" className="flex items-center gap-2 text-sm font-medium">
        {busy ? <RefreshCw aria-label="Moving" className="h-4 w-4 animate-spin text-muted-foreground" /> : online ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <Laptop className="h-4 w-4 text-muted-foreground" />}
        {!status ? 'Checking…' : busy ? 'Moving…' : online ? 'Online' : 'On this Mac'}
      </p>
      <p className="text-xs text-muted-foreground">{online ? 'It works even when your Mac is off.' : 'Your files stay on this Mac. You can use it from anywhere while the Mac is on.'}</p>
      {!binding && connections.length > 1 && <select aria-label="Network" value={choice} onChange={event => { setChoice(event.target.value); if (event.target.value) void run('relay', event.target.value) }} className="w-full rounded-md border border-border bg-background p-2 text-sm">
        <option value="">Choose a network</option>
        {connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select>}
      {!online && readiness && <div className={`space-y-1 text-xs ${tooBig ? 'text-amber-600' : 'text-muted-foreground'}`}>
        <p>Size {sizeLabel(readiness.measurement.bytes)} of {sizeLabel(readiness.measurement.max_bytes)} · {readiness.measurement.files.toLocaleString()} of {readiness.measurement.max_files.toLocaleString()} files</p>
        {tooBig
          ? <>
            <p className="font-medium">Too big to move online. Move large folders out of this Crew, or add them to .gitignore.</p>
            {(readiness.measurement.largest_folders?.length ?? 0) > 0 && <p>Biggest folders: {readiness.measurement.largest_folders!.map(folder => `${folder.path} (${sizeLabel(folder.bytes)})`).join(', ')}</p>}
            {(readiness.measurement.files_over_limit?.length ?? 0) > 0 && <p>Files over {sizeLabel(readiness.measurement.max_file_bytes)}: {readiness.measurement.files_over_limit!.join(', ')}</p>}
          </>
          : <>
            <p>Moving takes about {Math.max(1, Math.round(readiness.estimated_seconds / 60))} {Math.round(readiness.estimated_seconds / 60) > 1 ? 'minutes' : 'minute'}. Your files, including .env and keys{(readiness.secrets ?? 0) > 0 ? `, and ${readiness.secrets} selected ${readiness.secrets === 1 ? 'secret' : 'secrets'}` : ''}, go with it.</p>
            {(readiness.measurement.dependency_files?.length ?? 0) > 0 && <p>Packages are installed again online ({readiness.measurement.dependency_files!.join(', ')}).</p>}
            <p>Programs and tools only on your Mac don't go with it.</p>
          </>}
      </div>}
      {unfinished && !busy
        ? <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last move didn't finish. Your work is kept.</span><Button size="sm" variant="outline" onClick={() => void run(unfinished)}>Finish</Button></div>
        : binding && <Button size="sm" variant="outline" disabled={busy || (!online && tooBig)} onClick={() => void run(online ? 'local' : 'server')}>{online ? 'Move back to this Mac' : 'Move online'}</Button>}
      {error && <div className="space-y-1">
        <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">It didn't finish. Your work is kept.</span></div>
        <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Details</summary><p className="mt-1 break-all">{error}</p></details>
      </div>}
    </section>
    {binding && <section aria-label="Who can use it" className="space-y-3 border-t border-border pt-4 text-sm">
      <h3 className="font-semibold">Who can use it</h3>
      <div className="space-y-1">
        <p className="flex items-center gap-2 font-medium"><CheckCircle2 className="h-4 w-4 text-emerald-500" />You</p>
        <p className="text-xs text-muted-foreground">{online ? 'From anywhere.' : 'From anywhere while your Mac is on.'}</p>
        {link && <p className="text-xs">Website and phone: <a className="underline" href={link} target="_blank" rel="noopener noreferrer">{link.replace(/^https?:\/\//, '')}</a> ✓ Ready</p>}
      </div>
      <div className="space-y-1">
        <p className="font-medium">People</p>
        {online
          ? <><p className="text-xs text-muted-foreground">Choose who can use it on its page online.</p>{link && <Button size="sm" variant="outline" onClick={() => window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')}>Share…</Button>}</>
          : sharing
            ? <div className="space-y-2 rounded-md border border-border p-3 text-xs">
              <p>So people can use it any time, even when your Mac is off, it moves online first. Nothing runs on your Mac for them.</p>
              <div className="flex gap-2"><Button size="sm" disabled={busy || tooBig} onClick={() => void moveAndShare()}>Move and share</Button><Button size="sm" variant="outline" onClick={() => setSharing(false)}>Cancel</Button></div>
            </div>
            : <><p className="text-xs text-muted-foreground">Nobody else yet.</p><Button size="sm" variant="outline" disabled={busy} onClick={() => setSharing(true)}>Share…</Button></>}
      </div>
    </section>}
  </div>
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
