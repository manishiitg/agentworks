import { useEffect, useState } from 'react'
import { Button } from '../../components/ui/button'
import { openConnectedServers } from '../../components/workspace/ConnectedServersMenuItem'
import api from '../../services/api'
import { desktopRequest } from '../../services/desktopConnections'
import { hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type Binding = { side: 'local' | 'server'; editing: 'local' | 'server'; connection_id?: string; peer_project_id: string; peer_server?: string; updated_at?: string }
type Status = { project_id: string; binding: Binding | null }

export function CrewSyncPanel({ projectId }: { projectId: string }) {
  const connections = useConnectedWorkStore(state => state.connections)
  const local = hasDesktopConnections() && getRawActiveWorkspaceId() === 'local'
  const [status, setStatus] = useState<Status | null>(null)
  const [connection, setConnection] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    const identity = captureChatIdentity(), abort = new AbortController()
    setStatus(null); setError(''); setConnection('')
    if (local) void useConnectedWorkStore.getState().refresh()
    void api.get<Status>(`/api/crew-sync/${encodeURIComponent(projectId)}`, { signal: abort.signal, skipSessionContext: true })
      .then(({ data }) => { if (!abort.signal.aborted && isChatIdentityCurrent(identity)) { setStatus(data); setConnection(data.binding?.connection_id || '') } })
      .catch(() => { if (!abort.signal.aborted && isChatIdentityCurrent(identity)) setError('Could not load project sync. Check the connection and try again.') })
    return () => abort.abort()
  }, [projectId, local])
  const run = async (action: 'update' | 'server' | 'local') => {
    const identity = captureChatIdentity()
    setBusy(true); setError('')
    try {
      const data = await desktopRequest<Status>(`/${encodeURIComponent(connection)}/crew-sync/${encodeURIComponent(projectId)}`, 'POST', { action })
      if (isChatIdentityCurrent(identity)) setStatus(data)
      void useConnectedWorkStore.getState().refresh()
    } catch (cause) {
      if (isChatIdentityCurrent(identity)) setError(cause instanceof Error ? cause.message : 'Sync failed. Your changes have been kept.')
    } finally { if (isChatIdentityCurrent(identity)) setBusy(false) }
  }
  const binding = status?.binding
  return <section aria-label="Project sync" className="max-w-xl space-y-4 text-sm">
    <p>Keep a server copy of this project so its agent can work while your Mac is off. Choose one place to edit the project files.</p>
    {error && <p role="alert" className="text-destructive">{error}</p>}
    {!status && !error && <p role="status">Loading project sync…</p>}
    {binding && <div className="space-y-1 rounded-lg border border-border p-3">
      <p className="font-medium">Editing on {binding.editing === 'local' ? 'your Mac' : 'the server'}</p>
      {binding.updated_at && <p className="text-muted-foreground">Last synced {new Date(binding.updated_at).toLocaleString()}</p>}
      <p className="text-muted-foreground">Server conversations and results stay available. Project files use the last synced version.</p>
    </div>}
    {local ? <>
      {!binding && <label className="block space-y-2"><span>Server</span><select aria-label="Sync server" value={connection} onChange={event => setConnection(event.target.value)} className="w-full rounded-md border border-border bg-background p-2">
        <option value="">Choose a connected server</option>
        {connections.filter(item => item.id !== 'local').map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select></label>}
      {!binding && <Button variant="link" className="h-auto p-0" onClick={() => openConnectedServers()}>Connect a server</Button>}
      <div className="flex flex-wrap gap-2">
        {binding?.editing !== 'server' && <>
          <Button disabled={!status || !connection || busy} onClick={() => void run('update')}>{busy ? 'Syncing…' : binding ? 'Update server' : 'Make available on server'}</Button>
          {binding && <Button variant="outline" disabled={busy} onClick={() => void run('server')}>Work on server</Button>}
        </>}
        {binding?.editing === 'server' && <Button disabled={busy} onClick={() => void run('local')}>{busy ? 'Bringing changes back…' : 'Work on this Mac'}</Button>}
      </div>
      {binding?.peer_server && <a className="inline-block underline" href={`${binding.peer_server}/crew/projects/${encodeURIComponent(binding.peer_project_id)}`} target="_blank" rel="noopener noreferrer">Open server Crew</a>}
      <p className="text-xs text-muted-foreground">Starts Only me. Logins, private chats and platform records stay on their own computer. Changes made outside AgentWorks are checked before files are replaced.</p>
    </> : <p className="text-muted-foreground">{binding ? 'Use this Crew’s local Mac app to sync files or move editing between computers.' : 'Open a local project in the Mac app and choose this server to set up sync.'}</p>}
  </section>
}
