import { useEffect, useState, type ReactNode } from 'react'
import { Check, Copy, Laptop } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { useChatStore } from '../../stores/useChatStore'
import { getApiBaseUrl } from '../../services/api'
import { useCodeFilesPreference, useLocalFileDevices, writeCodeFilesPreference } from './codeLocalFiles'

function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false)
  return <div className="flex items-start gap-2 rounded-md border border-border bg-muted/40 p-2">
    <code className="min-w-0 flex-1 break-all text-xs">{command}</code>
    <Button size="icon" variant="ghost" aria-label="Copy command" onClick={() => void navigator.clipboard.writeText(command).then(() => setCopied(true)).catch(() => setCopied(false))}>{copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}</Button>
  </div>
}
function ComputerSetup() {
  const base = getApiBaseUrl() || window.location.origin
  const quoted = `'${base.replace(/'/g, "'\\''")}'`
  return <details className="rounded-lg border border-border p-3" open>
    <summary className="cursor-pointer text-sm font-medium">Connect your computer</summary>
    <div className="mt-3 space-y-3 text-sm">
      <p>Run these commands in your computer’s terminal (macOS or Linux). First install the AgentWorks CLI:</p>
      <CopyCommand command={`curl -fsSL '${`${base}/api/downloads/cli/install-agentworks.sh`.replace(/'/g, "'\\''")}' | sh -s -- --server ${quoted} --no-login`} />
      <CopyCommand command={`agentworks --config ~/.config/agentworks/executor.json login --server ${quoted} --scopes devices:connect`} />
      <p>Approve the browser sign-in. Replace the path below with your project folder:</p>
      <CopyCommand command="agentworks --config ~/.config/agentworks/executor.json executor connect --device my-computer --write-folder project=/absolute/path/to/project" />
      <p className="text-xs text-muted-foreground">This allows file edits and sandboxed commands such as builds and tests. Shell commands are enabled automatically for every shared folder. Use --folder instead of --write-folder to allow inspection without file changes. Keep the command running; Ctrl-C disconnects. Folders and permissions are approved on your computer.</p>
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
  const { devices, error, checked } = useLocalFileDevices(local || setupOpen)
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
          <span role="status" className="text-xs text-muted-foreground">{local ? resource ? 'Connected' : !selected ? 'Choose a folder' : checked ? 'Offline' : 'Checking…' : 'Server files active · setting up local connection'}</span>
          {local ? <Button size="sm" variant="outline" disabled={busy} onClick={() => setConfirmServer(true)}>Disconnect local files</Button> : <Button size="sm" variant="ghost" onClick={() => { setSetupOpen(false); setDraftKey('') }}>Cancel setup</Button>}
        </div>
        {confirmServer && <div className="space-y-3 rounded-lg border border-border bg-muted/30 p-3">
          <p className="text-sm font-medium">Switch this chat to server files?</p>
          <p className="text-xs leading-5 text-muted-foreground">Future file edits and commands will use the server workspace. This chat’s normal Code features, including MCP connections, skills, secrets, integrations, schedules and dashboards, become available again. Switching does not move or sync your laptop project. Earlier messages and tool results remain in server chat history. The CLI keeps running until you stop it with Ctrl-C.</p>
          <div className="flex gap-2"><Button size="sm" disabled={busy} onClick={() => savePreference({ location: 'server' })}>Switch to server files</Button><Button size="sm" variant="ghost" onClick={() => setConfirmServer(false)}>Keep local connection</Button></div>
        </div>}
        <ComputerSetup />
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
