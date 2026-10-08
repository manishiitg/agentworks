import { useEffect, useState, type ReactNode } from 'react'
import { Check, Copy, Download, FolderOpen, Laptop, Loader2, RefreshCw, Terminal, WifiOff } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { Input } from '../../components/ui/Input'
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
  const [folder, setFolder] = useState('')
  const [device, setDevice] = useState('my-computer')
  const [writable, setWritable] = useState(true)
  const [downloads, setDownloads] = useState(false)
  const value = folder.trim()
  const validFolder = value.startsWith('/') && value !== '/' || value.startsWith('~/') && value.length > 2
  const validDevice = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(device)
  const project = value.startsWith('~/') ? `"$HOME"/${shellQuote(value.slice(2))}` : shellQuote(value)
  const cli = '"$HOME/.local/bin/agentworks" --config "$HOME/.config/agentworks/executor.json"'
  const command = `${cli} executor connect --device ${shellQuote(device)} ${writable ? '--write-folder' : '--folder'} project=${validFolder ? project : '/absolute/path/to/project'}${downloads ? ' --downloads' : ''}`
  return <details className="group rounded-xl border border-border bg-background" open={!connected && !reconnecting}>
    <summary className="flex cursor-pointer items-center gap-2 px-4 py-3 text-sm font-medium"><Terminal className="h-4 w-4 text-muted-foreground" />{connected ? 'Set up another computer' : reconnecting ? 'CLI setup and reconnect' : 'Set up your computer'}<span className="ml-auto text-xs font-normal text-muted-foreground">macOS · Linux</span></summary>
    <div className="space-y-5 border-t border-border px-4 py-4">
      <p className="text-xs leading-5 text-muted-foreground">Run these commands on your own computer. Your model stays on this server; the CLI connects your files. Already installed? Start at step 2.</p>
      <SetupStep number={1} title="Install the CLI">
        <CopyCommand label="install command" command={`curl -fsSL ${shellQuote(`${base}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${shellQuote(base)} --no-login`} />
        <p className="text-xs text-muted-foreground">Installs AgentWorks in ~/.local/bin. No model or server runs on your computer.</p>
      </SetupStep>
      <SetupStep number={2} title="Sign in to this server">
        <CopyCommand label="sign-in command" command={`${cli} login --server ${shellQuote(base)} --scopes devices:connect`} />
        <p className="text-xs leading-5 text-muted-foreground">A browser opens. Approve the local connection, then return to your terminal. This sign-in is separate from remote MCP access.</p>
      </SetupStep>
      <SetupStep number={3} title="Choose what to share">
        <div className="space-y-3">
          <div className="space-y-1"><label className="text-xs font-medium" htmlFor="local-project-folder">Project folder on your computer</label><Input id="local-project-folder" value={folder} onChange={event => setFolder(event.target.value)} placeholder="~/Projects/my-app" autoComplete="off" spellCheck={false} /><p className="text-xs text-muted-foreground">Use an absolute path or ~/… . Paths with spaces are supported.</p>{value && !validFolder && <p className="text-xs text-destructive">Enter a project path, not your home folder or /.</p>}</div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1"><label className="text-xs font-medium" htmlFor="local-computer-name">Computer name</label><Input id="local-computer-name" value={device} onChange={event => setDevice(event.target.value)} autoComplete="off" spellCheck={false} />{!validDevice && <p className="text-xs text-destructive">Use letters, numbers, dashes or underscores.</p>}</div>
            <div className="space-y-1"><label className="text-xs font-medium" htmlFor="local-project-access">Project access</label><select id="local-project-access" className="h-9 w-full rounded-md border border-border bg-background px-2 text-xs" value={writable ? 'write' : 'read'} onChange={event => setWritable(event.target.value === 'write')}><option value="write">Read and write</option><option value="read">Read only</option></select></div>
          </div>
          <label className="flex cursor-pointer items-start gap-2 rounded-lg border border-border p-3"><input type="checkbox" className="mt-0.5" checked={downloads} onChange={event => setDownloads(event.target.checked)} /><span><span className="flex items-center gap-1.5 text-xs font-medium"><Download className="h-3.5 w-3.5" />Also share Downloads</span><span className="mt-1 block text-xs leading-5 text-muted-foreground">Allow reading and changing files in ~/Downloads alongside this project. Off unless you enable it.</span></span></label>
          <CopyCommand label="connection command" command={command} disabled={!validFolder || !validDevice} />
          <p className="text-xs leading-5 text-muted-foreground">{!validFolder ? 'Enter your project folder to copy a ready-to-run command. ' : ''}Shell commands are enabled automatically. Read-only access blocks changes to the project. Keep this terminal running; Ctrl-C disconnects.</p>
        </div>
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
        <div className="flex items-center justify-between gap-2"><p className="text-xs text-muted-foreground">{devices.length ? `${devices.length} computer${devices.length === 1 ? '' : 's'} available · updates automatically` : 'Connected computers appear here automatically.'}</p><Button size="sm" variant="ghost" disabled={refreshing} onClick={refresh}><RefreshCw className={`h-3.5 w-3.5 ${refreshing ? 'animate-spin' : ''}`} />Check connection</Button></div>
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

export function CodeFilesPanel({ sessionId, serverFiles, onManageConnection }: { sessionId: string; serverFiles: ReactNode; onAsk: (message: string) => Promise<void>; onManageConnection: () => void }) {
  const preference = useCodeFilesPreference(sessionId)
  if (preference.location === 'computer') return <div className="h-full overflow-y-auto p-4"><CodeLocalFilesSettings sessionId={sessionId} /></div>
  return <div className="flex h-full min-h-0 flex-col">
    <div className="flex items-center gap-2 border-b border-border px-3 py-2">
      <span className="flex-1 text-xs font-medium">Server files</span>
      <Button size="sm" variant="ghost" onClick={onManageConnection}>Manage file connection</Button>
    </div>
    <div className="min-h-0 flex-1">{serverFiles}</div>
  </div>
}
