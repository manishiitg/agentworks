import { useEffect, useState, type ReactNode } from 'react'
import { Check, Copy, Download, FolderOpen, Laptop, Loader2, RefreshCw, Terminal, WifiOff } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { useChatStore } from '../../stores/useChatStore'
import { getApiBaseUrl } from '../../services/api'
import { useCodeFilesPreference, useLocalFileDevices, writeCodeFilesPreference } from './codeLocalFiles'

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
    <div className={`flex items-start gap-2 rounded-lg border border-border bg-muted/40 p-3 ${disabled ? 'opacity-50' : ''}`}>
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

function ComputerSetup({ connected, reconnecting }: { connected: boolean; reconnecting: boolean }) {
  const base = getApiBaseUrl() || window.location.origin
  const cli = '"$HOME/.local/bin/agentworks"'
  return <details className="group rounded-xl border border-border bg-background" open={!connected && !reconnecting}>
    <summary className="flex cursor-pointer items-center gap-2 px-4 py-3 text-sm font-medium"><Terminal className="h-4 w-4 text-muted-foreground" />{connected ? 'Set up another computer' : reconnecting ? 'CLI setup and reconnect' : 'Set up your computer'}<span className="ml-auto text-xs font-normal text-muted-foreground">macOS · Linux</span></summary>
    <div className="space-y-5 border-t border-border px-4 py-4">
      <p className="text-xs leading-5 text-muted-foreground">Run these commands on your own computer. Your model stays on this server; the CLI connects your project folder. Already installed? Start at step 2.</p>
      <SetupStep number={1} title="Install the CLI">
        <CopyCommand label="install command" command={`curl -fsSL ${shellQuote(`${base}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${shellQuote(base)} --no-login`} />
        <p className="text-xs text-muted-foreground">Installs AgentWorks in ~/.local/bin. No model or server runs on your computer.</p>
      </SetupStep>
      <SetupStep number={2} title="Go to your project folder and start">
        <CopyCommand label="start command" command={`cd /path/to/your/project && ${cli} start --server ${shellQuote(base)}`} />
        <p className="text-xs leading-5 text-muted-foreground">It shares the folder you are in with read and write access, and the agent can run commands there. The first time it opens a browser to approve this computer. It then asks whether to run in the background or keep the terminal open, and whether to open this website. Next time just run <code className="font-mono">agentworks start</code>.</p>
      </SetupStep>
      <SetupStep number={3} title="Check it, stop it, or fix it">
        <p className="text-xs leading-5 text-muted-foreground">Press <span className="font-medium">Verify connection</span> below to confirm this computer is connected. <code className="font-mono">agentworks stop</code> ends sharing for the current folder, <code className="font-mono">agentworks status</code> lists what is shared, and <code className="font-mono">agentworks start --debug</code> prints diagnostics and every request from the server if something does not work. Add <code className="font-mono">--block .env</code> to hide a file, or <code className="font-mono">--downloads</code> to also share ~/Downloads.</p>
      </SetupStep>
    </div>
  </details>
}

/** The connection belongs to this Code session's files, not its runtime. */
export function CodeLocalFilesSettings({ sessionId }: { sessionId: string }) {
  const preference = useCodeFilesPreference(sessionId)
  const local = preference.location === 'computer'
  const selected = local ? preference.target : undefined
  const selectedKey = selected ? JSON.stringify([selected.device_id, selected.resource_id]) : ''
  const [setupOpen, setSetupOpen] = useState(false)
  const [draftKey, setDraftKey] = useState(selectedKey)
  const [confirmServer, setConfirmServer] = useState(false)
  const [settingError, setSettingError] = useState<string | null>(null)
  const [verified, setVerified] = useState(false)
  const busy = useChatStore(state => Object.values(state.chatTabs).some(tab => tab.sessionId === sessionId && (tab.isStreaming || tab.hasRunningBgAgents)))
  const { devices, error, checked, refreshing, refresh } = useLocalFileDevices(local || setupOpen)
  useEffect(() => { setDraftKey(selectedKey); setSetupOpen(false); setConfirmServer(false); setSettingError(null) }, [sessionId, selectedKey, local])
  const resource = devices.find(device => device.device_id === selected?.device_id)?.resources.find(folder => folder.id === selected?.resource_id)
  const draftResource = devices.flatMap(device => device.resources.map(folder => ({ ...folder, deviceId: device.device_id }))).find(folder => JSON.stringify([folder.deviceId, folder.id]) === draftKey)
  const savePreference = (pref: Parameters<typeof writeCodeFilesPreference>[1]) => {
    if (busy) return
    try { writeCodeFilesPreference(sessionId, pref); setSettingError(null) }
    catch { setSettingError('This browser could not save the file connection.') }
  }
  const applyFolder = () => {
    if (!draftResource) return
    savePreference({ location: 'computer', target: { device_id: draftResource.deviceId, resource_id: draftResource.id } })
  }
  return <SettingsCard icon={<Laptop className="h-4 w-4 text-primary" />} title="Local CLI connection" description="Choose where this chat works with files. Your agent and model always run on the server.">
    {!sessionId ? <p className="text-sm text-muted-foreground">Open a Code chat to connect local files.</p> : !local && !setupOpen ?
      <Button variant="outline" disabled={busy} onClick={() => setSetupOpen(true)}>Connect local files</Button> : <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2.5">
            <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${resource || !local && devices.length ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'bg-muted text-muted-foreground'}`}>{resource || !local && devices.length ? <Laptop className="h-4 w-4" /> : !checked ? <Loader2 className="h-4 w-4 animate-spin" /> : selected ? <WifiOff className="h-4 w-4" /> : <Terminal className="h-4 w-4" />}</span>
            <div><p role="status" className="text-sm font-medium">{local ? resource ? 'Connected' : !selected ? 'Choose a folder' : checked ? 'Offline' : 'Checking connection…' : devices.length ? 'Your CLI is connected' : checked ? 'Waiting for your CLI' : 'Checking connection…'}</p><p className="text-xs text-muted-foreground">{resource ? `${selected?.device_id} · ${resource.id}` : local ? 'Local mode stays active.' : 'Server files stay active until you confirm a folder.'}</p></div>
          </div>
          {local ? <Button size="sm" variant="outline" disabled={busy} onClick={() => setConfirmServer(true)}>Disconnect local files</Button> : <Button size="sm" variant="ghost" onClick={() => { setSetupOpen(false); setDraftKey('') }}>Cancel setup</Button>}
        </div>
        {confirmServer && <div className="space-y-3 rounded-lg border border-border bg-muted/30 p-3">
          <p className="text-sm font-medium">Switch this chat to server files?</p>
          <p className="text-xs leading-5 text-muted-foreground">Future file edits and commands will use the server workspace. This chat’s normal Code features, including MCP connections, skills, secrets, integrations, schedules and dashboards, become available again. Switching does not move or sync your laptop project. Earlier messages and tool results remain in server chat history. The CLI keeps running until you stop it with Ctrl-C.</p>
          <div className="flex gap-2"><Button size="sm" disabled={busy} onClick={() => savePreference({ location: 'server' })}>Switch to server files</Button><Button size="sm" variant="ghost" onClick={() => setConfirmServer(false)}>Keep local connection</Button></div>
        </div>}
        {local && resource && <div className="flex flex-wrap gap-2 rounded-lg border border-emerald-500/20 bg-emerald-500/5 p-3 text-xs"><span className="flex items-center gap-1.5"><FolderOpen className="h-3.5 w-3.5" />{resource.writable ? 'Project: read and write' : 'Project: read only'}</span>{resource.downloads && <span className="flex items-center gap-1.5"><Download className="h-3.5 w-3.5" />Downloads: read and write</span>}</div>}
        {local && selected && checked && !resource && !error && <div className="space-y-1 rounded-lg border border-border bg-muted/30 p-3"><p className="text-xs font-medium">Reconnect your computer to continue file work</p><p className="text-xs leading-5 text-muted-foreground">Wake your computer, check its network and keep the CLI running. It reconnects automatically. Conversation can continue; files will not switch to the server.</p></div>}
        <ComputerSetup connected={devices.length > 0} reconnecting={!!selected} />
        <div className="space-y-1">
          <div className="flex items-center justify-between gap-2"><p className="text-xs text-muted-foreground">{devices.length ? `${devices.length} computer${devices.length === 1 ? '' : 's'} available · updates automatically` : 'Connected computers appear here automatically.'}</p><Button size="sm" variant="outline" disabled={refreshing} onClick={() => { setVerified(true); refresh() }}><RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />Verify connection</Button></div>
          {verified && checked && !refreshing && <p role="status" className={`text-xs leading-5 ${devices.length ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}`}>{error ? error : devices.length ? `Connected: ${devices.flatMap(device => device.resources.map(folder => `${device.device_id} / ${folder.id}`)).join(', ')}.` : 'Nothing is connected yet. Run agentworks start inside your project folder; if it does not connect, agentworks start --debug shows why.'}</p>}
        </div>
        <label className="block text-xs font-medium" htmlFor="code-local-folder">Computer and shared folder</label>
        <select id="code-local-folder" aria-label="Computer and shared folder" value={draftKey} disabled={busy} className="w-full rounded-md border border-border bg-background p-2 text-sm" onChange={event => { setDraftKey(event.target.value); setConfirmServer(false) }}>
          <option value="">Choose a folder…</option>
          {selected && !resource && <option value={selectedKey}>{selected.device_id} / {selected.resource_id} — Offline</option>}
          {devices.flatMap(device => device.resources.map(folder => <option key={`${device.device_id}/${folder.id}`} value={JSON.stringify([device.device_id, folder.id])}>{device.device_id} / {folder.id} — {folder.shell ? folder.writable ? 'Files and commands' : 'Read-only files and commands' : folder.writable ? 'Can edit' : 'Read only'}</option>))}
        </select>
        {draftResource && draftKey !== selectedKey && <div className="space-y-3 rounded-lg border border-border bg-muted/30 p-3">
          <p className="text-sm font-medium">{local ? 'Change this chat’s local folder' : 'Use local files for this chat'}</p>
          <ul className="list-disc space-y-1 pl-4 text-xs leading-5 text-muted-foreground">
            <li>File access and shell commands will use {draftResource.deviceId} / {draftResource.id}. {draftResource.writable ? 'The agent can change files and run builds, tests and git commands within your CLI permissions.' : 'This folder is read only: the agent can inspect files and run commands that do not change them.'}</li>
            {draftResource.downloads && <li>Downloads is also shared with read and write access. The agent can inspect, create and change files there alongside your project.</li>}
            <li>Commands can access the internet, services on your computer (localhost), and your local network. Folder permissions limit file access; they do not limit network destinations.</li>
            <li>Your agent and model stay on the server. File contents and command output are sent to the server and model provider and may be saved in server chat history. People with authorized administrator or Code review access can read that history.</li>
            <li>MCP connections, reusable skills, project secrets, integrations, schedules, dashboards, browser tools and background agents are unavailable in Local mode.</li>
            <li>{local ? 'The previous folder will no longer be used by this chat; files are not moved.' : 'Server files will no longer be used by this chat; files are not moved.'} If the CLI disconnects, local actions fail until it reconnects. Other chats and existing server schedules stay unchanged.</li>
          </ul>
          <Button size="sm" disabled={busy} onClick={applyFolder}>Use this folder</Button>
        </div>}
        {resource && <p className="text-xs text-muted-foreground">{resource.shell ? 'Shell commands enabled on this computer for the selected folder.' : 'Update and reconnect your CLI to enable shell commands for this folder.'}</p>}
        <p className="text-xs leading-5 text-muted-foreground">This connection applies to this chat only. Change it here in the right-side panel. The composer shows its current connection; it cannot switch modes. In Local mode, the right side shows only Local CLI connection, Costs and Models. Ctrl-C in the CLI stops sharing your folder.</p>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>}
    {busy && <p className="text-xs text-muted-foreground">Wait for the current turn to finish before changing the file connection.</p>}
    {settingError && <p role="alert" className="text-sm text-destructive">{settingError}</p>}
  </SettingsCard>
}

export function CodeFilesPanel({ sessionId, serverFiles }: { sessionId: string; serverFiles: ReactNode; onAsk: (message: string) => Promise<void> }) {
  const preference = useCodeFilesPreference(sessionId)
  if (preference.location === 'computer') return <div className="h-full overflow-y-auto p-4"><CodeLocalFilesSettings sessionId={sessionId} /></div>
  // Server files are the normal case: no header, no label. The connection settings stay in the right-side settings tab.
  return <div className="h-full min-h-0">{serverFiles}</div>
}
