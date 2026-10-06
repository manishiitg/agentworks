import { useState, type ReactNode } from 'react'
import { Check, Copy, Laptop } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { SettingsCard } from '../../components/ui/SettingsCard'
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
      <CopyCommand command="agentworks --config ~/.config/agentworks/executor.json executor connect --device my-computer --folder project=/absolute/path/to/project" />
      <p className="text-xs text-muted-foreground">Use --write-folder instead of --folder to allow edits. Keep the command running; Ctrl-C disconnects. Folders and permissions are approved on your computer.</p>
    </div>
  </details>
}

/** The connection belongs to this Code session's files, not its runtime. */
export function CodeLocalFilesSettings({ sessionId }: { sessionId: string }) {
  const preference = useCodeFilesPreference(sessionId)
  const { devices, error, checked } = useLocalFileDevices(preference.location === 'computer')
  const [settingError, setSettingError] = useState<string | null>(null)
  const selected = preference.location === 'computer' ? preference.target : undefined
  const resource = devices.find(device => device.device_id === selected?.device_id)?.resources.find(folder => folder.id === selected?.resource_id)
  const selectedKey = selected ? JSON.stringify([selected.device_id, selected.resource_id]) : ''
  const savePreference = (pref: Parameters<typeof writeCodeFilesPreference>[1]) => {
    try { writeCodeFilesPreference(sessionId, pref); setSettingError(null) }
    catch { setSettingError('This browser could not save the file connection.') }
  }
  return <SettingsCard icon={<Laptop className="h-4 w-4 text-primary" />} title="Local CLI connection" description="Local mode uses a minimal tool set. Connect your CLI and choose a shared folder; your agent and model run on the server.">
    {!sessionId ? <p className="text-sm text-muted-foreground">Open a Code chat to connect local files.</p> : preference.location === 'server' ?
      <Button variant="outline" onClick={() => savePreference({ location: 'computer' })}>Connect local files</Button> : <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span role="status" className="text-xs text-muted-foreground">{resource ? 'Connected' : !selected ? 'Choose a folder' : checked ? 'Offline' : 'Checking…'}</span>
          <Button size="sm" variant="outline" onClick={() => savePreference({ location: 'server' })}>Disconnect local files</Button>
        </div>
        <ComputerSetup />
        <label className="block text-xs font-medium" htmlFor="code-local-folder">Computer and shared folder</label>
        <select id="code-local-folder" aria-label="Computer and shared folder" value={selectedKey} className="w-full rounded-md border border-border bg-background p-2 text-sm" onChange={event => {
          if (!event.target.value) return
          const [device_id, resource_id] = JSON.parse(event.target.value) as string[]
          savePreference({ location: 'computer', target: { device_id, resource_id } })
        }}>
          <option value="">Choose a folder…</option>
          {selected && !resource && <option value={selectedKey}>{selected.device_id} / {selected.resource_id} — Offline</option>}
          {devices.flatMap(device => device.resources.map(folder => <option key={`${device.device_id}/${folder.id}`} value={JSON.stringify([device.device_id, folder.id])}>{device.device_id} / {folder.id} — {folder.writable ? 'Can edit' : 'Read only'}</option>))}
        </select>
        <p className="text-xs text-muted-foreground">This browser remembers the connection for this chat only. The right side shows only this CLI connection, Costs and Models. File access is through the agent in chat. MCP connections, skills, secrets and background agents are disabled in Local mode, including before folder selection. Other Code chats keep their own file access. Disconnect here to return this chat to server files; Ctrl-C in the CLI stops sharing the computer folder.</p>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>}
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
