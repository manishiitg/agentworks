import { forwardRef, useEffect, useState, type ReactNode } from 'react'
import { CheckCircle2, CircleDashed, Cloud, Folder, Laptop, Terminal, TriangleAlert, Users, type LucideIcon, type LucideProps } from 'lucide-react'
import api, { getAuthToken, getDesktopApiBaseUrl, setSlackSetupConnection } from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { Button } from '../../components/ui/Button'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { Switch } from '../../components/ui/Switch'
import { HostedAiAppGuide } from '../../components/integrations/HostedAiAppGuide'
import { ProductSlackPanel } from '../../components/integrations/ProductSlackPanel'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useLocalFileDevices } from './codeLocalFiles'
import { CrewTeamTab } from '../../components/workspace/CrewTeamTab'
export { useMacTabShareLine, useMacSharedAgents, openCrewAccess, takeCrewAccess, openCrewAskThread } from './MacSharedAgents'
export type { MacAgentShare } from './MacSharedAgents'
import { useProjectTeam } from './teamApi'
import { CrewCodingApps } from './CrewCodingApps'
import { CrewPublishAgent, type AgentPlacement } from './CrewPublishAgent'
import { hasPendingCrewAccess } from './MacSharedAgents'
import { schedulerApi } from '../../api/scheduler'
import { useTeamCopyStatus } from './TeamWhenMacOff'

type Binding = { connection_id?: string; peer_server?: string; peer_project_id: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' }
// Move online reports real progress (PLAT-856-509): bytes sent so far, whether the Mac's schedules wait for the move,
// and, when it finished, where the Mac folder is kept.
type MoveReport = { phase?: MovePhase; place?: CloudState; bytes_done?: number; bytes_total?: number; schedules_paused?: boolean; message?: string; backup_path?: string; local_folder_kept?: string }
// What of this Crew the server keeps (owner, 4 Nov "Where your data is"): the server lists it by name, e.g. an agent's
// always-online copy or a team's shared folder; empty while everything stays on this Mac.
type ServerCopy = { name: string; detail: string }
type SyncStatus = MoveReport & { binding: Binding | null; on_server?: ServerCopy[]; shared_agents?: number; placements?: Record<string, AgentPlacement> }
// The move's own steps, as the server reports them (astra, PLAT-856-509).
type MovePhase = 'files' | 'database' | 'chats' | 'schedules_watchers' | 'dashboards_functions' | 'activating' | 'done'
const MOVE_PHASES: MovePhase[] = ['files', 'database', 'chats', 'schedules_watchers', 'dashboards_functions', 'activating', 'done']
type Readiness = { fits: boolean; estimated_seconds: number; secrets?: number; measurement: { dependency_files?: string[]; files: number; bytes: number; max_files: number; max_bytes: number; max_file_bytes: number; largest_folders?: { path: string; bytes: number }[]; files_over_limit?: string[] } }
const sizeLabel = (bytes: number) => bytes >= 1 << 30 ? `${(bytes / (1 << 30)).toFixed(1)} GB` : bytes >= 1 << 20 ? `${Math.round(bytes / (1 << 20))} MB` : 'under 1 MB'
type LaptopSettings = { device_id: string; folders: { id: string; path: string; access: 'read' | 'write' }[]; commands: boolean }

/** In the AgentWorks app a Crew lives on this Mac or online (owner, 3 Nov); on a server the page is its laptop access. */
export const crewCloudMode = () => hasDesktopConnections()

export type CloudState = 'mac' | 'mac_off' | 'moving' | 'online' | 'offline'

// One reading per Crew for the whole page (owner, 3 Nov: the page takes ONE flag): every panel that asks where a
// Crew lives shares this cache and its single poll, so the header, tabs, menu and panels always agree.
/** Someone else's ask to a shared agent on this Mac, from the Mac's own status (the last ten minutes). */
export type CrewAsk = { tab: string; agent: string; sender: string; conversation_key: string; session_id: string; started_at: string; active: boolean }
type PlaceEntry = { place?: CloudState; shared?: number; asking?: CrewAsk[]; listeners: Set<(place?: CloudState) => void>; timer?: number; abort?: AbortController; load?: () => void }
const placeCache = new Map<string, PlaceEntry>()

function watchCrewPlace(projectId: string, listener: (place?: CloudState) => void) {
  let entry = placeCache.get(projectId)
  if (!entry) {
    const created: PlaceEntry = { listeners: new Set() }
    entry = created
    placeCache.set(projectId, created)
    const load = () => {
      created.abort?.abort()
      created.abort = new AbortController()
      void api.get<{ place?: CloudState; shared_agents?: number; asking?: CrewAsk[] }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: created.abort.signal, skipSessionContext: true })
        .then(({ data }) => {
          const shared = data.shared_agents || 0
          const asking = data.asking || []
          if (!data.place || (data.place === created.place && shared === created.shared && JSON.stringify(asking) === JSON.stringify(created.asking || []))) return
          created.place = data.place
          created.shared = shared
          created.asking = asking
          created.listeners.forEach(notify => notify(data.place))
        }).catch(() => {})
    }
    created.load = load
    load()
    created.timer = window.setInterval(load, 5000)
  }
  entry.listeners.add(listener)
  listener(entry.place)
  const watched = entry
  return () => {
    watched.listeners.delete(listener)
    if (watched.listeners.size > 0) return
    window.clearInterval(watched.timer); watched.abort?.abort()
    placeCache.delete(projectId)
  }
}

/** Reads a Crew's place again at once, after an action changed it (Reach on/off, Move online), so every panel agrees. */
export function refreshCrewPlace(projectId: string) { placeCache.get(projectId)?.load?.() }

/** Where the Crew lives, as the server reports it (crew-sync status "place"); the page only shows it. */
export function useYourComputerSummary(enabled: boolean, projectId?: string): CloudState | undefined {
  const [state, setState] = useState<CloudState>()
  useEffect(() => {
    setState(undefined)
    if (!enabled || !projectId) return
    return watchCrewPlace(projectId, setState)
  }, [enabled, projectId])
  return state
}

/** How many of this Crew's agents others can message, from the same reading as its place (PLAT-856-509). */
export function useCrewSharedAgentCount(projectId?: string): number {
  const [count, setCount] = useState(0)
  useEffect(() => {
    if (!projectId) return
    return watchCrewPlace(projectId, () => setCount(placeCache.get(projectId)?.shared || 0))
  }, [projectId])
  return count
}

/** Who asked this Crew's shared agents on this Mac lately, newest first (owner, 3 Nov). */
export function useCrewAsking(projectId?: string): CrewAsk[] {
  const [asking, setAsking] = useState<CrewAsk[]>([])
  useEffect(() => {
    if (!projectId) return
    return watchCrewPlace(projectId, () => setAsking(placeCache.get(projectId)?.asking || []))
  }, [projectId])
  return asking
}

/** The page's one flag: this Crew lives on its owner's Mac and this screen is not that Mac (the website or phone),
 * so its online record is empty. Every panel decides from this, never from its own data. */
export function useCrewLivesOnMac(projectId?: string): boolean {
  const place = useYourComputerSummary(Boolean(projectId), projectId)
  // The Mac app's own local Crews are the Mac itself; the same Crew opened through a connected server is its online copy.
  return (place === 'mac' || place === 'mac_off') && !(crewCloudMode() && getRawActiveWorkspaceId() === 'local')
}

export const cloudStateLabel: Record<CloudState, string> = { mac: 'On this Mac', mac_off: 'Mac off', moving: 'Publishing…', online: 'Online', offline: 'On this Mac · Offline' }
/** The Access row icon: a laptop while it lives on a Mac, a cloud online, people for someone else's Crew, with a dot:
 * green reachable now, grey Mac off, amber moving. */
export function cloudStateIcon(state?: CloudState, theirs = false): LucideIcon {
  const Icon = theirs ? Users : state === 'online' ? Cloud : Laptop
  const dot = !state || state === 'offline' ? '' : state === 'mac_off' ? 'bg-muted-foreground' : state === 'moving' ? 'bg-amber-500' : 'bg-emerald-500'
  const key = `${theirs}:${state}`
  let Marked = tintedIcons.get(key)
  if (!Marked) {
    Marked = forwardRef<SVGSVGElement, LucideProps>(({ className, ...props }, ref) => <span className="relative inline-flex shrink-0"><Icon ref={ref} {...props} className={className} />{dot && <span aria-hidden="true" className={`absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full ring-1 ring-background ${dot}`} />}</span>) as unknown as LucideIcon
    Marked.displayName = 'AccessIcon'
    tintedIcons.set(key, Marked)
  }
  return Marked
}
const tintedIcons = new Map<string, LucideIcon>()

export function YourComputerPanel({ projectId, sessionId, onFullScreen }: { projectId: string; sessionId?: string; canEdit: boolean; onFullScreen?: (full: boolean) => void }) {
  if (!crewCloudMode()) return <div><LaptopCard sessionId={sessionId} /></div>
  // A Crew opened from a connected server lives online.
  if (getRawActiveWorkspaceId() !== 'local') return <section aria-label="Where it lives" className="space-y-3 text-sm">
    <p role="status" className="flex items-center gap-2 font-semibold"><CheckCircle2 className="h-4 w-4 text-emerald-500" />Online</p>
  </section>
  return <div><WhereItLivesCard projectId={projectId} sessionId={sessionId} onFullScreen={onFullScreen} /></div>
}

function WhereItLivesCard({ projectId, sessionId, onFullScreen }: { projectId: string; sessionId?: string; onFullScreen?: (full: boolean) => void }) {
  const team = useProjectTeam(projectId)
  const [section, setSection] = useState<'mac' | 'publish' | 'team'>(() => hasPendingCrewAccess(projectId) ? 'team' : 'mac')
  const [publishConnection, setPublishConnection] = useState('')
  useEffect(() => {
    const openTeam = (event: Event) => { if ((event as CustomEvent<{ projectId: string }>).detail?.projectId === projectId) setSection('team') }
    window.addEventListener('agentworks:open-crew-access', openTeam)
    return () => window.removeEventListener('agentworks:open-crew-access', openTeam)
  }, [projectId])
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const name = tab?.metadata?.agentProfileProjectTitle || 'this'
  const workspace = tab?.metadata?.agentProfileWorkspace
  const [agentCount, setAgentCount] = useState<number>()
  const [scheduleCount, setScheduleCount] = useState<number>()
  useEffect(() => {
    setAgentCount(undefined); setScheduleCount(undefined)
  }, [projectId])
  useEffect(() => {
    if (section !== 'publish' || !workspace) return
    let active = true
    void schedulerApi.listJobs({ entity_type: 'product', limit: 1000 }).then(data => {
      if (active && data.total <= data.jobs.length) setScheduleCount(data.jobs.filter(job => job.workspace_path?.replace(/\/+$/, '') === workspace.replace(/\/+$/, '')).length)
    }).catch(() => {})
    return () => { active = false }
  }, [section, workspace])
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [busyAction, setBusyAction] = useState('')
  const [moved, setMoved] = useState<MoveReport | null>(null)
  const [reaching, setReaching] = useState(false)
  const [asking, setAsking] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [open, setOpen] = useState<'' | 'code' | 'ai' | 'move' | 'slack'>('')
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId, reload])
  const binding = status?.binding
  const online = binding?.editing === 'server'
  const moving = busyAction === 'server' || binding?.pending_action === 'server' || status?.place === 'moving'
  // Before moving online, the Mac says whether this Crew fits (the server decides the numbers).
  const [readiness, setReadiness] = useState<Readiness>()
  useEffect(() => {
    if (online) return
    const abort = new AbortController()
    void api.get<Readiness>(`/api/crew-sync/${encodeURIComponent(projectId)}/readiness`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setReadiness(data) }).catch(() => {})
    return () => abort.abort()
  }, [projectId, online])
  const tooBig = !online && readiness?.fits === false
  const target = binding?.connection_id || publishConnection || (connections.length === 1 ? connections[0].id : '')
  // A network is named by its server's address, such as agents.example.com.
  const networkName = (id?: string) => { const item = connections.find(c => c.id === id); try { return item ? new URL(item.server).host : '' } catch { return item?.name || '' } }
  const reach = (via: string) => { setAsking(false); setReaching(true); void run('relay', via).finally(() => setReaching(false)) }
  const link = binding?.peer_server ? `${binding.peer_server}/crew/projects/${encodeURIComponent(binding.peer_project_id)}` : ''
  const run = async (action: 'server' | 'relay' | 'offline', via = target) => {
    setBusy(true); setBusyAction(action); setError('')
    try {
      const reply = await desktopRequest<MoveReport>(`/${encodeURIComponent(via)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action })
      if (action === 'server' && reply?.place === 'online') setMoved(reply)
      return true
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It did not finish. Your work is kept.'); return false }
    finally { setBusy(false); setBusyAction(''); setReload(value => value + 1); refreshCrewPlace(projectId) }
  }
  const move = async (share: boolean) => {
    setOpen('')
    if (await run('server') && share && link) window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')
  }
  const minutes = readiness ? Math.max(1, Math.round(readiness.estimated_seconds / 60)) : 0
  const facts = readiness && !tooBig ? { size: sizeLabel(readiness.measurement.bytes), time: `about ${minutes} ${minutes > 1 ? 'minutes' : 'minute'}` } : undefined
  const way = (title: string, line: string, action: ReactNode) => <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2 border-t border-border py-2.5 pl-3 text-sm">
    <span>{title}</span>{line && <span className="text-[12.5px] text-muted-foreground">{line}</span>}
    <div className="ml-auto flex flex-wrap items-center gap-1.5">{action}</div>
  </div>
  // Move online takes the whole page (owner, 3 Nov mockup): the step, then its progress, then done.
  const [doneSeen, setDoneSeen] = useState(false)
  const done = online && Boolean(moved) && !doneSeen
  const step: MoveStep | '' = open === 'move' && !online ? 'confirm' : moving ? 'moving' : done ? 'done' : ''
  useEffect(() => { onFullScreen?.(step !== ''); return () => onFullScreen?.(false) }, [step, onFullScreen])
  if (step) return <PublishCrewScreen step={step} name={name} facts={facts} tooBig={tooBig} status={status} backup={moved?.backup_path || status?.backup_path} error={error}
    onCancel={() => setOpen('')} onMove={() => void move(false)} onOpen={() => { setDoneSeen(true); if (link) window.open(link, '_blank', 'noopener,noreferrer') }} />
  const network = networkName(target) || 'your server'
  const tabs = [
    { id: 'mac', icon: '💻', label: 'Mac online', detail: 'you, from anywhere', tone: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' },
    { id: 'publish', icon: '☁️', label: 'Publish', detail: 'agents on the server', tone: 'bg-sky-500/10 text-sky-700 dark:text-sky-300' },
    { id: 'team', icon: '👥', label: 'Team', detail: 'work with people', tone: 'bg-violet-500/10 text-violet-700 dark:text-violet-300' },
  ] as const
  const serverCopies = [...(status?.on_server || []), ...(team.team?.server_data ? [{ name: `${team.team.server_data.name}/ (shared folder)`, detail: 'kept on the server so your team can sync it' }] : [])]
  return <div className="space-y-3.5 text-sm">
    <div role="tablist" aria-label="Crew access" className="grid grid-cols-3 gap-1 rounded-xl border border-border bg-muted/30 p-1">
      {tabs.map((item, index) => <button key={item.id} type="button" role="tab" id={`crew-access-${projectId}-${item.id}`} aria-controls={`crew-access-panel-${projectId}-${item.id}`} aria-selected={section === item.id} tabIndex={section === item.id ? 0 : -1}
        onClick={() => setSection(item.id)} onKeyDown={event => {
          const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1
          if (next < 0) return
          event.preventDefault(); setSection(tabs[next].id); document.getElementById(`crew-access-${projectId}-${tabs[next].id}`)?.focus()
        }} className={`min-w-0 rounded-lg px-2 py-2 text-center ${section === item.id ? item.tone : 'text-muted-foreground hover:bg-muted'}`}>
        <span className="block font-semibold"><span aria-hidden="true">{item.icon}</span> {item.label}</span><span className="mt-0.5 block text-[11px]">{item.detail}</span>
      </button>)}
    </div>
    {section === 'mac' && <section role="tabpanel" id={`crew-access-panel-${projectId}-mac`} aria-labelledby={`crew-access-${projectId}-mac`} className="space-y-3">
      {binding?.connection_id && !online
        ? <MacOnlineDataLine key={`${projectId}:${binding.connection_id}`} projectId={projectId} connection={binding.connection_id} network={network} copies={serverCopies} />
        : <MacDataLine>{online ? `This Crew now runs on ${network}. Open it on the Publish tab.` : !status ? 'Checking where this Crew’s data is kept…' : allOnMac(network)}</MacDataLine>}
      <div className="px-0.5">
        {status && !online && !moving && <label className="flex items-center justify-between gap-3 border-t border-border py-2.5">
          <span className="font-semibold">Use from phone and web</span>
          <Switch aria-label="Use from phone and web" checked={Boolean(binding) || reaching || asking} disabled={busy || !connections.length} onCheckedChange={on => { if (!on) { if (asking) setAsking(false); else void run('offline') } else if (target) reach(target); else setAsking(true) }} />
        </label>}
        {asking && !binding && <div role="group" aria-label="Which network?" className="space-y-1.5 border-t border-border py-3">
          <p className="text-xs text-muted-foreground">Which network?</p>
          {connections.map(item => <Button key={item.id} size="sm" variant="outline" className="w-full justify-start rounded-full px-3.5" onClick={() => reach(item.id)}>{networkName(item.id)}</Button>)}
        </div>}
        <div aria-disabled={!binding} className={binding ? '' : 'pointer-events-none opacity-50'}>
          {way('Website', 'any browser or phone', <>
            <Button className="h-7 rounded-full px-3" size="sm" variant="outline" disabled={!link} onClick={() => window.open(link, '_blank', 'noopener,noreferrer')}>Open</Button>
            <Button className="h-7 rounded-full px-3" size="sm" variant="outline" disabled={!link} aria-expanded={open === 'code'} aria-controls={`crew-phone-code-${projectId}`} onClick={() => setOpen(open === 'code' ? '' : 'code')}>{open === 'code' ? 'Hide QR' : 'QR for phone'}</Button>
          </>)}
          {open === 'code' && link && <div id={`crew-phone-code-${projectId}`} className="flex justify-center border-t border-border p-3"><LinkCode link={link} /></div>}
          {way('ChatGPT or Claude', `“ask my ${name} Crew to…”`, <Button className="h-7 rounded-full px-3" size="sm" variant="outline" disabled={!binding?.peer_server} onClick={() => setOpen(open === 'ai' ? '' : 'ai')}>{open === 'ai' ? 'Close' : 'Connect'}</Button>)}
          {open === 'ai' && binding?.peer_server && <div className="border-t border-border py-3"><ConnectAiApp endpoint={`${binding.peer_server}/api/external/v1/mcp`} name={name} onDone={() => setOpen('')} /></div>}
          {way('Slack DM', '', <Button className="h-7 rounded-full px-3" size="sm" variant="outline" disabled={!binding?.connection_id} onClick={() => setOpen(open === 'slack' ? '' : 'slack')}>{open === 'slack' ? 'Close' : 'Connect'}</Button>)}
          {open === 'slack' && binding?.connection_id && <div className="border-t border-border py-3"><MacCrewSlack connection={binding.connection_id} project={binding.peer_project_id} /></div>}
          <CrewCodingApps connection={binding?.connection_id} crewName={name} />
        </div>
        <p className="mt-1 text-[12.5px] text-muted-foreground">{online ? 'Open this published Crew from the Publish tab.' : 'Only you. Works while this Mac is on.'}</p>
        {!binding && !online && <p className="mt-1 text-xs text-muted-foreground">Turn on Use from phone and web to connect your other apps.</p>}
      </div>
    </section>}
    {section === 'publish' && <section role="tabpanel" id={`crew-access-panel-${projectId}-publish`} aria-labelledby={`crew-access-${projectId}-publish`} className="space-y-3">
      <p className="flex items-start gap-2 rounded-[10px] bg-sky-500/10 px-3 py-2 text-[13px] text-sky-800 dark:text-sky-200"><span aria-hidden="true">☁️</span><span>Publish runs it on {network} from now on, with your Mac on or off: chat, memory, skills, selected files, schedules and triggers go with it, using your AI login on the server. Publish the whole Crew, or just one agent.</span></p>
      {!connections.length && <p className="text-xs text-muted-foreground">Sign in to a server in Connections first.</p>}
      {!binding && connections.length > 1 && <label className="block space-y-1 text-xs">Server<select className="block w-full rounded-lg border border-border bg-background p-2 text-sm" value={publishConnection} onChange={event => setPublishConnection(event.target.value)}><option value="">Choose a server</option>{connections.map(item => <option key={item.id} value={item.id}>{networkName(item.id)}</option>)}</select></label>}
      <div className="flex flex-wrap items-center gap-2.5 rounded-xl border border-sky-500/25 p-3">
        <span aria-hidden="true" className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-md bg-muted text-xs">🧩</span>
        <div className="min-w-0 flex-1 basis-48"><p className="font-semibold">Whole Crew</p>
          <p className="text-[12.5px] text-muted-foreground">{online ? `Published on ${network}. Your Mac folder is kept.` : `All ${agentCount === undefined ? 'its agents' : `${agentCount} ${agentCount === 1 ? 'agent' : 'agents'}`}, the Crew folder${facts ? ` (${facts.size})` : ''} and ${scheduleCount === undefined ? 'its schedules' : `its ${scheduleCount} ${scheduleCount === 1 ? 'schedule' : 'schedules'}`}. The Mac folder is kept as a backup.`}</p>
          {!online && tooBig && <p className="text-xs text-amber-600">This Crew is too large to publish. Move large folders out first.</p>}
        </div>
        {online ? <Button className="ml-auto h-7 rounded-full px-3" size="sm" variant="outline" disabled={!link} onClick={() => window.open(link, '_blank', 'noopener,noreferrer')}>Open Crew</Button> : <Button className="ml-auto h-7 rounded-full px-3" size="sm" disabled={!status || !target || busy} onClick={() => setOpen('move')}>Publish Crew</Button>}
      </div>
      {!online && binding?.connection_id && <CrewPublishAgent key={projectId} projectId={projectId} connection={binding.connection_id} serverLink={link} serverName={network} reportedPlacements={status?.placements} onAgentCount={setAgentCount} />}
      <p className="text-[12.5px] text-muted-foreground">Use it for things that should keep running: a workflow, a daily report, a watcher.</p>
    </section>}
    {section === 'team' && <section role="tabpanel" id={`crew-access-panel-${projectId}-team`} aria-labelledby={`crew-access-${projectId}-team`} className="space-y-3">
      {binding && !online && binding.connection_id ? <CrewTeamTab projectId={projectId} connection={binding.connection_id} network={networkName(binding.connection_id)} /> : online ? <Button className="rounded-full" size="sm" variant="outline" disabled={!link} onClick={() => window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')}>Open Team on the server</Button> : <p className="text-[13px] text-muted-foreground">Connect this Crew to a server from <button type="button" className="text-primary underline" onClick={() => setSection('mac')}>Mac online</button> first.</p>}
    </section>}
    {error && <p role="alert" className="text-xs text-amber-600">{error}</p>}
  </div>
}

const allOnMac = (network: string) => `Everything is on this Mac. Use it from your phone, the web or your other apps while the Mac is on; your messages pass through ${network}, your files don't.`
function MacDataLine({ children }: { children: ReactNode }) {
  return <p className="flex items-start gap-2 rounded-[10px] bg-emerald-500/10 px-3 py-2 text-[13px] text-emerald-800 dark:text-emerald-200"><span aria-hidden="true">🔒</span><span>{children}</span></p>
}
function MacOnlineDataLine({ projectId, connection, network, copies }: { projectId: string; connection: string; network: string; copies: ServerCopy[] }) {
  const { status, error } = useTeamCopyStatus(projectId, connection)
  const names = copies.map(copy => copy.name)
  if (status?.ready) names.push('your shared agents’ instructions, skills, memory and chats for the team copy')
  return <MacDataLine>{names.length
    ? `Your Mac does the work while it is on. Messages pass through ${network}; the server also keeps ${names.join(', ')}. Your other files stay on this Mac.`
    : !status || error ? `Your Mac does the work while it is on; messages pass through ${network}. Files shared through Team or Publish are kept there; other files stay on this Mac.`
    : allOnMac(network)}</MacDataLine>
}

/** Slack for a Mac Crew, inside the app (owner, 3 Nov): the same setup as on its page online, on its online copy. */
function MacCrewSlack({ connection, project }: { connection: string; project: string }) {
  const [workspace, setWorkspace] = useState('')
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    setSlackSetupConnection(connection)
    const abort = new AbortController()
    void fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}/api/crew-sync/${encodeURIComponent(project)}`, { signal: abort.signal, headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
      .then(response => response.ok ? response.json() : Promise.reject(new Error(String(response.status))))
      .then((data: { workspace?: string }) => { if (data.workspace) setWorkspace(data.workspace); else setFailed(true) })
      .catch(() => { if (!abort.signal.aborted) setFailed(true) })
    return () => { abort.abort(); setSlackSetupConnection('') }
  }, [connection, project])
  if (failed) return <p className="text-xs text-muted-foreground">Slack setup could not be opened. Check that you are signed in, then try again.</p>
  if (!workspace) return <p role="status" className="text-xs text-muted-foreground">Opening Slack setup…</p>
  return <ProductSlackPanel workspacePath={workspace} noun="Crew" section="bot" />
}

/** The Move online step: size and time, what moves, then the owner's click. One-way (owner, 3 Nov). */
type MoveStep = 'confirm' | 'moving' | 'done'

/** Move online, as one full-page step (owner, 3 Nov mockup): what moves and what stays, size and time, then progress
 * from the move's own report, then done. One-way, no extra gates. */
function PublishCrewScreen({ step, name, facts, tooBig, status, backup, error, onCancel, onMove, onOpen }: { step: MoveStep; name: string; facts?: { size: string; time: string }; tooBig: boolean; status: SyncStatus | null; backup?: string; error: string; onCancel: () => void; onMove: () => void; onOpen: () => void }) {
  const box = (title: string, items: string[]) => <div className="flex-1 rounded-2xl border border-border p-3"><p className="mb-1.5 text-xs font-medium text-muted-foreground">{title}</p><ul className="space-y-1 text-[13px]">{items.map(item => <li key={item}>{item}</li>)}</ul></div>
  if (step === 'done') return <section aria-label="Crew published" className="flex flex-col items-center gap-3 py-10 text-center text-sm">
    <Cloud className="h-10 w-10 text-primary" aria-hidden="true" />
    <p className="text-base font-semibold">"{name}" is online</p>
    <p className="max-w-sm text-[13px] text-muted-foreground">It works even when your Mac is off.{backup ? ` This Mac keeps a backup copy in "${backup.split('/').pop()}".` : ''}</p>
    <Button className="rounded-full px-3.5" onClick={onOpen}>Open it</Button>
  </section>
  if (step === 'moving') {
    const total = status?.bytes_total || 0, done = status?.bytes_done || 0
    // Each shown step covers some of the move's phases; without a phase (an older server) the bytes decide the first.
    const at = status?.phase ? MOVE_PHASES.indexOf(status.phase) : total > 0 && done >= total ? 2 : 0
    const state = (first: number, last: number): 'done' | 'now' | 'next' => at > last ? 'done' : at >= first ? 'now' : 'next'
    const steps: [string, 'done' | 'now' | 'next'][] = [['Files and database', state(0, 1)], ['Chats and agents', state(2, 2)], ['Schedules, watchers, dashboards', state(3, 4)], ['Ready online', state(5, 6)]]
    return <section aria-label="Publishing Crew" className="space-y-4 py-4 text-sm">
      <p className="text-base font-semibold">Publishing "{name}"…</p>
      <p className="text-[13px] text-muted-foreground">If it's interrupted, it picks up where it left off.</p>
      <div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-all" style={{ width: `${total ? Math.min(100, Math.round(done * 100 / total)) : 5}%` }} /></div>
      {total > 0 && <p className="text-[13px] tabular-nums">{sizeLabel(done)} <span className="text-muted-foreground">of {sizeLabel(total)}</span></p>}
      <ul className="space-y-1 text-[13px]">{steps.map(([label, state]) => <li key={label} className={state === 'next' ? 'text-muted-foreground' : ''}>{state === 'done' ? '✓' : state === 'now' ? '●' : '○'} {label}</li>)}</ul>
      {status?.schedules_paused && <p className="text-[13px] text-muted-foreground">{status.message || 'Its schedules wait until the move finishes.'}</p>}
      {error && <p className="text-[13px] text-amber-600">It stopped. Your work is kept. <button type="button" className="underline" onClick={onMove}>Retry Publish Crew</button></p>}
    </section>
  }
  return <section aria-label={`Publish ${name}`} className="space-y-4 py-2 text-sm">
    <div><p className="text-base font-semibold">Publish "{name}"</p><p className="text-[13px] text-muted-foreground">It will live online from now on, and work even when your Mac is off.</p></div>
    <div className="flex items-center justify-center gap-4 py-2 text-center text-xs text-muted-foreground">
      <span className="flex flex-col items-center gap-1"><Laptop className="h-7 w-7 text-foreground" aria-hidden="true" />On this Mac<span>now</span></span>
      <span aria-hidden="true" className="text-lg">→</span>
      <span className="flex flex-col items-center gap-1"><Cloud className="h-7 w-7 text-primary" aria-hidden="true" />Online<span>after the move</span></span>
    </div>
    {tooBig ? <p className="text-[13px] text-amber-600">It's too big to move online. Move large folders out of this Crew first.</p> : <>
      <div className="flex flex-col gap-2 sm:flex-row">{box('Moves', ['Files and its database', 'All its chats and agents', 'Schedules and watchers', 'Dashboards and functions'])}{box('Stays on this Mac', ['A backup copy of the folder', 'Apps and sign-ins installed only here'])}</div>
      <MoveFacts facts={facts} />
      <p className="text-[13px] text-amber-600">You can't move it back to this Mac. Afterwards you open it from Online, like any online Crew.</p>
    </>}
    {error && <p className="text-[13px] text-amber-600">{error}</p>}
    <div className="flex justify-end gap-2"><Button className="rounded-full px-3.5" variant="outline" onClick={onCancel}>Cancel</Button><Button className="rounded-full px-3.5" disabled={tooBig || !facts} onClick={onMove}>Publish Crew</Button></div>
  </section>
}

function MoveFacts({ facts }: { facts?: { size: string; time: string } }) {
  if (!facts) return <p className="text-xs text-muted-foreground">Checking its size…</p>
  return <div className="space-y-0.5 rounded-lg bg-muted/40 px-3 py-2 text-[13px]">
    <div className="flex justify-between"><span className="text-muted-foreground">Size</span>{facts.size}</div>
    <div className="flex justify-between"><span className="text-muted-foreground">Time</span>{facts.time}</div>
  </div>
}

/** A QR code of the Crew's link, drawn on this Mac (no network call). */
function LinkCode({ link }: { link: string }) {
  const [image, setImage] = useState('')
  useEffect(() => {
    let active = true
    void import('qrcode').then(QRCode => QRCode.toDataURL(link, { margin: 1, width: 176 })).then(url => { if (active) setImage(url) }).catch(() => {})
    return () => { active = false }
  }, [link])
  return image ? <img src={image} alt={`Code that opens ${link}`} className="h-44 w-44 rounded-md border border-border bg-white p-1" /> : <p className="text-muted-foreground">Drawing the code…</p>
}

/** The existing hosted AI app guide, pointed at this Crew's online address, plus a first sentence to try. */
function ConnectAiApp({ endpoint, name, onDone }: { endpoint: string; name: string; onDone: () => void }) {
  const [app, setApp] = useState<'chatgpt' | 'cowork'>('chatgpt')
  return <div className="space-y-2">
    <HostedAiAppGuide app={app} endpoint={endpoint}>
      <p className="text-sm"><span className="font-medium">Try it:</span> "Ask my {name} Crew what changed today."</p>
    </HostedAiAppGuide>
    <div className="flex gap-2"><Button className="rounded-full px-3.5" size="sm" variant="outline" onClick={() => setApp(app === 'chatgpt' ? 'cowork' : 'chatgpt')}>{app === 'chatgpt' ? 'Claude instead' : 'ChatGPT instead'}</Button><Button className="rounded-full px-3.5" size="sm" onClick={onDone}>Done</Button></div>
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
  return <section aria-label={title} className="rounded-2xl border border-border">
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
