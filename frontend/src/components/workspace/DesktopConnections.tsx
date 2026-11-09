import { openConnectedServers } from './ConnectedServersMenuItem'
import { LocationFilter } from './LocationFilter'
import { useProjectMenuHistoryStore, connectedWorkHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey, serverIconColors } from '../../utils/connectedWorkPresentation'
import { isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { connectedLocations, hasDesktopConnections, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'
import { groupByMac, macCrewStateText, macStatusText, type CrewLocationFields } from './macCrews'
import { useTeamInviteRows } from './TeamInviteRow'
import { SidebarCrewAgents } from './SidebarCrewAgents'
import { ChevronRight, Laptop } from 'lucide-react'
import { ServerIdentityIcon } from './ServerIdentityIcon'

type SidebarProject = { id: string; name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; shared_agents?: number; personal_agent?: boolean } & CrewLocationFields

/** Crews first, then a flat shared-dashboard list. Each row names its owner once.
 * The personal AgentWorks agent is rendered by its network group. */
function ProjectList<T extends SidebarProject>({ projects, render, invites }: { projects: T[]; render: (project: T) => ReactNode; invites: ReactNode[] }) {
  const rest = projects.filter(project => !project.personal_agent)
  const mine = rest.filter(project => project.owned_by_caller === true || (project.owned_by_caller === false && project.mode !== 'run'))
  const dashboards = rest.filter(project => project.owned_by_caller === false && project.mode === 'run')
  return <>{invites.length > 0 && <div className="mb-2 space-y-1">{invites}</div>}
    {mine.map(render)}
    {rest.filter(project => project.owned_by_caller === undefined).map(render)}
    {dashboards.length > 0 && <section aria-label="Dashboards shared with me" className="mb-3 space-y-1">
      <h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">Shared</h2>
      {dashboards.map(render)}
    </section>}
    </>
}

function useWorkHistory(projects: SidebarProject[]) {
  const active = getRawActiveWorkspaceId()
  const historyScope = connectedWorkHistoryScope()
  const recent = useProjectMenuHistoryStore(state => state.recent[historyScope])
  const pinned = useProjectMenuHistoryStore(state => state.pinned[historyScope])
  const setPinned = useProjectMenuHistoryStore(state => state.setPinned)
  const visit = useProjectMenuHistoryStore(state => state.visit)
  const selectedProject = projects.find(project => project.selected)
  const selectedKey = selectedProject ? connectedProjectKey(active, selectedProject.kind, selectedProject.id) : ''
  useEffect(() => { if (selectedKey) visit(historyScope, selectedKey) }, [historyScope, selectedKey, visit])
  return { recent, pinned, canPin: Boolean(historyScope), setPinned: (key: string, value: boolean) => setPinned(historyScope, key, value), visit: (key: string) => visit(historyScope, key) }
}
export function DesktopConnections({ children, projects = [] }: { children?: ReactNode; projects?: SidebarProject[] }) {
  const crews = projects.filter(project => project.kind === 'crew')
  return hasDesktopConnections() ? <ConnectedServers projects={crews}>{children}</ConnectedServers> : <StandaloneWork projects={crews}>{children}</StandaloneWork>
}
function StandaloneWork({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const { recent, visit } = useWorkHistory(projects)
  const orderedProjects = [...projects].sort((a, b) => {
    const rank = (project: SidebarProject) => {
      const index = recent?.indexOf(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id)) ?? -1
      return project.selected && index < 0 ? -1 : index < 0 ? Infinity : index
    }
    return rank(a) - rank(b) || a.name.localeCompare(b.name)
  })
  // The open Crew lists its agents under it (owner, 4 Nov, "Left menu").
  const renderProject = ({ location, mac, state, ...project }: SidebarProject) => <Fragment key={`${project.kind}:${project.id}`}><SidebarProjectRow {...project}
    showIcon={project.kind !== 'crew' || project.personal_agent || project.mode === 'run'}
    stateLine={location === 'mac' ? macCrewStateText(state) : undefined} offline={location === 'mac' && mac?.online === false} onMac={location === 'mac' ? (mac?.online ? 'on' : 'off') : undefined} onOpen={() => {
    visit(connectedProjectKey(getRawActiveWorkspaceId(), project.kind, project.id))
    openSidebarProject(project.kind, project.id)
  }} />{project.kind === 'crew' && project.selected && <SidebarCrewAgents projectId={project.id} />}</Fragment>
  const invites = useTeamInviteRows()
  const { macs, server } = groupByMac(orderedProjects)
  return <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
    {macs.map(({ mac, rows }) => <section key={mac.device_id} aria-label={`On your Mac: ${mac.name}`} className="mb-3 space-y-1">
      <h2 className="flex min-w-0 items-center gap-1.5 px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">
        <span className="shrink-0">On your Mac</span>
        <span role="img" aria-label={`${mac.name} · ${macStatusText(mac)}`} title={`${mac.name} · ${macStatusText(mac)}`} className={`h-1.5 w-1.5 shrink-0 rounded-full ${mac.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
      </h2>
      {rows.map(renderProject)}
    </section>)}
    {orderedProjects.filter(project => project.personal_agent).map(renderProject)}
    {macs.length > 0 && server.length > 0 && <h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">On the server</h2>}
    <div className="space-y-1"><ProjectList projects={server} render={renderProject} invites={invites} /></div>
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
  const { recent, pinned, canPin, setPinned, visit } = useWorkHistory(projects)
  const [filter, setFilter] = useState('all')
  const invites = useTeamInviteRows()
  useEffect(() => { if (filter !== 'all' && !connections.some(connection => connection.id === filter)) setFilter('all') }, [connections, filter])
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
    for (const project of inventory[connection.id]?.projects || []) {
      if (project.kind === 'crew') byId.set(`${project.kind}:${project.id}`, project)
    }
    if (connection.id === active) for (const project of projects) byId.set(`${project.kind}:${project.id}`, { ...byId.get(`${project.kind}:${project.id}`), ...project })
    return [...byId.values()].map(project => ({ ...project, connection }))
  }).sort((a, b) => {
    const rank = (row: typeof a) => { const index = recent?.indexOf(connectedProjectKey(row.connection.id, row.kind, row.id)) ?? -1; return index < 0 ? Infinity : index }
    return rank(a) - rank(b) || a.name.localeCompare(b.name) || a.connection.name.localeCompare(b.connection.name) || a.id.localeCompare(b.id)
  })
  // The filter is only which server (owner, 4 Nov).
  const visible = rows.filter(row => filter === 'all' || row.connection.id === filter)
  const serverColors = serverIconColors(connections)
  const keyFor = (project: typeof rows[number]) => connectedProjectKey(project.connection.id, project.kind, project.id)
  // Saved shortcuts contain IDs only. Resolve them against the current admitted inventory on every render.
  const crewByKey = new Map(rows.filter(row => row.kind === 'crew' && !row.personal_agent).map(row => [keyFor(row), row]))
  const pinnedKeys = new Set(pinned || [])
  const recentRows = [...(pinned || []), ...(recent || []).filter(key => !pinnedKeys.has(key) && crewByKey.has(key)).slice(0, 5)]
    .flatMap(key => { const row = crewByKey.get(key); return row ? [row] : [] })
  const renderProject = (project: typeof rows[number], shortcut = false) => {
    const key = keyFor(project)
    return <Fragment key={key}><SidebarProjectRow {...project}
      showIcon={project.kind !== 'crew' || project.personal_agent || project.mode === 'run'}
      selected={project.connection.id === active && project.selected}
      location={project.connection.id === 'local' ? 'On your Mac' : project.connection.name} remote={project.connection.id !== 'local'} showLocation={shortcut}
      pinned={pinnedKeys.has(key)} onTogglePin={canPin && project.kind === 'crew' && !project.personal_agent ? () => setPinned(key, !pinnedKeys.has(key)) : undefined}
      onOpen={() => {
        visit(key)
        if (project.connection.id === active) openSidebarProject(project.kind, project.id)
        else open(project.connection.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)
      }} />{!shortcut && project.kind === 'crew' && project.connection.id === active && project.selected && <SidebarCrewAgents projectId={project.id} />}</Fragment>
  }
  const locations = connections.filter(connection => filter === 'all' || connection.id === filter)
  return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
    <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
      {recentRows.length > 0 && <section aria-label="Recent Crews" className="mb-3 space-y-1">
        <h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">Recent</h2>
        {recentRows.map(project => renderProject(project, true))}
      </section>}
      <div className="flex items-center px-2 py-2">
        {connections.length > 1 && <LocationFilter label="Filter work" value={filter} onChange={setFilter} connections={connections} />}
      </div>
      {invites.length > 0 && <div className="mb-2 space-y-1">{invites}</div>}
      {locations.map(connection => {
        const projectsHere = visible.filter(row => row.connection.id === connection.id)
        if (!projectsHere.length) return null
        const local = connection.id === 'local'
        const localConnected = inventory[connection.id]?.status === 'connected'
        const name = local ? 'On your Mac' : connection.name
        return <NetworkSection key={connection.id} name={name} active={connection.id === active || filter === connection.id}
          icon={local ? <><Laptop className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /><span role="img" aria-label={localConnected ? 'Local connection available' : 'Local connection not ready'} className={`h-1.5 w-1.5 shrink-0 rounded-full ${localConnected ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} /></> : <ServerIdentityIcon connectionId={connection.id} hidden className={`h-3.5 w-3.5 shrink-0 ${serverColors[connection.server] || 'text-muted-foreground'}`} />}>
          {projectsHere.filter(project => project.personal_agent).map(project => renderProject(project))}
          <ProjectList projects={projectsHere} render={project => renderProject(project)} invites={[]} />
        </NetworkSection>
      })}
      {(filter === 'all' || filter === active) && children}
        {locations.map(connection => {
        const status = inventory[connection.id]?.status
        if (!status) return <p key={connection.id} role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading {connection.name}…</p>
        if (status === 'unavailable') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => void refresh()}>{connection.name} unavailable · Retry</button>
        if (status === 'sign_in') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => openConnectedServers(connection.server)}>{connection.name} · Sign in again</button>
        return null
      })}
      {!visible.length && locations.every(connection => inventory[connection.id]?.status === 'connected') && <p className="px-2.5 py-2 text-xs text-muted-foreground">No Crews here yet.</p>}
    </div>
    {directoryError && <p role="alert" className="px-3 text-xs text-destructive">{directoryError}</p>}

  </div>
}

/** Browse a network without changing the connection used by the current chat. */
function NetworkSection({ name, active, icon, children }: { name: string; active: boolean; icon: ReactNode; children: ReactNode }) {
  const [expanded, setExpanded] = useState(active)
  useEffect(() => { if (active) setExpanded(true) }, [active])
  return <section aria-label={`Network: ${name}`} className="mb-2 space-y-1">
    <button type="button" aria-label={`${name} Crews`} aria-expanded={expanded} onClick={() => setExpanded(value => !value)}
      className="flex min-h-9 w-full items-center gap-2 rounded-lg px-3 text-left text-[13px] font-medium text-foreground/80 hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
      <ChevronRight className={`h-3 w-3 shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} aria-hidden="true" />{icon}<span className="truncate">{name}</span>
    </button>
    {expanded && children}
  </section>
}
