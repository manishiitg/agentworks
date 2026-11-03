import { forwardRef, useEffect, useState, type ReactNode } from 'react'
import { CheckCircle2, CircleDashed, Folder, Globe, Hash, Laptop, MessageSquare, RefreshCw, Smartphone, Terminal, TriangleAlert, type LucideIcon, type LucideProps } from 'lucide-react'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { useChatStore } from '../../stores/useChatStore'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { laptopSettingsEndpoint } from '../../components/workspace/laptopAccess'
import { Button } from '../../components/ui/Button'
import { CodeLocalFilesSettings } from './CodeFilesPanel'
import { ServerProjectLocalOpen } from './ServerProjectLocalOpen'
import { HostedAiAppGuide } from '../../components/integrations/HostedAiAppGuide'
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
  return <div><WhereItLivesCard projectId={projectId} sessionId={sessionId} /></div>
}

function WhereItLivesCard({ projectId, sessionId }: { projectId: string; sessionId?: string }) {
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const name = tab?.metadata?.agentProfileProjectTitle || 'this'
  const workspacePath = tab?.metadata?.agentProfileWorkspace || ''
  const connections = useConnectedWorkStore(state => state.connections).filter(item => item.id !== 'local')
  const [status, setStatus] = useState<SyncStatus | null>(null)
  const [choice, setChoice] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const [open, setOpen] = useState<'' | 'code' | 'ai' | 'move' | 'share'>('')
  useEffect(() => {
    const abort = new AbortController()
    const load = () => void api.get<SyncStatus>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true }).then(({ data }) => { if (!abort.signal.aborted) setStatus(data) }).catch(() => {})
    load()
    const timer = window.setInterval(load, 5000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId, reload])
  const binding = status?.binding
  const online = binding?.editing === 'server'
  const moving = busy || binding?.pending_action === 'server'
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
  return <div className="overflow-hidden rounded-2xl border border-border bg-card text-sm">
    <section aria-label="Where it lives" className="space-y-1 border-b border-border px-4 py-3">
      <p className="mb-1.5 text-xs text-muted-foreground">Where it lives</p>
      <div className="flex items-center justify-between gap-2.5">
        <p role="status" className="flex items-center gap-2 text-base font-semibold">
          {moving ? <RefreshCw aria-hidden="true" className="h-4 w-4 animate-spin text-muted-foreground" /> : <span aria-hidden="true" className={`h-2.5 w-2.5 rounded-full ${online || binding ? 'bg-emerald-500 shadow-[0_0_0_3px_rgba(62,207,122,0.2)]' : 'bg-muted-foreground/60'}`} />}
          {!status ? 'Checking…' : moving ? 'Moving online…' : online ? 'Online' : 'On this Mac'}
        </p>
        {binding && !online && !moving && open !== 'move' && <Button size="sm" variant="outline" onClick={() => setOpen('move')}>Move online</Button>}
      </div>
      <p className="text-[13px] text-muted-foreground">{moving ? 'You can keep chatting. It picks up where it left off if interrupted.' : online ? 'It works even when your Mac is off.' : 'Your files stay on this Mac. You can use it from anywhere while the Mac is on.'}</p>
      {status && <p className="text-[13px] text-muted-foreground">Its schedules run here too.</p>}
      {!binding && connections.length > 1 && <select aria-label="Network" value={choice} onChange={event => { setChoice(event.target.value); if (event.target.value) void run('relay', event.target.value) }} className="mt-2 w-full rounded-md border border-border bg-background p-2 text-sm">
        <option value="">Choose a network</option>
        {connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select>}
      {open === 'move' && <MoveOnline name={name} facts={facts} tooBig={tooBig} onCancel={() => setOpen('')} onMove={() => void move(false)} />}
      {binding?.pending_action === 'local' && !busy && <div className="flex items-center gap-2 pt-1 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last move didn't finish. Your work is kept.</span><Button size="sm" variant="outline" onClick={() => void run('local')}>Finish</Button></div>}
      {error && <div className="flex items-center gap-2 pt-1 text-xs"><TriangleAlert className="h-4 w-4 shrink-0 text-amber-500" /><span className="flex-1">It didn't finish. Your work is kept. <span className="text-muted-foreground">{error}</span></span></div>}
    </section>
    {binding && <section aria-label="Who can use it" className="space-y-1 px-4 py-3">
      <p className="mb-1.5 text-xs text-muted-foreground">Who can use it</p>
      <div className="grid grid-cols-[72px_1fr_auto] items-center gap-2.5 py-1.5"><span className="font-semibold">You</span><span className="text-[13px] text-muted-foreground">{online ? 'From anywhere' : 'From anywhere while your Mac is on'}</span><span className="text-emerald-500">✓</span></div>
      <div className="overflow-hidden rounded-xl border border-border">
        {way(<Smartphone className="h-4 w-4" />, 'Phone', 'Scan to open on your phone', <Button size="sm" variant="outline" disabled={!link} onClick={() => setOpen(open === 'code' ? '' : 'code')}>{open === 'code' ? 'Hide code' : 'Show code'}</Button>)}
        {open === 'code' && link && <div className="flex justify-center border-b border-border p-3"><LinkCode link={link} /></div>}
        {way(<Globe className="h-4 w-4" />, 'Website', 'Open it in any browser', <span className="flex items-center gap-2">{ready}<Button size="sm" variant="outline" disabled={!link} onClick={() => window.open(link, '_blank', 'noopener,noreferrer')}>Open</Button></span>)}
        {way(<MessageSquare className="h-4 w-4" />, 'ChatGPT or Claude', `Then just say "ask my ${name} Crew to…"`, <Button size="sm" variant="outline" disabled={!binding.peer_server} onClick={() => setOpen(open === 'ai' ? '' : 'ai')}>{open === 'ai' ? 'Close' : 'Connect'}</Button>)}
        {open === 'ai' && binding.peer_server && <div className="border-b border-border p-3"><ConnectAiApp endpoint={`${binding.peer_server}/api/external/v1/mcp`} name={name} onDone={() => setOpen('')} /></div>}
        {way(<Hash className="h-4 w-4" />, 'Slack', 'Direct message it in Slack. Only you; to use it in channels, share it with people.', <Button size="sm" variant="outline" disabled={!link} onClick={() => window.open(`${link}?view=slack`, '_blank', 'noopener,noreferrer')}>Connect</Button>)}
      </div>
      <div className="grid grid-cols-[72px_1fr_auto] items-center gap-2.5 py-1.5"><span className="font-semibold">Assistants</span><span className="text-[13px] text-muted-foreground">Other people's AI assistants can ask it questions while your Mac is on · coming next</span>
        <span aria-label="Assistants: off, coming next" className="inline-flex overflow-hidden rounded-lg border border-border text-[13px] opacity-60"><span className="bg-muted px-2.5 py-1">Off</span><span className="px-2.5 py-1 text-muted-foreground">On</span></span></div>
      <div className="grid grid-cols-[72px_1fr_auto] items-center gap-2.5 py-1.5"><span className="font-semibold">People</span><span className="text-[13px] text-muted-foreground">{online ? 'Choose who can use it on its page online' : 'Nobody else yet'}</span>
        {online
          ? <Button size="sm" variant="outline" disabled={!link} onClick={() => window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')}>Share…</Button>
          : <Button size="sm" variant="outline" disabled={moving} onClick={() => setOpen(open === 'share' ? '' : 'share')}>Share…</Button>}
      </div>
      {open === 'share' && !online && <ShareWithTeam name={name} workspacePath={workspacePath} facts={facts} canMove={!tooBig && !moving} onMoveAndShare={() => void move(true)} onCancel={() => setOpen('')} />}
    </section>}
  </div>
}

/** The Move online step: size and time, what moves, then the owner's click. One-way (owner, 3 Nov). */
function MoveOnline({ name, facts, tooBig, onCancel, onMove }: { name: string; facts?: { size: string; time: string }; tooBig: boolean; onCancel: () => void; onMove: () => void }) {
  return <div className="mt-2 space-y-3 rounded-xl border border-border bg-background/40 p-3">
    <p className="font-semibold">Move "{name}" online</p>
    {tooBig
      ? <p className="text-[13px] text-amber-600">It's too big to move online. Move large folders out of this Crew first.</p>
      : <>
        <p className="text-[13px] text-muted-foreground">It will work even when your Mac is off. After the move it lives online only.</p>
        <MoveFacts facts={facts} />
        <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">What moves</summary><p className="mt-1">Its files and your chats move; apps installed only on this Mac don't, and this Mac keeps a backup copy of the folder.</p></details>
      </>}
    <div className="flex justify-end gap-2"><Button size="sm" variant="outline" onClick={onCancel}>Cancel</Button><Button size="sm" disabled={tooBig || !facts} onClick={onMove}>Move online</Button></div>
  </div>
}

function MoveFacts({ facts }: { facts?: { size: string; time: string } }) {
  if (!facts) return <p className="text-xs text-muted-foreground">Checking its size…</p>
  return <div className="space-y-0.5 rounded-lg bg-muted/40 px-3 py-2 text-[13px]">
    <div className="flex justify-between"><span className="text-muted-foreground">Size</span>{facts.size}</div>
    <div className="flex justify-between"><span className="text-muted-foreground">Time</span>{facts.time}</div>
    <div className="flex justify-between"><span className="text-muted-foreground">Your chats</span>move with it</div>
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
