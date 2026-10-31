import { openConnectedServers } from './ConnectedServersMenuItem'
import { LocationFilter } from './LocationFilter'
import { useProjectMenuHistoryStore, projectMenuHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey, serverIconColors } from '../../utils/connectedWorkPresentation'
import { isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { useEffect, useState, type ReactNode } from 'react'
import { connectedLocations, hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'

type SidebarProject = { id: string; name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean }

function useWorkHistory(projects: SidebarProject[]) {
  const active = getRawActiveWorkspaceId()
  // Use the local account's navigation history across connection reloads.
  // It stores only selectors; admitted inventory still decides visible rows.
  let owner = ''
  try { owner = JSON.parse(localStorage.getItem('auth-storage') || '{}').state?.user?.id || '' } catch { /* No saved owner yet. */ }
  const historyScope = owner ? projectMenuHistoryScope('local', owner, 'connected-work', 'sidebar') : ''
  const recent = useProjectMenuHistoryStore(state => state.recent[historyScope])
  const visit = useProjectMenuHistoryStore(state => state.visit)
  const selectedProject = projects.find(project => project.selected)
  const selectedKey = selectedProject ? connectedProjectKey(active, selectedProject.kind, selectedProject.id) : ''
  useEffect(() => { if (selectedKey) visit(historyScope, selectedKey) }, [historyScope, selectedKey, visit])
  return { recent, visit: (key: string) => visit(historyScope, key) }
}
export function DesktopConnections({ children, projects = [] }: { children?: ReactNode; projects?: SidebarProject[] }) {
  return hasDesktopConnections() ? <ConnectedServers projects={projects}>{children}</ConnectedServers> : <StandaloneWork projects={projects}>{children}</StandaloneWork>
}
function StandaloneWork({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const { recent, visit } = useWorkHistory(projects)
  const [filter, setFilter] = useState('all')
  const orderedProjects = [...projects].sort((a, b) => {
    const rank = (project: SidebarProject) => {
      const index = recent?.indexOf(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id)) ?? -1
      return project.selected && index < 0 ? -1 : index < 0 ? Infinity : index
    }
    return rank(a) - rank(b) || a.name.localeCompare(b.name)
  })
  const renderProject = (project: SidebarProject) => <SidebarProjectRow key={`${project.kind}:${project.id}`} {...project} onOpen={() => {
    visit(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id))
    openSidebarProject(project.kind, project.id)
  }} />
  return <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
    <div className="py-2"><LocationFilter label="Filter work" value={filter} onChange={setFilter} connections={[]} workTypes /></div>
    <div className="space-y-1">{orderedProjects.filter(project => filter === 'all' || project.kind === filter).map(renderProject)}</div>
    {children}
    {window.electronAPI?.getDesktopConnection && <p className="px-2.5 py-2 text-xs text-muted-foreground">Start the app with the desktop launcher to connect another server.</p>}
  </div>
}

function ConnectedServers({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const active = getRawActiveWorkspaceId()
  const state = useConnectedWorkStore()
  const connections = connectedLocations(state)
  const inventory = isChatIdentityCurrent(state.identity) ? state.inventory : {}
  const { refresh, error: directoryError } = state
  const { recent, visit } = useWorkHistory(projects)
  const [filter, setFilter] = useState('all')
  useEffect(() => { if (!['all', 'crew', 'goal'].includes(filter) && !connections.some(connection => connection.id === filter)) setFilter('all') }, [connections, filter])
  useEffect(() => {
    void refresh()
    const onFocus = () => void refresh()
    const timer = window.setInterval(() => { if (!document.hidden) void refresh() }, 60_000)
    window.addEventListener('focus', onFocus)
    return () => { window.clearInterval(timer); window.removeEventListener('focus', onFocus) }
  }, [refresh])
  const open = (id: string, path: string) => {
    void window.electronAPI?.openDesktopConnection?.(id, path).catch(() => useConnectedWorkStore.setState({ error: 'Could not open this server. Try again.' }))
  }
  const rows = connections.flatMap(connection => {
    const byId = new Map<string, SidebarProject>()
    for (const project of inventory[connection.id]?.projects || []) byId.set(`${project.kind}:${project.id}`, project)
    if (connection.id === active) for (const project of projects) byId.set(`${project.kind}:${project.id}`, project)
    return [...byId.values()].map(project => ({ ...project, connection }))
  }).sort((a, b) => {
    const rank = (row: typeof a) => { const index = recent?.indexOf(connectedProjectKey(row.connection.id, row.kind, row.id)) ?? -1; return index < 0 ? Infinity : index }
    return rank(a) - rank(b) || a.name.localeCompare(b.name) || a.connection.name.localeCompare(b.connection.name) || a.id.localeCompare(b.id)
  })
  const visible = rows.filter(row => filter === 'crew' || filter === 'goal' ? row.kind === filter : filter === 'all' || row.connection.id === filter)
  const serverColors = serverIconColors(connections)
  const renderProject = (project: typeof rows[number]) => <SidebarProjectRow key={`${project.connection.id}:${project.kind}:${project.id}`} {...project}
    selected={project.connection.id === active && project.selected}
    remote={project.connection.id !== 'local'} serverColor={serverColors[project.connection.server]} location={project.connection.id !== 'local' ? project.connection.name : undefined}
    onOpen={() => {
      visit(connectedProjectKey(project.connection.id, project.kind, project.id))
      if (project.connection.id === active) openSidebarProject(project.kind, project.id)
      else open(project.connection.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)
    }} />
  const locations = connections.filter(connection => ['all', 'crew', 'goal'].includes(filter) || connection.id === filter)
  return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
    <div className="flex shrink-0 items-center justify-between gap-2 px-4 py-2">
      <LocationFilter label="Filter work" value={filter} onChange={setFilter} connections={connections} workTypes />
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      <div className="space-y-1">{visible.map(renderProject)}</div>
      {(filter === 'all' || filter === active) && children}
      {locations.map(connection => {
        const status = inventory[connection.id]?.status
        if (!status) return <p key={connection.id} role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading {connection.name}…</p>
        if (status === 'unavailable') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => void refresh()}>{connection.name} unavailable · Retry</button>
        if (status === 'sign_in') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => openConnectedServers(connection.server)}>{connection.name} · Sign in again</button>
        return null
      })}
      {!visible.length && locations.every(connection => inventory[connection.id]?.status === 'connected') && <p className="px-2.5 py-2 text-xs text-muted-foreground">No Crews or Goals here yet.</p>}
    </div>
    {directoryError && <p role="alert" className="px-3 text-xs text-destructive">{directoryError}</p>}

  </div>
}
