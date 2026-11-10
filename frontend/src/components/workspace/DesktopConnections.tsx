import { openConnectedServers } from './ConnectedServersMenuItem'
import { useProjectMenuHistoryStore, connectedWorkHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { connectedLocations, hasDesktopConnections, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import type { DesktopConnection } from '../../services/desktopConnections'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'
import { groupByMac, macCrewStateText, macStatusText, type CrewLocationFields } from './macCrews'
import { useTeamInviteRows } from './TeamInviteRow'
import { useCrewAgentsStore } from '../chat/AgentCard'
import { SidebarCrewAgents } from './SidebarCrewAgents'
import { ChevronRight, Laptop } from 'lucide-react'
import { ServerIdentityIcon } from './ServerIdentityIcon'
import { useSidebarExpansionStore, sidebarExpansionKey } from '../../stores/useSidebarExpansionStore'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { useChatStore } from '../../stores/useChatStore'
import { ProjectFilterMenu, ProjectGroupsMenu, matchesMenuFilter, useMenuGroups, type MenuFilter } from './MenuGroups'


type SidebarProject = { id: string; name: string; shared_line?: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; shared_agents?: number; personal_agent?: boolean } & CrewLocationFields

/** Crews you build or work on. Dashboards shared with you are not listed here: they have their own page (owner,
 * 10 Nov). The personal AgentWorks agent is on the Home page only. */
const menuListed = (project: SidebarProject) => !project.personal_agent && !(project.owned_by_caller === false && project.mode === 'run')
function ProjectList<T extends SidebarProject>({ projects, render }: { projects: T[]; render: (project: T) => ReactNode }) {
  return <>{projects.filter(menuListed).map(render)}</>
}

/** The menu filter over listed projects: what is shown, and the dropdown's count for each choice. */
function useProjectFilter<T extends SidebarProject>(projects: T[], keyOf: (project: T) => string) {
  const { groups, filter } = useMenuGroups()
  const listed = projects.filter(menuListed)
  const counts = (choice: MenuFilter) => listed.filter(project => matchesMenuFilter(choice, groups, keyOf(project), project)).length
  return { counts, active: filter !== 'all', shown: (project: T) => matchesMenuFilter(filter, groups, keyOf(project), project) }
}

function useWorkHistory(projects: SidebarProject[]) {
  const active = getRawActiveWorkspaceId()
  const historyScope = connectedWorkHistoryScope()
  const pinned = useProjectMenuHistoryStore(state => state.pinned[historyScope])
  const setPinned = useProjectMenuHistoryStore(state => state.setPinned)
  const movePinned = useProjectMenuHistoryStore(state => state.movePinned)
  const visit = useProjectMenuHistoryStore(state => state.visit)
  const selectedProject = projects.find(project => project.selected)
  const selectedKey = selectedProject ? connectedProjectKey(active, selectedProject.kind, selectedProject.id) : ''
  useEffect(() => { if (selectedKey) visit(historyScope, selectedKey) }, [historyScope, selectedKey, visit])
  return { pinned, movePinned: (key: string, target: string) => movePinned(historyScope, key, target), canPin: Boolean(historyScope), setPinned: (key: string, value: boolean) => setPinned(historyScope, key, value), visit: (key: string) => visit(historyScope, key) }
}
export function DesktopConnections({ children, projects = [] }: { children?: ReactNode; projects?: SidebarProject[] }) {
  const crews = projects.filter(project => project.kind === 'crew')
  // Team invites sit above the network tabs and every project list (owner, 10 Nov), once for all networks.
  const invites = useTeamInviteRows()
  return <>
    {invites.length > 0 && <div aria-label="Team invites" className="shrink-0 space-y-1 px-2 pb-2">{invites}</div>}
    <ProjectMenu projects={crews}>{children}</ProjectMenu>
  </>
}
// All projects starts open when nothing is pinned, so the menu is never empty (owner, 10 Nov); with pins it starts
// closed. The person's own open or close is kept either way.
function useAllProjectsExpanded(hasPins: boolean) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'all-projects')
  return useSidebarExpansionStore(state => state.expanded[key] ?? !hasPins)
}
/** Whether All projects is open, for the sidebar around the menu (its footer shows only then). */
export function useAllProjectsOpen() {
  const hasPins = useProjectMenuHistoryStore(state => Boolean(state.pinned[connectedWorkHistoryScope()]?.length))
  return useAllProjectsExpanded(hasPins)
}
function AllProjects({ children, hasPins, counts }: { children: ReactNode; hasPins: boolean; counts: (filter: MenuFilter) => number }) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'all-projects')
  const expanded = useAllProjectsExpanded(hasPins)
  const toggle = useSidebarExpansionStore(state => state.toggle)
  return <section aria-label="All projects" className={`min-h-0 shrink-0 ${expanded ? '' : 'mt-auto'}`}>
    {/* The heading is the filter (owner, 10 Nov): All, Mine, Shared with me or one of the person's groups. */}
    <div className="flex w-full shrink-0 items-center gap-2 rounded-lg px-2 py-1.5">
      <div className="min-w-0 flex-1"><ProjectFilterMenu counts={counts} /></div>
      <button type="button" aria-expanded={expanded} aria-label={expanded ? 'Hide projects' : 'Show projects'} onClick={() => toggle(key, !hasPins)} className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight aria-hidden="true" className={`h-4 w-4 shrink-0 transition-transform ${expanded ? 'rotate-90' : ''}`} />
      </button>
    </div>
    {expanded && <div className="mt-1">{children}</div>}
  </section>
}
/** The one left menu. In the desktop app it lists every connected network (the Mac and each server); on the website
 * it is the same menu with this server as its only network. The current network's Crews come from the navigation list
 * the sidebar loads; other networks' from the desktop app's inventory. */
function ProjectMenu({ children, projects }: { children?: ReactNode; projects: SidebarProject[] }) {
  const desktop = hasDesktopConnections()
  const active = getRawActiveWorkspaceId()
  const state = useConnectedWorkStore()
  useConnectedWorkPolling(desktop)
  const current = desktop && isChatIdentityCurrent(state.identity)
  const connections: DesktopConnection[] = desktop ? connectedLocations(state) : [{ id: active, name: '', server: '' }]
  const inventory = current ? state.inventory : {}
  const directoryError = desktop ? state.error : ''
  const { pinned, canPin, setPinned, movePinned, visit } = useWorkHistory(projects)
  const allProjectsExpanded = useAllProjectsExpanded(Boolean(pinned?.length))
  const [selectedLocations, setSelectedLocations] = useState<string[]>([active])
  useEffect(() => {
    if (!desktop || !state.loaded || !isChatIdentityCurrent(state.identity) || !connections.length) return
    setSelectedLocations(previous => {
      const remaining = previous.filter(id => connections.some(connection => connection.id === id))
      const next = remaining.length ? remaining : [connections.find(connection => connection.id === active)?.id || connections[0].id]
      return next.length === previous.length && next.every((id, index) => id === previous[index]) ? previous : next
    })
  }, [desktop, connections, active, state.loaded, state.identity])
  const open = (id: string, path: string) => {
    void window.electronAPI?.openDesktopConnection?.(id, path).catch(() => useConnectedWorkStore.setState({ error: 'Could not open this server. Try again.' }))
  }
  const rows = connections.flatMap(connection => (connection.id === active ? projects : (inventory[connection.id]?.projects || []).filter(project => project.kind === 'crew') as SidebarProject[])
    .map(project => ({ ...project, connection })))
    .sort((a, b) => a.name.localeCompare(b.name) || a.connection.name.localeCompare(b.connection.name) || a.connection.id.localeCompare(b.connection.id) || a.id.localeCompare(b.id))
  // Location buttons only filter navigation; the current chat keeps its connection.
  const visible = rows.filter(row => selectedLocations.includes(row.connection.id))
  const keyFor = (project: typeof rows[number]) => connectedProjectKey(project.connection.id, project.kind, project.id)
  // Saved shortcuts contain IDs only. Resolve them against the current admitted inventory on every render.
  const crewByKey = new Map(rows.filter(row => row.kind === 'crew' && !row.personal_agent).map(row => [keyFor(row), row]))
  const pinnedKeys = new Set(pinned || [])
  const pinnedRows = (pinned || [])
    .flatMap(key => { const row = crewByKey.get(key); return row ? [row] : [] })
  const renderProject = (project: typeof rows[number], shortcut = false) => {
    const { location: where, mac, state: crewState, connection, ...rest } = project
    const key = keyFor(project)
    const here = connection.id === active
    const pinnedIndex = pinnedRows.findIndex(item => keyFor(item) === key)
    const move = (offset: number) => {
      const target = pinnedRows[pinnedIndex + offset]
      if (target) movePinned(key, keyFor(target))
    }
    const crew = project.kind === 'crew' && !project.personal_agent
    const row = (agentsExpanded?: boolean, onToggleAgents?: () => void, activityLabel?: string) => <SidebarProjectRow {...rest} agentsExpanded={agentsExpanded} onToggleAgents={onToggleAgents} activityLabel={agentsExpanded === false ? activityLabel : undefined}
      showIcon={project.kind !== 'crew' || project.personal_agent || project.mode === 'run'}
      onMoveUp={shortcut && canPin && pinnedIndex > 0 ? () => move(-1) : undefined} onMoveDown={shortcut && canPin && pinnedIndex >= 0 && pinnedIndex < pinnedRows.length - 1 ? () => move(1) : undefined}
      selected={here && project.selected}
      location={desktop ? connection.id === 'local' ? 'On your Mac' : connection.name : undefined} remote={desktop && connection.id !== 'local'} showLocation={desktop && (shortcut || selectedLocations.length > 1)}
      pinned={pinnedKeys.has(key)} onTogglePin={canPin && crew ? () => setPinned(key, !pinnedKeys.has(key)) : undefined}
      groupsMenu={crew ? <ProjectGroupsMenu name={project.name} projectKey={key} /> : undefined}
      stateLine={where === 'mac' ? macCrewStateText(crewState) : undefined} offline={where === 'mac' && mac?.online === false} onMac={where === 'mac' ? (mac?.online ? 'on' : 'off') : undefined}
      onOpen={() => {
        visit(key)
        if (here) openSidebarProject(project.kind, project.id)
        else open(connection.id, project.kind === 'crew' ? `/crew/projects/${encodeURIComponent(project.id)}` : `/workflows/${encodeURIComponent(project.id)}`)
      }} />
    return crew
      ? <ExpandableCrew key={key} projectId={project.id} connectionId={connection.id} selected={here && !!project.selected} >{row}</ExpandableCrew>
      : <Fragment key={key}>{row()}</Fragment>
  }
  const locations = connections.filter(connection => selectedLocations.includes(connection.id))
  const showingAll = connections.length > 1 && locations.length === connections.length
  const filtered = useProjectFilter(visible, keyFor)
  const unpinnedRows = visible.filter(project => !pinnedKeys.has(keyFor(project)) && filtered.shown(project))
  const { macs, server } = groupByMac(unpinnedRows)
  return <div className="flex min-h-0 flex-1 flex-col" aria-label="Connected work">
    <div className={`flex min-h-0 flex-1 flex-col px-2 pb-3 ${allProjectsExpanded ? 'overflow-y-auto' : ''}`}>
      {pinnedRows.length > 0 && <section aria-label="Pinned Crews" className={`mb-3 space-y-1 ${allProjectsExpanded ? 'shrink-0' : 'min-h-0 overflow-y-auto'}`}>
        {pinnedRows.map(project => renderProject(project, true))}
      </section>}
      <AllProjects hasPins={Boolean(pinned?.length)} counts={filtered.counts}>
      {connections.length > 1 && <div role="group" aria-label="Show Crews from" className="mb-2 flex items-center gap-1 overflow-x-auto px-1 py-2">
        {connections.map(connection => {
          const selected = selectedLocations.includes(connection.id)
          const name = connection.id === 'local' ? 'Mac' : connection.name
          // One network at a time, or All at the end (owner, 10 Nov).
          return <button key={connection.id} type="button" aria-pressed={selected && !showingAll} title={connection.id === 'local' ? 'On your Mac' : connection.name}
            onClick={() => setSelectedLocations([connection.id])}
            className={`flex h-9 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected && !showingAll ? 'bg-foreground/[0.08] text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
            {connection.id === 'local' ? <Laptop className="h-3.5 w-3.5 shrink-0" aria-hidden="true" /> : <ServerIdentityIcon connectionId={connection.id} hidden className="h-3.5 w-3.5 shrink-0" />}
            <span className="max-w-28 truncate">{name}</span>
          </button>
        })}
        <button type="button" aria-pressed={showingAll} title="Crews from every network" onClick={() => setSelectedLocations(connections.map(connection => connection.id))}
          className={`flex h-9 shrink-0 items-center rounded-lg px-2.5 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${showingAll ? 'bg-foreground/[0.08] text-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>All</button>
      </div>}
      {/* Your own Crews that live on a Mac and are reached through this server, under that Mac. */}
      {macs.map(({ mac, rows: macRows }) => <section key={mac.device_id} aria-label={`On your Mac: ${mac.name}`} className="mb-3 space-y-1">
        <h2 className="flex min-w-0 items-center gap-1.5 px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">
          <span className="shrink-0">On your Mac</span>
          <span role="img" aria-label={`${mac.name} · ${macStatusText(mac)}`} title={`${mac.name} · ${macStatusText(mac)}`} className={`h-1.5 w-1.5 shrink-0 rounded-full ${mac.online ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`} />
        </h2>
        {macRows.map(project => renderProject(project))}
      </section>)}
      {macs.length > 0 && server.length > 0 && <h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">On the server</h2>}
      {/* The personal AgentWorks agent is on the Home page, not in the left menu (owner, 10 Nov). */}
      <div className="space-y-1"><ProjectList projects={server} render={project => renderProject(project)} /></div>
      {selectedLocations.includes(active) && children}
      {locations.filter(connection => connection.id !== active).map(connection => {
        const status = inventory[connection.id]?.status
        if (!status) return <p key={connection.id} role="status" className="px-2.5 py-2 text-xs text-muted-foreground">Loading {connection.name}…</p>
        if (status === 'unavailable') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => void state.refresh()}>{connection.name} unavailable · Retry</button>
        if (status === 'sign_in') return <button key={connection.id} className="block px-2.5 py-2 text-left text-xs text-muted-foreground underline" onClick={() => openConnectedServers(connection.server)}>{connection.name} · Sign in again</button>
        return null
      })}
      {filtered.active && visible.length > 0 && !unpinnedRows.length && <p className="px-2.5 py-2 text-xs text-muted-foreground">Nothing here for this filter.</p>}
      {!visible.length && locations.every(connection => connection.id !== active && inventory[connection.id]?.status === 'connected') && <p className="px-2.5 py-2 text-xs text-muted-foreground">No Crews here yet.</p>}
      {!desktop && window.electronAPI?.getDesktopConnection && <p className="px-2.5 py-2 text-xs text-muted-foreground">Start the app with the desktop launcher to connect another server.</p>}
      </AllProjects>
    </div>
    {directoryError && <p role="alert" className="px-3 text-xs text-destructive">{directoryError}</p>}
  </div>
}

/** A Crew row; the open Crew lists its agents under it (owner, 10 Nov: always, no caret). */
function ExpandableCrew({ projectId, connectionId, selected, children }: { projectId: string; connectionId: string; selected: boolean; children: (expanded: boolean, onToggle: undefined, activityLabel?: string) => ReactNode }) {
  const liveProjectId = useCrewAgentsStore(state => state.projectId)
  const activity = useConnectedWorkStore(state => isChatIdentityCurrent(state.identity) ? state.activity[connectionId]?.sessions : undefined)
  // The active-session cache is already refreshed for the current connection, including outside the desktop app.
  const localSessions = useChatStore(state => state.activeSessionsCache)
  const sessions = (connectionId === getRawActiveWorkspaceId() ? localSessions : activity || [])
    .filter(session => session.crew_project_id === projectId)
  const activityLabel = sessions.some(hasActiveSessionWork) ? 'Running' : sessions.some(runtimeNeedsUserInput) ? 'Needs your input' : undefined
  return <>{children(selected, undefined, activityLabel)}{selected && liveProjectId === projectId && <SidebarCrewAgents projectId={projectId} />}</>
}
