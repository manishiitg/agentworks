import { AgentMark } from '../chat/AgentIcon'
import { workConversationAgent } from '../../utils/workConversationKey'
import { useAgentMenuState } from '../chat/agentMenuState'
import type { ActiveSessionInfo } from '../../services/api-types'
import { openAgentTab } from '../../utils/openAgentTab'
import { agentApi, getAuthToken, getDesktopApiBaseUrl, getLocalApiBaseUrl } from '../../services/api'
import type { AgentProfileSideChat } from '../../services/api-types'
import { openAppProduct } from '../../utils/appNavigation'
import { productRoutePath } from '../../utils/productRoutes'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { useTeamTab } from '../../products/work/teamApi'
import { openConnectedServers } from './ConnectedServersMenuItem'
import { useProjectMenuHistoryStore, connectedWorkHistoryScope } from '../../stores/useProjectMenuHistoryStore'
import { connectedProjectKey } from '../../utils/connectedWorkPresentation'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { getRawActiveWorkspaceId } from '../../utils/desktopConnection'
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { connectedLocations, hasDesktopConnections, useConnectedWorkPolling, useConnectedWorkStore } from '../../stores/useConnectedWorkStore'
import type { DesktopConnection } from '../../services/desktopConnections'
import { SidebarProjectRow, openSidebarProject } from './SidebarServerGroup'
import { groupByMac, macCrewStateText, macStatusText, type CrewLocationFields } from './macCrews'
import { useTeamInviteRows } from './TeamInviteRow'
import { useCrewAgentsStore } from '../chat/AgentCard'
import { SidebarCrewAgents } from './SidebarCrewAgents'
import { ChevronRight } from 'lucide-react'
import { useSidebarExpansionStore, sidebarExpansionKey } from '../../stores/useSidebarExpansionStore'
import { runtimeNeedsUserInput } from '../../utils/runtimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { useChatStore } from '../../stores/useChatStore'
import { ProjectFilterMenu, ProjectGroupsMenu, matchesMenuFilter, useMenuGroups, type MenuFilter, type MenuNetwork } from './MenuGroups'


type SidebarProject = { id: string; name: string; shared_line?: string; team_busy?: { state: 'running' | 'waiting' | ''; line: string; people?: { name: string; state: 'working' | 'waiting' | 'online' | 'offline' }[] }; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; mode?: 'builder' | 'run'; owned_by_caller?: boolean; owner_name?: string; shared_agents?: number; personal_agent?: boolean } & CrewLocationFields

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
function AllProjects({ children, hasPins, counts, networks, network, onNetwork }: { children: ReactNode; hasPins: boolean; counts: (filter: MenuFilter) => number; networks?: MenuNetwork[]; network?: string; onNetwork?: (id: string) => void }) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'all-projects')
  const expanded = useAllProjectsExpanded(hasPins)
  const toggle = useSidebarExpansionStore(state => state.toggle)
  return <section aria-label="All projects" className={`min-h-0 shrink-0 ${expanded ? '' : 'mt-auto'}`}>
    {/* The heading is the filter (owner, 10 Nov): All, Mine, Shared with me or one of the person's groups. */}
    <div className="flex w-full shrink-0 items-center gap-2 rounded-lg px-2 py-1.5">
      <div className="min-w-0 flex-1"><ProjectFilterMenu counts={counts} networks={networks} network={network} onNetwork={onNetwork} /></div>
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
    const row = (agentsExpanded?: boolean, onToggleAgents?: () => void, activityLabel?: string, activityTitle?: string) => <SidebarProjectRow {...rest} agentsExpanded={agentsExpanded} onToggleAgents={onToggleAgents} activityLabel={agentsExpanded === false ? activityLabel : undefined} activityTitle={activityTitle}
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
      ? <ExpandableCrew key={key} projectId={project.id} connectionId={connection.id} teamBusy={project.team_busy} selected={here && !!project.selected} initialOpen={shortcut || (here && !!project.selected)}>{row}</ExpandableCrew>
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
      <AllProjects hasPins={Boolean(pinned?.length)} counts={filtered.counts}
        networks={connections.map(connection => ({ id: connection.id, name: connection.id === 'local' ? 'Mac' : connection.name }))}
        network={showingAll ? 'all' : selectedLocations[0]}
        onNetwork={id => setSelectedLocations(id === 'all' ? connections.map(connection => connection.id) : [id])}>
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

/** Each expanded Crew loads its own admitted chats, including on other connections. */
function ExpandableCrew({ projectId, connectionId, selected, initialOpen, teamBusy, children }: { projectId: string; connectionId: string; selected: boolean; initialOpen: boolean; teamBusy?: SidebarProject['team_busy']; children: (expanded: boolean, onToggle: () => void, activityLabel?: string, activityTitle?: string) => ReactNode }) {
  const key = sidebarExpansionKey(connectedWorkHistoryScope(), 'crew', connectionId, projectId)
  const saved = useSidebarExpansionStore(state => state.expanded[key])
  const toggle = useSidebarExpansionStore(state => state.toggle)
  const expanded = saved ?? initialOpen
  // Opening a project shows its agents; they stay open when another project is opened, and the caret closes them
  // (owner, 10 Nov), so several projects' agents can be open at once.
  useEffect(() => { if (selected && saved !== true) toggle(key, false) }, [selected]) // eslint-disable-line react-hooks/exhaustive-deps
  const [loadedChats, setLoadedChats] = useState<{ identity: number; chats: AgentProfileSideChat[] }>({ identity: captureChatIdentity(), chats: [] })
  const chats = isChatIdentityCurrent(loadedChats.identity) ? loadedChats.chats : []
  const liveProjectId = useCrewAgentsStore(state => state.projectId)
  const [error, setError] = useState(false)
  const activity = useConnectedWorkStore(state => isChatIdentityCurrent(state.identity) ? state.activity[connectionId]?.sessions : undefined)
  // The active-session cache is already refreshed for the current connection, including outside the desktop app.
  // Keep observing it while collapsed; no need to open/load the project's chat list to know it is working.
  const localSessions = useChatStore(state => state.activeSessionsCache)
  const sessions = (connectionId === getRawActiveWorkspaceId() ? localSessions : activity || [])
    .filter(session => session.crew_project_id === projectId)
  const running = sessions.some(hasActiveSessionWork)
  // A teammate working here shows on the row too, from the server's note (owner, 10 Nov), with its line on hover.
  const activityLabel = running || teamBusy?.state === 'running' ? 'Running' : sessions.some(runtimeNeedsUserInput) ? 'Needs your input' : undefined
  const activityTitle = !running && teamBusy?.state === 'running' ? teamBusy.line : undefined
  // This network's chat changes arrive as live notices; another network's list is checked every minute.
  const here = connectionId === getRawActiveWorkspaceId()
  const [reload, setReload] = useState(0)
  useLiveRefetch(() => setReload(value => value + 1), { kinds: ['sessions'], fallbackMs: 60_000, minIntervalMs: 5_000, enabled: expanded && here })
  useEffect(() => {
    if (!expanded) return
    let cancelled = false
    const identity = captureChatIdentity()
    const load = async () => {
      try {
        const current = connectionId === getRawActiveWorkspaceId()
        let result: AgentProfileSideChat[]
        if (current) result = await agentApi.listAgentProfileSideChats('work', projectId, true)
        else {
          const base = connectionId === 'local' ? getLocalApiBaseUrl() : `${getDesktopApiBaseUrl()}/api/connections/${connectionId}`
          const response = await fetch(`${base}/api/agent-profiles/work/side-chats?project_id=${encodeURIComponent(projectId)}&with_main=1`, { headers: { Authorization: `Bearer ${getAuthToken() || ''}` }, signal: AbortSignal.timeout(15000) })
          if (!response.ok) throw new Error('Chats unavailable')
          result = await response.json()
        }
        if (!cancelled && isChatIdentityCurrent(identity)) { setLoadedChats({ identity, chats: Array.isArray(result) ? result : [] }); setError(false) }
      } catch { if (!cancelled && isChatIdentityCurrent(identity)) setError(true) }
    }
    void load()
    const timer = here ? undefined : window.setInterval(() => { if (!document.hidden) void load() }, 60_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [expanded, projectId, connectionId, here, reload])
  const open = (chat: AgentProfileSideChat) => {
    const agent = chat.is_main ? undefined : workConversationAgent(chat.conversation_key)
    const route = { surface: 'work' as const, id: projectId, agent }
    if (connectionId === getRawActiveWorkspaceId()) {
      const existing = Object.values(useChatStore.getState().chatTabs).find(tab => tab.sessionId === chat.session_id && tab.metadata?.agentProfileProjectId === projectId)
      if (existing) openAgentTab(existing.tabId)
      else openAppProduct(route)
    }
    else void window.electronAPI?.openDesktopConnection?.(connectionId, productRoutePath(route))
    window.dispatchEvent(new Event('close-app-sidebar'))
  }
  return <>{children(expanded, () => toggle(key, initialOpen), activityLabel, activityTitle)}{expanded && (selected && liveProjectId === projectId ? <SidebarCrewAgents projectId={projectId} /> : <div role="list" aria-label="Agents" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">{error ? <span className="px-2 text-xs text-muted-foreground">Could not load agents</span> : chats.filter(chat => chat.pinned !== false).map(chat => {
      const session = sessions.find(item => item.session_id === chat.session_id)
      return <button key={chat.conversation_key} type="button" role="listitem" onClick={() => open(chat)} className="flex w-full min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-left text-[13px] text-muted-foreground hover:bg-muted hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10 max-md:pr-11"><span className="min-w-0 flex-1 truncate">{chat.name || (chat.is_main ? 'Main' : 'Agent')}</span><ProjectAgentMark projectId={projectId} sessionId={chat.session_id} session={session} local={connectionId === getRawActiveWorkspaceId()} /></button>
    })}</div>)}{expanded && connectionId === getRawActiveWorkspaceId() && <TeammatesWorking projectId={projectId} />}</>
}

/** Inactive project rows use the same marks as the open Crew, including unread completions. */
function ProjectAgentMark({ projectId, sessionId, session, local }: { projectId: string; sessionId: string; session?: ActiveSessionInfo; local: boolean }) {
  const tabId = useChatStore(state => local ? Object.values(state.chatTabs).find(tab => tab.sessionId === sessionId && tab.metadata?.agentProfileProjectId === projectId)?.tabId : undefined)
  const state = useAgentMenuState(tabId || `sidebar:${projectId}:${sessionId}`, undefined, local ? { sessionId } : undefined)
  const remoteState = runtimeNeedsUserInput(session) ? 'waiting' : session && hasActiveSessionWork(session) ? 'running' : null
  return <AgentMark state={local ? state : remoteState} />
}

/** Under an open project: each teammate agent working or waiting there now, as the Team tab reads it (owner, 10 Nov:
 * someone working on the project should show in the menu, not only in the header's person chips). */
function TeammatesWorking({ projectId }: { projectId: string }) {
  const team = useTeamTab(projectId)
  const busy = (team.view?.agents || []).filter(agent => !agent.mine && (agent.activity === 'running' || agent.activity === 'waiting'))
  if (!busy.length) return null
  return <div role="list" aria-label="Teammates working" className="ml-6 space-y-0.5 border-l border-border pb-1 pl-1.5">
    {busy.map(agent => <div key={agent.key} role="listitem" title={agent.status_label || undefined} className="flex min-w-0 items-center gap-2 rounded-md py-1 pl-2 pr-7 text-[13px] text-muted-foreground">
      <span className="min-w-0 flex-1 truncate">{agent.owner_name}'s {agent.name}</span>
      <span className="shrink-0 text-[11px]">{agent.activity === 'running' ? 'Working' : 'Waiting'}</span>
      <AgentMark state={agent.activity === 'running' ? 'running' : 'waiting'} />
    </div>)}
  </div>
}
