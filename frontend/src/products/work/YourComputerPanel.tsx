import { forwardRef, useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, CheckCircle2, CircleDashed, Cloud, Folder, Laptop, Lock, Terminal, TriangleAlert, Users, type LucideIcon, type LucideProps } from 'lucide-react'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { Button } from '../../components/ui/Button'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { Switch } from '../../components/ui/Switch'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useLocalFileDevices } from './codeLocalFiles'
import { ConnectServerForm } from '../../components/providers/ProvidersOverview'
import { AskButton, teamCard } from './TeamTab'
import { ShareCrew, SlackTab } from './ShareCrew'
import { CrewDashboardsTab } from './CrewDashboardsTab'
export { openCrewAccess, openCrewAskThread } from './MacSharedAgents'
import { useTeamTab } from './teamApi'
import { MacAgentBridge } from './MacAgentBridge'
import { OnlineAgentsCard, type OnlineAgentPlacement } from './OnlineAgentsCard'
import { hasPendingCrewAccess, pendingCrewAccessSection, type CrewAccessSection } from './MacSharedAgents'

type Binding = { connection_id?: string; peer_server?: string; peer_project_id: string; updated_at?: string; editing: 'local' | 'server'; pending_action?: 'update' | 'server' }
type MoveReport = { place?: CloudState; message?: string }
// What of this Crew the server keeps (owner, 4 Nov "Where your data is"): the server lists it by name, e.g. an agent's
// always-online copy or a team's shared folder; empty while everything stays on this Mac.
type ServerCopy = { name: string; detail: string }
/** The Access page's shape, written by the server (crew_access_page.go): which tabs, whether this app holds the Mac's
 * controls ("mac") or shows the Mac-only rows as text, those rows' text, and the extra sections. */
type AccessTab = { id: CrewAccessSection; label: string; detail?: string; group?: 'team' | 'online' }
type AccessText = { line: string; note?: string }
// The Mac online tab's This Mac card, worded by the server (crew_access_page.go crewAccessMacCard).
type MacCard = { online: boolean; status: string; line: string; kept?: string; reach?: { who: string; how: string }[] }
type SyncStatus = MoveReport & { binding: Binding | null; placements?: Record<string, OnlineAgentPlacement>; on_server?: ServerCopy[]; shared_agents?: number
  access_view?: string; access_summary?: string; access_here?: 'mac'; access_tabs?: AccessTab[]; access_mac?: MacCard; access_mac_locked?: string; access_publish?: AccessText; access_laptop?: boolean }
type LaptopSettings = { device_id: string; folders: { id: string; path: string; access: 'read' | 'write' }[]; commands: boolean }

/** In the AgentWorks app a Crew lives on this Mac or online (owner, 3 Nov); on a server the page is its laptop access. */
export const crewCloudMode = () => hasDesktopConnections()

export type CloudState = 'mac' | 'mac_off' | 'moving' | 'online' | 'offline'

// One reading per Crew for the whole page (owner, 3 Nov: the page takes ONE flag): every panel that asks where a
// Crew lives shares this cache and its single poll, so the header, tabs, menu and panels always agree.
/** Someone else's ask to a shared agent on this Mac, from the Mac's own status (the last ten minutes). */
export type CrewAsk = { tab: string; agent: string; sender: string; channel?: string; conversation_key: string; session_id: string; started_at: string; active: boolean }
/** One line of the Access tree under the side panel's Access row, written by the server (owner, 5 Nov mockup). */
export type CrewAccessLine = { kind: 'mac' | 'team' | 'team_live' | 'dashboard' | 'public' | 'folder' | 'clash'; group?: 'team' | 'online'; title: string; detail?: string; path?: string; ask?: string; target?: string; dashboards?: { name: string; path: string }[]; count?: number; tag?: string; progress?: { direction: string; bytes_done: number; bytes_total: number } }
type PlaceEntry = { place?: CloudState; chatOnly?: boolean; chatOnlyLine?: string; shared?: number; asking?: CrewAsk[]; listeners: Set<(place?: CloudState) => void>; timer?: number; abort?: AbortController; load?: () => void }
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
      void api.get<{ place?: CloudState; chat_only?: boolean; chat_only_line?: string; shared_agents?: number; asking?: CrewAsk[] }>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: created.abort.signal, skipSessionContext: true })
        .then(({ data }) => {
          const shared = data.shared_agents || 0
          const asking = data.asking || []
          const chatOnly = data.chat_only === true
          const chatOnlyLine = typeof data.chat_only_line === 'string' && data.chat_only_line ? data.chat_only_line : undefined
          if (!data.place || (data.place === created.place && chatOnly === Boolean(created.chatOnly) && chatOnlyLine === created.chatOnlyLine && shared === created.shared && JSON.stringify(asking) === JSON.stringify(created.asking || []))) return
          created.place = data.place
          created.chatOnly = chatOnly
          created.chatOnlyLine = chatOnlyLine
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

/** The server's one line above the chat of your own Mac copy opened through the server (owner, 12 Nov). */
export function useCrewChatOnlyLine(projectId?: string): string | undefined {
  const [line, setLine] = useState<string>()
  useEffect(() => {
    setLine(undefined)
    if (!projectId) return
    return watchCrewPlace(projectId, () => setLine(placeCache.get(projectId)?.chatOnlyLine))
  }, [projectId])
  return line
}

/** The server's flag: this person reaches their own Mac Crew through the server, so the screen is its chat alone. */
export function useCrewChatOnly(projectId?: string): boolean {
  const [chatOnly, setChatOnly] = useState(false)
  useEffect(() => {
    setChatOnly(false)
    if (!projectId) return
    return watchCrewPlace(projectId, () => setChatOnly(placeCache.get(projectId)?.chatOnly === true))
  }, [projectId])
  return chatOnly
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

/** The one Access page (owner, 5 Nov: "one Access page, not two"), the same in the Mac app and on the website. The
 * server's crew-sync reply decides it: the tabs, and whether this app holds the Mac's controls (the Mac app's own Crew)
 * or shows those rows as text (the website, or a Crew from a server); then the People and Dashboards tabs and laptop
 * access where the server lists them. */
export function YourComputerPanel({ projectId, sessionId, workspacePath, onBack }: { projectId: string; sessionId?: string; workspacePath?: string; canEdit?: boolean; onFullScreen?: (full: boolean) => void; onBack?: () => void }) {
  return <div><CrewAccessPage projectId={projectId} sessionId={sessionId} workspacePath={workspacePath} onBack={onBack} /></div>
}

function CrewAccessPage({ projectId, sessionId, workspacePath, onBack }: { projectId: string; sessionId?: string; workspacePath?: string; onBack?: () => void }) {
  const team = useTeamTab(projectId).view?.team
  const teamChanged = () => window.dispatchEvent(new CustomEvent('agentworks:team-updated'))
  // The last Online tab is remembered per Crew in this browser (owner, 5 Nov); Team is its own page, opened from its
  // menu row or by an agent asking for its sharing, so it is never the remembered tab.
  const tabKey = `agentworks:crew-access-tab:${projectId}`
  const [section, setSectionState] = useState<CrewAccessSection>(() => {
    if (hasPendingCrewAccess(projectId)) return pendingCrewAccessSection(projectId)
    try { const saved = window.localStorage.getItem(tabKey); if (saved === 'mac' || saved === 'online-agents' || saved === 'publish' || saved === 'dashboards' || saved === 'slack') return saved } catch { /* storage unavailable */ }
    return 'mac'
  })
  const setSection = (next: CrewAccessSection) => { setSectionState(next); if (next !== 'team') try { window.localStorage.setItem(tabKey, next) } catch { /* storage unavailable */ } }
  const [publishConnection, setPublishConnection] = useState('')
  useEffect(() => {
    const openTeam = (event: Event) => { const detail = (event as CustomEvent<{ projectId: string; section?: CrewAccessSection }>).detail; if (detail?.projectId === projectId) setSection(detail.section || 'team') }
    window.addEventListener('agentworks:open-crew-access', openTeam)
    return () => window.removeEventListener('agentworks:open-crew-access', openTeam)
  }, [projectId])
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [reaching, setReaching] = useState(false)
  const [asking, setAsking] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [open, setOpen] = useState<'' | 'code'>('')
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    window.addEventListener('agentworks:team-updated', load)
    return () => { window.clearInterval(timer); window.removeEventListener('agentworks:team-updated', load); abort.abort() }
  }, [projectId, reload])
  // This app holds the Mac's controls only when the server says so (its own local backend); elsewhere they are text.
  const here = status?.access_here === 'mac'
  const binding = status?.binding
  const online = binding?.editing === 'server'
  const target = binding?.connection_id || publishConnection || (connections.length === 1 ? connections[0].id : '')
  // A network is named by its server's address, such as agents.example.com.
  const networkName = (id?: string) => { const item = connections.find(c => c.id === id); try { return item ? new URL(item.server).host : '' } catch { return item?.name || '' } }
  const reach = (via: string) => { setAsking(false); setReaching(true); void run('relay', via).finally(() => setReaching(false)) }
  const link = binding?.peer_server ? `${binding.peer_server}/crew/projects/${encodeURIComponent(binding.peer_project_id)}` : ''
  const run = async (action: 'relay' | 'offline', via = target) => {
    setBusy(true); setError('')
    try {
      await desktopRequest<MoveReport>(`/${encodeURIComponent(via)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action })
      teamChanged()
      return true
    }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'It did not finish. Your work is kept.'); return false }
    finally { setBusy(false); setReload(value => value + 1); refreshCrewPlace(projectId) }
  }
  const way = (title: string, line: string, action: ReactNode) => <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2 py-2.5 text-sm">
    <div className="min-w-0 flex-1"><p className="font-semibold">{title}</p><p className="text-[12.5px] text-muted-foreground">{line}</p></div>
    <div className="flex flex-wrap items-center gap-1.5">{action}</div>
  </div>
  // Website and phone: the Mac app opens its Crew on the paired server; the website is that server.
  const reachLink = here ? link : `${window.location.origin}/crew/projects/${encodeURIComponent(projectId)}`
  // Esc goes back too, unless the person is typing or a dialog is open.
  useEffect(() => {
    if (!onBack) return
    const key = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      if (event.key !== 'Escape' || event.defaultPrevented || target?.closest('input,textarea,select,[contenteditable="true"],[role="dialog"]')) return
      onBack()
    }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [onBack])
  const network = networkName(target) || 'your server'
  const pairedConnection = connections.find(item => item.id === binding?.connection_id)
  const pairedServer = binding?.peer_server || pairedConnection?.server
  let pairedHost = ''
  try { pairedHost = pairedServer ? new URL(pairedServer).host : '' } catch { /* Invalid saved address has no display host. */ }
  const pairedName = pairedConnection?.name || pairedHost

  const tone = 'bg-background text-foreground shadow-sm ring-1 ring-border'
  // Two pages from one reply (owner, 11 Nov: Share split into Team and Online): the server puts each tab in a group; the
  // Team page shows the team tab, the Online page the rest. A tab the server does not list for this person falls back
  // to its page's first one.
  const page = section === 'team' ? 'team' : 'online'
  const allTabs = (status?.access_tabs || []).map(item => ({ ...item, tone }))
  const pageTabs = allTabs.filter(item => (item.group || 'online') === page)
  const tabs = pageTabs.length ? pageTabs : allTabs
  const shown: CrewAccessSection | undefined = tabs.some(item => item.id === section) ? section : tabs[0]?.id
  const needsServer = here && !connections.length && !online
  if (!status) return <p role="status" className="text-sm text-muted-foreground">Loading…</p>
  if (status.access_view === 'summary') return <p className="text-sm text-muted-foreground">{status.access_summary}</p>
  // A row only the Mac can change, as the server words it here.
  const macText = (text?: AccessText) => text ? <div className="space-y-1.5 px-1"><MacDataLine>{text.line}</MacDataLine>{text.note && <p className="text-xs text-muted-foreground">{text.note}</p>}</div> : null
  const serverCopies = [...(status?.on_server || []), ...(team?.server_data ? [{ name: `Shared with the team: ${team.server_data.name}`, detail: 'kept on the server so your team can sync it' }] : [])]
  // Team on while Mac online is off (the Mac app's own Crew): the Mac turns both on (the server decides, owner 5 Nov).
  const turnOnTeam = async () => {
    if (!target) { setSection('mac'); setAsking(true); return }
    setBusy(true); setError('')
    try { await desktopRequest(`/${encodeURIComponent(target)}/crew-teams/${encodeURIComponent(projectId)}/switch`, 'POST', { on: true }); teamChanged() }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'The Team could not be turned on. Try again.') }
    finally { setBusy(false); setReload(value => value + 1); refreshCrewPlace(projectId) }
  }
  return <div className="space-y-3.5 text-sm">
    {/* No title row (owner, 4 Nov): a small back arrow beside the tabs, and Esc, return to the workspace. */}
    {(tabs.length > 1 || shown !== 'team') && <div className="flex items-stretch gap-2">
    {onBack && <button type="button" aria-label="Back to workspace" title="Back (Esc)" onClick={onBack} className="grid w-8 shrink-0 place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:w-10"><ArrowLeft aria-hidden="true" className="h-4 w-4" /></button>}
    {tabs.length > 1 && <div role="tablist" aria-label={page === 'team' ? 'Team' : 'Online'} className={`grid min-w-0 flex-1 ${tabs.length === 2 ? 'grid-cols-2' : tabs.length === 3 ? 'grid-cols-3' : 'grid-cols-4'} gap-1 rounded-xl border border-border bg-muted/30 p-1`}>
      {tabs.map((item, index) => <button key={item.id} type="button" role="tab" id={`crew-access-${projectId}-${item.id}`} aria-controls={`crew-access-panel-${projectId}-${item.id}`} aria-selected={shown === item.id} tabIndex={shown === item.id ? 0 : -1}
        onClick={() => setSection(item.id)} onKeyDown={event => {
          const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1
          if (next < 0) return
          event.preventDefault(); setSection(tabs[next].id); document.getElementById(`crew-access-${projectId}-${tabs[next].id}`)?.focus()
        }} className={`min-w-0 rounded-lg px-2 py-2 text-center ${shown === item.id ? item.tone : 'text-muted-foreground hover:bg-muted'}`}>
        <span className="block font-semibold">{item.label}</span><span className="mt-0.5 block text-[11px]">{item.detail}</span>
      </button>)}
    </div>}
    </div>}
    {/* No server connected yet (owner, 4 Nov): one sign-in card here, so the person never leaves Access; the tabs show
        their content once the connection exists. Browser approval stays the person's own step. */}
    {needsServer && <SignInToServer onConnected={() => setReload(value => value + 1)} />}
    {!here && shown === 'publish' && <section role="tabpanel" id={`crew-access-panel-${projectId}-publish`} aria-labelledby={`crew-access-${projectId}-publish`} className="space-y-3">
      {macText(status.access_publish)}
      {(status.on_server || []).map(copy => <p key={copy.name} className="px-1 text-xs"><span className="font-medium">{copy.name}</span> <span className="text-muted-foreground">· {copy.detail}</span></p>)}
    </section>}
    {/* Mac online (owner, 6 Nov, approved mockup): two cards, the same on the Mac app and the website. The server
        words This Mac; the Mac app holds its switch, the website shows it. */}
    {!(here && needsServer) && shown === 'mac' && <section role="tabpanel" id={`crew-access-panel-${projectId}-mac`} aria-labelledby={`crew-access-${projectId}-mac`} className="space-y-3">
      {here && online ? <MacDataLine>{`This Crew now runs on ${network}. Open it on the Publish tab.`}</MacDataLine> : <>
        <section aria-label="This Mac" className={teamCard}>
          <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">This Mac</h3><AskButton projectId={projectId} card="mac" /></div>
          <div className="flex items-center gap-2.5">
            <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${status.access_mac?.online ? 'bg-emerald-500 ring-2 ring-emerald-500/20' : 'bg-muted-foreground/60'}`} />
            <span className="min-w-0 flex-1 truncate text-[15px] font-semibold">{status.access_mac?.status}</span>
            {status.access_mac_locked
              ? <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-border px-2.5 py-0.5 text-xs text-muted-foreground"><Lock aria-hidden="true" className="h-3 w-3" />{status.access_mac_locked}</span>
              : here ? <Switch aria-label="Mac online" checked={Boolean(binding) || reaching || asking} disabled={busy || !connections.length} onCheckedChange={on => { if (!on) { if (asking) setAsking(false); else void run('offline') } else if (target) reach(target); else setAsking(true) }} />
                : <span title="Turn this off in the Mac app."><Switch aria-label="Mac online" checked disabled /></span>}
          </div>
          {connections.length > 1 && pairedHost && <p className="break-words text-[12.5px] text-muted-foreground">Network · <span className="text-foreground">{pairedName}</span>{pairedName !== pairedHost && <> · {pairedHost}</>}</p>}
          <p className="text-[12.5px] text-muted-foreground">{status.access_mac?.line}</p>
          {status.access_mac?.kept && <p className="text-xs text-muted-foreground">{status.access_mac.kept}{serverCopies.length > 0 && <> <button type="button" className="text-primary hover:underline" onClick={() => setSection('team')}>What's kept on the server</button></>}</p>}
          {!!status.access_mac?.reach?.length && <div aria-label="Who can reach this Crew" className="space-y-1">
            <p className="text-xs font-medium text-muted-foreground">Who can reach this Crew</p>
            <dl className="divide-y divide-border text-[12.5px]">{status.access_mac.reach.map(row => <div key={row.who} className="grid grid-cols-[38%_1fr] gap-2 py-1.5">
              <dt className="font-medium">{row.who}</dt><dd className="text-muted-foreground">{row.how}</dd></div>)}</dl>
          </div>}
          {asking && !binding && <div role="group" aria-label="Which network?" className="space-y-1.5 border-t border-border pt-2.5">
            <p className="text-xs text-muted-foreground">Which network?</p>
            {connections.map(item => <Button key={item.id} size="sm" variant="outline" className="w-full justify-start rounded-full px-3.5" onClick={() => reach(item.id)}>{networkName(item.id)}</Button>)}
          </div>}
          {/* Website and phone is This Mac's last row (owner, 6 Nov: the ChatGPT/Claude row is gone, so no card of one row). */}
          <div aria-disabled={!reachLink} className={`border-t border-border ${reachLink ? '' : 'pointer-events-none opacity-50'}`}>
            {way('Website and phone', 'Open this Crew in any browser', <>
              <Button className="h-7 rounded-full px-3" size="sm" variant="outline" disabled={!reachLink} onClick={() => window.open(reachLink, '_blank', 'noopener,noreferrer')}>Open</Button>
              <Button className="h-7 rounded-full px-3" size="sm" variant="outline" disabled={!reachLink} aria-expanded={open === 'code'} aria-controls={`crew-phone-code-${projectId}`} onClick={() => setOpen(open === 'code' ? '' : 'code')}>{open === 'code' ? 'Hide QR' : 'QR for phone'}</Button>
            </>)}
            {open === 'code' && reachLink && <div id={`crew-phone-code-${projectId}`} className="flex justify-center p-3"><LinkCode link={reachLink} /></div>}
          </div>
          {!reachLink && <p className="text-xs text-muted-foreground">Turn on Mac online to open this Crew in a browser or on your phone.</p>}
        </section>

        {/* Public on AgentBridge is chosen per agent here, on Mac online only (owner, 6 Nov). */}
        <MacAgentBridge projectId={projectId} />
      </>}
    </section>}
    {shown === 'online-agents' && <section role="tabpanel" id={`crew-access-panel-${projectId}-online-agents`} aria-labelledby={`crew-access-${projectId}-online-agents`} className="space-y-3">
      {binding ? <OnlineAgentsCard key={`${projectId}:${binding.connection_id || ''}:${binding.peer_project_id}`} projectId={projectId}
          placements={status.placements} connection={here ? binding.connection_id : undefined}
          serverProject={binding.peer_project_id} network={pairedName || network} website={reachLink} /> : <div className="space-y-2 rounded-xl border border-border p-3"><h3 className="font-semibold">Online agents</h3><p className="text-[12.5px] text-muted-foreground">Connect this Crew to a server from Mac online to see its online agents.</p><Button size="sm" variant="outline" onClick={() => setSection('mac')}>Open Mac online</Button></div>}
    </section>}
    {here && !needsServer && shown === 'publish' && <section role="tabpanel" id={`crew-access-panel-${projectId}-publish`} aria-labelledby={`crew-access-${projectId}-publish`} className="space-y-3">
      <div className="flex justify-end"><AskButton projectId={projectId} card="publish" /></div>
      {!binding && connections.length > 1 && <label className="block space-y-1 text-xs">Server<select className="block w-full rounded-lg border border-border bg-background p-2 text-sm" value={publishConnection} onChange={event => setPublishConnection(event.target.value)}><option value="">Choose a server</option>{connections.map(item => <option key={item.id} value={item.id}>{networkName(item.id)}</option>)}</select></label>}
      {online && <div className="flex flex-wrap items-center gap-2.5 rounded-xl border border-sky-500/25 p-3">
        <div className="min-w-0 flex-1 basis-48"><p className="font-semibold">Whole Crew</p><p className="text-[12.5px] text-muted-foreground">Runs on {network}. Your Mac keeps a copy of its folder.</p></div>
        <Button className="ml-auto h-7 rounded-full px-3" size="sm" variant="outline" disabled={!link} onClick={() => window.open(link, '_blank', 'noopener,noreferrer')}>Open Crew</Button>
      </div>}
      {!online && binding?.connection_id && <div className="rounded-xl border border-border p-3"><p className="font-semibold">One agent</p>
        <p className="text-[12.5px] text-muted-foreground">Open the agent's menu and choose Run online. It makes a separate online agent, named with · online, that runs only on {network}; the agent itself stays on this Mac.</p></div>}
    </section>}
    {/* Team (owner, 11 Nov: its own menu row and page): the server's /share reply's team option. The Mac app's own Crew
        turns the Team on through the Mac while Mac online is off. */}
    {!(here && needsServer) && shown === 'team' && <section role="tabpanel" id={`crew-access-panel-${projectId}-team`} aria-labelledby={`crew-access-${projectId}-team`} className="space-y-3">
      <ShareCrew projectId={projectId} onBack={tabs.length > 1 ? undefined : onBack} onTurnOn={here ? () => void turnOnTeam() : undefined} />
    </section>}
    {/* Slack (owner, 9 Nov: its own Online tab): the Crew's Slack bot and who can talk to it. */}
    {shown === 'slack' && <section role="tabpanel" id={`crew-access-panel-${projectId}-slack`} aria-labelledby={`crew-access-${projectId}-slack`} className="space-y-3">
      <SlackTab projectId={projectId} workspacePath={workspacePath} />
    </section>}
    {/* Dashboards (owner, 9 Nov: their own Online tab): everything this Crew publishes, or "No dashboard yet" with
        its Ask; a Mac Crew that was never online lists its own. */}
    {shown === 'dashboards' && <section role="tabpanel" id={`crew-access-panel-${projectId}-dashboards`} aria-labelledby={`crew-access-${projectId}-dashboards`} className="space-y-3">
      <CrewDashboardsTab projectId={projectId} />
    </section>}
    {status.access_laptop && <LaptopCard sessionId={sessionId} />}
    {error && <p role="alert" className="text-xs text-amber-600">{error}</p>}
  </div>
}

function MacDataLine({ children }: { children: ReactNode }) {
  return <p className="px-1 text-[13px] text-muted-foreground">{children}</p>
}

/** The Access page before this Mac has a server: why, then the same connect form as Servers. */
function SignInToServer({ onConnected }: { onConnected: () => void }) {
  return <section aria-label="Sign in to a server" className="space-y-3 rounded-xl border border-border p-4">
    <p className="text-[13px] text-muted-foreground">Sign in to your company's AgentWorks server to use this Crew from your phone and the web, publish agents and work with a team. Your files stay on this Mac.</p>
    <ConnectServerForm onConnected={onConnected} />
  </section>
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


function LaptopCard({ sessionId }: { sessionId?: string }) {
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const endpoint = laptopSettingsEndpoint(tab?.metadata?.agentProfileId, tab?.metadata?.agentProfileProjectId)
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
  const summary = !settings ? 'Checking…' : !settings.device_id ? 'Off' : `Your laptop · ${online ? 'online' : 'offline'}`
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
