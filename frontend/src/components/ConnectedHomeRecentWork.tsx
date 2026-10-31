import { useEffect, useState } from 'react'
import { Loader2, RefreshCw } from 'lucide-react'
import { connectedLocations, useConnectedWorkStore } from '../stores/useConnectedWorkStore'
import { connectedWorkHistoryScope, useProjectMenuHistoryStore } from '../stores/useProjectMenuHistoryStore'
import { connectedProjectKey, serverIconColors } from '../utils/connectedWorkPresentation'
import { getRawActiveWorkspaceId } from '../utils/desktopConnection'
import { isChatIdentityCurrent } from '../utils/chatIdentity'
import { openSidebarProject, SidebarProjectRow } from './workspace/SidebarServerGroup'
import { openConnectedServers } from './workspace/ConnectedServersMenuItem'

/** Reuse admitted navigation metadata; never fetch another server's conversations for Home. */
export function ConnectedHomeRecentWork() {
  const state = useConnectedWorkStore()
  const connections = connectedLocations(state)
  const current = isChatIdentityCurrent(state.identity)
  const inventory = current ? state.inventory : {}
  const scope = connectedWorkHistoryScope()
  const recent = useProjectMenuHistoryStore(history => history.recent[scope])
  const visit = useProjectMenuHistoryStore(history => history.visit)
  const [error, setError] = useState('')
  useEffect(() => { void state.refresh() }, [state.refresh])
  const directory = new Map(connections.flatMap(connection => inventory[connection.id]?.status === 'connected'
    ? inventory[connection.id].projects.map(project => [connectedProjectKey(connection.id, project.kind, project.id), { project, connection }] as const) : []))
  // Only actual visits belong in Recent work. Never fill it with alphabetic directory entries.
  const rows = (recent || []).flatMap(key => directory.has(key) ? [{ key, ...directory.get(key)! }] : []).slice(0, 8)
  const loading = !current || !state.loaded || connections.some(connection => !inventory[connection.id])
  const unavailable = connections.filter(connection => inventory[connection.id]?.status === 'unavailable')
  const signedOut = connections.filter(connection => inventory[connection.id]?.status === 'sign_in')
  const colors = serverIconColors(connections)
  return <section aria-labelledby="home-recent-title" className="mx-auto w-full max-w-3xl border-t border-border pt-4">
    <div className="mb-2 flex items-center justify-between gap-3">
      <h2 id="home-recent-title" className="text-sm font-semibold">Recent work</h2>
      <button type="button" aria-label="Refresh recent work" onClick={() => void state.refresh()} className="grid h-8 w-8 place-items-center rounded-md text-muted-foreground hover:bg-muted"><RefreshCw aria-hidden="true" className="h-3.5 w-3.5" /></button>
    </div>
    <ul className="space-y-1">{rows.map(({ key, project, connection }) => <li key={key}>
      <SidebarProjectRow {...project} serverId={connection.id} remote={connection.id !== 'local'} location={connection.id !== 'local' ? connection.name : undefined} serverColor={colors[connection.server]} onOpen={() => {
        visit(scope, key)
        setError('')
        if (connection.id === getRawActiveWorkspaceId()) openSidebarProject(project.kind, project.id)
        else void window.electronAPI?.openDesktopConnection?.(connection.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)
          .catch(() => setError('Could not open this server. Try again.'))
      }} />
    </li>)}</ul>
    {loading && <p role="status" className="flex items-center gap-2 py-3 text-xs text-muted-foreground"><Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" />Loading recent work…</p>}
    {!loading && !rows.length && !unavailable.length && !signedOut.length && !state.error && <p className="py-3 text-xs text-muted-foreground">Open a Crew or Goal from the sidebar. Your recent work will appear here.</p>}
    {(unavailable.length > 0 || state.error) && <button type="button" onClick={() => void state.refresh()} className="py-2 text-left text-xs text-muted-foreground underline">Some recent work could not be loaded. Retry</button>}
    {signedOut.map(connection => <button type="button" key={connection.id} onClick={() => openConnectedServers(connection.server)} className="block py-2 text-left text-xs text-muted-foreground underline">Sign in to {connection.name} again</button>)}
    {error && <p role="alert" className="py-2 text-xs text-destructive">{error}</p>}
  </section>
}
