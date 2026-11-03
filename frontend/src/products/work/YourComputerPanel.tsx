import { forwardRef, useEffect, useState, type ReactNode } from 'react'
import { CheckCircle2, CircleDashed, Cloud, Folder, Globe, Hash, Laptop, MessageSquare, RefreshCw, Smartphone, Terminal, TriangleAlert, Users, type LucideIcon, type LucideProps } from 'lucide-react'
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

type Binding = { connection_id?: string; peer_server?: string; peer_project_id: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' }
// Move online reports real progress (PLAT-856-509): bytes sent so far, whether the Mac's schedules wait for the move,
// and, when it finished, where the Mac folder is kept.
type MoveReport = { phase?: MovePhase; place?: CloudState; bytes_done?: number; bytes_total?: number; schedules_paused?: boolean; message?: string; backup_path?: string; local_folder_kept?: string }
type SyncStatus = MoveReport & { binding: Binding | null }
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
type PlaceEntry = { place?: CloudState; shared?: number; listeners: Set<(place?: CloudState) => void>; timer?: number; abort?: AbortController; load?: () => void }
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
      void api.get<{ place?: CloudState; shared_agents?: number }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: created.abort.signal, skipSessionContext: true })
        .then(({ data }) => {
          const shared = data.shared_agents || 0
          if (!data.place || (data.place === created.place && shared === created.shared)) return
          created.place = data.place
          created.shared = shared
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

/** The page's one flag: this Crew lives on its owner's Mac and this screen is not that Mac (the website or phone),
 * so its online record is empty. Every panel decides from this, never from its own data. */
export function useCrewLivesOnMac(projectId?: string): boolean {
  const place = useYourComputerSummary(Boolean(projectId), projectId)
  // The Mac app's own local Crews are the Mac itself; the same Crew opened through a connected server is its online copy.
  return (place === 'mac' || place === 'mac_off') && !(crewCloudMode() && getRawActiveWorkspaceId() === 'local')
}

export const cloudStateLabel: Record<CloudState, string> = { mac: 'On this Mac', mac_off: 'Mac off', moving: 'Moving online…', online: 'Online', offline: 'On this Mac · Offline' }
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
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const name = tab?.metadata?.agentProfileProjectTitle || 'this'
  const workspacePath = tab?.metadata?.agentProfileWorkspace || ''
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [busyAction, setBusyAction] = useState('')
  const [moved, setMoved] = useState<MoveReport | null>(null)
  const [reaching, setReaching] = useState(false)
  const [asking, setAsking] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [open, setOpen] = useState<'' | 'code' | 'ai' | 'move' | 'share' | 'slack'>('')
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
  const target = binding?.connection_id || (connections.length === 1 ? connections[0].id : '')
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
  const way = (icon: ReactNode, title: string, line: string, action: ReactNode) => <div className="grid grid-cols-[22px_1fr_auto] items-center gap-2.5 border-b border-border px-3 py-2 last:border-b-0">
    <span aria-hidden="true" className="flex justify-center text-muted-foreground">{icon}</span>
    <span className="min-w-0"><span className="block text-sm font-medium">{title}</span><span className="block text-xs text-muted-foreground">{line}</span></span>
    {action}
  </div>
  const ready = <span className="text-xs text-emerald-500">✓ Ready</span>
  // Move online takes the whole page (owner, 3 Nov mockup): the step, then its progress, then done.
  const [doneSeen, setDoneSeen] = useState(false)
  const done = online && Boolean(moved) && !doneSeen
  const step: MoveStep | '' = open === 'move' && !online ? 'confirm' : moving ? 'moving' : done ? 'done' : ''
  useEffect(() => { onFullScreen?.(step !== ''); return () => onFullScreen?.(false) }, [step, onFullScreen])
  if (step) return <MoveOnlineScreen step={step} name={name} facts={facts} tooBig={tooBig} status={status} backup={moved?.backup_path || status?.backup_path} error={error}
    onCancel={() => setOpen('')} onMove={() => void move(false)} onOpen={() => { setDoneSeen(true); if (link) window.open(link, '_blank', 'noopener,noreferrer') }} />
  // The boxes sit on the page directly, without section labels (owner, 3 Nov: less text).
  return <div className="space-y-3 text-sm">
    <section aria-label="Where it lives" className="space-y-1">
      <div className="flex items-center justify-between gap-2.5">
        <p role="status" className="flex items-center gap-2 text-base font-semibold">
          {moving ? <RefreshCw aria-hidden="true" className="h-4 w-4 animate-spin text-muted-foreground" /> : <span aria-hidden="true" className={`h-2.5 w-2.5 rounded-full ${online || binding ? 'bg-emerald-500 shadow-[0_0_0_3px_rgba(62,207,122,0.2)]' : 'bg-muted-foreground/60'}`} />}
          {!status ? 'Checking…' : moving ? (status.bytes_total ? `Moving online… ${sizeLabel(status.bytes_done || 0)} of ${sizeLabel(status.bytes_total)}` : 'Moving online…') : online ? 'Online' : binding ? ['On this Mac', networkName(binding.connection_id)].filter(Boolean).join(' · ') : 'On this Mac · Offline'}
        </p>
        {binding && !online && !moving && open !== 'move' && <Button size="sm" variant="outline" onClick={() => setOpen('move')}>Move online</Button>}
      </div>
      {(moving || online) && <p className="text-[13px] text-muted-foreground">{moving ? 'Your work is kept. If it stops, Retry Move online picks up where it left off.' : 'It works even when your Mac is off.'}</p>}
      {status?.schedules_paused && <p className="text-[13px] text-muted-foreground">{status.message || 'Its schedules wait until the move finishes.'}</p>}
      {(status?.schedules_paused || binding?.pending_action === 'server') && !busy && <Button size="sm" variant="outline" className="mt-1" onClick={() => void move(false)}>Retry Move online</Button>}
      {(moved || status?.backup_path || status?.local_folder_kept) && online && <div className="mt-2 flex items-center gap-2 rounded-xl border border-border px-3 py-2 text-[13px]">
        <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500" />
        <span className="min-w-0 flex-1 break-words">{(moved?.backup_path || status?.backup_path) ? `Moved online. Your Mac folder is kept as ${moved?.backup_path || status?.backup_path}` : 'Moved online. Your folder on this Mac is unchanged.'}</span>
        {link && <Button size="sm" variant="outline" onClick={() => window.open(link, '_blank', 'noopener,noreferrer')}>Open online</Button>}
      </div>}
      {status && !online && !moving && <label className="mt-2 flex items-center justify-between gap-3 rounded-xl border border-border px-3 py-2">
        <span className="text-sm">Reach it from your phone and the web</span>
        <Switch aria-label="Reach it from your phone and the web" checked={Boolean(binding) || reaching || asking} disabled={busy || !connections.length} onCheckedChange={on => { if (!on) { if (asking) setAsking(false); else void run('offline') } else if (target) reach(target); else setAsking(true) }} />
      </label>}
      {asking && !binding && <div role="group" aria-label="Which network?" className="mt-2 space-y-1.5 rounded-xl border border-border p-3">
        <p className="text-xs text-muted-foreground">Which network?</p>
        {connections.map(item => <Button key={item.id} size="sm" variant="outline" className="w-full justify-start" onClick={() => reach(item.id)}>{networkName(item.id)}</Button>)}
      </div>}
      {error && <div className="flex items-center gap-2 pt-1 text-xs"><TriangleAlert className="h-4 w-4 shrink-0 text-amber-500" /><span className="flex-1">It didn't finish. Your work is kept. <span className="text-muted-foreground">{error}</span></span></div>}
    </section>
    {status && <section aria-label="Who can use it" className="space-y-1">
      {binding && <div className="grid grid-cols-[72px_1fr_auto] items-center gap-2.5 py-1.5"><span className="font-semibold">You</span><span className="text-[13px] text-muted-foreground">{online ? 'From anywhere' : 'From anywhere while your Mac is on'}</span><span className="text-emerald-500">✓</span></div>}
      <div aria-disabled={!binding} className={`overflow-hidden rounded-xl border border-border ${binding ? '' : 'pointer-events-none opacity-50'}`}>
        {way(<Smartphone className="h-4 w-4" />, 'Phone', 'Scan to open on your phone', <Button size="sm" variant="outline" disabled={!link} onClick={() => setOpen(open === 'code' ? '' : 'code')}>{open === 'code' ? 'Hide code' : 'Show code'}</Button>)}
        {open === 'code' && link && <div className="flex justify-center border-b border-border p-3"><LinkCode link={link} /></div>}
        {way(<Globe className="h-4 w-4" />, 'Website', 'Open it in any browser', <span className="flex items-center gap-2">{ready}<Button size="sm" variant="outline" disabled={!link} onClick={() => window.open(link, '_blank', 'noopener,noreferrer')}>Open</Button></span>)}
        {way(<MessageSquare className="h-4 w-4" />, 'ChatGPT or Claude', `Then just say "ask my ${name} Crew to…"`, <Button size="sm" variant="outline" disabled={!binding?.peer_server} onClick={() => setOpen(open === 'ai' ? '' : 'ai')}>{open === 'ai' ? 'Close' : 'Connect'}</Button>)}
        {open === 'ai' && binding?.peer_server && <div className="border-b border-border p-3"><ConnectAiApp endpoint={`${binding.peer_server}/api/external/v1/mcp`} name={name} onDone={() => setOpen('')} /></div>}
        {way(<Hash className="h-4 w-4" />, 'Slack', 'Direct message it in Slack. Only you; to use it in channels, share it with people.', <Button size="sm" variant="outline" disabled={!binding?.connection_id} onClick={() => setOpen(open === 'slack' ? '' : 'slack')}>{open === 'slack' ? 'Close' : 'Connect'}</Button>)}
        {open === 'slack' && binding?.connection_id && <div className="border-b border-border p-3"><MacCrewSlack connection={binding.connection_id} project={binding.peer_project_id} /></div>}
      </div>
      {!binding && <p className="text-[12px] text-muted-foreground">Turn on Reach to use it from other places</p>}
      <div className="grid grid-cols-[72px_1fr_auto] items-center gap-2.5 py-1.5"><span className="font-semibold">People</span><span className="text-[13px] text-muted-foreground">{online ? 'Choose who can use it on its page online' : 'Nobody else yet'}</span>
        {online
          ? <Button size="sm" variant="outline" disabled={!link} onClick={() => window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')}>Share…</Button>
          : <Button size="sm" variant="outline" disabled={moving} onClick={() => setOpen(open === 'share' ? '' : 'share')}>Share…</Button>}
      </div>
      {binding && !online && binding.connection_id && <MacSharedAgents projectId={projectId} connection={binding.connection_id} />}
      {open === 'share' && !online && <ShareWithTeam name={name} workspacePath={workspacePath} facts={facts} canMove={!tooBig && !moving} onMoveAndShare={() => void move(true)} onCancel={() => setOpen('')} />}
    </section>}
  </div>
}

type SharedAgent = { tab: string; name: string; purpose: string; audience: 'everyone' | 'chosen'; people?: string[] }
type Person = { id: string; username: string; email?: string }

// Reads one JSON reply from the Crew's network through the Mac's saved sign-in.
async function viaConnection<T>(connection: string, path: string): Promise<T> {
  const response = await fetch(`${getDesktopApiBaseUrl()}/api/connections/${encodeURIComponent(connection)}${path}`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` } })
  if (!response.ok) throw new Error(String(response.status))
  return response.json() as Promise<T>
}

/** Who can message your agents (owner, 3 Nov, mockup local-cloud-agent-sharing): Main is always only yours; each side
 * tab can be opened to chosen people or everyone, with one line about what it does. Saved as you go. Shown only when the
 * Crew's network supports it. */
function MacSharedAgents({ projectId, connection }: { projectId: string; connection: string }) {
  const [tabs, setTabs] = useState<{ tab: string; name: string }[] | null>(null)
  const [shared, setShared] = useState<SharedAgent[]>([])
  const [people, setPeople] = useState<Person[]>([])
  const [supported, setSupported] = useState(false)
  const [adding, setAdding] = useState('')
  const [search, setSearch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const endpoint = `/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`
  useEffect(() => {
    let live = true
    void viaConnection<{ shared_agents?: boolean }>(connection, '/api/crew-sync-capabilities').then(caps => { if (live) setSupported(Boolean(caps.shared_agents)) }).catch(() => {})
    void viaConnection<{ users: Person[] }>(connection, '/api/users/directory').then(data => { if (live) setPeople(data.users || []) }).catch(() => {})
    void desktopRequest<{ tabs: { tab: string; name: string }[]; agents: SharedAgent[] }>(endpoint, 'POST', { action: 'agents' })
      .then(data => { if (!live) return; setTabs(data.tabs || []); setShared(data.agents || []) })
      .catch(cause => { if (live) { setTabs([]); setError(cause instanceof Error ? cause.message : "This Crew's agents could not be read.") } })
    return () => { live = false }
  }, [connection, projectId, endpoint])
  if (!supported || !tabs) return null
  // Saves the whole list; an agent waits to be saved until it has its one line and, for chosen people, someone.
  const save = async (next: SharedAgent[]) => {
    setShared(next)
    const ready = next.filter(agent => agent.purpose.trim() && (agent.audience === 'everyone' || agent.people?.length))
    setBusy(true); setError('')
    try { await desktopRequest(endpoint, 'POST', { action: 'share_agents', agents: ready }) }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It was not saved. Try again.') }
    finally { setBusy(false) }
  }
  const update = (tab: string, change: Partial<SharedAgent> | null, persist = true) => {
    const current = shared.find(agent => agent.tab === tab)
    const base = current || { tab, name: tabs.find(row => row.tab === tab)?.name || tab, purpose: '', audience: 'chosen' as const, people: [] }
    const next = change === null ? shared.filter(agent => agent.tab !== tab) : [...shared.filter(agent => agent.tab !== tab), { ...base, ...change }]
    if (persist) void save(next); else setShared(next)
  }
  const personName = (id: string) => people.find(person => person.id === id)?.username || id
  const matches = (agent: SharedAgent) => people.filter(person => !agent.people?.includes(person.id) && `${person.username} ${person.email || ''}`.toLowerCase().includes(search.trim().toLowerCase())).slice(0, 6)
  return <section aria-label="Who can message your agents" className="mt-2 space-y-2 rounded-xl border border-border p-3">
    <p className="text-sm font-semibold">Who can message your agents</p>
    <div className="flex items-center gap-2 rounded-lg bg-muted/40 px-2.5 py-2 text-[13px]"><span aria-hidden="true">🔒</span><span><span className="font-medium">Main</span> is always only yours.</span></div>
    {tabs.map(row => {
      const agent = shared.find(item => item.tab === row.tab)
      return <div key={row.tab} className="space-y-2 rounded-lg border border-border p-2.5">
        <div className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 items-center gap-2 text-sm font-medium"><MessageSquare aria-hidden="true" className="h-4 w-4 shrink-0" /><span className="truncate">{row.name}</span></span>
          {agent ? <label className="flex items-center gap-2 text-xs text-muted-foreground">Others can message this agent<Switch aria-label={`Others can message ${row.name}`} checked disabled={busy} onCheckedChange={() => update(row.tab, null)} /></label>
            : <label className="flex items-center gap-2 text-xs text-muted-foreground">Only you<Switch aria-label={`Others can message ${row.name}`} checked={false} disabled={busy} onCheckedChange={() => update(row.tab, {}, false)} /></label>}
        </div>
        {agent && <>
          <input aria-label={`What ${row.name} does`} value={agent.purpose} maxLength={300} placeholder="What it does, in one line" onChange={event => update(row.tab, { purpose: event.target.value }, false)} onBlur={() => void save(shared)} className="w-full rounded-md border border-border bg-background px-2 py-1 text-sm" />
          <div role="radiogroup" aria-label={`Who can message ${row.name}`} className="inline-flex overflow-hidden rounded-lg border border-border text-xs">
            {(['chosen', 'everyone'] as const).map(choice => <button key={choice} type="button" role="radio" aria-checked={agent.audience === choice} disabled={busy} onClick={() => update(row.tab, { audience: choice })} className={`px-2.5 py-1 ${agent.audience === choice ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}`}>{choice === 'chosen' ? 'Chosen people' : 'Everyone'}</button>)}
          </div>
          {agent.audience === 'chosen' && <div className="flex flex-wrap items-center gap-1.5">
            {(agent.people || []).map(id => <span key={id} className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs">{personName(id)}<button type="button" aria-label={`Remove ${personName(id)}`} onClick={() => update(row.tab, { people: (agent.people || []).filter(person => person !== id) })} className="text-muted-foreground hover:text-foreground">×</button></span>)}
            {adding === row.tab ? <span className="relative">
              <input autoFocus aria-label="Find a person" value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { setAdding(''); setSearch('') } }} placeholder="Name or email" className="w-40 rounded-md border border-border bg-background px-2 py-0.5 text-xs" />
              {search.trim() && <span className="absolute left-0 top-full z-10 mt-1 block w-56 overflow-hidden rounded-md border border-border bg-popover shadow">
                {matches(agent).map(person => <button key={person.id} type="button" onClick={() => { update(row.tab, { people: [...(agent.people || []), person.id] }); setAdding(''); setSearch('') }} className="block w-full px-2 py-1 text-left text-xs hover:bg-muted">{person.username}{person.email && person.email !== person.username ? <span className="ml-1 text-muted-foreground">{person.email}</span> : null}</button>)}
                {!matches(agent).length && <span className="block px-2 py-1 text-xs text-muted-foreground">Nobody found</span>}
              </span>}
            </span> : <button type="button" onClick={() => { setAdding(row.tab); setSearch('') }} className="rounded-full border border-dashed border-border px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted">+ Add</button>}
          </div>}
          {!agent.purpose.trim() && <p className="text-[11px] text-amber-600">Write one line about what it does to share it.</p>}
        </>}
      </div>
    })}
    {!tabs.length && <p className="text-xs text-muted-foreground">Add an agent with + in the chat to share it.</p>}
    {shared.length > 0 && <p className="text-[12px] text-muted-foreground">They can ask these agents anything the agents can see in this Crew. They never see your other chats. Works while this Mac is on.</p>}
    {error && <p className="text-xs text-amber-600">{error}</p>}
  </section>
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
function MoveOnlineScreen({ step, name, facts, tooBig, status, backup, error, onCancel, onMove, onOpen }: { step: MoveStep; name: string; facts?: { size: string; time: string }; tooBig: boolean; status: SyncStatus | null; backup?: string; error: string; onCancel: () => void; onMove: () => void; onOpen: () => void }) {
  const box = (title: string, items: string[]) => <div className="flex-1 rounded-xl border border-border p-3"><p className="mb-1.5 text-xs font-medium text-muted-foreground">{title}</p><ul className="space-y-1 text-[13px]">{items.map(item => <li key={item}>{item}</li>)}</ul></div>
  if (step === 'done') return <section aria-label="Moved online" className="flex flex-col items-center gap-3 py-10 text-center text-sm">
    <Cloud className="h-10 w-10 text-primary" aria-hidden="true" />
    <p className="text-base font-semibold">"{name}" is online</p>
    <p className="max-w-sm text-[13px] text-muted-foreground">It works even when your Mac is off.{backup ? ` This Mac keeps a backup copy in "${backup.split('/').pop()}".` : ''}</p>
    <Button onClick={onOpen}>Open it</Button>
  </section>
  if (step === 'moving') {
    const total = status?.bytes_total || 0, done = status?.bytes_done || 0
    // Each shown step covers some of the move's phases; without a phase (an older server) the bytes decide the first.
    const at = status?.phase ? MOVE_PHASES.indexOf(status.phase) : total > 0 && done >= total ? 2 : 0
    const state = (first: number, last: number): 'done' | 'now' | 'next' => at > last ? 'done' : at >= first ? 'now' : 'next'
    const steps: [string, 'done' | 'now' | 'next'][] = [['Files and database', state(0, 1)], ['Chats and agents', state(2, 2)], ['Schedules, watchers, dashboards', state(3, 4)], ['Ready online', state(5, 6)]]
    return <section aria-label="Moving online" className="space-y-4 py-4 text-sm">
      <p className="text-base font-semibold">Moving "{name}" online…</p>
      <p className="text-[13px] text-muted-foreground">If it's interrupted, it picks up where it left off.</p>
      <div className="h-2 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-primary transition-all" style={{ width: `${total ? Math.min(100, Math.round(done * 100 / total)) : 5}%` }} /></div>
      {total > 0 && <p className="text-[13px] tabular-nums">{sizeLabel(done)} <span className="text-muted-foreground">of {sizeLabel(total)}</span></p>}
      <ul className="space-y-1 text-[13px]">{steps.map(([label, state]) => <li key={label} className={state === 'next' ? 'text-muted-foreground' : ''}>{state === 'done' ? '✓' : state === 'now' ? '●' : '○'} {label}</li>)}</ul>
      {status?.schedules_paused && <p className="text-[13px] text-muted-foreground">{status.message || 'Its schedules wait until the move finishes.'}</p>}
      {error && <p className="text-[13px] text-amber-600">It stopped. Your work is kept. <button type="button" className="underline" onClick={onMove}>Retry Move online</button></p>}
    </section>
  }
  return <section aria-label={`Move ${name} online`} className="space-y-4 py-2 text-sm">
    <div><p className="text-base font-semibold">Move "{name}" online</p><p className="text-[13px] text-muted-foreground">It will live online from now on, and work even when your Mac is off.</p></div>
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
    <div className="flex justify-end gap-2"><Button variant="outline" onClick={onCancel}>Cancel</Button><Button disabled={tooBig || !facts} onClick={onMove}>Move online</Button></div>
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
    <div className="flex gap-2"><Button size="sm" variant="outline" onClick={() => setApp(app === 'chatgpt' ? 'cowork' : 'chatgpt')}>{app === 'chatgpt' ? 'Claude instead' : 'ChatGPT instead'}</Button><Button size="sm" onClick={onDone}>Done</Button></div>
  </div>
}

type ShareAgent = { chat_id: string; name: string; purpose: string; selected: boolean }

/** Share with your team (owner, 3 Nov): Team or Full sync. Only the move is built; the rest shows what comes next. */
function ShareWithTeam({ name, workspacePath, facts, canMove, onMoveAndShare, onCancel }: { name: string; workspacePath: string; facts?: { size: string; time: string }; canMove: boolean; onMoveAndShare: () => void; onCancel: () => void }) {
  const [kind, setKind] = useState<'team' | 'full'>('team')
  const [agents, setAgents] = useState<ShareAgent[]>()
  useEffect(() => {
    if (!workspacePath) return
    let active = true
    void api.post<{ agents?: ShareAgent[] }>('/api/dashboards', { workspace: workspacePath, action: 'share_status' }).then(({ data }) => { if (active) setAgents(data.agents || []) }).catch(() => { if (active) setAgents([]) })
    return () => { active = false }
  }, [workspacePath])
  const pick = (value: 'team' | 'full', title: string, detail: string) => <button type="button" aria-pressed={kind === value} onClick={() => setKind(value)}
    className={`flex-1 rounded-md border p-2 text-left ${kind === value ? 'border-primary bg-primary/5' : 'border-border'}`}><span className="block text-sm font-medium">{title}</span><span className="block text-xs text-muted-foreground">{detail}</span></button>
  return <section aria-label={`Share ${name} with your team`} className="mt-2 space-y-3 rounded-xl border border-border bg-background/40 p-3 text-xs">
    <p className="text-sm font-semibold">Share "{name}" with your team</p>
    <div className="flex gap-2">{pick('team', 'Team', 'Everyone keeps their own files')}{pick('full', 'Full sync', 'Co-owners get everything')}</div>
    {kind === 'team' ? <>
      <div className="space-y-1">
        <p className="font-medium">Team folder</p>
        <div className="flex items-center gap-2"><Folder aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><span className="flex-1 text-muted-foreground">Everyone gets this folder on their own Mac, kept in step. Everything else stays on each person's own Mac.</span><Button size="sm" variant="outline" disabled>Choose</Button></div>
      </div>
      <div className="space-y-2">
        <p className="font-medium">Agents your team can ask</p>
        {!agents ? <p className="text-muted-foreground">Loading agents…</p> : agents.length === 0 ? <p className="text-muted-foreground">No named agents yet.</p> : agents.map(agent => <div key={agent.chat_id} className="flex items-start gap-2">
          <input type="checkbox" disabled checked={false} aria-label={`Let your team ask ${agent.name}`} className="mt-0.5" />
          <span className="flex-1"><span className="font-medium">{agent.name}</span>{agent.purpose && <span className="block text-muted-foreground">{agent.purpose}</span>}<span className="block text-muted-foreground">Stays yours. Nobody else sees it.</span></span>
          <span className="flex shrink-0 overflow-hidden rounded border border-border opacity-50"><span className="px-1.5 py-0.5">Chosen people</span><span className="border-l border-border px-1.5 py-0.5">Everyone</span></span>
        </div>)}
        <p className="text-muted-foreground">When you tick an agent: people can ask this agent anything it can see in this Crew.</p>
      </div>
      <p className="rounded-md bg-emerald-500/10 p-2 text-emerald-700 dark:text-emerald-400">These agents always answer: from your Mac while it's on, and online while it's off. Online they only see the team folder.</p>
      <p className="text-muted-foreground">Coming next: choosing the team folder and which agents your team can ask.</p>
      <div className="flex justify-end gap-2"><Button size="sm" variant="outline" onClick={onCancel}>Cancel</Button><Button size="sm" disabled>Share</Button></div>
    </> : <>
      <div className="flex items-center gap-2"><span className="flex-1"><span className="font-medium">Co-owners</span><span className="block text-muted-foreground">Add them on its page online after the move.</span></span><Button size="sm" variant="outline" disabled>+ Add</Button></div>
      <ul className="space-y-1">
        <li>✓ All agents, files and your chats with them move online. Everything works when your Mac is off.</li>
        <li>✓ Each co-owner gets the whole project on their Mac. <span className="text-muted-foreground">(coming next)</span></li>
        <li>✓ The newest change wins everywhere, your Mac included. Earlier versions are kept in history. <span className="text-muted-foreground">(coming next)</span></li>
      </ul>
      <p className="text-muted-foreground">Code history (git) is not synced. Use push and pull as usual.</p>
      <p className="font-medium">Moves online first</p>
      <MoveFacts facts={facts} />
      <div className="flex justify-end gap-2"><Button size="sm" variant="outline" onClick={onCancel}>Cancel</Button><Button size="sm" disabled={!canMove || !facts} onClick={onMoveAndShare}>Move and share</Button></div>
    </>}
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

/** For a Mac Crew's tab in the Mac app: who it is shared with, in a few words ("Shared with user2"), or '' when it is
 * not shared. Read from this Mac's own shared list and its network's people. */
export function useMacTabShareLine(projectId?: string, conversationKey?: string): string {
  const [line, setLine] = useState('')
  useEffect(() => {
    setLine('')
    const tab = conversationKey?.split(':chat:')[1]
    if (!projectId || !tab || !crewCloudMode() || getRawActiveWorkspaceId() !== 'local') return
    let live = true
    void (async () => {
      const { data } = await api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { skipSessionContext: true })
      const connection = data.binding?.connection_id
      if (!connection || !live) return
      const { agents } = await desktopRequest<{ agents: SharedAgent[] }>(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action: 'agents' })
      const agent = agents?.find(item => item.tab === tab)
      if (!agent || !live) return
      if (agent.audience === 'everyone') { setLine('Shared with everyone'); return }
      const people = await viaConnection<{ users: Person[] }>(connection, '/api/users/directory').then(result => result.users || []).catch(() => [] as Person[])
      const names = (agent.people || []).map(id => people.find(person => person.id === id)?.username || 'someone')
      if (live) setLine(`Shared with ${names.length > 2 ? `${names.slice(0, 2).join(', ')} and ${names.length - 2} more` : names.join(' and ')}`)
    })().catch(() => {})
    return () => { live = false }
  }, [projectId, conversationKey])
  return line
}
