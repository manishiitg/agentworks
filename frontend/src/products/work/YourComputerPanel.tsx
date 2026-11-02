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

type Binding = { archive_only?: boolean; mode?: 'continuous' | 'manual' | 'off' | 'relay'; connection_id?: string; peer_server?: string; peer_project_id: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' | 'local' | 'download' }
type SyncStatus = { binding: Binding | null; auto?: { state: string; message?: string } }
type LaptopSettings = { device_id: string; folders: { id: string; path: string; access: 'read' | 'write' }[]; commands: boolean }

/** In the AgentWorks app a Crew is either in the cloud (on a network) or only on this computer; on a server the page is its laptop access. */
export const crewCloudMode = () => hasDesktopConnections()
// Most people have one network, so its name is shown only when there are several.
const networkName = (binding: Binding | null | undefined, connections: { id: string; name: string }[]) =>
  connections.filter(item => item.id !== 'local').length > 1 ? connections.find(item => item.id === binding?.connection_id)?.name || '' : ''

export type CloudState = 'on' | 'syncing' | 'check' | 'off'
/** The menu row's state: on, syncing, check or off. The row's own icon shows it; the network name stays on the page. */
export function useYourComputerSummary(enabled: boolean, projectId?: string, profileId?: string): CloudState | undefined {
  const [state, setState] = useState<CloudState>()
  useEffect(() => {
    setState(undefined)
    if (!enabled || !projectId) return
    const abort = new AbortController(), options = { signal: abort.signal, skipSessionContext: true }
    const load = () => {
      const request: Promise<CloudState> = crewCloudMode() && getRawActiveWorkspaceId() !== 'local' ? Promise.resolve('on') : crewCloudMode()
        ? api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, options).then(({ data }) => {
          // A relay twin moves no files: the Crew is used from the phone while the Mac is on.
          if (!data.binding || data.binding.mode === 'off' || data.binding.mode === 'relay') return 'off'
          if (data.auto?.state === 'syncing') return 'syncing'
          if (data.binding.pending_action || (data.auto?.state && !['synced', 'idle'].includes(data.auto.state))) return 'check'
          return 'on'
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
export const cloudStateLabel: Record<CloudState, string> = { on: 'On', syncing: 'Syncing', check: 'Check', off: 'Off' }
/** The row icon in the state's colour: light green when on, pulsing while it syncs, amber to check, grey when off. */
export function cloudStateIcon(Icon: LucideIcon, state?: CloudState): LucideIcon {
  const tone = state === 'on' ? '!text-emerald-400/90' : state === 'syncing' ? '!text-emerald-400/90 animate-pulse' : state === 'check' ? '!text-amber-500' : ''
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
  // A Crew opened from a connected server is already in the cloud; the AgentWorks app offers to open it here.
  const connection = getRawActiveWorkspaceId()
  if (connection !== 'local') return <section aria-label="Cloud" className="space-y-3 text-sm">
    <p role="status" className="flex items-center gap-2 font-semibold"><CheckCircle2 className="h-4 w-4 text-emerald-500" />In the cloud</p>
    <ServerProjectLocalOpen projectId={projectId} connection={connection} />
  </section>
  return <div><CloudCard projectId={projectId} /></div>
}

function Switch({ on, disabled, label, onChange }: { on: boolean; disabled?: boolean; label: string; onChange: () => void }) {
  return <button type="button" role="switch" aria-checked={on} aria-label={label} disabled={disabled} onClick={onChange}
    className={`relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:opacity-50 ${on ? 'bg-emerald-500' : 'bg-muted-foreground/30'}`}>
    <span className={`absolute top-0.5 h-5 w-5 rounded-full bg-white transition-all ${on ? 'left-[22px]' : 'left-0.5'}`} />
  </button>
}

function CloudCard({ projectId }: { projectId: string }) {
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [choice, setChoice] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [laptop, setLaptop] = useState<{ state: string; message?: string }>()
  const [cloudUses, setCloudUses] = useState<{ apps: { name: string; status?: string }[]; secrets: string[]; accounts: { name: string; kind: string }[] }>()
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    const loadLaptop = () => void api.get<{ state: string; message?: string }>(`/api/crew-cloud-laptop/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setLaptop(data) }).catch(() => {})
    load(); loadLaptop()
    void api.get<NonNullable<typeof cloudUses>>(`/api/crew-cloud-apps/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setCloudUses(data) }).catch(() => {})
    const timer = window.setInterval(() => { load(); loadLaptop() }, 5000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId, reload])
  // A paired Crew turned off keeps its pair (both copies stay); the switch reads the server's saved mode.
  const paired = status?.binding
  const binding = paired && paired.mode !== 'off' && paired.mode !== 'relay' ? paired : null
  const relay = paired?.mode === 'relay'
  const network = networkName(binding, connections)
  const target = paired?.connection_id || choice || (connections.length === 1 ? connections[0].id : '')
  const run = async (action: 'update' | 'server' | 'local' | 'download' | 'mode' | 'relay', mode?: 'continuous' | 'off', via = target) => {
    setBusy(true); setError('')
    try { await desktopRequest(`/${encodeURIComponent(via)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', mode ? { action, mode } : { action }) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It did not finish. Your work is kept.') }
    finally { setBusy(false); setReload(value => value + 1) }
  }
  const problem = error || (status?.auto?.state && !['syncing', 'synced', 'idle'].includes(status.auto.state) ? status.auto.message || '' : '')
  const syncing = busy || status?.auto?.state === 'syncing'
  return <section aria-label="Cloud" className="space-y-3">
    {binding?.archive_only && <p className="text-sm text-muted-foreground">Your conversation is restored into a new AI session when you switch computers. Files finish syncing before the next reply.</p>}
    <div className="flex items-center gap-3">
      <div className="min-w-0 flex-1">
        <p role="status" className="flex items-center gap-2 text-sm font-semibold">
          {syncing ? <RefreshCw aria-label="Syncing" className="h-4 w-4 animate-spin text-muted-foreground" /> : binding ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <CircleDashed className="h-4 w-4 text-muted-foreground" />}
          {!status ? 'Checking…' : binding ? (network ? `In the cloud on ${network}` : 'In the cloud') : 'Only on this computer'}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">{binding ? 'Use this Crew from the website or your phone.' : relay ? 'You can use this Crew from the website or your phone while this Mac is on. Turn on to keep working when your Mac is off.' : connections.length > 1 ? 'Choose a server to use this Crew from the website or your phone.' : 'Turn on to use this Crew from the website or your phone.'}</p>
        {binding && <p className="mt-1 text-xs text-muted-foreground">{laptop?.state === 'existing' || laptop?.state === 'needs_app' ? laptop.message : 'While Cloud is on, people using this Crew run commands in this folder on your computer.'}</p>}
      </div>
      <Switch on={Boolean(binding)} disabled={!status || busy || !target} label="Cloud" onChange={() => void (binding ? run('mode', 'off') : paired ? run('mode', 'continuous') : run('update'))} />
    </div>
    {!paired && connections.length > 1 && <select aria-label="Network" value={choice} onChange={event => { setChoice(event.target.value); if (event.target.value) void run('relay', undefined, event.target.value) }} className="w-full rounded-md border border-border bg-background p-2 text-sm">
      <option value="">Choose a network</option>
      {connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
    </select>}
        {binding?.pending_action && !busy && <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last update was interrupted.</span><Button size="sm" variant="outline" onClick={() => void run(binding.pending_action!)}>Finish</Button></div>}
    {problem && !binding?.pending_action && !syncing && <div className="space-y-1">
      <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last update didn’t finish. Your work is kept.</span>{binding && <Button size="sm" variant="outline" onClick={() => void run('update')}>Try again</Button>}</div>
      <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Details</summary><p className="mt-1 break-all">{problem}</p></details>
    </div>}
    {binding && cloudUses && (cloudUses.apps.length + cloudUses.secrets.length + cloudUses.accounts.length > 0) && <div className="space-y-1 border-t border-border pt-3 text-xs">
      <p className="font-medium">In the cloud this Crew uses</p>
      {cloudUses.apps.length > 0 && <p><span className="text-muted-foreground">Apps: </span>{cloudUses.apps.map(app => app.name).join(', ')}</p>}
      {cloudUses.secrets.length > 0 && <p><span className="text-muted-foreground">Secrets: </span>{cloudUses.secrets.join(', ')}</p>}
      {cloudUses.accounts.length > 0 && <p><span className="text-muted-foreground">Accounts: </span>{cloudUses.accounts.map(account => `${account.kind === 'google' ? 'Google' : 'Slack'} · ${account.name}`).join(', ')}</p>}
      <p className="text-muted-foreground">They run on the server with its sign-ins. Secret values stay there.</p>
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
