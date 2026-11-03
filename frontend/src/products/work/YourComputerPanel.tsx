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
  const [open, setOpen] = useState<'' | 'code' | 'ai' | 'share'>('')
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
  const moveAndShare = async () => {
    setOpen('')
    if (await run('server') && link) window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')
  }
  const minutes = readiness ? Math.max(1, Math.round(readiness.estimated_seconds / 60)) : 0
  const moveSummary = readiness ? `${sizeLabel(readiness.measurement.bytes)} · about ${minutes} ${minutes > 1 ? 'minutes' : 'minute'}` : ''
  return <div className="space-y-5">
    <section aria-label="Where it lives" className="space-y-2">
      <h3 className="text-sm font-semibold">Where it lives</h3>
      <div className="flex items-center gap-3">
        <p role="status" className="flex flex-1 items-center gap-2 text-sm font-medium">
          {moving ? <RefreshCw aria-hidden="true" className="h-4 w-4 animate-spin text-muted-foreground" /> : online ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <Laptop className="h-4 w-4 text-muted-foreground" />}
          {!status ? 'Checking…' : moving ? 'Moving online…' : online ? 'Online' : 'On this Mac'}
        </p>
        {binding && !online && !moving && <Button size="sm" variant="outline" disabled={tooBig} onClick={() => void run('server')}>Move online</Button>}
      </div>
      <p className="text-xs text-muted-foreground">{moving ? 'You can keep chatting. It picks up where it left off if interrupted.' : online ? 'It works even when your Mac is off.' : 'Your files stay on this Mac. You can use it from anywhere while the Mac is on.'}</p>
      {status && <p className="text-xs text-muted-foreground">Its schedules run here too.</p>}
      {!binding && connections.length > 1 && <select aria-label="Network" value={choice} onChange={event => { setChoice(event.target.value); if (event.target.value) void run('relay', event.target.value) }} className="w-full rounded-md border border-border bg-background p-2 text-sm">
        <option value="">Choose a network</option>
        {connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select>}
      {!online && !moving && readiness && <details className={`text-xs ${tooBig ? 'text-amber-600' : 'text-muted-foreground'}`} open={tooBig}>
        <summary className="cursor-pointer">Moving online: {sizeLabel(readiness.measurement.bytes)} · {readiness.measurement.files.toLocaleString()} files{tooBig ? ' · too big' : `, about ${minutes} ${minutes > 1 ? 'minutes' : 'minute'}`}</summary>
        <div className="mt-1 space-y-1">
          {tooBig
            ? <>
              <p className="font-medium">Too big to move online (up to {sizeLabel(readiness.measurement.max_bytes)} and {readiness.measurement.max_files.toLocaleString()} files). Move large folders out of this Crew, or add them to .gitignore.</p>
              {(readiness.measurement.largest_folders?.length ?? 0) > 0 && <p>Biggest folders: {readiness.measurement.largest_folders!.map(folder => `${folder.path} (${sizeLabel(folder.bytes)})`).join(', ')}</p>}
              {(readiness.measurement.files_over_limit?.length ?? 0) > 0 && <p>Files over {sizeLabel(readiness.measurement.max_file_bytes)}: {readiness.measurement.files_over_limit!.join(', ')}</p>}
            </>
            : <>
              <p>Your files, including .env and keys{(readiness.secrets ?? 0) > 0 ? `, and ${readiness.secrets} selected ${readiness.secrets === 1 ? 'secret' : 'secrets'}` : ''}, and your chats go with it. After the move it lives online only; this Mac keeps a copy of the folder as a backup.</p>
              {(readiness.measurement.dependency_files?.length ?? 0) > 0 && <p>Packages are installed again online ({readiness.measurement.dependency_files!.join(', ')}).</p>}
              <p>Programs and tools only on your Mac don't go with it.</p>
            </>}
        </div>
      </details>}
      {binding?.pending_action === 'local' && !busy && <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">The last move didn't finish. Your work is kept.</span><Button size="sm" variant="outline" onClick={() => void run('local')}>Finish</Button></div>}
      {error && <div className="space-y-1">
        <div className="flex items-center gap-2 text-xs"><TriangleAlert className="h-4 w-4 text-amber-500" /><span className="flex-1">It didn't finish. Your work is kept.</span></div>
        <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Details</summary><p className="mt-1 break-all">{error}</p></details>
      </div>}
    </section>
    {binding && <section aria-label="Who can use it" className="space-y-3 border-t border-border pt-4 text-sm">
      <h3 className="font-semibold">Who can use it</h3>
      <div className="space-y-2">
        <div className="flex items-center gap-2"><span className="font-medium">You</span><span className="flex-1 text-xs text-muted-foreground">{online ? 'From anywhere' : 'From anywhere while your Mac is on'}</span><CheckCircle2 aria-label="Ready" className="h-4 w-4 text-emerald-500" /></div>
        <ul className="space-y-2 border-l border-border pl-3 text-xs">
          {link && <li className="space-y-2">
            <div className="flex items-center gap-2"><Smartphone aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><span className="flex-1"><span className="font-medium">Phone</span> · Scan to open on your phone</span><Button size="sm" variant="outline" onClick={() => setOpen(open === 'code' ? '' : 'code')}>{open === 'code' ? 'Hide code' : 'Show code'}</Button></div>
            {open === 'code' && <LinkCode link={link} />}
          </li>}
          {link && <li className="flex items-center gap-2"><Globe aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><span className="min-w-0 flex-1 truncate"><span className="font-medium">Website</span> · <a className="underline" href={link} target="_blank" rel="noopener noreferrer">{link.replace(/^https?:\/\//, '')}</a></span><span className="text-emerald-600">✓ Ready</span></li>}
          {binding.peer_server && <li className="space-y-2">
            <div className="flex items-center gap-2"><MessageSquare aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><span className="flex-1"><span className="font-medium">ChatGPT or Claude</span> · Then just say "ask my {name} Crew to…"</span><Button size="sm" variant="outline" onClick={() => setOpen(open === 'ai' ? '' : 'ai')}>{open === 'ai' ? 'Close' : 'Connect'}</Button></div>
            {open === 'ai' && <ConnectAiApp endpoint={`${binding.peer_server}/api/external/v1/mcp`} name={name} onDone={() => setOpen('')} />}
          </li>}
          {link && <li className="flex items-center gap-2"><Hash aria-hidden="true" className="h-3.5 w-3.5 text-muted-foreground" /><span className="flex-1"><span className="font-medium">Slack</span> · Direct message it in Slack. Only you; to use it in channels, share it with people.</span><Button size="sm" variant="outline" onClick={() => window.open(`${link}?view=slack`, '_blank', 'noopener,noreferrer')}>Connect</Button></li>}
        </ul>
      </div>
      <div className="flex items-center gap-2">
        <span className="flex-1"><span className="font-medium">Assistants</span><span className="block text-xs text-muted-foreground">Other people's AI assistants can ask it questions while your Mac is on. Coming next.</span></span>
        <button type="button" role="switch" aria-checked={false} aria-label="Assistants" disabled className="relative h-6 w-11 shrink-0 rounded-full bg-muted-foreground/30 opacity-50"><span className="absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white" /></button>
      </div>
      <div className="space-y-2">
        <div className="flex items-center gap-2"><span className="flex-1"><span className="font-medium">People</span><span className="block text-xs text-muted-foreground">{online ? 'Choose who can use it on its page online.' : 'Nobody else yet'}</span></span>
          {online
            ? link && <Button size="sm" variant="outline" onClick={() => window.open(`${link}?view=share`, '_blank', 'noopener,noreferrer')}>Share…</Button>
            : <Button size="sm" variant="outline" disabled={moving} onClick={() => setOpen(open === 'share' ? '' : 'share')}>{open === 'share' ? 'Close' : 'Share…'}</Button>}
        </div>
        {open === 'share' && !online && <ShareWithTeam name={name} workspacePath={workspacePath} moveSummary={moveSummary} canMove={!tooBig && !moving} onMoveAndShare={() => void moveAndShare()} onCancel={() => setOpen('')} />}
      </div>
    </section>}
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
function ShareWithTeam({ name, workspacePath, moveSummary, canMove, onMoveAndShare, onCancel }: { name: string; workspacePath: string; moveSummary: string; canMove: boolean; onMoveAndShare: () => void; onCancel: () => void }) {
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
  return <section aria-label={`Share ${name} with your team`} className="space-y-3 rounded-md border border-border p-3 text-xs">
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
      {moveSummary && <p>Moves online first · {moveSummary}</p>}
      <div className="flex justify-end gap-2"><Button size="sm" variant="outline" onClick={onCancel}>Cancel</Button><Button size="sm" disabled={!canMove} onClick={onMoveAndShare}>Move and share</Button></div>
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
