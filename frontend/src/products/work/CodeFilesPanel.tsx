import { useEffect, useState, type ReactNode } from 'react'
import { Check, Copy, Laptop, RefreshCw, Terminal } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { getApiBaseUrl } from '../../services/api'
import { cliUpdateNeeded, useCodeFilesPreference, useLatestCliVersion, useLocalFileDevices, writeCodeFilesPreference } from './codeLocalFiles'

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
  const name = workspaceName || 'your workspace name'
  const installCommand = windows
    ? `& ([scriptblock]::Create((irm ${psQuote(`${base}/api/downloads/cli/install-agentworks.ps1`)}))) -Server ${psQuote(base)} -NoLogin`
    : `curl -fsSL ${shellQuote(`${base}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${shellQuote(base)} --no-login`
  const startCommand = windows
    ? `agentworks start --server ${psQuote(base)} --workspace ${psQuote(name)}`
    : `agentworks start --server ${shellQuote(base)} --workspace ${shellQuote(name)}`
  return <details className="group" open={!connected && !reconnecting}>
    <summary className="flex cursor-pointer items-center gap-2 px-4 py-3 text-sm font-medium"><Terminal className="h-4 w-4 text-muted-foreground" />{connected ? 'Set up another computer' : reconnecting ? 'CLI setup and reconnect' : 'Set up your computer'}<span className="ml-auto text-xs font-normal text-muted-foreground">macOS · Linux · Windows</span></summary>
    <div className="space-y-5 border-t border-border px-4 py-4">
      <div role="group" aria-label="Your computer" className="flex gap-1.5">
        <Button size="sm" variant={windows ? 'ghost' : 'secondary'} aria-pressed={!windows} onClick={() => setWindows(false)}>macOS / Linux</Button>
        <Button size="sm" variant={windows ? 'secondary' : 'ghost'} aria-pressed={windows} onClick={() => setWindows(true)}>Windows</Button>
      </div>
      <p className="text-xs leading-5 text-muted-foreground">Run these commands on your own computer{windows ? ' in PowerShell' : ''}. Your model stays on this server; the CLI connects your project folder. Already installed? Start at step 2.</p>
      <SetupStep number={1} title="Install the CLI">
        <CopyCommand label="install command" command={installCommand} />
        <p className="text-xs text-muted-foreground">{windows ? 'Installs AgentWorks in %LOCALAPPDATA%\\agentworks and adds it to your PATH; no administrator rights. Running commands on your computer needs Git for Windows (git-scm.com). No model or server runs on your computer.' : 'Installs AgentWorks in ~/.local/bin. No model or server runs on your computer.'}</p>
      </SetupStep>
      <SetupStep number={2} title="Go to your project folder and start">
        <CopyCommand label="start command" command={startCommand} />
        <p className="text-xs leading-5 text-muted-foreground">It shares the folder you are in with this workspace (the name after --workspace, as shown in Settings), with read and write access, and the agent can run commands there. The CLI remembers the workspace for that folder. The first time it opens a browser to approve this computer. It then asks whether to run in the background or keep the terminal open, and opens this website. Next time just run <code className="font-mono">agentworks start</code>.</p>
      </SetupStep>
      <SetupStep number={3} title="Check it, stop it, or fix it">
        <p className="text-xs leading-5 text-muted-foreground">Press <span className="font-medium">Verify connection</span> below to confirm this computer is connected. <code className="font-mono">agentworks stop</code> ends sharing for the current folder, <code className="font-mono">agentworks status</code> lists what is shared, <code className="font-mono">agentworks watch</code> follows a background share, and <code className="font-mono">agentworks debug</code> writes one file to send to support. <code className="font-mono">agentworks start --debug</code> prints diagnostics and every request from the server. Add <code className="font-mono">--block .env</code> to hide a file from the file tools, or <code className="font-mono">--downloads</code> to also share your Downloads folder.</p>
        {windows && <p className="text-xs leading-5 text-muted-foreground">On Windows, commands the agent runs are not sandboxed: they run with your account&rsquo;s permissions. The folder rules protect file reads and edits only.</p>}
      </SetupStep>
    </div>
  </details>
}

/** Optional laptop tools; the Crew's normal files and terminal stay on the server. */
export function CodeLocalFilesSettings({ sessionId, workspaceName }: { sessionId: string; workspaceName?: string }) {
  const preference = useCodeFilesPreference(sessionId)
  const selected = preference.location === 'computer' ? preference.target : undefined
  const selectedKey = selected ? JSON.stringify([selected.device_id, selected.resource_id]) : ''
  const [draftKey, setDraftKey] = useState(selectedKey)
  const [setupOpen, setSetupOpen] = useState(false)
  const [settingError, setSettingError] = useState<string | null>(null)
  const { devices, error, checked, refreshing, refresh } = useLocalFileDevices(Boolean(sessionId))
  const latest = useLatestCliVersion(Boolean(sessionId))
  useEffect(() => { setDraftKey(selectedKey); setSettingError(null) }, [sessionId, selectedKey])
  const device = devices.find(item => item.device_id === selected?.device_id)
  const folder = device?.resources.find(item => item.id === selected?.resource_id)
  const available = devices.flatMap(item => item.resources.filter(resource => resource.id !== 'downloads').map(resource => ({ device_id: item.device_id, resource_id: resource.id })))
  const draft = available.find(item => JSON.stringify([item.device_id, item.resource_id]) === draftKey)
  const save = (target?: { device_id: string; resource_id: string }) => {
    try {
      writeCodeFilesPreference(sessionId, target ? { location: 'computer', target } : { location: 'server' })
      setSettingError(null)
    } catch { setSettingError('Could not save this laptop connection.') }
  }
  return <SettingsCard unboxed icon={<Laptop className="h-4 w-4 text-primary" />} title="Laptop connection" description="Your Crew runs on the server. Optionally let your Builder chat run commands on your computer and copy local files into this Crew.">
    <div className="space-y-3">
      <p className="text-xs leading-5 text-muted-foreground">Files, Terminal, connections, functions and background work continue on the server. Laptop tools are available only to you in your Builder chat, while the CLI is connected. Sharing the Crew does not share your computer. Files are copied only when requested; there is no sync.</p>
      {selected && <div className="flex items-center justify-between gap-2 rounded-md bg-muted/40 p-3">
        <div><p role="status" className="text-sm font-medium">{folder ? 'Connected' : checked ? 'Laptop not connected' : 'Checking connection…'}</p><p className="text-xs text-muted-foreground">{selected.device_id} · {selected.resource_id}</p></div>
        <Button size="sm" variant="outline" onClick={() => save()}>Disconnect laptop</Button>
      </div>}
      {selected && checked && !folder && <p className="text-xs text-muted-foreground">Server work remains available. To use laptop tools again, open your shared folder on that computer and start AgentWorks.</p>}
      {cliUpdateNeeded(device, latest) && <p role="status" className="rounded-md bg-amber-500/10 p-3 text-xs">Update the CLI on your computer with <code>agentworks update</code>, then run <code>agentworks stop</code> and <code>agentworks start</code>. Choose the connected folder again here after updating.</p>}
      {!setupOpen ? <Button variant="outline" disabled={!sessionId} onClick={() => setSetupOpen(true)}>{selected ? 'Change connected folder' : 'Connect your computer'}</Button> : <>
        <ComputerSetup connected={devices.length > 0} reconnecting={Boolean(selected)} workspaceName={workspaceName} />
        <label htmlFor="crew-laptop-folder" className="block text-xs font-medium">Your computer and shared folder</label>
        <select id="crew-laptop-folder" value={draftKey} className="w-full rounded-md border border-border bg-background p-2 text-sm" onChange={event => setDraftKey(event.target.value)}>
          <option value="">Choose a connected folder…</option>
          {selected && !folder && <option value={selectedKey}>{selected.device_id} / {selected.resource_id} — Offline</option>}
          {available.map(item => <option key={JSON.stringify(item)} value={JSON.stringify([item.device_id, item.resource_id])}>{item.device_id} / {item.resource_id}</option>)}
        </select>
        <p className="text-xs leading-5 text-muted-foreground">The local command tool uses your existing CLI permissions. Commands can reach your computer’s services and network. Returned output and copied files reach the server and model and may be saved in chat history. Server secrets are not forwarded.</p>
        <div className="flex gap-2"><Button size="sm" disabled={!draft || draftKey === selectedKey} onClick={() => { if (draft) save(draft) }}>Connect folder</Button><Button size="sm" variant="ghost" onClick={() => setSetupOpen(false)}>Close setup</Button></div>
      </>}
      <Button size="sm" variant="ghost" disabled={refreshing} onClick={refresh}><RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />Check connection</Button>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {settingError && <p role="alert" className="text-sm text-destructive">{settingError}</p>}
    </div>
  </SettingsCard>
}

export function CodeFilesPanel({ serverFiles }: { sessionId: string; serverFiles: ReactNode; onAsk: (message: string) => Promise<void> }) {
  return <div className="h-full min-h-0">{serverFiles}</div>
}
