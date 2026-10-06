import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Copy, Folder, Laptop, RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { codeLocalFilesApi, localFileError, useCodeFilesPreference, writeCodeFilesPreference, type CodeLocalFileTarget, type LocalFile, type LocalFileDevice, type LocalFileEntry, type LocalFileReceipt } from './codeLocalFiles'

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

export function CodeServerTerminalNotice({ workspacePath }: { workspacePath: string }) {
  const preference = useCodeFilesPreference(workspacePath)
  return preference.location === 'computer' ? <p className="border-b border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">Server terminal · This terminal cannot run commands in your selected computer folder. The local connection supports files only.</p> : null
}

function LocalFolderFiles({ target, writable, connected, guardHint, onAsk }: { target: CodeLocalFileTarget; writable: boolean; connected: boolean; guardHint: string; onAsk: (message: string) => Promise<void> }) {
  const [directory, setDirectory] = useState('.')
  const [entries, setEntries] = useState<LocalFileEntry[]>([])
  const [file, setFile] = useState<LocalFile | null>(null)
  const [content, setContent] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [receipt, setReceipt] = useState<LocalFileReceipt | null>(null)
  // Keep a failed write's exact ID and payload for safe reconciliation.
  const pendingWrite = useRef<{ content: string; expected_revision: string; request_id: string } | null>(null)
  const generation = useRef(0)
  const load = useCallback(async (path: string, operation: 'list' | 'read') => {
    const current = ++generation.current
    setBusy(true); setError(null)
    try {
      const result = await codeLocalFilesApi.call(target, operation, path)
      if (generation.current !== current) return
      if (operation === 'list') { setDirectory(path); setEntries(result.entries || []); setFile(null) }
      else { setFile(result.file || null); setContent(result.file?.content || '') }
      setReceipt(null); pendingWrite.current = null
    } catch (cause) { if (generation.current === current) setError(localFileError(cause)) }
    finally { if (generation.current === current) setBusy(false) }
  }, [target])
  useEffect(() => { void load('.', 'list'); return () => { generation.current += 1 } }, [load])
  const save = async () => {
    if (!file) return
    const current = generation.current
    setBusy(true); setError(null)
    const write = pendingWrite.current || { content, expected_revision: file.revision, request_id: crypto.randomUUID() }
    pendingWrite.current = write
    try {
      const result = await codeLocalFilesApi.call(target, 'write', file.path, write)
      if (generation.current !== current) return
      if (!result.receipt) throw new Error('Write receipt unavailable; retry to reconcile.')
      setFile({ ...file, content: write.content, revision: result.receipt.revision }); setContent(write.content)
      setReceipt(result.receipt); pendingWrite.current = null
    } catch (cause) { if (generation.current === current) setError(`${localFileError(cause)} Retry uses the same write request. Reload to check the current file before making another edit.`) }
    finally { if (generation.current === current) setBusy(false) }
  }
  return <div className="flex min-h-0 flex-1 flex-col gap-3">
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span>{writable ? 'Can edit' : 'Read only'} · {guardHint}</span>
      <span>Agent and LLM run on the server. Requested file contents reach the server and LLM.</span>
    </div>
    <p className="text-xs text-muted-foreground">The terminal and browser run on the server. Local builds and tests are not available in this connection.</p>
    <div className="flex items-center gap-2">
      <Button size="sm" variant="outline" disabled={busy || !connected || (!file && directory === '.')} onClick={() => void load(file ? directory : directory.split('/').slice(0, -1).join('/') || '.', 'list')}>{file ? 'Folder' : 'Up'}</Button>
      <span className="min-w-0 flex-1 truncate font-mono text-xs">{file?.path || directory}</span>
      <Button size="sm" variant="ghost" disabled={busy || !connected} aria-label="Reload local files" onClick={() => void load(file?.path || directory, file ? 'read' : 'list')}><RefreshCw className={`h-4 w-4 ${busy ? 'animate-spin' : ''}`} /></Button>
      {file && <Button size="sm" variant="outline" disabled={busy || !connected} onClick={() => void onAsk(`Look at ${JSON.stringify(file.path)} in my selected local Code folder (${target.resource_id} on ${target.device_id}). Use the local file tools.`).catch(cause => setError(localFileError(cause)))}>Ask Code</Button>}
    </div>
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {file ? <>
      <textarea aria-label="Local file content" spellCheck={false} value={content} readOnly={!writable || !connected || busy || pendingWrite.current !== null} onChange={event => setContent(event.target.value)} className="min-h-64 flex-1 resize-none rounded-md border border-border bg-background p-3 font-mono text-xs" />
      {writable && <Button size="sm" disabled={busy || !connected || (content === (file.content || '') && !pendingWrite.current)} onClick={() => void save()}>{pendingWrite.current ? 'Retry save' : 'Save to computer'}</Button>}
      {receipt && <p role="status" className="text-xs text-muted-foreground">Saved by {receipt.identity.username || receipt.identity.user_id || 'you'} · revision {receipt.revision.slice(0, 12)}</p>}
    </> : <div className="min-h-0 flex-1 overflow-auto rounded-md border border-border">
      {!busy && entries.length === 0 && !error && <p className="p-4 text-sm text-muted-foreground">No visible files in this folder.</p>}
      {entries.map(entry => <button key={entry.path} disabled={busy || !connected} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted" onClick={() => void load(entry.path, entry.type === 'folder' || entry.type === 'directory' ? 'list' : 'read')}>
        {(entry.type === 'folder' || entry.type === 'directory') && <Folder className="h-4 w-4 shrink-0" />}<span className="truncate">{entry.path.split('/').pop()}</span>
      </button>)}
    </div>}
  </div>
}

/** Code alone offers this source selection; Crew and other products keep their existing Files view. */
export function CodeFilesPanel({ workspacePath, serverFiles, onAsk }: { workspacePath: string; serverFiles: ReactNode; onAsk: (message: string) => Promise<void> }) {
  const accountCanEdit = useAuthStore(state => state.user?.can_edit !== false && state.user?.role !== 'viewer')
  const accountID = useAuthStore(state => state.user?.id)
  const workspaceID = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const preference = useCodeFilesPreference(workspacePath)
  const [devices, setDevices] = useState<LocalFileDevice[]>([])
  const [error, setError] = useState<string | null>(null)
  const [checked, setChecked] = useState(false)
  const [setup, setSetup] = useState(false)
  const knownFolders = useRef(new Map<string, LocalFileDevice['resources'][number]>())
  useEffect(() => {
    if (preference.location !== 'computer') return
    let active = true
    let loading = false
    setDevices([]); setChecked(false)
    const refresh = async () => {
      if (loading) return
      loading = true
      try { const listed = await codeLocalFilesApi.devices(); if (active) { setDevices(listed); setError(null); setChecked(true) } }
      catch (cause) { if (active) { setDevices([]); setError(localFileError(cause)); setChecked(true) } }
      finally { loading = false }
    }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [preference.location, accountID, workspaceID])
  const selected = preference.location === 'computer' ? preference.target : undefined
  const device = devices.find(item => item.device_id === selected?.device_id)
  const resource = device?.resources.find(item => item.id === selected?.resource_id)
  const selectedKey = selected ? JSON.stringify([selected.device_id, selected.resource_id]) : ''
  const editorKey = JSON.stringify([accountID, workspaceID, selectedKey])
  if (resource) knownFolders.current.set(editorKey, resource)
  const knownResource = resource || knownFolders.current.get(editorKey)
  const choose = (value: string) => {
    const [device_id, resource_id] = JSON.parse(value) as string[]
    writeCodeFilesPreference(workspacePath, { location: 'computer', target: { device_id, resource_id } })
  }
  return <div className="flex h-full min-h-0 flex-col">
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
      <label className="text-xs font-medium" htmlFor="code-files-location">Files location</label>
      <select id="code-files-location" aria-label="Files location" value={preference.location} className="rounded-md border border-border bg-background p-1 text-sm" onChange={event => { setChecked(false); writeCodeFilesPreference(workspacePath, event.target.value === 'computer' ? { location: 'computer' } : { location: 'server' }) }}>
        <option value="server">Server</option><option value="computer">My computer</option>
      </select>
      {preference.location === 'computer' && <><span role="status" className="text-xs text-muted-foreground">{resource ? 'Connected' : checked ? 'Offline' : 'Checking…'}</span><Button size="sm" variant="ghost" onClick={() => setSetup(value => !value)}><Laptop className="mr-1 h-4 w-4" />Connect computer</Button></>}
    </div>
    {preference.location === 'server' ? <div className="min-h-0 flex-1">{serverFiles}</div> : <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">
      {(setup || (!resource && checked && devices.length === 0)) && <ComputerSetup />}
      <label className="text-xs font-medium" htmlFor="code-local-folder">Computer and shared folder</label>
      <select id="code-local-folder" aria-label="Computer and shared folder" value={selectedKey} className="rounded-md border border-border bg-background p-2 text-sm" onChange={event => { if (event.target.value) choose(event.target.value) }}>
        <option value="">Choose a folder…</option>
        {selected && !resource && <option value={selectedKey}>{selected.device_id} / {selected.resource_id} — Offline</option>}
        {devices.flatMap(item => item.resources.map(folder => <option key={`${item.device_id}/${folder.id}`} value={JSON.stringify([item.device_id, folder.id])}>{item.device_id} / {folder.id} — {folder.writable ? 'Can edit' : 'Read only'}</option>))}
      </select>
      <p className="text-xs text-muted-foreground">This browser remembers your choice for this Code project. Chats and project settings remain on the server.</p>
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {selected && !resource && checked && <p className="text-sm text-muted-foreground">Your selected folder is offline or no longer shared. Reconnect it on your computer to continue. Code will not switch to server files.</p>}
      {selected && knownResource && <LocalFolderFiles key={editorKey} target={selected} connected={Boolean(resource)} writable={knownResource.writable && accountCanEdit} guardHint={knownResource.guard.read_only_paths?.length || knownResource.guard.blocked_paths?.length || knownResource.guard.blocked_write_paths?.length ? 'Folder restrictions apply' : 'Protected files stay guarded'} onAsk={onAsk} />}
    </div>}
  </div>
}
