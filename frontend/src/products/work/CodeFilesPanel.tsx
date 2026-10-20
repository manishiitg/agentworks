import { useEffect, useState, type ReactNode } from 'react'
import { Check, Copy, Laptop, RefreshCw, Terminal, Folder, ArrowUp, X } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
import api, { getApiBaseUrl } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { cliUpdateNeeded, localFileError, useLatestCliVersion, useLocalFileDevices } from './codeLocalFiles'

function shellQuote(value: string) { return `'${value.replace(/'/g, "'\\''")}'` }

function CopyCommand({ command, label, disabled = false }: { command: string; label: string; disabled?: boolean }) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState(false)
  useEffect(() => { setCopied(false); setError(false) }, [command])
  useEffect(() => {
    if (!copied) return
    const timer = window.setTimeout(() => setCopied(false), 2000)
    return () => window.clearTimeout(timer)
  }, [copied])
  const copy = async () => {
    try { await navigator.clipboard.writeText(command); setCopied(true); setError(false) }
    catch { setError(true) }
  }
  return <div>
    <div className={`flex items-start gap-2 rounded-md bg-muted/40 p-3 ${disabled ? 'opacity-50' : ''}`}>
      <code className="min-w-0 flex-1 whitespace-pre-wrap break-all font-mono text-[11px] leading-5">{command}</code>
      <Button size="icon" variant="ghost" disabled={disabled} aria-label={`Copy ${label}`} title={copied ? 'Copied' : 'Copy'} onClick={() => void copy()}>{copied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}</Button>
    </div>
    <span role="status" className="text-xs text-muted-foreground">{copied ? 'Copied. Paste into your computer’s terminal.' : error ? 'Could not copy. Select the command and copy it manually.' : ''}</span>
  </div>
}

function SetupStep({ number, title, children }: { number: number; title: string; children: ReactNode }) {
  return <div className="flex gap-3">
    <span aria-hidden="true" className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border bg-background text-xs font-medium text-muted-foreground">{number}</span>
    <div className="min-w-0 flex-1 space-y-2"><h4 className="text-sm font-medium">{title}</h4>{children}</div>
  </div>
}

// PowerShell single-quoted string: a quote inside is doubled.
function psQuote(value: string) { return `'${value.replace(/'/g, "''")}'` }
const looksLikeWindows = () => typeof navigator !== 'undefined' && /windows/i.test(navigator.userAgent || '')

function ComputerSetup({ connected, reconnecting, workspaceName }: { connected: boolean; reconnecting: boolean; workspaceName?: string }) {
  const base = getApiBaseUrl() || window.location.origin
  const [windows, setWindows] = useState(looksLikeWindows)
  void workspaceName
  const installCommand = windows
    ? `& ([scriptblock]::Create((irm ${psQuote(`${base}/api/downloads/cli/install-agentworks.ps1`)}))) -Server ${psQuote(base)} -NoLogin`
    : `curl -fsSL ${shellQuote(`${base}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${shellQuote(base)} --no-login`
  const startCommand = windows
    ? `agentworks start --server ${psQuote(base)}`
    : `agentworks start --server ${shellQuote(base)}`
  return <details className="group" open={!connected && !reconnecting}>
    <summary className="flex cursor-pointer items-center gap-2 px-4 py-3 text-sm font-medium"><Terminal className="h-4 w-4 text-muted-foreground" />{connected ? 'Set up another computer' : reconnecting ? 'CLI setup and reconnect' : 'Set up your computer'}<span className="ml-auto text-xs font-normal text-muted-foreground">macOS · Linux · Windows</span></summary>
    <div className="space-y-5 border-t border-border px-4 py-4">
      <div role="group" aria-label="Your computer" className="flex gap-1.5">
        <Button size="sm" variant={windows ? 'ghost' : 'secondary'} aria-pressed={!windows} onClick={() => setWindows(false)}>macOS / Linux</Button>
        <Button size="sm" variant={windows ? 'secondary' : 'ghost'} aria-pressed={windows} onClick={() => setWindows(true)}>Windows</Button>
      </div>
      <p className="text-xs leading-5 text-muted-foreground">Run these commands on your own computer{windows ? ' in PowerShell' : ''}. Your model stays on this server; the CLI connects this computer. Choose folders in Crew settings. Already installed? Start at step 2.</p>
      <SetupStep number={1} title="Install the CLI">
        <CopyCommand label="install command" command={installCommand} />
        <p className="text-xs text-muted-foreground">{windows ? 'Installs AgentWorks in %LOCALAPPDATA%\\agentworks and adds it to your PATH; no administrator rights. Running commands on your computer needs Git for Windows (git-scm.com). No model or server runs on your computer.' : 'Installs AgentWorks in ~/.local/bin. No model or server runs on your computer.'}</p>
      </SetupStep>
      <SetupStep number={2} title="Connect your computer once">
        <CopyCommand label="start command" command={startCommand} />
        <p className="text-xs leading-5 text-muted-foreground">Run from any folder. Sign in with the same account you use here. AgentBridge and laptop access use the same CLI and saved login. No folder is shared automatically. It can stay connected in the background.</p>
      </SetupStep>
      <SetupStep number={3} title="Choose folders here">
        <p className="text-xs leading-5 text-muted-foreground">Choose your computer below, add folders and set Read or Read &amp; write. You can change these at any time. <code>agentworks status</code> checks the connection; <code>agentworks stop</code> stops laptop access and pauses AgentBridge messaging. Detaching one chat leaves the laptop connection available.</p>
      </SetupStep>
    </div>
  </details>
}

interface LaptopFolder { id: string; path: string; access: 'read' | 'write' }
interface LaptopSettings { device_id: string; folders: LaptopFolder[] }
interface LaptopDirectory { path: string; parent: string; folders: string[] }

/** The backend owns grants. No project manifest or browser preference grants laptop access. */
export function CodeLocalFilesSettings({ sessionId }: { sessionId: string; workspaceName?: string }) {
  const tab = useChatStore(state => Object.values(state.chatTabs).find(item => item.sessionId === sessionId))
  const project = tab?.metadata?.agentProfileProjectId
  const profile = tab?.metadata?.agentProfileId
  const endpoint = project && profile ? `/api/agent-profiles/${encodeURIComponent(profile)}/projects/${encodeURIComponent(project)}/laptop` : ''
  const [settings, setSettings] = useState<LaptopSettings>({ device_id: '', folders: [] })
  const [directory, setDirectory] = useState<LaptopDirectory>()
  const [path, setPath] = useState('')
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const { devices, error: deviceError, refreshing, refresh } = useLocalFileDevices(Boolean(endpoint))
  const latest = useLatestCliVersion(Boolean(endpoint))
  useEffect(() => {
    let active = true
    setLoaded(false); setDirectory(undefined); setSettings({ device_id: '', folders: [] }); setError(undefined)
    if (endpoint) void api.get<LaptopSettings>(endpoint, { skipSessionContext: true }).then(result => { if (active) { setSettings(result.data); setLoaded(true) } }).catch(cause => { if (active) setError(localFileError(cause)) })
    return () => { active = false }
  }, [endpoint])
  const device = devices.find(item => item.device_id === settings.device_id)
  const save = async (next: LaptopSettings) => {
    if (!endpoint) return
    setBusy(true); setError(undefined)
    try { const result = await api.put<LaptopSettings>(endpoint, next, { skipSessionContext: true }); setSettings(result.data); window.dispatchEvent(new Event('crew-laptop-settings-changed')); return true }
    catch (cause) { setError(localFileError(cause)); return false }
    finally { setBusy(false) }
  }
  const browse = async (next: string) => {
    setBusy(true); setError(undefined)
    try {
      const query = new URLSearchParams({ device_id: settings.device_id, path: next })
      const result = await api.get<LaptopDirectory>(`${endpoint}/folders?${query}`, { skipSessionContext: true })
      setDirectory(result.data); setPath(result.data.path)
    } catch (cause) { setError(localFileError(cause)) }
    finally { setBusy(false) }
  }
  const join = (root: string, child: string) => `${root.replace(/[\\/]$/, '')}${root.includes('\\') ? '\\' : '/'}${child}`
  return <SettingsCard unboxed icon={<Laptop className="h-4 w-4 text-primary" />} title="Laptop access" description="Connect your computer once, then choose the folders this Crew can use.">
    <div className="space-y-4">
      <p className="text-xs leading-5 text-muted-foreground">Crew runs on the server. Laptop access is available only to you in your Builder chat while the CLI is connected. Sharing this Crew does not share your computer. AgentBridge uses the same CLI.</p>
      <ComputerSetup connected={devices.length > 0} reconnecting={Boolean(settings.device_id)} />
      <label htmlFor="crew-laptop-device" className="block text-xs font-medium">Your computer</label>
      <select id="crew-laptop-device" value={settings.device_id} disabled={busy || !loaded} className="w-full rounded-md border border-border bg-background p-2 text-sm" onChange={event => { setDirectory(undefined); void save({ device_id: event.target.value, folders: [] }) }}>
        <option value="">No laptop access</option>
        {settings.device_id && !device && <option value={settings.device_id}>{settings.device_id} — Offline</option>}
        {devices.map(item => <option key={item.device_id} value={item.device_id}>{item.device_id}</option>)}
      </select>
      {settings.device_id && !device && <p role="status" className="text-xs text-muted-foreground">This computer is offline. Run <code>agentworks start</code> there. Server work continues.</p>}
      {cliUpdateNeeded(device, latest) && <p role="status" className="text-xs">Update once: <code>agentworks update</code>, then <code>agentworks stop</code> and <code>agentworks start</code>. Choose folders here.</p>}
      {settings.folders.map(folder => <div key={folder.id} className="flex items-center gap-2 rounded-md border border-border p-3">
        <Folder className="h-4 w-4 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1 break-all text-xs">{folder.path}</span>
        <select aria-label={`Access for ${folder.path}`} value={folder.access} disabled={busy || !device} className="rounded border border-border bg-background p-1 text-xs" onChange={event => void save({ ...settings, folders: settings.folders.map(item => item.id === folder.id ? { ...item, access: event.target.value as LaptopFolder['access'] } : item) })}>
          <option value="read">Read</option><option value="write">Read &amp; write</option>
        </select>
        <Button size="icon" variant="ghost" aria-label={`Remove ${folder.path}`} disabled={busy} onClick={() => void save({ ...settings, folders: settings.folders.filter(item => item.id !== folder.id) })}><X className="h-4 w-4" /></Button>
      </div>)}
      {device && <Button size="sm" variant="outline" disabled={busy || !device.settings_managed} onClick={() => void browse('')}>Add folder</Button>}
      {directory && <div className="space-y-2 rounded-md border border-border p-3">
        <form className="flex gap-2" onSubmit={event => { event.preventDefault(); void browse(path) }}><input aria-label="Folder path" value={path} onChange={event => setPath(event.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background p-2 text-xs" /><Button size="sm" variant="outline" disabled={busy}>Open</Button></form>
        <Button size="sm" variant="ghost" disabled={busy || directory.parent === directory.path} onClick={() => void browse(directory.parent)}><ArrowUp className="h-3 w-3" />Parent folder</Button>
        <div className="max-h-52 overflow-auto">{directory.folders.map(name => <button key={name} disabled={busy} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted" onClick={() => void browse(join(directory.path, name))}><Folder className="h-4 w-4" />{name}</button>)}</div>
        <div className="flex gap-2"><Button size="sm" disabled={busy || settings.folders.some(folder => folder.path === directory.path)} onClick={() => void save({ ...settings, folders: [...settings.folders, { id: '', path: directory.path, access: 'read' }] }).then(saved => { if (saved) setDirectory(undefined) })}>Allow this folder</Button><Button size="sm" variant="ghost" onClick={() => setDirectory(undefined)}>Close</Button></div>
      </div>}
      <p className="text-xs leading-5 text-muted-foreground">Read allows reading and copying files into Crew. Read &amp; write also allows local commands. Commands run as you on this computer: Windows uses your account permissions; macOS uses its existing light sandbox. Folder permissions are not full command isolation. Local output and copied files reach the server and may be saved in chat. Server secrets are not forwarded.</p>
      <Button size="sm" variant="ghost" disabled={refreshing} onClick={refresh}><RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />Check connection</Button>
      {(error || deviceError) && <p role="alert" className="text-sm text-destructive">{error || deviceError}</p>}
    </div>
  </SettingsCard>
}

export function CodeFilesPanel({ serverFiles }: { sessionId: string; serverFiles: ReactNode; onAsk: (message: string) => Promise<void> }) {
  return <div className="h-full min-h-0">{serverFiles}</div>
}
