import { useEffect, useRef, useState } from 'react'
import { Button } from '../../components/ui/Button'
import { openConnectedServers } from '../../components/workspace/ConnectedServersMenuItem'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type Binding = { side: 'local' | 'server'; editing: 'local' | 'server'; connection_id?: string; peer_project_id: string; peer_server?: string; updated_at?: string; pending_action?: 'update' | 'server' | 'local' | 'download' }
type Status = { project_id: string; binding: Binding | null; auto?: { state: string; message?: string; checked_at?: string } }

export function CrewSyncPanel({ projectId }: { projectId: string }) {
  const connections = useConnectedWorkStore(state => state.connections)
  const local = hasDesktopConnections() && getRawActiveWorkspaceId() === 'local'
  const currentProject = useRef(projectId)
  currentProject.current = projectId
  const [reload, setReload] = useState(0)
  const [status, setStatus] = useState<Status | null>(null)
  const [connection, setConnection] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    const identity = captureChatIdentity(), abort = new AbortController()
    setStatus(null); setError(''); setConnection(''); setBusy(false)
    if (local) void useConnectedWorkStore.getState().refresh()
    void api.get<Status>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true })
      .then(({ data }) => { if (!abort.signal.aborted && isChatIdentityCurrent(identity)) { setStatus(data); setConnection(data.binding?.connection_id || '') } })
      .catch(() => { if (!abort.signal.aborted && isChatIdentityCurrent(identity)) setError('Could not load project sync. Check the connection and try again.') })
    return () => abort.abort()
  }, [projectId, local, reload])
  const run = async (action: 'update' | 'server' | 'local' | 'download') => {
    const identity = captureChatIdentity()
    const current = () => isChatIdentityCurrent(identity) && currentProject.current === projectId
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<Status>(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action })
      if (current()) setStatus(data)
      void useConnectedWorkStore.getState().refresh()
    } catch (cause) {
      if (current()) {
        setError(cause instanceof Error ? cause.message : 'Sync failed. Your changes have been kept.')
        const latest = await api.get<Status>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { skipSessionContext: true }).catch(() => null)
        if (current() && latest) setStatus(latest.data)
      }
    } finally { if (current()) setBusy(false) }
  }
  useEffect(() => {
    if (!status?.binding) return
    const identity = captureChatIdentity(), abort = new AbortController()
    const timer = window.setInterval(() => {
      void api.get<Status>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true })
        .then(({ data }) => { if (!abort.signal.aborted && isChatIdentityCurrent(identity)) setStatus(data) })
        .catch(() => {})
    }, 3000)
    return () => { window.clearInterval(timer); abort.abort() }
  }, [projectId, status?.binding?.peer_project_id])
  const binding = status?.binding
  return <section aria-label="Project sync" className="max-w-xl space-y-4 text-sm">
    <p>Use this Crew from the website or your phone. While editing on your Mac, its main chat continues the same local agent and conversation. Keep AgentWorks open on your Mac.</p>
    {error && <div><p role="alert" className="text-destructive">{error}</p>{!status && <Button variant="link" onClick={() => setReload(value => value + 1)}>Try again</Button>}</div>}
    {!status && !error && <p role="status">Loading project sync…</p>}
    {binding && <div className="space-y-1 rounded-lg border border-border p-3">
      <p className="font-medium">Editing on {binding.editing === 'local' ? 'your Mac' : 'the server'}</p>
      {binding.updated_at && <p className="text-muted-foreground">Last synced {new Date(binding.updated_at).toLocaleString()}</p>}
      <p role="status">{status.auto?.state === 'syncing' ? 'Syncing…' : status.auto?.state === 'synced' ? 'Up to date' : status.auto?.message || 'Files sync automatically while AgentWorks is open on your Mac.'}</p>
      <p className="text-muted-foreground">Saved file changes sync automatically from the editing computer. Switching waits for the latest changes. Switching to the server uses its separate conversation and agent.</p>
    </div>}
    {local ? <>
      {!binding && <label className="block space-y-2"><span>Server</span><select aria-label="Sync server" value={connection} onChange={event => setConnection(event.target.value)} className="w-full rounded-md border border-border bg-background p-2">
        <option value="">Choose a connected server</option>
        {connections.filter(item => item.id !== 'local').map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select></label>}
      {!binding && <Button variant="link" className="h-auto p-0" onClick={() => openConnectedServers()}>Connect a server</Button>}
      <div className="flex flex-wrap gap-2">
        {binding?.pending_action && <Button disabled={busy} onClick={() => void run(binding.pending_action!)}>{busy ? 'Finishing transfer…' : 'Finish interrupted transfer'}</Button>}
        {!binding?.pending_action && binding?.editing !== 'server' && <>
          <Button disabled={!status || !connection || busy} onClick={() => void run('update')}>{busy ? 'Syncing…' : binding ? 'Sync now' : 'Make available on server'}</Button>
          {binding && <Button variant="outline" disabled={busy} onClick={() => void run('server')}>Work on server</Button>}
        </>}
        {!binding?.pending_action && binding?.editing === 'server' && <Button disabled={busy} onClick={() => void run('local')}>{busy ? 'Bringing changes back…' : 'Work on this Mac'}</Button>}
      </div>
      {binding?.peer_server && <a className="inline-block underline" href={`${binding.peer_server}/crew/projects/${encodeURIComponent(binding.peer_project_id)}`} target="_blank" rel="noopener noreferrer">Open server Crew</a>}
      <p className="text-xs text-muted-foreground">Starts Only me. Logins, private chats and platform records stay on their own computer. Current Git branch, history and staged changes travel with the files. Other branches and Git logins stay put. Edits outside AgentWorks are checked before replacement.</p>
    </> : <p className="text-muted-foreground">{binding ? 'Keep AgentWorks open on your Mac for automatic file sync. Use the Mac app to move editing between computers.' : 'Open a local project in the Mac app and choose this server to set up sync.'}</p>}
  </section>
}
